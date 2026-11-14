package state

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — one connect per state folder, and one node identity per folder, minted once.

// THE RACE, HELD WITH A BARRIER: every racer has decided to mint before any of them writes. They must all come away with
// the same id — the one that reached the disk first — and the file must hold it.
func TestRacingFirstStartsAgreeOnOneIdentity(t *testing.T) {
	dir := t.TempDir()
	const racers = 8
	var arrived sync.WaitGroup
	arrived.Add(racers)
	release := make(chan struct{})
	beforeMinting = func() { arrived.Done(); <-release }
	t.Cleanup(func() { beforeMinting = nil })

	ids := make([]string, racers)
	errs := make([]error, racers)
	var done sync.WaitGroup
	for i := 0; i < racers; i++ {
		done.Add(1)
		go func(i int) { defer done.Done(); ids[i], errs[i] = LoadOrCreateNodeID(dir) }(i)
	}
	arrived.Wait()
	close(release)
	done.Wait()

	stored, _ := os.ReadFile(filepath.Join(dir, "nth-agent", "node-id"))
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("racer %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] || ids[i] != strings.TrimSpace(string(stored)) {
			t.Fatalf("two first starts on one folder came away with different identities: %v (file holds %q)", ids, stored)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "nth-agent"))
	if len(entries) != 1 {
		t.Fatalf("nothing but node-id is left behind: %v", entries)
	}
}
