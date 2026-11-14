package state

import (
	"errors"
	"strings"
	"testing"
)

// ONE PHONE, ONE CONNECT, ON THE WHOLE MACHINE (ANOTHER_ACCOUNT_AND_LIMITS). The one-connect rule was the state folder's,
// and the folder follows HOME: a second connect of the same account with another HOME, or one of another account sharing
// a root-run adb server or the system's usbmuxd, offered the same phone under a second identity, so one lease's capture
// could record another's run. A phone is claimed on the machine itself, wherever the folder is.
func TestAPhoneIsClaimedByOneConnectOnTheWholeMachine(t *testing.T) {
	dir := t.TempDir()
	release, err := holdPhoneIn(dir, "R5CT30ABCDE")
	if err != nil {
		t.Fatalf("the first claim is taken: %v", err)
	}
	if _, err := holdPhoneIn(dir, "R5CT30ABCDE"); err == nil {
		t.Fatal("a second claim on the same phone, from another state folder or account, must be refused")
	} else if !errors.Is(err, ErrPhoneHeld) || !strings.Contains(err.Error(), "another connect") {
		t.Fatalf("the refusal says another connect holds it: %v", err)
	}
	other, err := holdPhoneIn(dir, "R5CT31FGHIJ")
	if err != nil {
		t.Fatalf("another phone is claimed on its own: %v", err)
	}
	other()
	release()
	again, err := holdPhoneIn(dir, "R5CT30ABCDE")
	if err != nil {
		t.Fatalf("a phone given back is claimed again: %v", err)
	}
	again()
}
