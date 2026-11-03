//go:build windows

package state

import (
	"errors"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/private"
)

// holdFile takes an exclusive lock on path's first byte (LockFileEx): the same one-connect-per-folder rule flock holds on
// macOS and Linux (connectlock_unix.go). Windows releases it when the process ends.
func holdFile(path string) (func(), error) {
	unlock, err := private.LockFile(path)
	if errors.Is(err, private.ErrLocked) {
		return nil, ErrAnotherConnect
	}
	return unlock, err
}
