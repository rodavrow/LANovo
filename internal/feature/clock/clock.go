package clock

import (
	"net"
	"sync"
	"time"

	shareddhcp "github.com/ygelfand/libcountertop/pkg/network/dhcp"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/timezone"
	sharedclock "github.com/ygelfand/libcountertop/pkg/system/clock"

	"github.com/ygelfand/LANovo/internal/component"
	"github.com/ygelfand/LANovo/internal/config"
	"github.com/ygelfand/LANovo/internal/feature/dhcp"
	"github.com/ygelfand/LANovo/internal/layout"
)

func init() {
	component.Register(sharedcomponent.Network, Get, sharedcomponent.Order(20),
		sharedcomponent.Supervise(service.Restart(5*time.Second, time.Minute)))
}

type Clock struct{ *sharedclock.Clock }

var (
	once   sync.Once
	shared *Clock
)

func Get() *Clock {
	once.Do(func() {
		shared = &Clock{sharedclock.New(sharedclock.Options{
			Servers: servers,
			Built:   layout.BuildDate,
			RTC:     "/dev/rtc0",
			Zone: timezone.PolicyOptions{
				Read:   func() schema.Time { return config.Get().Time },
				Home:   func(s string) error { return config.Set().Time().Home(s) },
				Chosen: func(s string) error { return config.Set().Time().Chosen(s) },
			},
		})}
	})
	return shared
}

func (c *Clock) Restore(config.Config) { c.Clock.Restore() }

func servers() []net.IP { return timeServers(dhcp.Get().Lease()) }

// timeServers are the NTP servers DHCP offers, or else the gateway, which on most home networks
// answers NTP itself. The shared clock tries the public pool after them, so a gateway that does
// not serve time still leaves the clock set.
func timeServers(lease *shareddhcp.Lease) []net.IP {
	switch {
	case lease == nil:
		return nil
	case len(lease.NTP) > 0:
		return lease.NTP
	case lease.Router != nil:
		return []net.IP{lease.Router}
	}
	return nil
}
