//go:build darwin || linux

package termwrap_test

import (
	"testing"

	"golang.org/x/sys/unix"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/termwrap"
)

// A TERMINAL IS FOLDED, AT ITS OWN WIDTH, read as it is now: a pseudo-terminal the test opens and sizes itself, then
// resizes, the way a person drags the window wider.
func TestATerminalIsFoldedAtItsOwnWidth(t *testing.T) {
	terminal := openTerminal(t)
	size := func(cols uint16) {
		t.Helper()
		if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: cols}); err != nil {
			t.Fatalf("sizing the pseudo-terminal: %v", err)
		}
	}
	size(60)
	w, ok := termwrap.For(terminal).(*termwrap.Writer)
	if !ok {
		t.Fatalf("a terminal is folded, got %T", termwrap.For(terminal))
	}
	if got := w.Width(); got != 60 {
		t.Fatalf("folded at the terminal's 60 columns, got %d", got)
	}
	size(132)
	if got := w.Width(); got != 132 {
		t.Fatalf("a terminal made wider is folded at its new width, got %d", got)
	}
}
