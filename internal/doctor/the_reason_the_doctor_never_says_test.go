package doctor

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// THE_REASON_THE_DOCTOR_NEVER_SAYS — every check carried its justification in `Need`, populated for all of them
// and read by NO renderer: a person at the terminal could ask "why did it say ready?" (--why, the caveats) but
// never "why is this a requirement at all", though the answer was written down per check, in code. --json carried
// the field all along; the human surface — the most-read one this agent has — did not.
//
// THE DECISION (item 1): `Need` became visible, behind --why, and the checks.go header's hand-kept enumeration
// was retired — it had already drifted (nine entries, twelve checks; three never added). The guard is therefore a
// CENSUS AGAINST THE LIVE REPORT, not against prose: every check Diagnose appends must say why it is required,
// and the renderer must show every one of those reasons behind the flag.
//
// WEIGHT (item 2): a reason is REFERENCE on every check, failing ones included — the remedy is the action and
// the caveat is the diagnosis — so nothing prints by default, and the count line accounts for the new kind of
// withheld thing, because its own docblock promises nothing is ever silently gone. These seals carry the weight
// rules for `Need` themselves: nine_greens_test.go's fixture is HISTORY (the real log of the night that motivated
// it, verbatim) and may not gain Need values to see this slice.

// stubEnv is a machine that can answer nothing — every seam errors or is absent. Need is set at construction,
// before any probe runs, so even this machine's report must carry every reason.
func stubEnv() Env {
	return Env{
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run: func(context.Context, string, ...string) (string, error) {
			return "", errors.New("cannot run anything")
		},
		DialTimeout: func(string, string, time.Duration) (net.Conn, error) {
			return nil, errors.New("refused")
		},
		AppiumStatus:     func(context.Context, string) error { return errors.New("no appium") },
		IosDeveloperMode: nil,
	}
}

// ── the census: every check Diagnose appends says why it is required ────────────────────────────────────────

func TestEveryCheckDiagnoseAppendsSaysWhyItIsRequired(t *testing.T) {
	report := Diagnose(context.Background(), stubEnv(), nil)

	if len(report.Checks) < 13 { // 12 → 13: unusable devices (THE_PHONE_THE_DOCTOR_SAW) — the floor moves with the set, deliberately
		t.Fatalf("the census must have seen the real report, got %d checks", len(report.Checks))
	}
	for _, c := range report.Checks {
		if strings.TrimSpace(c.Need) == "" {
			t.Fatalf("%q says nothing about why it is required — a requirement nobody can trace to a caller is a "+
				"requirement somebody guessed, and the header enumeration that used to carry these by hand was "+
				"retired in favour of THIS census", c.Name)
		}
	}
}

// ── --why shows every reason the report holds ───────────────────────────────────────────────────────────────

// needy is a report whose checks cover the classes a renderer might be tempted to treat differently: a blocker,
// a green, an advisory, a NotRequired and an Unknown — each with a distinct Need, so a skip of any ONE class
// fails on that class's name.
func needy() Report {
	return Report{Checks: []Check{
		{Name: "blocker", State: Missing, Need: "reason-on-a-blocker", Remedy: "do the thing"},
		{Name: "green", State: Satisfied, Need: "reason-on-a-green"},
		{Name: "advisory", State: Missing, Advisory: true, Need: "reason-on-an-advisory", Remedy: "later"},
		{Name: "not-needed", State: NotRequired, Need: "reason-on-a-not-required"},
		{Name: "unknown", State: Unknown, Need: "reason-on-an-unknown"},
	}}
}

func TestWhyPrintsEveryChecksReason(t *testing.T) {
	r := needy()
	flat := unwrapped(render(t, r, Options{Why: true}))

	// Walked over the report itself, like the caveat seal, so a check added later is covered without anybody
	// remembering to add it here — and one line per class above, so a class-scoped skip names its class.
	for _, c := range r.Checks {
		if !strings.Contains(flat, "why required: "+c.Need) {
			t.Fatalf("%s's reason is reachable through no flag at all; got:\n%s", c.Name, flat)
		}
	}
}

// ── the default output withholds the reasons, and the count line says so ────────────────────────────────────

