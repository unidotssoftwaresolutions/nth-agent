//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableEscapes switches a Windows console into reading escape codes (ENABLE_VIRTUAL_TERMINAL_PROCESSING), and answers
// false when it cannot — an older console — which turns colour off for that output and leaves every word of it in place.
func enableEscapes(f *os.File) bool {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
