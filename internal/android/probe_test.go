package android_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(data)
}

// runner answers `adb devices -l` with the fixture and every other call (the per-device getprop reads the
// probe now makes) with a BLANK answer — the shape of a device that did not describe itself, so these
// inventory tests say nothing about descriptor fields (describe_test.go does).
func runner(out string, err error) android.Runner {
	return func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "devices" {
			return out, err
		}
		return "", nil
	}
}

// THE HEADLINE: adb's captured empty output is an ANSWER (zero devices), while adb's captured failure
// is NOT an inventory at all. These two fixtures are the same "empty" to a naive parser.
func TestEmptyIsAnsweredAndFailureIsNot(t *testing.T) {
	answered := android.Probe{Run: runner(fixture(t, "empty.txt"), nil), Enabled: true}.Outcome(context.Background())
	if !answered.IsAnswered() {
		t.Fatalf("nothing attached must be ANSWERED, got %s", answered.KindString())
	}
	if len(answered.Offerable()) != 0 || len(answered.Unusable()) != 0 {
		t.Fatalf("expected two empty lists, got %d/%d", len(answered.Offerable()), len(answered.Unusable()))
	}

	// The runner is the PRODUCTION shape (adbRunner's own wrapping), not a hand-written error the
	// test then asserts back — see reason_test.go for why that distinction cost this repo a sentence.
	failed := android.Probe{Run: productionRunnerShape(t, "server-not-acking.txt"), Enabled: true}.
		Outcome(context.Background())
	if !failed.IsCouldNotAsk() {
		t.Fatalf("a server that will not ACK is COULD-NOT-ASK, got %s", failed.KindString())
	}
	if len(failed.Offerable()) != 0 {
		t.Fatal("a failed probe must carry no devices at all")
	}
	if !strings.Contains(failed.Reason(), "ACK") {
		t.Fatalf("the reason must carry adb's own words FROM THE FIXTURE, got %q", failed.Reason())
	}
	// NOT `TrimSpace(...) != ""` — the constructor supplies that. The marker's absence is the guard.
	if failed.Remedy() == inventory.MissingRemedyMarker {
		t.Fatalf("this path ships a CouldNotAsk with no remedy of its own: %q", failed.Remedy())
	}
}

// NEVER PUBLISH WHAT YOU CANNOT OFFER: the unauthorized/offline/no-permissions devices are reported,
// with their verbatim states, and none of them reaches the offerable list.
func TestUnusableDevicesAreShownAndNeverOffered(t *testing.T) {
	outcome := android.Probe{Run: runner(fixture(t, "attached-mixed.txt"), nil), Enabled: true}.Outcome(context.Background())

	if len(outcome.Offerable()) != 1 || outcome.Offerable()[0].ID != "R5CT30ABCDE" {
		t.Fatalf("exactly the `device`-state handset is offerable, got %+v", outcome.Offerable())
	}
	states := map[string]string{}
	remedies := map[string]string{} // remedy → the state that produced it
	for _, u := range outcome.Unusable() {
		states[u.ID] = u.State
		// NOT merely non-empty: remedyFor's default supplies that. Each state must get its OWN
		// sentence — one remedy repeated for three different states tells nobody what to do.
		if prior, repeated := remedies[u.Remedy]; repeated {
			t.Fatalf("states %q and %q share one remedy — the reader is told the same thing about different problems", prior, u.State)
		}
		remedies[u.Remedy] = u.State
	}
	want := map[string]string{
		"R5CT99ZZZZZ":      "unauthorized",
		"emulator-5554":    "offline",
		"0123456789ABCDEF": "no permissions",
	}
	for id, state := range want {
		if states[id] != state {
			t.Fatalf("device %s: want verbatim state %q, got %q", id, state, states[id])
		}
	}
	for _, d := range outcome.Offerable() {
		if _, unusable := want[d.ID]; unusable {
			t.Fatalf("%s is attached-but-unusable and MUST NOT be offerable", d.ID)
		}
	}
}

// THE VERBATIM-STATE RULE: a state this parser has never seen survives the trip and is shown — never
// flattened into "other", and never silently dropped.
func TestAnUnknownStateSurvivesVerbatim(t *testing.T) {
	outcome := android.Probe{Run: runner(fixture(t, "unknown-state.txt"), nil), Enabled: true}.Outcome(context.Background())

	if len(outcome.Unusable()) != 1 {
		t.Fatalf("the unknown-state device must be reported, got %d", len(outcome.Unusable()))
	}
	entry := outcome.Unusable()[0]
	if entry.State != "quarantined" {
		t.Fatalf("want the platform's own word %q, got %q", "quarantined", entry.State)
	}
	if strings.Contains(strings.ToLower(entry.Remedy), "other") {
		t.Fatalf("an unknown state must not be described as %q", "other")
	}
	// `remedyFor`'s exhaustive default means "is it non-empty?" can never fail — the default supplies
	// the answer. KEPT AND STRENGTHENED rather than deleted, because the default is legitimate (an
	// unrecognised state must still get a next step): what is asserted instead is that the reader is
	// told the state is unrecognised, and that this is NOT one of the specific states' remedies —
	// which is what would break if an unknown state were ever routed into a known branch.
	if !strings.Contains(entry.Remedy, "does not recognise") {
		t.Fatalf("an unknown state must SAY it is unrecognised, got %q", entry.Remedy)
	}
	known := android.Probe{Run: runner(fixture(t, "attached-mixed.txt"), nil), Enabled: true}.Outcome(context.Background())
	for _, u := range known.Unusable() {
		if u.Remedy == entry.Remedy {
			t.Fatalf("state %q got the unknown-state remedy — a recognised state must get its own", u.State)
		}
	}
}

// A disabled probe is not a faulty one, and holds no opinion about devices at all.
func TestDisabledProbeIsNotAsked(t *testing.T) {
	outcome := android.Probe{Run: runner("", errors.New("must not be called")), Enabled: false}.Outcome(context.Background())
	if !outcome.IsNotAsked() {
		t.Fatalf("a switched-off probe is NOT-ASKED, got %s", outcome.KindString())
	}
	if outcome.Platform() != inventory.Android {
		t.Fatalf("the platform still rides the outcome, got %q", outcome.Platform())
	}
}