func TestTheDefaultOutputWithholdsEveryReasonAndTheCountLineAccountsForIt(t *testing.T) {
	text := render(t, needy(), Options{})
	flat := unwrapped(text)

	// WEIGHT: not one reason prints by default — on any class, the blocker included. A sentence on every check
	// is the one-visual-weight log this output already stopped being.
	if strings.Contains(flat, "why required:") || strings.Contains(flat, "reason-on-") {
		t.Fatalf("a reason is reference and prints only behind %s; got:\n%s", WhyFlag, text)
	}
	// THE PROMISE: nothing is silently gone — the new kind of withheld thing is named, with the flag.
	if !strings.Contains(flat, "why it is required") {
		t.Fatalf("the count line must account for the withheld reasons; got:\n%s", text)
	}
	if !strings.Contains(flat, WhyFlag) {
		t.Fatalf("the count line must name the flag; got:\n%s", text)
	}
}

func TestAReportWithReasonsAndNoCaveatsStillAdvertisesTheFlag(t *testing.T) {
	r := Report{Checks: []Check{{Name: "green", State: Satisfied, Need: "reason-on-a-green"}}}
	flat := unwrapped(render(t, r, Options{}))

	if !strings.Contains(flat, "why it is required") || !strings.Contains(flat, WhyFlag) {
		t.Fatalf("reasons withheld with no caveats withheld is still something withheld; got:\n%s", flat)
	}
}

func TestSealTwosSentenceSurvivesWordForWordWhenOnlyCaveatsAreWithheld(t *testing.T) {
	// tonight()'s fixture carries no Need values (it is HISTORY, verbatim), so the sentence the existing seals
	// pin must come out unchanged — this test states that dependency in words where the next editor will look.
	flat := unwrapped(render(t, tonight(), Options{}))
	if !strings.Contains(flat, "2 checks that do not stop a run state what they did NOT establish — run") {
		t.Fatalf("with no Need in the report the count line must be byte-what-it-was; got:\n%s", flat)
	}
	if strings.Contains(flat, "why it is required") {
		t.Fatal("a report with no reasons must not claim to be withholding any")
	}
}

// ── an Unknown with a reason is still an Unknown ────────────────────────────────────────────────────────────

func TestAReasonNeverMakesAnUnknownReadAsSatisfied(t *testing.T) {
	r := Report{Checks: []Check{{Name: "unknown", State: Unknown, Need: "reason-on-an-unknown"}}}
	text := render(t, r, Options{Why: true})

	var line string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "unknown:") {
			line = l
		}
	}
	if !strings.Contains(line, "could not establish") {
		t.Fatalf("an Unknown stays 'could not establish' whatever it carries; got %q", line)
	}
	if strings.Contains(line, "ready") {
		t.Fatalf("ANYTHING NOT ESTABLISHED IS Unknown, never Satisfied — a reason changes nothing; got %q", line)
	}
	if !strings.HasPrefix(line, "?? ") {
		t.Fatalf("the gutter still says could-not-establish; got %q", line)
	}
}

// ── the header's retirement is complete: no per-check enumeration remains to rot ────────────────────────────

func TestTheRetiredHeaderEnumerationDidNotComeBack(t *testing.T) {
	// The one entry that STAYS is usbmuxd — the decision NOT to have a check, which no check can carry. Anything
	// else re-enumerated by hand beside the machine-checked Need field would be the rot coming back: the header
	// held nine entries against twelve checks when this slice found it.
	source, err := readChecksSource()
	if err != nil {
		t.Fatal(err)
	}
	header := source[:strings.Index(source, "func Diagnose")]
	if !strings.Contains(header, "usbmuxd") {
		t.Fatal("the usbmuxd decision has no check to live on — it must survive in the header")
	}
	for _, retired := range []string{"\n//   adb ", "\n//   Appium ", "\n//   xcuitest ", "\n//   provisioning profile "} {
		if strings.Contains(header, retired) {
			t.Fatalf("the header enumerates%s again — the reasons live in Need, shown by %s and carried in --json; "+
				"a hand-kept second copy is what drifted to nine-against-twelve", strings.TrimPrefix(retired, "\n//  "), WhyFlag)
		}
	}
}

func readChecksSource() (string, error) {
	b, err := os.ReadFile("checks.go")
	return string(b), err
}
