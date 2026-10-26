package capture

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ── THE CAPTURE FOLDER STAYS OUT OF BACKUPS (A_MACHINE_THAT_STAYS_THE_CUSTOMERS) ─────────────────────────────────
//
// The capture folder holds the CA's private key and, for an hour at most, masked capture files. An OS backup copied them
// off the machine, onto disks and shares other people can read, where `capture disable` and the hour never reach. So:
//
//   - on macOS the folder carries Time Machine's STICKY exclusion — the extended attribute
//     com.apple.metadata:com_apple_backup_excludeItem holding a binary plist of "com.apple.backupd", which is what
//     `tmutil addexclusion` (without -p) writes. It is set on the folder itself, so it travels with it; no tmutil runs and
//     no setting of the Mac changes;
//   - on Linux the folder holds a CACHEDIR.TAG (bford.info/cachedir), the mark that backup tools which honour it (restic,
//     borg, tar --exclude-caches, Déjà Dup among others) leave out. Tools that do not honour it still copy it.
//
// Set when `capture enable` makes the folder, and again each time connect starts. WHAT IT CANNOT DO: reach a copy a backup
// already made, or a backup tool that ignores the mark. The texts say so (texts.go, README).

// cacheDirTag is the file name and the signature line the specification requires, word for word.
const (
	cacheDirTagName      = "CACHEDIR.TAG"
	cacheDirTagSignature = "Signature: 8a477f597d28d172789f06886806bc55"
)

// ErrNotExcluded is a capture folder whose backup exclusion could not be set.
var ErrNotExcluded = errors.New("this machine's backups could not be told to leave the capture folder out")

// keepOutOfBackups marks root for this system's backups (backup_darwin.go, backup_linux.go).
func keepOutOfBackups(root string) error {
	if err := excludeFromBackups(root); err != nil {
		return errors.Join(ErrNotExcluded, err)
	}
	return nil
}

// writeCacheDirTag writes root's CACHEDIR.TAG, once: a tag already there is left as it is.
func writeCacheDirTag(root string) error {
	path := filepath.Join(root, cacheDirTagName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(cacheDirTagSignature + "\n" +
		"# This folder holds nth-agent's network capture: its CA's key and short-lived capture files. Backups leave it out.\n")
	return errors.Join(writeErr, file.Close())
}
