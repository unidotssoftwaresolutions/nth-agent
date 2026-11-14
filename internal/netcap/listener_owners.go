package netcap

import "net"

// ── EVERY LISTENER ON A PORT, AND WHO MADE IT (ANOTHER_ACCOUNT_AND_LIMITS) ───────────────────────────────────────────
//
// ListenersOn says where each listener on a port is bound; ListenerOwnersOn says that and, for each, the account the
// kernel says made it — read from the same tables, with nothing sent anywhere. It answers what a connection cannot: who
// holds a port nobody has connected to yet (adb's server on ::1 beside 127.0.0.1, an emulator's console, the port the
// agent declares for an iPhone's WebDriverAgent), and whether anything listens there at all, without waiting for a dial
// to be refused (which takes a second or more on Windows' loopback).

// Listener is one listening socket: where it is bound, and the account the kernel says made it (UID, in this package's
// numbers; Known is false when the kernel would not say).
type Listener struct {
	IP    net.IP
	UID   int
	Known bool
}

// ListenerOwnersOn names every TCP listener on port on this machine, and who made each. found is false when the table
// could not be read.
func ListenerOwnersOn(port int) (listeners []Listener, found bool) {
	if port <= 0 || port > 65535 {
		return nil, false
	}
	return listenerOwners(port)
}

// Takes says whether this listener takes connections made to ip: it is bound there, or to the wildcard (0.0.0.0 or ::,
// counted for both families, since a dual-stack :: takes IPv4 too).
func (l Listener) Takes(ip net.IP) bool { return l.IP.Equal(ip) || l.IP.IsUnspecified() }

func addressesOf(listeners []Listener) []net.IP {
	var addresses []net.IP
	for _, l := range listeners {
		addresses = append(addresses, l.IP)
	}
	return addresses
}
