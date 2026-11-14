//go:build darwin || linux

package builds

import "syscall"

// freeBytesAt is the disk free for this account where dir is, as the file system reports it.
func freeBytesAt(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
