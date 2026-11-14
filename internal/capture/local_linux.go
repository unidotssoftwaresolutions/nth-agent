//go:build linux

package capture

import "syscall"

// networkFileSystems are the statfs magic numbers of the file systems Linux reaches over a network (linux/magic.h, and
// the cluster file systems' own).
var networkFileSystems = map[int64]string{
	0x6969: "nfs", 0x517B: "smb", 0xFF534D42: "cifs", 0xFE534D42: "smb2", 0x73757245: "coda", 0x5346414F: "afs",
	0x01021997: "9p", 0x00C36400: "ceph", 0x01161970: "gfs2", 0x7461636F: "ocfs2", 0x0BD00BD0: "lustre", 0x47504653: "gpfs",
}

// storageOfFolder asks this Linux kernel which file system dir is on (statfs's f_type).
func storageOfFolder(dir string) (string, bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return "", false, err
	}
	if kind, network := networkFileSystems[int64(st.Type)&0xFFFFFFFF]; network {
		return kind, true, nil
	}
	return "local", false, nil
}
