// Package egress keeps the display from reaching anything beyond the local network, when the
// network.internet setting is "local".
//
// It sits under the switches that stop internet lookups at their source: a chain of its own,
// hooked first in OUTPUT, that returns for the allowed destinations and rejects everything else.
// Rejecting rather than dropping makes a caller fail at once instead of retrying into a silent
// firewall. netd's chains are left alone.
package egress

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const Chain = "lanovo_egress"

// DefaultConfig is written to the config path the first time the service runs without one.
const DefaultConfig = `# LANovo egress allowlist, used when the network.internet setting is "local".
# Then anything not listed is rejected: TCP with a reset, everything else with
# "administratively prohibited". Under "open" or "quiet" nothing is blocked.
#
# "local" cuts whatever needs the internet: firmware and helper downloads from GitHub, the cast
# engine's first download, cloud photo albums, radar maps, lyrics, YouTube and Google-hosted
# cast apps. Add any network you still rely on with an "allow" line, then run
# "lanovod egress apply" (or change the setting) to load it.
#
#   allow <prefix>     an IPv4 or IPv6 network the display may reach
#   log <n>            log at most n rejected packets a minute to the kernel log (0: none)
#
# The global IPv6 /64 the display is on is always allowed.

log 6

# private IPv4, link-local, multicast (mDNS and SSDP for cast discovery), DHCP broadcast
allow 10.0.0.0/8
allow 172.16.0.0/12
allow 192.168.0.0/16
allow 169.254.0.0/16
allow 224.0.0.0/4
allow 255.255.255.255/32

# unique-local, link-local and multicast IPv6 (DHCPv6, mDNS)
allow fc00::/7
allow fe80::/10
allow ff00::/8
`

// Config is the allowlist from the config file, and whether the block is wanted at all, which
// comes from the network.internet setting.
type Config struct {
	Enabled      bool
	Allow        []netip.Prefix
	LogPerMinute int
}

// Wanted reads from saved settings whether the block should be on: only when network.internet
// is "local". It decodes nothing else, so it can read the file while lanovod writes it.
func Wanted(state []byte) (bool, error) {
	var saved struct {
		Network struct {
			Internet schema.Internet `json:"internet"`
		} `json:"network"`
	}
	if err := json.Unmarshal(state, &saved); err != nil {
		return false, fmt.Errorf("egress: the saved settings: %w", err)
	}
	return saved.Network.Internet.Local(), nil
}

func (c Config) allows(p netip.Prefix) bool { return slices.Contains(c.Allow, p) }

type Family int

const (
	V4 Family = iota
	V6
)

func (f Family) iptables() string {
	if f == V6 {
		return "ip6tables"
	}
	return "iptables"
}

func (f Family) rejectOther() string {
	if f == V6 {
		return "icmp6-adm-prohibited"
	}
	return "icmp-admin-prohibited"
}

func (f Family) holds(p netip.Prefix) bool { return p.Addr().Is6() == (f == V6) }

// Parse reads a config in the DefaultConfig format. Enabled is left for the caller to set.
func Parse(r io.Reader) (Config, error) {
	var c Config
	s := bufio.NewScanner(r)
	for n := 1; s.Scan(); n++ {
		line, _, _ := strings.Cut(s.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return Config{}, fmt.Errorf("line %d: want a keyword and one value, got %q", n, line)
		}
		if err := c.set(fields[0], fields[1]); err != nil {
			return Config{}, fmt.Errorf("line %d: %w", n, err)
		}
	}
	if err := s.Err(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) set(key, value string) error {
	switch key {
	case "log":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("log takes a count of zero or more, not %q", value)
		}
		c.LogPerMinute = n
	case "allow":
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return err
		}
		if p != p.Masked() {
			return fmt.Errorf("%s has host bits set; did you mean %s?", p, p.Masked())
		}
		if p.Bits() == 0 {
			return fmt.Errorf("refusing %s: it allows the whole internet", p)
		}
		c.Allow = append(c.Allow, p)
	default:
		return fmt.Errorf("unknown keyword %q", key)
	}
	return nil
}

// Load reads the config at path, writing DefaultConfig there first if there is none.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		raw = []byte(DefaultConfig)
		if err := safe.WriteFile(path, raw, 0o644); err != nil {
			return Config{}, fmt.Errorf("egress: writing the default %s: %w", path, err)
		}
	} else if err != nil {
		return Config{}, fmt.Errorf("egress: %w", err)
	}
	c, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		return Config{}, fmt.Errorf("egress: %s: %w", path, err)
	}
	return c, nil
}

