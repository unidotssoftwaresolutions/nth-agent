//go:build darwin || linux

package termwrap

import (
	"os"

	"golang.org/x/sys/unix"
)

// terminalWidth is the width of the terminal f is, now, and whether f is a terminal at all: one that answers the size
// question. A file, a pipe or the null device does not.
func terminalWidth(f *os.File) (int, bool) {
	conn, err := f.SyscallConn()
	if err != nil {
		return 0, false
	}
	var size *unix.Winsize
	var asked error
	if err := conn.Control(func(fd uintptr) { size, asked = unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ) }); err != nil || asked != nil || size.Col == 0 {
		return 0, false
	}
	return int(size.Col), true
}
