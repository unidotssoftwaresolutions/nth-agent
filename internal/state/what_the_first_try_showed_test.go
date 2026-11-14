package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/state"
)

// WHAT_THE_FIRST_TRY_SHOWED: the file a refused identity names, and the folder doctor names, are the ones the id is
// really kept in — spelled once, here.
func TestTheNodeIdIsKeptInTheFileItsRefusalNames(t *testing.T) {
	dir := t.TempDir()
	id, err := state.LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Folder(dir); got != filepath.Join(dir, "nth-agent") {
		t.Fatalf("the agent's folder is <config>/nth-agent, got %q", got)
	}
	if got := state.NodeIDFile(dir); got != filepath.Join(state.Folder(dir), "node-id") {
		t.Fatalf("the node id is kept in node-id inside the agent's folder, got %q", got)
	}
	data, err := os.ReadFile(state.NodeIDFile(dir))
	if err != nil || strings.TrimSpace(string(data)) != id {
		t.Fatalf("the file NodeIDFile names must hold the id LoadOrCreateNodeID returned (%q): %q %v", id, data, err)
	}
}
