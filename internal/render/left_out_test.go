package render_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1 — `devices` lists a phone it left out for not being on the cable, how it
// is reached, and the flag; a platform with only such a phone is not "no devices attached".
func TestDevicesListsALeftOutPhoneWithTheFlag(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.WithLeftOut(inventory.Answered(inventory.Android, nil, nil), []inventory.LeftOut{{ID: "emulator-5554", How: "an emulator"}}),
	}
	var out strings.Builder
	if err := render.Human(&out, outcomes, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "no devices attached") ||
		!strings.Contains(out.String(), "LEFT OUT emulator-5554  — an emulator; the agent offers only phones on a USB cable (--offer-uncabled-devices offers it too)") {
		t.Fatalf("got:\n%s", out.String())
	}
}
