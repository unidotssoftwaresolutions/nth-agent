package netcap

import "net"

// ListenersOn names every local address a TCP listener on port is bound to on this machine — 127.0.0.1, ::1, a network
// address of this machine, or the wildcard (0.0.0.0 or ::) that is all of them — read from the kernel's own table, the one
// the owner checks read: the pcblist64 sysctl on macOS, /proc/net/tcp and tcp6 on Linux, GetExtendedTcpTable on Windows.
// Nothing is sent anywhere, to this machine's own addresses included. found is false when the table could not be read.
func ListenersOn(port int) (addresses []net.IP, found bool) {
	if port <= 0 || port > 65535 {
		return nil, false
	}
	return listenAddresses(port)
}
