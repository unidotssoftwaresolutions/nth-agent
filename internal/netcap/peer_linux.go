//go:build linux

package netcap

import (
	"encoding/binary"
	"net"
	"os"
)

// peerUID asks this Linux kernel (see peer_procnet.go): /proc/net/tcp, then /proc/net/tcp6 for a dual-stack socket.
func peerUID(peer, us *net.TCPAddr) (int, bool) {
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		contents, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		if uid, found := uidInProcNet(contents, peer, us, binary.NativeEndian); found {
			return uid, true
		}
	}
	return 0, false
}

// listenerUID asks this Linux kernel which account holds the listening socket at at: /proc/net/tcp, then /proc/net/tcp6
// for a dual-stack listener.
func listenerUID(at *net.TCPAddr) (int, bool) {
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		contents, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		if uid, found := listenerUIDInProcNet(contents, at, binary.NativeEndian); found {
			return uid, true
		}
	}
	return 0, false
}
