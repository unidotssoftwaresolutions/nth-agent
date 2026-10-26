package farm_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// THE CAVEAT TRAVELS, and it travels as an ANSWER. The farm's own vocabulary already carries a stated blind
// spot on an Answered outcome (PlatformInventoryStatus.Answered), so this needs no new wire field — but a
// caveat that stops at the agent's own terminal is a caveat nobody reading a machine's health ever sees.
func TestAnAnsweredPlatformsCaveatReachesTheWire(t *testing.T) {
	outcome := inventory.AnsweredWithCaveat(inventory.Android,
		[]inventory.Offerable{{ID: "R5CT64M346B"}}, nil,
		"this agent cannot establish whether an app can be installed without somebody at the device",
		"approve one installation by hand to see whether it asks")

	report := farm.BuildReport([]inventory.Outcome{outcome})

	if len(report.Outcomes) != 1 {
		t.Fatalf("one platform, one outcome, got %d", len(report.Outcomes))
	}
	wire := report.Outcomes[0]

	// STATUS STAYS ANSWERED. Sending a blind spot as CouldNotAsk would throw away an answer we have — the
	// farm's reconcile acts on answered platforms, and the offerable half IS established.
	if wire.Status != farm.WireAnswered {
		t.Fatalf("a blind spot is not a failure — status must stay Answered, got %s", wire.Status)
	}
	if wire.OfferableCount == nil || *wire.OfferableCount != 1 {
		t.Fatalf("the answered half must still count its device, got %v", wire.OfferableCount)
	}
	if wire.Reason == nil || !strings.Contains(*wire.Reason, "without somebody at the device") {
		t.Fatalf("the caveat's reason must ride the wire, got %v", wire.Reason)
	}
	if wire.Remedy == nil || strings.TrimSpace(*wire.Remedy) == "" {
		t.Fatalf("the caveat's remedy must ride the wire, got %v", wire.Remedy)
	}
}

// An answered platform with NOTHING to add sends no reason at all — silence stays silence on the wire, so a
// reader never sees an empty caveat and learns to ignore the field.
func TestAnAnsweredPlatformWithoutACaveatSendsNoReason(t *testing.T) {
	report := farm.BuildReport([]inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "serial"}}, nil),
	})
	if report.Outcomes[0].Reason != nil || report.Outcomes[0].Remedy != nil {
		t.Fatalf("no caveat means no words, got reason=%v remedy=%v", report.Outcomes[0].Reason, report.Outcomes[0].Remedy)
	}
}

// A FAILURE still travels as a failure. The two use the same wire keys, so the seal that the caveat rides
// them must be paired with one that CouldNotAsk did not quietly become an answer.
func TestAFailureStillTravelsAsAFailure(t *testing.T) {
	report := farm.BuildReport([]inventory.Outcome{
		inventory.CouldNotAsk(inventory.Android, "adb could not be asked", "install platform-tools"),
	})
	if report.Outcomes[0].Status != farm.WireCouldNotAsk {
		t.Fatalf("a failure must stay CouldNotAsk, got %s", report.Outcomes[0].Status)
	}
	if report.Outcomes[0].OfferableCount != nil {
		t.Fatal("a platform that could not be asked counts nothing")
	}
}