// OwnPrefixes turns the device's addresses into the global IPv6 /64s it sits on.
func OwnPrefixes(addrs []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range addrs {
		ip := a.Addr()
		if !ip.Is6() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		p := netip.PrefixFrom(ip, 64).Masked()
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// Rules renders the chain for one family as iptables-restore input. Declaring the chain flushes
// it, so loading the same rules twice leaves one copy.
func Rules(c Config, f Family, own []netip.Prefix) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("*filter")
	line(":%s - [0:0]", Chain)
	line("-A %s -o lo -j RETURN", Chain)
	for _, p := range slices.Concat(c.Allow, own) {
		if f.holds(p) {
			line("-A %s -d %s -j RETURN", Chain, p)
		}
	}
	if c.LogPerMinute > 0 {
		line(`-A %s -m limit --limit %d/min --limit-burst %d -j LOG --log-prefix "%s: " --log-level 4`,
			Chain, c.LogPerMinute, c.LogPerMinute, Chain)
	}
	line("-A %s -p tcp -j REJECT --reject-with tcp-reset", Chain)
	line("-A %s -j REJECT --reject-with %s", Chain, f.rejectOther())
	line("COMMIT")
	return b.String()
}

// Runner runs a command with stdin; it is the one side effect Apply has.
type Runner interface {
	Run(name string, stdin string, args ...string) error
}

// Apply loads the chain and hooks it first in OUTPUT, for each family. A family whose rules do
// not load is left unhooked, so a bad config fails open rather than cutting the display off.
func Apply(r Runner, c Config, own []netip.Prefix) error {
	var errs []error
	for _, f := range []Family{V4, V6} {
		if err := apply(r, c, f, own); err != nil {
			errs = append(errs, fmt.Errorf("egress: %s: %w", f.iptables(), err))
		}
	}
	return errors.Join(errs...)
}

func apply(r Runner, c Config, f Family, own []netip.Prefix) error {
	hooked := r.Run(f.iptables(), "", "-w", "-C", "OUTPUT", "-j", Chain) == nil
	if !c.Enabled {
		if hooked {
			return r.Run(f.iptables(), "", "-w", "-D", "OUTPUT", "-j", Chain)
		}
		return nil
	}
	if err := r.Run(f.iptables()+"-restore", Rules(c, f, own), "-w", "5", "--noflush"); err != nil {
		return err
	}
	if hooked {
		return nil
	}
	return r.Run(f.iptables(), "", "-w", "-I", "OUTPUT", "1", "-j", Chain)
}

// netd declares OUTPUT afresh when it sets up its own chains, which drops any hook put there
// before it. Its jumps go in as oem_out, fw_OUTPUT, st_OUTPUT, bw_OUTPUT; wait for the last.
const netdHook = "bw_OUTPUT"

// AwaitNetd waits for netd to finish setting up OUTPUT for one family, checking up to tries times
// with pause between checks.
func AwaitNetd(r Runner, f Family, tries int, pause func()) error {
	for i := range tries {
		if r.Run(f.iptables(), "", "-w", "-C", "OUTPUT", "-j", netdHook) == nil {
			return nil
		}
		if i < tries-1 {
			pause()
		}
	}
	return fmt.Errorf("egress: netd has not set up %s OUTPUT", f.iptables())
}

// netdTries bounds AwaitNetd in Run: netd sets up its chains within seconds of starting.
const netdTries = 60

// Run loads the allowlist at path and applies it on or off as wanted, waiting for netd first
// when it goes on. pause sits between checks on netd.
func Run(r Runner, path string, wanted bool, pause func()) (Config, []netip.Prefix, error) {
	conf, own, err := Inputs(path)
	if err != nil {
		return Config{}, nil, err
	}
	conf.Enabled = wanted
	if wanted {
		for _, f := range []Family{V4, V6} {
			if err := AwaitNetd(r, f, netdTries, pause); err != nil {
				return conf, own, err
			}
		}
	}
	return conf, own, Apply(r, conf, own)
}

// Inputs are the allowlist at path, off until the caller says otherwise, and the device's own
// global IPv6 /64s.
func Inputs(path string) (Config, []netip.Prefix, error) {
	conf, err := Load(path)
	if err != nil {
		return Config{}, nil, err
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return Config{}, nil, fmt.Errorf("egress: the device's addresses: %w", err)
	}
	var prefixes []netip.Prefix
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			prefixes = append(prefixes, p)
		}
	}
	return conf, OwnPrefixes(prefixes), nil
}

// Exec runs commands for real.
type Exec struct{}

func (Exec) Run(name string, stdin string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}
