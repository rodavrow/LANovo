package egress

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTheDefaultConfig(t *testing.T) {
	c, err := Parse(strings.NewReader(DefaultConfig))
	if err != nil {
		t.Fatalf("Parse(DefaultConfig) = %v", err)
	}
	if c.Enabled {
		t.Error("the config file turned the block on; only the setting does that")
	}
	for _, want := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "224.0.0.0/4",
		"255.255.255.255/32", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		if !c.allows(netip.MustParsePrefix(want)) {
			t.Errorf("the default config does not allow %s", want)
		}
	}
	if c.LogPerMinute != 6 {
		t.Errorf("LogPerMinute = %d, want 6", c.LogPerMinute)
	}
}

func TestParseRejectsBadLines(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"unknown keyword", "permit 10.0.0.0/8\n", "line 1: unknown"},
		{"bad prefix", "allow 10.0.0/8\n", "line 1:"},
		{"host bits set", "allow 10.0.0.1/8\n", "line 1:"},
		{"enabled belongs to the setting", "enabled yes\n", "line 1: unknown"},
		{"negative log", "log -1\n", "line 1:"},
		{"missing value", "\n\nallow\n", "line 3:"},
		{"default route", "allow 0.0.0.0/0\n", "line 1: refusing"},
		{"default route v6", "allow ::/0\n", "line 1: refusing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse(%q) = %v, want an error containing %q", tt.body, err, tt.want)
			}
		})
	}
}

func TestParseIgnoresCommentsAndBlankLines(t *testing.T) {
	body := "# a comment\n\n  allow 192.168.0.0/16  # trailing\nlog 3\n"
	c, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if c.Enabled || len(c.Allow) != 1 || c.LogPerMinute != 3 {
		t.Fatalf("Parse = %+v, want one rule, log 3 and the block left off", c)
	}
}

func TestWantedOnlyForLocal(t *testing.T) {
	for _, tt := range []struct {
		state string
		want  bool
	}{
		{`{"network":{"internet":"local","verify":true}}`, true},
		{`{"network":{"internet":"quiet"}}`, false},
		{`{"network":{"verify":true}}`, false},
		{`{}`, false},
	} {
		got, err := Wanted([]byte(tt.state))
		if err != nil || got != tt.want {
			t.Errorf("Wanted(%s) = %v, %v; want %v", tt.state, got, err, tt.want)
		}
	}
	if _, err := Wanted([]byte("not json")); err == nil {
		t.Error("Wanted(not json) = nil error")
	}
}

func TestRulesV4(t *testing.T) {
	c := Config{
		Enabled:      true,
		LogPerMinute: 6,
		Allow: []netip.Prefix{
			netip.MustParsePrefix("192.168.0.0/16"),
			netip.MustParsePrefix("fc00::/7"),
		},
	}
	want := strings.Join([]string{
		"*filter",
		":lanovo_egress - [0:0]",
		"-A lanovo_egress -o lo -j RETURN",
		"-A lanovo_egress -d 192.168.0.0/16 -j RETURN",
		"-A lanovo_egress -m limit --limit 6/min --limit-burst 6 -j LOG " +
			`--log-prefix "lanovo_egress: " --log-level 4`,
		"-A lanovo_egress -p tcp -j REJECT --reject-with tcp-reset",
		"-A lanovo_egress -j REJECT --reject-with icmp-admin-prohibited",
		"COMMIT",
		"",
	}, "\n")
	if got := Rules(c, V4, nil); got != want {
		t.Errorf("Rules(V4) =\n%s\nwant\n%s", got, want)
	}
}

// The video player fetches from whatever port a server on the network listens on, so the default
// allowlist must pass a private address without naming a protocol or a port.
func TestDefaultRulesReachAPrivateServerOnAnyPort(t *testing.T) {
	c, err := Parse(strings.NewReader(DefaultConfig))
	if err != nil {
		t.Fatalf("Parse(DefaultConfig) = %v", err)
	}
	c.Enabled = true
	server := netip.MustParseAddr("192.168.1.85")

	for line := range strings.SplitSeq(Rules(c, V4, nil), "\n") {
		f := strings.Fields(line)
		if len(f) == 6 && f[2] == "-d" && f[5] == "RETURN" &&
			netip.MustParsePrefix(f[3]).Contains(server) {
			return
		}
		if strings.Contains(line, "REJECT") {
			break
		}
	}
	t.Errorf("no rule before the rejects returns for %s on every port:\n%s",
		server, Rules(c, V4, nil))
}

func TestRulesV6AddsTheDevicePrefixAndSkipsLogWhenOff(t *testing.T) {
	c := Config{Enabled: true, Allow: []netip.Prefix{netip.MustParsePrefix("fe80::/10")}}
	own := []netip.Prefix{netip.MustParsePrefix("2a02:a45e:45d5:20::/64")}
	got := Rules(c, V6, own)
	for _, want := range []string{
		"-A lanovo_egress -d fe80::/10 -j RETURN\n",
		"-A lanovo_egress -d 2a02:a45e:45d5:20::/64 -j RETURN\n",
		"-A lanovo_egress -j REJECT --reject-with icmp6-adm-prohibited\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Rules(V6) lacks %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "LOG") {
		t.Errorf("Rules(V6) logs with LogPerMinute 0:\n%s", got)
	}
}

func TestRulesKeepEachFamilyToItsOwnPrefixes(t *testing.T) {
	c := Config{Enabled: true, Allow: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("ff00::/8"),
	}}
	if got := Rules(c, V4, nil); strings.Contains(got, "ff00::/8") {
		t.Errorf("Rules(V4) names an IPv6 prefix:\n%s", got)
	}
	if got := Rules(c, V6, nil); strings.Contains(got, "10.0.0.0/8") {
		t.Errorf("Rules(V6) names an IPv4 prefix:\n%s", got)
	}
}

