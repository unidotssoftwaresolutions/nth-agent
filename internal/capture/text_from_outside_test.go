package capture

import (
	"strings"
	"testing"
	"time"
)

// TEXT FROM OUTSIDE (WHAT_A_LEASE_CAN_ASK). `capture setup` names the phone by its own model, read from the phone, and kept
// it for doctor and `capture status`; a model holding an escape code reached the terminal both times. What setup prints, and
// what the status says of a phone, is made fit for a terminal first.
const hostileModel = "SM\x1b]52;c;aGk=\x07A546B\u009b2J"

func TestSetupPrintsNoEscapeThePhoneSupplied(t *testing.T) {
	phone := &fakePhone{serial: "R5CT30ABCDE", manufacturer: "samsung\x1b[2J", model: hostileModel}
	m := newMachine(t)
	m.attach(phone)
	run := runSetup(t, m, phone.serial, 100*time.Millisecond)
	for _, bad := range []string{"\x1b", "\x07", "\u009b"} {
		if strings.Contains(run.out, bad) {
			t.Fatalf("capture setup printed %q from the phone's own words:\n%q", bad, run.out)
		}
	}
	if !strings.Contains(run.out, "R5CT30ABCDE") {
		t.Fatalf("the phone is still named:\n%s", run.out)
	}
}

func TestTheStatusSaysNoEscapeAStoredModelHolds(t *testing.T) {
	m := newMachine(t, &fakePhone{serial: "R5CT30ABCDE", manufacturer: "samsung", model: hostileModel})
	status := ReadStatus(m.store, m.clock.Now())
	joined := strings.Join(status.Lines, "\n")
	for _, bad := range []string{"\x1b", "\x07", "\u009b"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("the capture status says %q from a stored model:\n%q", bad, joined)
		}
	}
	if !strings.Contains(joined, "R5CT30ABCDE (SM") {
		t.Fatalf("the phone and its model are still said:\n%s", joined)
	}
}
