package capture

import (
	"strings"
	"testing"
)

// THE KEY IS MADE ON THIS MACHINE'S OWN DISK (ANOTHER_ACCOUNT_AND_LIMITS). The capture's key lives in this account's state
// folder, which an administrator can put on a file server (a network home): its bytes would cross the network as it is
// made and at every capture, and the server and its backups would keep copies, readable by whoever runs them. So capture
// is not turned on where the folder is on network storage.
func TestCaptureIsNotTurnedOnWhereItsKeyWouldLeaveThisMachine(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{"nfs", "smbfs", "cifs", "afpfs", "webdav"} {
		err := onThisMachinesDisk(dir, func(string) (string, bool, error) { return kind, true, nil })
		if err == nil {
			t.Fatalf("a state folder on %s must not get a capture key", kind)
		}
		for _, words := range []string{kind, "network", "not turned on"} {
			if !strings.Contains(err.Error(), words) {
				t.Errorf("%s: the refusal must say %q: %v", kind, words, err)
			}
		}
	}
	if err := onThisMachinesDisk(dir, func(string) (string, bool, error) { return "apfs", false, nil }); err != nil {
		t.Fatalf("a folder on this machine's disk is used: %v", err)
	}
	// This machine's own temporary folder is on its own disk, asked of the real system.
	if err := onThisMachinesDisk(dir, nil); err != nil {
		t.Fatalf("the test's own folder is on this machine's disk: %v", err)
	}
}
