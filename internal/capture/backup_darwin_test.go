//go:build darwin

package capture

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"howett.net/plist"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — Time Machine copied the capture folder: the CA's private key and the masked
// capture files, kept on a backup disk past `capture disable` and the hour. The folder now carries Time Machine's own
// sticky exclusion, the attribute `tmutil addexclusion` writes, set on the folder itself; no tmutil runs and no setting of
// this Mac changes. Read back here from a temporary folder this test owns.
const excludeAttribute = "com.apple.metadata:com_apple_backup_excludeItem"

func readAttribute(t *testing.T, path string) []byte {
	t.Helper()
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, excludeAttribute, buf)
	if err != nil {
		t.Fatalf("%s carries no Time Machine exclusion: %v", path, err)
	}
	return buf[:n]
}

func TestTheCaptureFolderIsKeptOutOfTimeMachine(t *testing.T) {
	store := StoreIn(t.TempDir())
	if _, err := Enable(store, "bench-mac", "v-test", time.Now(), runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	value := readAttribute(t, store.Root)
	var decoded string
	if _, err := plist.Unmarshal(value, &decoded); err != nil || decoded != "com.apple.backupd" {
		t.Fatalf("the value is the one tmutil writes — a binary plist of \"com.apple.backupd\": %q (%v)", value, err)
	}
	if !bytes.HasPrefix(value, []byte("bplist00")) {
		t.Fatalf("a binary plist, as tmutil writes it: %q", value)
	}
}
