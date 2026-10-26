//go:build darwin

package netcap

import (
	"errors"
	"net"
	"syscall"
)

// peerUID asks this Mac's kernel (see peer_pcblist.go). The list can grow between the sysctl's size question and its
// answer, which it says with ENOMEM; it is asked again, a bounded number of times.
func peerUID(peer, us *net.TCPAddr) (int, bool) {
	for attempt := 0; attempt < 3; attempt++ {
		list, err := syscall.Sysctl("net.inet.tcp.pcblist64")
		if errors.Is(err, syscall.ENOMEM) {
			continue
		}
		if err != nil {
			return 0, false
		}
		// syscall.Sysctl drops one trailing NUL byte, which can only be the closing xinpgen's last: the reader stops
		// before it.
		return uidInPCBList([]byte(list), peer, us)
	}
	return 0, false
}

// listenerUID asks this Mac's kernel which account holds the listening socket at at (see peer_pcblist.go).
func listenerUID(at *net.TCPAddr) (int, bool) {
	for attempt := 0; attempt < 3; attempt++ {
		list, err := syscall.Sysctl("net.inet.tcp.pcblist64")
		if errors.Is(err, syscall.ENOMEM) {
			continue
		}
		if err != nil {
			return 0, false
		}
		return listenerUIDInPCBList([]byte(list), at)
	}
	return 0, false
}
