//go:build windows

package capture

import (
	"context"
	"errors"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/private"
)

// ONE PROCESS CHANGES A PHONE AT A TIME, as on macOS and Linux (lock_unix.go): whichever is changing a phone holds that
// phone's lock — here an exclusive lock on its file's first byte (LockFileEx), which Windows releases if the process dies.

type phoneLock struct{ unlockFile func() }

var errPhoneBusy = errors.New("another nth-agent process is changing this phone right now")

// tryLockPhone takes serial's lock if nobody holds it, or answers errPhoneBusy.
func (s Store) tryLockPhone(serial string) (*phoneLock, error) {
	if err := ownFolder(s.locksDir()); err != nil {
		return nil, err
	}
	unlock, err := private.LockFile(s.lockPath(serial))
	if errors.Is(err, private.ErrLocked) {
		return nil, errPhoneBusy
	}
	if err != nil {
		return nil, err
	}
	return &phoneLock{unlockFile: unlock}, nil
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
	if l == nil || l.unlockFile == nil {
		return
	}
	l.unlockFile()
	l.unlockFile = nil
}

// lockingSupported says whether this system can hold a phone's lock.
const lockingSupported = true
