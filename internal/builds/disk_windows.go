//go:build windows

package builds

import "golang.org/x/sys/windows"

// freeBytesAt is the disk free for this account where dir is, as Windows reports it (GetDiskFreeSpaceEx, which counts
// this account's quota).
func freeBytesAt(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
