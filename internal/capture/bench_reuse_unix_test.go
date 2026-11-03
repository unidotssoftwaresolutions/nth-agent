//go:build !windows

package capture

import "syscall"

// noReuseAddr turns SO_REUSEADDR off on a socket — a file descriptor here, a handle on Windows.
func noReuseAddr(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
}
