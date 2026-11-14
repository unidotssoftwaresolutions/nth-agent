// Package private keeps a folder or a file to the account this agent runs as: the capture CA's key, the capture files, the
// node id, the builds this machine fetched.
//
// ON macOS AND LINUX that is the file's mode: 0700 for a folder, 0600 for a file, and anything a group or other users may
// read, write or enter is refused (private_unix.go).
//
// ON WINDOWS a mode means nothing — os.Chmod there only sets or clears read-only — so the same rule is an access list:
// one entry, full control for this account, and nothing inherited from the folder above (private_windows.go). A folder's
// entry is inherited by everything made in it, so a file is private from the moment it exists, and is made so again by
// name. A path whose access list names any other account, or which has none (no list is open to everyone), is refused.
package private

import "errors"

// ErrLocked is a lock another process holds (LockFile, on Windows).
var ErrLocked = errors.New("another process holds this lock")
