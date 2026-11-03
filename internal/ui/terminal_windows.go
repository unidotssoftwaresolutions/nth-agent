//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// TerminalWidth is the width of the console window f is, now, and whether f is a console at all: one that answers the
// size question. A file, a pipe or the null device does not.
func TerminalWidth(f *os.File) (int, bool) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(f.Fd()), &info); err != nil {
		return 0, false
	}
	width := int(info.Window.Right-info.Window.Left) + 1
	if width <= 0 {
		return 0, false
	}
	return width, true
}
