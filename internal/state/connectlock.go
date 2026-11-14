package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ── ONE connect PER STATE FOLDER (A_MACHINE_THAT_STAYS_THE_CUSTOMERS) ──────────────────────────────────────────
//
// The state folder holds this machine's node identity and its capture store. Two connects on one folder — easy to get by
// starting a second one for another workspace — offered the same phones under two identities, and one lease's capture
// could record another lease's run. So connect holds the folder for as long as it runs: a lock on connect.lock, which the
// system releases if the process dies, so a crash never leaves the folder held. A second connect is refused before it
// registers anything.

// ErrAnotherConnect is a second connect on a folder another one holds.
var ErrAnotherConnect = errors.New("another npx nthcase-agent connect is already running on this machine for this account — " +
	"stop it first (Ctrl-C in its terminal), then start this one. Two would offer the same phones to the farm twice")

// HoldConnect takes the state folder in configDir for this connect, or answers ErrAnotherConnect. release gives it back.
func HoldConnect(configDir string) (release func(), err error) {
	dir := filepath.Join(configDir, "nth-agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the config directory: %w", err)
	}
	return holdFile(filepath.Join(dir, "connect.lock"))
}
