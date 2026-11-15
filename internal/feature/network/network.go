package network

import (
	"log/slog"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/network/policy"
	sharedwifi "github.com/ygelfand/libcountertop/pkg/network/wifi"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/LANovo/internal/component"
	"github.com/ygelfand/LANovo/internal/config"
	"github.com/ygelfand/LANovo/internal/egress"
	"github.com/ygelfand/LANovo/internal/feature/dhcp"
	"github.com/ygelfand/LANovo/internal/feature/homeassistant"
	hwwifi "github.com/ygelfand/LANovo/internal/hardware/wifi"
	"github.com/ygelfand/LANovo/internal/layout"
)

func init() {
	component.Register(sharedcomponent.Network, Get, sharedcomponent.Order(5))
}

type Network struct {
	*policy.Policy
	wifi *sharedwifi.Mover

	// block loads or removes the egress block; a field so tests need no firewall.
	block func(on bool) error

	mu       sync.Mutex
	internet schema.Internet
	applying sync.Mutex
}

var get = sync.OnceValue(func() *Network {
	n := newNetwork(policy.New(config.NetworkSection), blockEgress)
	n.wifi = sharedwifi.NewMover(hwwifi.Get(), dhcp.Get().Fresh)
	n.DownloadThrough(homeassistant.Get().Downloads())
	component.Settings.Add(n.Settings())
	return n
})

func newNetwork(p *policy.Policy, block func(on bool) error) *Network {
	n := &Network{Policy: p, block: block, internet: p.Internet()}
	p.Changed.Listen(func(c schema.Network) { n.changed(c.Internet) })
	return n
}

func Get() *Network                          { return get() }
func (n *Network) Wifi() *sharedwifi.Mover   { return n.wifi }
func (n *Network) Restore(cfg config.Config) { n.Policy.Restore(cfg.Network.Verify) }

// changed re-applies the egress block when network.internet moves into or out of "local". init
// loads it at boot; this covers changes while lanovod runs.
func (n *Network) changed(now schema.Internet) {
	n.mu.Lock()
	was := n.internet
	n.internet = now
	n.mu.Unlock()
	if was.Local() == now.Local() {
		return
	}
	safe.Go("internet access", n.applyLatest)
}

func (n *Network) applyLatest() {
	n.applying.Lock()
	defer n.applying.Unlock()
	n.mu.Lock()
	now := n.internet
	n.mu.Unlock()
	if err := n.block(now.Local()); err != nil {
		slog.Error("the internet block could not be changed", "local", now.Local(), "err", err)
		return
	}
	slog.Info("internet access changed", "access", now.Label())
}

func blockEgress(on bool) error {
	_, _, err := egress.Run(egress.Exec{}, layout.EgressConfigPath, on,
		func() { time.Sleep(time.Second) })
	return err
}
