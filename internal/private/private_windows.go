//go:build windows

package private

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Folder makes dir (and its parents) and gives dir an access list for this account alone, which everything made in it
// inherits.
func Folder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return restrict(dir, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

// File gives path an access list for this account alone.
func File(path string) error { return restrict(path, windows.NO_INHERITANCE) }

// restrict replaces path's access list with one entry — full control for this account — and protects it, so nothing the
// folder above grants is inherited.
func restrict(path string, inheritance uint32) error {
	me, err := thisAccount()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(me),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// Problem says why path is not kept to this account — "open to S-1-5-32-545" — or "" when it is.
func Problem(path string, _ os.FileInfo) string {
	sids, err := Readers(path)
	if err != nil {
		return "its access list cannot be read"
	}
	if sids == nil {
		return "it has no access list, so every account may open it"
	}
	me, err := thisAccount()
	if err != nil {
		return "this account cannot be named"
	}
	for _, sid := range sids {
		if sid != me.String() {
			return "open to " + sid
		}
	}
	return ""
}

// Readers is every account path's access list lets in, as SIDs — nil when it has no list at all.
func Readers(path string) ([]string, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return nil, err
	}
	if dacl == nil {
		return nil, nil
	}
	sids := []string{}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return nil, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue // a deny entry lets nobody in
		}
		sids = append(sids, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}
	return sids, nil
}

func thisAccount() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

// LockFile opens path (making it, private) and takes an exclusive lock on its first byte, or answers ErrLocked when another
// process holds it. Windows releases the lock when the handle closes, and when the process ends — a crash never leaves it
// held. unlock releases it and closes the file.
func LockFile(path string) (unlock func(), err error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("a lock must be a regular file: " + path)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
