package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On Linux: the capture folder holds a CACHEDIR.TAG whose first line is the specification's signature, byte for byte —
// a tool that honours the tag checks exactly that — and a tag already there is left alone.
func TestTheCacheDirTagIsTheOneBackupToolsLookFor(t *testing.T) {
	root := t.TempDir()
	if err := writeCacheDirTag(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "CACHEDIR.TAG"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "Signature: 8a477f597d28d172789f06886806bc55") {
		t.Fatalf("the tag must open with the signature: %q", data)
	}
	if err := writeCacheDirTag(root); err != nil {
		t.Fatalf("a second start keeps the tag it finds: %v", err)
	}
}
