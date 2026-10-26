//go:build darwin || linux

package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ── ONE PROCESS CHANGES A PHONE AT A TIME ───────────────────────────────────────────────────────────────────────
//
// The running `connect` and a `capture setup` or `capture disable` typed in another terminal can each set, clear and read
// a phone's proxy. Whichever is changing a phone holds that phone's lock — an flock on its own file, which the system
// releases if the process dies, so a crash never leaves a phone locked. A capture holds it from the moment it starts until
// the phone is handed back.

type phoneLock struct{ file *os.File }

var errPhoneBusy = errors.New("another nth-agent process is changing this phone right now")

// tryLockPhone takes serial's lock if nobody holds it, or answers errPhoneBusy.
func (s Store) tryLockPhone(serial string) (*phoneLock, error) {
	if err := ownFolder(s.locksDir()); err != nil {
		return nil, err
	}
	path := s.lockPath(serial)
	// O_NOFOLLOW: a link left at a lock's name is refused, never followed (the folder is 0700, so only this user could
	// have left one — still not followed).
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("a phone's lock file must be a regular file: " + filepath.Base(path))
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errPhoneBusy
		}
		return nil, err
	}
	return &phoneLock{file: file}, nil
}

// lockPhone waits, until ctx ends, for serial's lock.
func (s Store) lockPhone(ctx context.Context, serial string) (*phoneLock, error) {
	for {
		lock, err := s.tryLockPhone(serial)
		if !errors.Is(err, errPhoneBusy) {
			return lock, err
		}
		select {
		case <-ctx.Done():
			return nil, errPhoneBusy
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (l *phoneLock) unlock() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// lockingSupported says whether this system can hold a phone's lock — the systems this agent ships for.
const lockingSupported = true
