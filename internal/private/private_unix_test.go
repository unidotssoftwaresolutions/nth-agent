//go:build !windows

package private_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/private"
)

// On macOS and Linux a private folder is 0700 and a private file 0600, and a mode a group or another user can read is
// named (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE moved the rule here from capture and the CA, unchanged).
func TestOnUnixPrivateIsTheOwnersModeAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "capture")
	if err := private.Folder(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := private.Folder(dir); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(dir)
	if info.Mode().Perm() != 0o700 || private.Problem(dir, info) != "" {
		t.Fatalf("a private folder is 0700 whatever it was: %v", info.Mode().Perm())
	}
	file := filepath.Join(dir, "key")
	if err := os.WriteFile(file, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Lstat(file)
	if got := private.Problem(file, info); got != "mode 0644" {
		t.Fatalf("a file others can read is named by its mode, got %q", got)
	}
	if err := private.File(file); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Lstat(file)
	if info.Mode().Perm() != 0o600 || private.Problem(file, info) != "" {
		t.Fatalf("a private file is 0600: %v", info.Mode().Perm())
	}
}
