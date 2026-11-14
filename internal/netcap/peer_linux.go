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

// listenAddresses asks this Linux kernel where each listener on port is bound: /proc/net/tcp and /proc/net/tcp6, both.
func listenAddresses(port int) ([]net.IP, bool) {
	listeners, ok := listenerOwners(port)
	return addressesOf(listeners), ok
}

// listenerOwners asks this Linux kernel for every listener on port, and the account that made each.
func listenerOwners(port int) ([]Listener, bool) {
	var all []Listener
	read := false
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		contents, err := os.ReadFile(table)
		if err != nil {
			continue // a kernel without IPv6 has no tcp6
		}
		listeners, ok := listenersInProcNet(contents, port, binary.NativeEndian)
		if !ok {
			return nil, false
		}
		read = true
		all = append(all, listeners...)
	}
	return all, read
}
