package netcap

import (
	"fmt"
	"net"
	"syscall"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// ── WHERE A CAPTURE MAY CONNECT: THE INTERNET, NOT THIS MACHINE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 2) ────────
//
// During a capture the phone's traffic leaves through this machine, so without a rule it could reach what this machine
// reaches and the phone cannot: the customer's own services on loopback, the metadata address of a cloud machine, the
// office network. The app under test, a web page it loads, or the remote person driving the phone could ask for any of
// them, and the answer would be recorded. So the proxy refuses:
//
//   - loopback (127.0.0.0/8, ::1) and "this host" (0.0.0.0/8, ::), which on most systems also reach loopback;
//   - link-local (169.254.0.0/16 — the cloud metadata address 169.254.169.254 among them — and fe80::/10);
//   - private (10/8, 172.16/12, 192.168/16) and unique-local (fc00::/7 — AWS's fd00:ec2::254 among them), and the
//     retired IPv6 site-local fec0::/10;
//   - carrier-grade NAT (100.64.0.0/10 — Alibaba's metadata address 100.100.100.200 among them);
//   - any of those carried inside IPv6: mapped (::ffff:a.b.c.d), the old compatible form (::a.b.c.d), and NAT64's
//     64:ff9b::/96, which a NAT64 gateway turns back into the address it carries.
//
// JUDGED ON THE ADDRESS IT ACTUALLY DIALS, after the name is resolved: the rule is the dialer's Control hook, which the
// system calls with the address of each connection it is about to make. A name that resolves to one of these, or that
// resolves to the internet once and to this machine the next time, is caught there, as is every address of a name that
// has several. A redirect is a new request from the phone, and is judged like the first.
//
// A REFUSED REQUEST IS IN THE CAPTURE, in the shape an entry already has (an exchange with no response, `_error` saying
// why), and the phone is told the same in a 502 — so a run never fails without saying so. --capture-allow-local-network
// (Options.ReachLocal) opens it, for a customer who tests against a local backend; connect says so on screen while it is.

// RefusedLocal is what a capture entry, and the phone, are told when the proxy will not connect where a request asked.
func RefusedLocal() string {
	return "refused: the capture proxy does not connect to this machine or its local network — to let a capture " +
		"reach a local backend, restart with " + command.OnThatMachine("connect … "+AllowLocalFlag)
}

// AllowLocalFlag is the flag that opens it. THE ONE HOME of its name.
const AllowLocalFlag = "--capture-allow-local-network"

// errLocalDestination is the dialer's refusal. It carries no address: nothing a request named is written into a sentence.
type errLocalDestination struct{}

func (errLocalDestination) Error() string { return RefusedLocal() }

var localRanges = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{
		"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
		"::/128", "::1/128", "fe80::/10", "fc00::/7", "fec0::/10",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("netcap: %s: %v", cidr, err))
		}
		out = append(out, network)
	}
	return out
}()

var nat64 = func() *net.IPNet { _, n, _ := net.ParseCIDR("64:ff9b::/96"); return n }()

// isLocalDestination says whether ip is this machine or its local network, by the list above.
func isLocalDestination(ip net.IP) bool {
	if ip == nil {
		return true // not an address the rule can read: refused
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4 // ::ffff:a.b.c.d is a.b.c.d
	} else if len(ip) == net.IPv6len {
		switch {
		case nat64.Contains(ip):
			return isLocalDestination(net.IP(ip[12:16]))
		case isZero(ip[:12]) && !isZero(ip[12:16]) && !ip.Equal(net.IPv6loopback):
			return isLocalDestination(net.IP(ip[12:16])) // ::a.b.c.d, the compatible form
		}
	}
	for _, network := range localRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// destinationControl is the proxy dialer's Control hook: the rule, on the address about to be dialled.
func (p *Proxy) destinationControl(_, address string, _ syscall.RawConn) error {
	if p.opts.ReachLocal {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errLocalDestination{}
	}
	refuses := p.opts.refuses
	if refuses == nil {
		refuses = isLocalDestination
	}
	if refuses(net.ParseIP(host)) {
		return errLocalDestination{}
	}
	return nil
}
