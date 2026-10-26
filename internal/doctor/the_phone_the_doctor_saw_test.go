package doctor

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// THE_PHONE_THE_DOCTOR_SAW_AND_NEVER_MENTIONED — Diagnose received every probe's Unusable devices and read them
// in exactly one place, platformIsInPlay's scope vote; the device itself reached no check. A person whose only
// phone sat "unauthorized" read every toolchain line green and "Nothing here is stopping a run", and then their
// run found no device. These seals hold the check that now names it — and hold platformIsInPlay to the answer it
// gave before this check became the outcomes' second reader.

func offerable(id string) inventory.Offerable { return inventory.Offerable{ID: id, Model: "Pixel 8"} }

// ── the named-device seal ───────────────────────────────────────────────────────────────────────────────────

func TestAnUnusableDeviceIsNamedWithItsVerbatimStateAndTheProbesOwnRemedy(t *testing.T) {
	theProbesSentence := "on the phone, tap Allow when the USB debugging prompt appears; if there is no prompt, re-plug the cable"
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, nil,
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "unauthorized", theProbesSentence)}),
	}

	c := unusableDevices(outcomes)

	if !strings.Contains(c.Detail, "4b0f92aa") {
		t.Fatalf("the device the probe saw must be NAMED — that is the whole slice; got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, `"unauthorized"`) {
		t.Fatalf("the state is the platform's own word, shown verbatim; got %q", c.Detail)
	}
	// THE ONE-REMEDY RULE, in its strongest form: with a single device the remedy IS the probe's sentence —
	// byte-for-byte, nothing paraphrased, nothing general written beside it (the usbmuxd rule, one file over).
	if c.Remedy != theProbesSentence {
		t.Fatalf("the remedy must be the probe's own sentence, verbatim and alone; got %q", c.Remedy)
	}
}

// THE REPORT-LEVEL HALF OF THE SAME SEAL — through Diagnose itself, because the function existing is not the
// fact reaching a person: injection 1 (drop the append) left the direct-call seal green and only the census
// floor red, and a floor is a number that a later check could quietly satisfy. The named device must be in the
// REPORT the doctor renders.
func TestDiagnoseItselfNamesTheUnusableDevice(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, nil,
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "unauthorized", "tap Allow on the phone")}),
	}
	report := Diagnose(t.Context(), stubEnv(), outcomes)

	for _, c := range report.Checks {
		if strings.Contains(c.Detail, "4b0f92aa") {
			return
		}
	}
	t.Fatal("the probe saw 4b0f92aa and no check in the report names it — the fact stayed in the argument list, " +
		"which is the exact state this slice exists to end")
}

// ── the verbatim-state seal ─────────────────────────────────────────────────────────────────────────────────

func TestAStateThisBuildHasNeverSeenSurvivesVerbatimAndNoRemedyIsInvented(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.IOS, []inventory.Offerable{offerable("00008030")},
			[]inventory.Unusable{inventory.NewUnusable("00008120", "quantum-flux handshake pending", "")}),
	}

	c := unusableDevices(outcomes)

	if !strings.Contains(c.Detail, `"quantum-flux handshake pending"`) {
		t.Fatalf("an unknown state survives the trip and is shown, never flattened to \"other\"; got %q", c.Detail)
	}
	if strings.Contains(c.Detail, "other") {
		t.Fatalf("flattened; got %q", c.Detail)
	}
	// The probe sent no remedy — the check SAYS that rather than inventing one, and the sentence names the state
	// so a reader knows what arrived.
	if !strings.Contains(c.Remedy, "no remedy") || !strings.Contains(c.Remedy, "quantum-flux") {
		t.Fatalf("an absent remedy is stated, not invented; got %q", c.Remedy)
	}
}

// ── does it block? decided per platform, from the same outcome ──────────────────────────────────────────────

func TestTheOnlyDeviceOfAPlatformBeingUnusableBlocksARun(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, nil,
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "unauthorized", "tap Allow on the phone")}),
	}

	c := unusableDevices(outcomes)

	if c.State != Missing || c.Advisory {
		t.Fatalf("a platform whose EVERY seen device is unusable has nothing for a run to use — that blocks; got %v advisory=%v", c.State, c.Advisory)
	}
	if !c.Blocks() {
		t.Fatal("Blocks() must say so, or the bottom line still claims nothing is stopping a run")
	}
}

