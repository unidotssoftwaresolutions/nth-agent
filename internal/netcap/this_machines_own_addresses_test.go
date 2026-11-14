package netcap

import (
	"errors"
	"net"
	"testing"
)

// THIS MACHINE'S OWN ADDRESSES (WHAT_A_LEASE_CAN_ASK). The rule named this machine by a fixed list of ranges, so an address
// of the internet that is this machine's own — a public IPv4 on an interface, a global IPv6 address — passed, and the
// system delivered the connection to whatever listens here on every address. The proxy now also refuses each address this
// machine's interfaces carry, read when it dials.
func TestACaptureCannotReachThisMachineByItsOwnPublicAddress(t *testing.T) {
	own := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("203.0.113.7"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8:5::7"), Mask: net.CIDRMask(64, 128)},
			&net.IPAddr{IP: net.ParseIP("198.51.100.9")},
		}, nil
	}
	p := NewProxy(Options{ownAddresses: own})
	for _, address := range []string{"203.0.113.7:443", "[2001:db8:5::7]:80", "198.51.100.9:22", "[::ffff:203.0.113.7]:443", "[64:ff9b::cb00:7107]:443"} {
		if err := p.destinationControl("tcp", address, nil); !errors.As(err, new(errLocalDestination)) {
			t.Errorf("%s is this machine's own address and was let through: %v", address, err)
		}
	}
	for _, address := range []string{"203.0.113.8:443", "[2001:db8:5::8]:80", "8.8.8.8:53"} {
		if err := p.destinationControl("tcp", address, nil); err != nil {
			t.Errorf("%s is another machine on the internet and was refused: %v", address, err)
		}
	}
	// --capture-allow-local-network opens this too, as it opens the rest of this machine.
	if err := NewProxy(Options{ownAddresses: own, ReachLocal: true}).destinationControl("tcp", "203.0.113.7:443", nil); err != nil {
		t.Errorf("with the flag, this machine's own address is reached: %v", err)
	}
}

// When this machine's addresses cannot be read, nothing is let through on the guess that it is not one of them.
func TestAnAddressIsRefusedWhenThisMachinesOwnCannotBeRead(t *testing.T) {
	p := NewProxy(Options{ownAddresses: func() ([]net.Addr, error) { return nil, errors.New("no table") }})
	if err := p.destinationControl("tcp", "8.8.8.8:53", nil); !errors.As(err, new(errLocalDestination)) {
		t.Fatalf("with this machine's own addresses unread, a dial must be refused: %v", err)
	}
}

// The real table: every address this machine's interfaces carry right now is refused by the proxy's own rule.
func TestEveryAddressThisMachineCarriesIsRefused(t *testing.T) {
	addrs, err := net.InterfaceAddrs()
	if err != nil || len(addrs) == 0 {
		t.Skipf("this machine's interface addresses cannot be read here: %v", err)
	}
	p := NewProxy(Options{})
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLinkLocalUnicast() && ip.To4() == nil {
			continue // fe80:: needs its zone to be dialled at all, and the ranges refuse it already
		}
		if err := p.destinationControl("tcp", net.JoinHostPort(ip.String(), "443"), nil); !errors.As(err, new(errLocalDestination)) {
			t.Errorf("%s is on this machine's interfaces and was let through: %v", ip, err)
		}
	}
}
