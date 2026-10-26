//go:build darwin || linux

package state

import (
	"errors"
	"os"
	"syscall"
)

// holdFile takes an flock on path: the capture's per-phone lock, for the whole folder (internal/capture/lock_unix.go).
func holdFile(path string) (func(), error) {
	// O_NOFOLLOW: a link left at the lock's name is refused, never followed.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("the state folder's lock must be a regular file: " + path)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAnotherConnect
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
