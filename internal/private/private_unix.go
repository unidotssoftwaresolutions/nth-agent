//go:build !windows

package private

import (
	"fmt"
	"os"
)

// Folder makes dir (and its parents) and leaves dir itself at 0700, whatever the umask or an earlier mode said.
func Folder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// File leaves path at 0600.
func File(path string) error { return os.Chmod(path, 0o600) }

// Problem says why path is not kept to this account — "mode 0644" — or "" when it is. info is path's Lstat.
func Problem(_ string, info os.FileInfo) string {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Sprintf("mode %04o", perm)
	}
	return ""
}