func TestAStuckDeviceBesideAUsableOneIsAdvisoryNotABlocker(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{offerable("usable-1")},
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "offline", "re-plug the cable")}),
	}

	c := unusableDevices(outcomes)

	if c.State != Missing || !c.Advisory {
		t.Fatalf("a machine that can still run on the other phone is not stopped — TRUE AND WORTH SAYING; got %v advisory=%v", c.State, c.Advisory)
	}
	if c.Blocks() {
		t.Fatal("an advisory must not move the bottom line")
	}
}

func TestAPlatformStuckWhollyBlocksEvenWhenTheOtherPlatformIsFine(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.IOS, []inventory.Offerable{offerable("00008030")}, nil),
		inventory.Answered(inventory.Android, nil,
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "unauthorized", "tap Allow on the phone")}),
	}

	if c := unusableDevices(outcomes); !c.Blocks() {
		t.Fatal("an Android run on this machine finds no device; an offerable iPhone does not change that")
	}
}

// ── NotRequired, never Satisfied, when there was no question ────────────────────────────────────────────────

func TestAMachineWithNothingToSayReportsNotRequiredNeverSatisfied(t *testing.T) {
	for name, outcomes := range map[string][]inventory.Outcome{
		"every device usable": {inventory.Answered(inventory.Android, []inventory.Offerable{offerable("usable-1")}, nil)},
		"no device at all":    {inventory.Answered(inventory.Android, nil, nil)},
		"probe could not ask": {inventory.CouldNotAsk(inventory.Android, "adb missing", "install adb")},
		"nothing asked":       nil,
	} {
		c := unusableDevices(outcomes)
		if c.State != NotRequired {
			t.Fatalf("%s: there was no question, so nothing was passed — want NotRequired, got %v", name, c.State)
		}
		if c.State == Satisfied {
			t.Fatalf("%s: a machine with no unusable device has not passed a test", name)
		}
	}
}

// ── platformIsInPlay answers exactly as before this check became a second reader ────────────────────────────

func TestPlatformIsInPlayAnswersExactlyAsItAlwaysDid(t *testing.T) {
	for name, tc := range map[string]struct {
		outcomes []inventory.Outcome
		want     bool
	}{
		"offerable device":       {[]inventory.Outcome{inventory.Answered(inventory.Android, []inventory.Offerable{offerable("a")}, nil)}, true},
		"only an unusable one":   {[]inventory.Outcome{inventory.Answered(inventory.Android, nil, []inventory.Unusable{inventory.NewUnusable("a", "offline", "re-plug")})}, true},
		"answered with nothing":  {[]inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)}, false},
		"could not ask":          {[]inventory.Outcome{inventory.CouldNotAsk(inventory.Android, "adb missing", "install adb")}, true},
		"not asked":              {[]inventory.Outcome{inventory.NotAsked(inventory.Android)}, false},
		"no outcome at all":      {nil, false},
		"other platform's phone": {[]inventory.Outcome{inventory.Answered(inventory.IOS, []inventory.Offerable{offerable("a")}, nil)}, false},
	} {
		if got := platformIsInPlay(inventory.Android, tc.outcomes); got != tc.want {
			t.Fatalf("%s: platformIsInPlay = %v, want %v — this helper's answer scopes every iOS check and must not "+
				"move because the outcomes gained a second reader", name, got, tc.want)
		}
	}
}

// ── the rendered bottom line, for the field check ───────────────────────────────────────────────────────────

func TestABlockedMachinesBottomLineNamesTheStuckPhone(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, nil,
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "unauthorized", "tap Allow on the phone")}),
	}
	flat := unwrapped(render(t, Report{Checks: []Check{unusableDevices(outcomes)}}, Options{}))

	if !strings.Contains(flat, "One thing stops a run happening on this machine") {
		t.Fatalf("the bottom line must stop claiming nothing is wrong; got:\n%s", flat)
	}
	if !strings.Contains(flat, "4b0f92aa") || !strings.Contains(flat, "tap Allow on the phone") {
		t.Fatalf("the blocker line carries the device and the probe's remedy; got:\n%s", flat)
	}
}
