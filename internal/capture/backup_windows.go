//go:build windows

package capture

import "golang.org/x/sys/windows"

// excludeFromBackups on Windows: the folder lives under Local AppData, which does not roam with a profile and which File
// History leaves out unless a person adds it. It holds a CACHEDIR.TAG too, for the backup tools that honour one, and
// Windows Search is told not to index it.
func excludeFromBackups(root string) error {
	if err := writeCacheDirTag(root); err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(path)
	if err != nil {
		return err
	}
	return windows.SetFileAttributes(path, attributes|windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED)
}