func TestOwnPrefixesKeepsGlobalIPv6AsSlash64(t *testing.T) {
	addrs := []netip.Prefix{
		netip.MustParsePrefix("192.168.20.131/24"),
		netip.MustParsePrefix("fe80::3e91:80ff:feb2:44d7/64"),
		netip.MustParsePrefix("2a02:a45e:45d5:20:3e91:80ff:feb2:44d7/64"),
		netip.MustParsePrefix("2a02:a45e:45d5:20::1234/128"),
		netip.MustParsePrefix("fd00:20::5/64"),
	}
	got := OwnPrefixes(addrs)
	want := []netip.Prefix{netip.MustParsePrefix("2a02:a45e:45d5:20::/64")}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("OwnPrefixes = %v, want %v", got, want)
	}
}

type call struct {
	name  string
	args  string
	stdin string
}

type fakeRunner struct {
	calls []call
	fail  map[string]error
}

func (f *fakeRunner) Run(name string, stdin string, args ...string) error {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, call{name, joined, stdin})
	if err, ok := f.fail[name+" "+joined]; ok {
		return err
	}
	return nil
}

func (f *fakeRunner) ran(name, args string) bool {
	for _, c := range f.calls {
		if c.name == name && c.args == args {
			return true
		}
	}
	return false
}

func TestApplyLoadsRulesThenHooksOutputOnce(t *testing.T) {
	r := &fakeRunner{fail: map[string]error{
		"iptables -w -C OUTPUT -j lanovo_egress":  errors.New("no such rule"),
		"ip6tables -w -C OUTPUT -j lanovo_egress": nil,
	}}
	c := Config{Enabled: true, Allow: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	if err := Apply(r, c, nil); err != nil {
		t.Fatalf("Apply = %v", err)
	}
	if !r.ran("iptables-restore", "-w 5 --noflush") || !r.ran("ip6tables-restore", "-w 5 --noflush") {
		t.Errorf("Apply did not load both families: %+v", r.calls)
	}
	if !r.ran("iptables", "-w -I OUTPUT 1 -j lanovo_egress") {
		t.Errorf("Apply did not hook IPv4 OUTPUT: %+v", r.calls)
	}
	if r.ran("ip6tables", "-w -I OUTPUT 1 -j lanovo_egress") {
		t.Errorf("Apply hooked IPv6 OUTPUT twice: %+v", r.calls)
	}
}

func TestApplyDoesNotHookWhenTheRulesFailToLoad(t *testing.T) {
	r := &fakeRunner{fail: map[string]error{
		"iptables-restore -w 5 --noflush": errors.New("bad rule"),
	}}
	c := Config{Enabled: true}
	if err := Apply(r, c, nil); err == nil {
		t.Fatal("Apply = nil, want the restore error")
	}
	if r.ran("iptables", "-w -I OUTPUT 1 -j lanovo_egress") {
		t.Errorf("Apply hooked OUTPUT to a chain that did not load: %+v", r.calls)
	}
}

func TestApplyDisabledRemovesTheHook(t *testing.T) {
	r := &fakeRunner{fail: map[string]error{
		"ip6tables -w -C OUTPUT -j lanovo_egress": errors.New("no such rule"),
	}}
	if err := Apply(r, Config{Enabled: false}, nil); err != nil {
		t.Fatalf("Apply(disabled) = %v", err)
	}
	if !r.ran("iptables", "-w -D OUTPUT -j lanovo_egress") {
		t.Errorf("Apply(disabled) left the IPv4 hook: %+v", r.calls)
	}
	if r.ran("iptables-restore", "-w 5 --noflush") {
		t.Errorf("Apply(disabled) loaded rules: %+v", r.calls)
	}
}

func TestAwaitNetdReturnsOnceNetdHookedOutput(t *testing.T) {
	r := &countingRunner{succeedOn: 3}
	pauses := 0
	if err := AwaitNetd(r, V4, 10, func() { pauses++ }); err != nil {
		t.Fatalf("AwaitNetd = %v", err)
	}
	if r.n != 3 || pauses != 2 {
		t.Errorf("AwaitNetd checked %d times and paused %d, want 3 and 2", r.n, pauses)
	}
	if r.last != "iptables -w -C OUTPUT -j bw_OUTPUT" {
		t.Errorf("AwaitNetd checked %q", r.last)
	}
}

func TestAwaitNetdGivesUp(t *testing.T) {
	r := &countingRunner{}
	if err := AwaitNetd(r, V6, 4, func() {}); err == nil {
		t.Fatal("AwaitNetd = nil with netd never ready")
	}
	if r.n != 4 {
		t.Errorf("AwaitNetd checked %d times, want 4", r.n)
	}
}

type countingRunner struct {
	n, succeedOn int
	last         string
}

func (c *countingRunner) Run(name string, _ string, args ...string) error {
	c.n++
	c.last = name + " " + strings.Join(args, " ")
	if c.succeedOn > 0 && c.n >= c.succeedOn {
		return nil
	}
	return errors.New("no such rule")
}

func TestLoadWritesTheDefaultWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "egress.conf")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if c.Enabled {
		t.Error("Load turned the block on; only the setting does that")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != DefaultConfig {
		t.Errorf("Load left %q, %v; want the default config written", raw, err)
	}
}

func TestLoadReportsAnUnreadableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "egress.conf")
	if err := os.WriteFile(path, []byte("allow nonsense\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Load = %v, want an error naming %s", err, path)
	}
}
