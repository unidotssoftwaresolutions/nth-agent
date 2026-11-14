package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/state"
)

// THE STABLE-IDENTITY SEAL: two starts, one id. The farm's ownership is write-once and an unknown id
// is a NEW row — a fresh id per start would leave one unreclaimable row per restart.
func TestTheNodeIdSurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first, err := state.LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	second, err := state.LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if first != second {
		t.Fatalf("the identity must survive a restart: %q then %q", first, second)
	}
	if len(first) != 36 {
		t.Fatalf("the farm's node ids are Guids, got %q", first)
	}
}

// Losing the file costs ONE ORPHAN ROW, not data: a new identity is minted and life continues.
func TestLosingTheFileMintsAFreshIdentity(t *testing.T) {
	dir := t.TempDir()
	first, _ := state.LoadOrCreateNodeID(dir)
	if err := os.Remove(filepath.Join(dir, "nth-agent", "node-id")); err != nil {
		t.Fatal(err)
	}
	second, err := state.LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatalf("after deletion: %v", err)
	}
	if first == second {
		t.Fatal("a deleted file cannot resurrect its id — a fresh one is minted (one orphan row, no data lost)")
	}
}

// THE ONE-FACT RULE: the state directory holds the node id and NOTHING else — no token, no inventory.
func TestTheStateDirectoryHoldsExactlyOneFact(t *testing.T) {
	dir := t.TempDir()
	if _, err := state.LoadOrCreateNodeID(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "nth-agent"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "node-id" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("exactly one file, the id: got %v", names)
	}
}
