//go:build !darwin && !linux && !windows

package ui

import "os"

// TerminalWidth is never a terminal on a system this agent is not built for: nothing is folded there.
func TerminalWidth(*os.File) (int, bool) { return 0, false }
