package render_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

func caveatOutcome() inventory.Outcome {
	return inventory.AnsweredWithCaveat(inventory.Android,
		[]inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B"}}, nil,
		"cannot establish whether an app installs without somebody at the device",
		"approve one installation by hand to see whether it asks")
}

// A READER MUST BE ABLE TO TELL THE TWO APART. An answered platform with a blind spot is a NOTE beside a
// real answer; a platform that could not be asked is a failure. Both carry reason+remedy, so the words
// alone cannot distinguish them — the rendering must.
func TestACaveatPrintsAsANoteAndNotAsAFailure(t *testing.T) {
	var out bytes.Buffer
	if err := render.Human(&out, []inventory.Outcome{caveatOutcome()}, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	text := out.String()

	if strings.Contains(text, "COULD NOT CHECK") {
		t.Fatalf("an answered platform must never print as a failure:\n%s", text)
	}
	if !strings.Contains(text, "1 usable") {
		t.Fatalf("the answer itself must still print:\n%s", text)
	}
	if !strings.Contains(text, "this answer does not cover") || !strings.Contains(text, "somebody at the device") {
		t.Fatalf("the blind spot must be shown to the reader:\n%s", text)
	}
	if !strings.Contains(text, "what to check by hand") {
		t.Fatalf("a caveat without its remedy is half a sentence:\n%s", text)
	}
}

// The machine rendering carries it in the same reason/remedy keys, with the kind still "answered" — what
// tells a caveat from a failure is the KIND, never the presence of words.
func TestTheMachineRenderingKeepsTheKindAnswered(t *testing.T) {
	var out bytes.Buffer
	if err := render.JSON(&out, []inventory.Outcome{caveatOutcome()}, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	text := out.String()

	if !strings.Contains(text, `"outcome": "answered"`) {
		t.Fatalf("the kind must stay answered:\n%s", text)
	}
	if !strings.Contains(text, `"reason"`) || !strings.Contains(text, `"remedy"`) {
		t.Fatalf("the caveat must be machine-readable:\n%s", text)
	}
	if !strings.Contains(text, `"devices"`) {
		t.Fatalf("an answered platform still lists what it established:\n%s", text)
	}
}

// An answered platform with no caveat prints no note — and its JSON carries no empty reason key.
func TestNoCaveatPrintsNothingExtra(t *testing.T) {
	plain := inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "serial"}}, nil)

	var human, machine bytes.Buffer
	_ = render.Human(&human, []inventory.Outcome{plain}, nil)
	_ = render.JSON(&machine, []inventory.Outcome{plain}, nil)

	if strings.Contains(human.String(), "does not cover") {
		t.Fatalf("silence must stay silent:\n%s", human.String())
	}
	if strings.Contains(machine.String(), `"reason"`) {
		t.Fatalf("an omitted caveat must not appear as an empty key:\n%s", machine.String())
	}
}
