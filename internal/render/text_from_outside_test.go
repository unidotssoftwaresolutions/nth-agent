package render_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// TEXT FROM OUTSIDE (WHAT_A_LEASE_CAN_ASK). A phone's name is the phone's to choose (an iPhone's is whatever its owner typed,
// an Android phone's comes from its own properties), and `devices` printed it as it came: an escape code in it could clear
// the screen, rewrite a line, or set the terminal's clipboard. Every value a device supplies is made fit for a terminal
// before it is printed — the filter connect's narration already uses — and the agent's own colours are kept.
const hostile = "Pix\x1b]52;c;aGVsbG8=\x07el\x1b[2J \u009b31m9\u202e"

func outsideOutcome() inventory.Outcome {
	return inventory.Answered(inventory.Android,
		[]inventory.Offerable{{ID: "R5CT30\x1b[HABCDE", Model: hostile, OsVersion: "15\x1b[8m"},
			{ID: "emulator-5554", Model: "sdk", Emulator: true, AVD: "Pixel_9\x1b]0;owned\x07"}},
		[]inventory.Unusable{inventory.NewUnusable("R5CT99\x1b[2KZZZ", "unauthorized\x1b[31m", "Tap Allow on the device.")})
}

func TestDevicesPrintsNoEscapeAPhoneSupplied(t *testing.T) {
	var plain bytes.Buffer
	if err := render.Human(&plain, []inventory.Outcome{outsideOutcome()}, nil); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\x1b", "\x07", "\u009b", "\u202e"} {
		if strings.Contains(plain.String(), bad) {
			t.Fatalf("devices printed %q from a phone's own words:\n%q", bad, plain.String())
		}
	}
	if !strings.Contains(plain.String(), "Pixel 9") || !strings.Contains(plain.String(), "R5CT99ZZZ") || !strings.Contains(plain.String(), "Pixel_9") {
		t.Fatalf("the phone is still named, without what was taken out:\n%s", plain.String())
	}
	// In colour, the only escape codes are the agent's own: the same output with them taken off holds none.
	var coloured bytes.Buffer
	if err := render.Styled(&coloured, ui.Mode{Color: true}, []inventory.Outcome{outsideOutcome()}, nil); err != nil {
		t.Fatal(err)
	}
	if got := ui.Plain(coloured.String()); strings.ContainsAny(got, "\x1b\x07") || strings.Contains(got, "\u009b") {
		t.Fatalf("a phone's escape survived beside the agent's own colours:\n%q", got)
	}
	// --json is escaped by its encoder and carries the phone's words as they came, for a program to read.
	var machine bytes.Buffer
	if err := render.JSON(&machine, []inventory.Outcome{outsideOutcome()}, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsAny(machine.Bytes(), "\x1b\x07") || !json.Valid(machine.Bytes()) {
		t.Fatalf("--json must stay escaped JSON:\n%q", machine.String())
	}
}
