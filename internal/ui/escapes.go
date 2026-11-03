//go:build !windows

package ui

import "os"

// enableEscapes readies a terminal to read escape codes, and answers whether it will.
//
// On macOS and Linux a terminal reads them as it is, so there is nothing to do. A Windows console must first be switched
// into reading them: escapes_windows.go flips the console's mode and answers false when it cannot, which turns colour off
// for that output and leaves every word of it in place.
func enableEscapes(*os.File) bool { return true }
