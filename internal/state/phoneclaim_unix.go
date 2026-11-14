//go:build darwin || linux

package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// phoneClaimsDir is the machine's /tmp, the same for every account and every HOME.
const phoneClaimsDir = "/tmp"

func holdPhoneIn(dir, serial string) (func(), error) {
	path := filepath.Join(dir, claimName(serial)+".lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	switch {
	case err == nil:
		// Made just now, by this process: readable by every account, so another connect can ask for the same lock.
		_ = file.Chmod(0o644)
	case errors.Is(err, fs.ErrExist):
		// Another connect's, or one left by an earlier run: opened to read, never written, never followed through a link.
		file, err = os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, fmt.Errorf("%w — its claim, %s, cannot be opened by this account (%v)", ErrPhoneHeld, path, err)
		}
	default:
		return nil, fmt.Errorf("the agent could not claim this phone on this machine (%v), so it does not offer it", err)
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("the claim for this phone, %s, is not a plain file, so the agent does not offer it", path)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrPhoneHeld
		}
		return nil, fmt.Errorf("the agent could not claim this phone on this machine (%v), so it does not offer it", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
