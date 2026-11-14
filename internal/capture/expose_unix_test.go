//go:build !windows

package capture

import (
	"os"
	"testing"
)

// exposeToOthers makes path readable by other users of this machine: a group-readable mode here.
func exposeToOthers(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	must(t, os.Chmod(path, mode))
}
