package termwrap_test

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// openTerminal opens a pseudo-terminal and returns the side a program writes to, as a person's terminal is to connect.
func openTerminal(t *testing.T) *os.File {
	t.Helper()
	control, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("a pseudo-terminal could not be opened to stand for a person's terminal: %v", err)
	}
	t.Cleanup(func() { control.Close() })
	fd := int(control.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlocking the pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("naming the pseudo-terminal: %v", err)
	}
	terminal, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening the pseudo-terminal's own side: %v", err)
	}
	t.Cleanup(func() { terminal.Close() })
	return terminal
}
