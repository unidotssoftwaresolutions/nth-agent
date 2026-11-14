//go:build windows

package state

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// phoneClaimsDir is not a folder on Windows: the claim is a named mutex in the machine's Global namespace.
const phoneClaimsDir = ""

func holdPhoneIn(_, serial string) (func(), error) {
	name, err := windows.UTF16PtrFromString(`Global\` + claimName(serial))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		_ = windows.CloseHandle(handle)
		return nil, ErrPhoneHeld
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return nil, ErrPhoneHeld // another account's: its mutex will not even open for this one
	case err != nil:
		return nil, fmt.Errorf("the agent could not claim this phone on this machine (%v), so it does not offer it", err)
	}
	return func() { _ = windows.CloseHandle(handle) }, nil
}
