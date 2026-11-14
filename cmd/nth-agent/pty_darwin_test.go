package main

import (
	"bytes"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openTerminal opens a pseudo-terminal: the side a program writes to, as a person's terminal is to connect, and the side
// that reads what the person would see.
func openTerminal(t *testing.T) (terminal, screen *os.File) {
	t.Helper()
	screen, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("a pseudo-terminal could not be opened to stand for a person's terminal: %v", err)
	}
	t.Cleanup(func() { screen.Close() })
	fd := int(screen.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatalf("granting the pseudo-terminal: %v", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatalf("unlocking the pseudo-terminal: %v", err)
	}
	name := make([]byte, 128)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		t.Fatalf("naming the pseudo-terminal: %v", errno)
	}
	terminal, err = os.OpenFile(string(name[:bytes.IndexByte(name, 0)]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening the pseudo-terminal's own side: %v", err)
	}
	return terminal, screen
}
