package capture

import "fmt"

// ── THE CAPTURE'S KEY IS MADE ON THIS MACHINE'S OWN DISK (ANOTHER_ACCOUNT_AND_LIMITS) ─────────────────────────────────
//
// The key that signs every certificate a captured phone trusts lives in this account's state folder, and an
// administrator can put that folder on a file server: a network home over NFS or SMB. Its bytes would then cross the
// network as it is made and at every capture, and the server and its own backups would keep copies outside `capture
// disable` and readable by whoever runs them — not "readable by you alone". Windows' folder is already the one that does
// not roam (internal/state/configdir.go); on a Mac and on Linux, where the folder is is asked of the file system, and
// capture is not turned on where it is network storage.

// ownLocalFolder is ownFolder, for the folder a capture's key is made in: this account's, and on this machine's disk.
func ownLocalFolder(dir string) error {
	if err := ownFolder(dir); err != nil {
		return err
	}
	return onThisMachinesDisk(dir, nil)
}

// onThisMachinesDisk is nil when dir is on this machine's own disk, and otherwise the refusal. storageOf names dir's
// file system and whether it is network storage; nil = this system's answer (storageOf in local_<system>.go). A TEST SEAM.
func onThisMachinesDisk(dir string, storageOf func(string) (kind string, network bool, err error)) error {
	if storageOf == nil {
		storageOf = storageOfFolder
	}
	kind, network, err := storageOf(dir)
	if err != nil {
		return fmt.Errorf("the agent could not tell whether %s is on this machine's own disk (%v), so capture was not turned on: its key must not leave this machine", dir, err)
	}
	if network {
		return fmt.Errorf("this account's state folder, %s, is on network storage (%s), so capture was not turned on: its key would cross "+
			"the network to a file server, which keeps a copy. Capture needs the folder on this machine's own disk", dir, kind)
	}
	return nil
}
