//go:build darwin

package capture

import (
	"strings"
	"syscall"
)

// mntLocal is MNT_LOCAL: the file system is on this machine (sys/mount.h).
const mntLocal = 0x00001000

// storageOfFolder asks this Mac whether dir's file system is local (statfs's MNT_LOCAL), and names it.
func storageOfFolder(dir string) (string, bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return "", false, err
	}
	var name strings.Builder
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		name.WriteByte(byte(c))
	}
	return name.String(), st.Flags&mntLocal == 0, nil
}
