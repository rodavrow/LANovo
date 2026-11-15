package clock

import (
	"net"
	"testing"

	shareddhcp "github.com/ygelfand/libcountertop/pkg/network/dhcp"
)

func TestTimeServersPreferWhatDHCPOffers(t *testing.T) {
	lease := &shareddhcp.Lease{
		Router: net.ParseIP("192.168.20.1"),
		NTP:    []net.IP{net.ParseIP("192.168.20.5")},
	}
	got := timeServers(lease)
	if len(got) != 1 || !got[0].Equal(net.ParseIP("192.168.20.5")) {
		t.Errorf("timeServers = %v, want the offered 192.168.20.5", got)
	}
}

func TestTimeServersFallBackToTheGateway(t *testing.T) {
	got := timeServers(&shareddhcp.Lease{Router: net.ParseIP("192.168.20.1")})
	if len(got) != 1 || !got[0].Equal(net.ParseIP("192.168.20.1")) {
		t.Errorf("timeServers = %v, want the gateway 192.168.20.1", got)
	}
}

func TestTimeServersWithoutALeaseOfferNone(t *testing.T) {
	if got := timeServers(nil); got != nil {
		t.Errorf("timeServers(nil) = %v, want none", got)
	}
	if got := timeServers(&shareddhcp.Lease{}); got != nil {
		t.Errorf("timeServers(no router) = %v, want none", got)
	}
}
