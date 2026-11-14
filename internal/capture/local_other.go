//go:build !darwin && !linux && !windows

package capture

// storageOfFolder has no file system to ask here, and capture does not run here (Supported).
func storageOfFolder(string) (string, bool, error) { return "", false, nil }
