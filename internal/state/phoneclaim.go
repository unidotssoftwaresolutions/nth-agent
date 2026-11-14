package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ── ONE PHONE, ONE connect, ON THE WHOLE MACHINE (ANOTHER_ACCOUNT_AND_LIMITS) ────────────────────────────────────────
//
// connect.lock holds the state folder, and the folder follows HOME (configdir.go). A second connect of this account
// started with another HOME, XDG_CONFIG_HOME or LOCALAPPDATA, or a connect of another account that shares a root-run adb
// server or the system's usbmuxd, saw the same phone and offered it under a second node identity, perhaps to another
// workspace: one lease's capture could record another lease's run, and a session from one could drive the phone during
// the other. So each phone is claimed on the machine itself, in a place that does not depend on HOME or on the account,
// before connect offers it; a phone another connect holds is not offered. The system lets a claim go when its process
// ends, so a crash never leaves a phone held.
//
// WHERE: on a Mac and on Linux, a file in /tmp per phone, flock'd — /tmp is the machine's, not the account's (TMPDIR is an
// account's own folder on a Mac). The file is made readable by every account, so another one can ask for the same lock;
// one found already there is opened read-only and never written, chmod'ed or followed through a link. On Windows, a
// named mutex in the machine's Global namespace.

// ErrPhoneHeld is a phone another connect on this machine holds.
var ErrPhoneHeld = errors.New("another connect on this machine already offers this phone (one of yours with another " +
	"state folder, or one another account runs)")

// HoldPhone claims serial for this connect on this whole machine. release gives it back.
func HoldPhone(serial string) (release func(), err error) { return holdPhoneIn(phoneClaimsDir, serial) }

// claimName is the name a phone's claim has: its serial's hash, so any serial makes a plain, bounded file name.
func claimName(serial string) string {
	sum := sha256.Sum256([]byte(serial))
	return "nth-agent-phone-" + hex.EncodeToString(sum[:16])
}
