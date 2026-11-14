//go:build windows

package capture

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// storageOfFolder asks this Windows whether dir's volume is a network drive (GetDriveType's DRIVE_REMOTE), or dir a UNC
// path.
func storageOfFolder(dir string) (string, bool, error) {
	if strings.HasPrefix(dir, `\\`) {
		return "a network share", true, nil
	}
	root := filepath.VolumeName(dir) + `\`
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", false, err
	}
	if windows.GetDriveType(path) == windows.DRIVE_REMOTE {
		return "a network drive", true, nil
	}
	return "local", false, nil
}
