//go:build linux

package capture

func excludeFromBackups(root string) error { return writeCacheDirTag(root) }
