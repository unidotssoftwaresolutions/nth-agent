//go:build windows

package capture

import "syscall"

// noReuseAddr turns SO_REUSEADDR off on a socket handle.
func noReuseAddr(fd uintptr) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
}
