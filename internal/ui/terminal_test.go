//go:build darwin || linux

package ui_test

import (
	"testing"

	"golang.org/x/sys/unix"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// A TERMINAL IS FOLDED, AT ITS OWN WIDTH, read as it is now: a pseudo-terminal the test opens and sizes itself, then
// resizes, the way a person drags the window wider.
//
// MOVED FROM termwrap (AN_AGENT_THAT_SPEAKS_PLAINLY), and ONE NUMBER CHANGED BECAUSE ITS SUBJECT DID: a terminal made 132
// columns wide is now folded at ui.Measure (100), the widest a line of prose is allowed to run. Termwrap folded at the full
// width; the measure is the new rule, sealed here at the size that shows it.
func TestATerminalIsFoldedAtItsOwnWidth(t *testing.T) {
	terminal := openTerminal(t)
	size := func(cols uint16) {
		t.Helper()
		if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: cols}); err != nil {
			t.Fatalf("sizing the pseudo-terminal: %v", err)
		}
	}
	size(60)
	mode := ui.For(terminal, ui.Asked{Env: environment(nil)})
	if !mode.Terminal {
		t.Fatalf("a pseudo-terminal is a terminal, got %+v", mode)
	}
	console := ui.NewConsole(terminal, mode)
	if got := console.Width(); got != 60 {
		t.Fatalf("folded at the terminal's 60 columns, got %d", got)
	}
	size(80)
	if got := console.Width(); got != 80 {
		t.Fatalf("a terminal made wider is folded at its new width, got %d", got)
	}
	size(132)
	if got := console.Width(); got != ui.Measure {
		t.Fatalf("a terminal wider than the measure is folded at the measure (%d), got %d", ui.Measure, got)
	}
}

// ON A REAL TERMINAL COLOUR IS ON — and each rule that turns it off turns it off there too, where it matters.
func TestColourIsOnForARealTerminalAndOffForEachRuleThatSaysSo(t *testing.T) {
	terminal := openTerminal(t)
	if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 80}); err != nil {
		t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
	if mode := ui.For(terminal, ui.Asked{Env: environment(map[string]string{"TERM": "xterm-256color"})}); !mode.Color {
		t.Fatalf("a terminal, with nothing turning colour off, is coloured: %+v", mode)
	}
	for name, asked := range map[string]ui.Asked{
		"NO_COLOR=":          {Env: environment(map[string]string{"NO_COLOR": ""})},
		"NO_COLOR=0":         {Env: environment(map[string]string{"NO_COLOR": "0"})},
		"--no-color":         {NoColor: true, Env: environment(nil)},
		"TERM=dumb":          {Env: environment(map[string]string{"TERM": "dumb"})},
		"--json":             {JSON: true, Env: environment(nil)},
		"NO_COLOR + --json":  {JSON: true, Env: environment(map[string]string{"NO_COLOR": "1"})},
		"TERM=dumb + LANG=C": {Env: environment(map[string]string{"TERM": "dumb", "LANG": "C"})},
	} {
		if mode := ui.For(terminal, asked); mode.Color {
			t.Errorf("%s: colour must be off on a terminal too, got %+v", name, mode)
		}
	}
	if mode := ui.For(terminal, ui.Asked{Env: environment(map[string]string{"TERM": "dumb"})}); !mode.ASCII {
		t.Errorf("a dumb terminal gets the plain-letter marks, got %+v", mode)
	}
}
