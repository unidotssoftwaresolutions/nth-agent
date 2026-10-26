//go:build !darwin && !linux

package termwrap

import "os"

// terminalWidth is never a terminal on a system this agent is not built for: nothing is folded there.
func terminalWidth(*os.File) (int, bool) { return 0, false }
