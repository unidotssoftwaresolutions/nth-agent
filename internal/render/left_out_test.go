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
		// AMENDED (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): an emulator on this machine is offered now; the phone left
		// out is one adb reaches over the network, and the reason names the emulators that are offered.
		inventory.WithLeftOut(inventory.Answered(inventory.Android, nil, nil), []inventory.LeftOut{{ID: "192.0.2.10:5555", How: "reached over the network (adb connect)"}}),
	}
	var out strings.Builder
	if err := render.Human(&out, outcomes, nil); err != nil {
		t.Fatal(err)
	}
	// AMENDED (AN_AGENT_THAT_SPEAKS_PLAINLY): the phone is a row of the table now, and the reason and the flag are the
	// three parts every problem is said in. Every fact the old line held is still asserted: the phone, how it is reached,
	// that only phones on a USB cable are offered, and the flag that offers it.
	text := out.String()
	if !strings.Contains(text, "192.0.2.10:5555  Android   —   left out: not on a USB cable") {
		t.Fatalf("the phone is a row of the table, got:\n%s", text)
	}
	text = strings.Join(strings.Fields(text), " ") // the sentences fold at the report's width; compared unfolded
	for _, want := range []string{
		"! Android phone 192.0.2.10:5555 is reached over the network (adb connect)",
		"The agent offers phones on a USB cable and the emulators on this machine, so this one is left out.",
		"→ connect it by USB cable, or offer it too: start connect with --offer-uncabled-devices",
	} {
		if strings.Contains(text, "no devices attached") || !strings.Contains(text, want) {
			t.Fatalf("want %q, got:\n%s", want, text)
		}
	}
}
