//go:build darwin

package capture

import "golang.org/x/sys/unix"

// timeMachineExclusion is the value `tmutil addexclusion` writes: a binary plist holding the one string
// "com.apple.backupd" — header, the string object, its offset, and the 32-byte trailer.
var timeMachineExclusion = append(append([]byte("bplist00\x5f\x10\x11"), "com.apple.backupd"...),
	0x08, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x1c)

const timeMachineExclusionAttribute = "com.apple.metadata:com_apple_backup_excludeItem"

func excludeFromBackups(root string) error {
	return unix.Setxattr(root, timeMachineExclusionAttribute, timeMachineExclusion, 0)
}
