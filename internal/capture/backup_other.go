//go:build !darwin && !linux

package capture

import "errors"

// Capture does not run here (Supported), so there is no folder to keep out.
func excludeFromBackups(string) error { return errors.New("not on this system") }
