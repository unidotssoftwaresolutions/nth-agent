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
	// AMENDED (AN_AGENT_THAT_SPEAKS_PLAINLY): the phone is a row of the table now, and the reason and the flag are the
	// three parts every problem is said in. Every fact the old line held is still asserted: the phone, how it is reached,
	// that only phones on a USB cable are offered, and the flag that offers it.
	text := out.String()
	if !strings.Contains(text, "emulator-5554  Android   —   left out: not on a USB cable") {
		t.Fatalf("the phone is a row of the table, got:\n%s", text)
	}
	text = strings.Join(strings.Fields(text), " ") // the sentences fold at the report's width; compared unfolded
	for _, want := range []string{
		"! Android phone emulator-5554 is an emulator",
		"The agent offers only phones on a USB cable, so this one is left out.",
		"→ connect it by USB cable, or offer it too: start connect with --offer-uncabled-devices",
	} {
		if strings.Contains(text, "no devices attached") || !strings.Contains(text, want) {
			t.Fatalf("want %q, got:\n%s", want, text)
		}
	}
}
