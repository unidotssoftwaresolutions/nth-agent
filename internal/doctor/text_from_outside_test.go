package doctor

import (
	"bytes"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
)

// TEXT FROM OUTSIDE (WHAT_A_LEASE_CAN_ASK). doctor prints what it observed — a phone's id, an adb state, the phone set up for
// capture with its model — and printed each as it came: an escape code a phone or a tool put in one reached the terminal.
// Every observed value is made fit for a terminal before it is printed; --json stays escaped JSON.
func TestDoctorPrintsNoEscapeAPhoneOrAToolSupplied(t *testing.T) {
	report := Report{
		Checks: []Check{{Name: "Phones attached", State: Missing,
			Detail: "R5CT99\x1b[2JZZ (android) is \"unauthorized\x1b]0;owned\x07\"", Remedy: "Tap Allow on the device."}},
		Capture: &capture.Status{Lines: []string{"network capture: on", "  phone set up: R5CT30ABCDE (Pix\x1b]52;c;aGk=\x07el \u009b2J8)"}},
	}
	var out bytes.Buffer
	if err := Human(&out, report, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\x1b", "\x07", "\u009b"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("doctor printed %q from a phone's or a tool's own words:\n%q", bad, out.String())
		}
	}
	if !strings.Contains(out.String(), "R5CT99") || !strings.Contains(out.String(), "R5CT30ABCDE (Pix") {
		t.Fatalf("what was observed is still said, without what was taken out:\n%s", out.String())
	}
}
