//go:build !(darwin || linux)

package capture

import (
	"context"
	"errors"
)

// On a system this agent does not ship for there is no phone lock, and so no capture: `capture enable` refuses before
// anything is made (see Supported).

type phoneLock struct{}

var errPhoneBusy = errors.New("network capture is not available on this system")

func (s Store) tryLockPhone(string) (*phoneLock, error) { return nil, errPhoneBusy }

func (s Store) lockPhone(context.Context, string) (*phoneLock, error) { return nil, errPhoneBusy }

func (l *phoneLock) unlock() {}

const lockingSupported = false
