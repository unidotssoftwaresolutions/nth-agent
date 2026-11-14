package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── A PHONE OFFERED IS A PROMISE TO DRIVE IT ──────────────────────────────────────────────────────────────────
//
// The agent published two of the operator's iPhones as offerable — `status: Available`, `kind: Physical` — while
// its own `doctor`, in the same process family, said of them: "the phone is reported as present and IS NOT
// OFFERED FOR RUNS. Nothing to install for it today." Both halves were false. A diagnostic that contradicts the
// product is worse than no diagnostic: it teaches a person to disbelieve the screen that is telling the truth.
//
// The seal is on the CONSISTENCY, not on the sentence: whatever iOS words the doctor grows next, they may not
// claim iOS devices are unoffered while `farm.AttachDevices` publishes them.

// repoRoot walks up to the module root, so these source reads work from any package's test binary.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found above the test's working directory")
		}
		dir = parent
	}
}

func read(t *testing.T, root string, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(body)
}

// PublishesIosOfferables reports whether the report builder puts iOS offerable devices on the wire. It reads the
// ONE door — AttachDevices — and asks whether that loop is platform-blind. A future exclusion of iOS would have to
// be written as a filter there, and this walk would see it.
func publishesIosOfferables(attachDevicesSource string) bool {
	body := attachDevicesSource
	if !strings.Contains(body, "o.Offerable()") {
		return false // it no longer publishes offerables at all
	}
	// An iOS exclusion would have to name the platform inside the loop that builds device rows.
	excluded := regexp.MustCompile(`(?i)(if|continue)[^\n]*\binventory\.IOS\b`)
	return !excluded.MatchString(body)
}

// funcBody returns the source of one top-level func, brace-matched — the same discipline as reading a JSX tag by
// depth: a `}` inside a nested block is not the end of the function.
func funcBody(source, name string) string {
	start := strings.Index(source, "func "+name+"(")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[start : i+1]
			}
		}
	}
	return ""
}

func TestNoDoctorSentenceClaimsIosDevicesAreNotOfferedWhileTheAgentOffersThem(t *testing.T) {
	root := repoRoot(t)
	attach := funcBody(read(t, root, "internal", "farm", "report.go"), "AttachDevices")
	if attach == "" {
		t.Fatal("AttachDevices not found — the walk must fail loudly rather than pass on an empty read")
	}
	if !publishesIosOfferables(attach) {
		t.Skip("the agent no longer publishes iOS offerables; the consistency this seal protects is not in play")
	}

	// EVERY sentence the doctor can print about iOS, from the EXECUTED Diagnose on a machine with an iPhone —
	// not from a hand-read of the source, so a sentence added tomorrow is covered by existence.
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030", Model: "iPhone 15"}}, nil),
	}
	report := doctor.Diagnose(context.Background(), machine{}.env(), outcomes)
	for _, c := range report.Checks {
		for _, sentence := range []string{c.Name, c.Need, c.Detail, c.Remedy} {
			if claimsNotOffered(sentence) {
				t.Fatalf("the agent publishes iOS offerables and this sentence says otherwise: %q", sentence)
			}
		}
	}

	// …and the same rule over every SENTENCE THIS PACKAGE CAN PRINT, because one can hide in a branch this
	// machine did not take (a driver present, a phone that answers, a version string that parses).
	//
	// PROSE IS NOT A SENTENCE THE PRODUCT PRINTS. The first draft scanned raw lines and caught its OWN comment,
	// which quotes the retired claim in order to explain it — the third time a census in this workspace has
	// measured its own explanation. What a person can read is a STRING LITERAL, so that is the population:
	// comments off first, then the literals out of what remains.
	for _, file := range []string{"checks.go", "render.go", "doctor.go"} {
		for _, literal := range stringLiterals(withoutComments(read(t, root, "internal", "doctor", file))) {
			if claimsNotOffered(literal) {
				t.Fatalf("internal/doctor/%s can print: %q", file, literal)
			}
		}
	}
}

// withoutComments removes Go line and block comments. A `//` inside a string literal is left alone — that one is
// text, and dropping the rest of its line would hide a sentence rather than reveal one.
func withoutComments(source string) string {
	var out strings.Builder
	inString, inRaw, inLine, inBlock, escaped := false, false, false, false, false
	for i := 0; i < len(source); i++ {
		c := source[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				out.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(source) && source[i+1] == '/' {
				inBlock = false
				i++
			}
		case inString:
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		case inRaw:
			out.WriteByte(c)
			if c == '`' {
				inRaw = false
			}
		case c == '"':
			inString = true
			out.WriteByte(c)
		case c == '`':
			inRaw = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(source) && source[i+1] == '/':
			inLine = true
		case c == '/' && i+1 < len(source) && source[i+1] == '*':
			inBlock = true
			i++
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// stringLiterals returns the contents of every double-quoted and backquoted literal — the sentences this package
// can actually put in front of a person.
func stringLiterals(source string) []string {
	var found []string
	for _, m := range regexp.MustCompile("`[^`]*`").FindAllString(source, -1) {
		found = append(found, strings.Trim(m, "`"))
	}
	for _, m := range regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`).FindAllString(source, -1) {
		found = append(found, strings.Trim(m, `"`))
	}
	return found
}

// claimsNotOffered: the sentence says devices are not offered / not available for runs. Deliberately narrow — it
// is looking for the CLAIM, not for the words "offer" or "iOS" anywhere near each other.
func claimsNotOffered(sentence string) bool {
	lower := strings.ToLower(sentence)
	for _, claim := range []string{"not offered", "is not offered", "are not offered", "not offered for runs", "cannot be offered"} {
		if strings.Contains(lower, claim) {
			return true
		}
	}
	return false
}

func TestTheConsistencyWalkReadsTheSourceAndDoesNotAgreeWithItself(t *testing.T) {
	// THE INSTRUMENT GUARD: two sources whose correct answers DIFFER, decided by the executed walk.
	blind := `func AttachDevices(report Report, outcomes []inventory.Outcome, appiumURL string, nil) Report {
		for _, o := range outcomes {
			for _, d := range o.Offerable() { devices = append(devices, wire(d)) }
		}
		return report
	}`
	filtered := `func AttachDevices(report Report, outcomes []inventory.Outcome, appiumURL string, nil) Report {
		for _, o := range outcomes {
			if o.Platform() == inventory.IOS { continue }
			for _, d := range o.Offerable() { devices = append(devices, wire(d)) }
		}
		return report
	}`
	none := `func AttachDevices(report Report, outcomes []inventory.Outcome, appiumURL string, nil) Report { return report }`

	if !publishesIosOfferables(blind) {
		t.Fatal("a platform-blind loop over Offerable() DOES publish iOS devices")
	}
	if publishesIosOfferables(filtered) {
		t.Fatal("a loop that skips inventory.IOS does not publish them, and the walk must see the difference")
	}
	if publishesIosOfferables(none) {
		t.Fatal("a builder that publishes no offerables at all publishes no iOS ones either")
	}

	// The claim detector decides too: it must catch the sentence that shipped and leave an honest one alone.
	if !claimsNotOffered("the phone is reported as present and is not offered for runs") {
		t.Fatal("the exact sentence that shipped must be caught")
	}
	if claimsNotOffered("an Appium session for ios needs the xcuitest driver installed in Appium") {
		t.Fatal("a sentence about a requirement is not a claim about offering")
	}

	// The comment strip and the literal extraction decide too: a claim in PROSE is not a claim the product makes,
	// and a claim in a LITERAL is — which is the exact distinction the first draft of this walk got backwards.
	src := "// the old remedy said \"is not offered for runs\"\nc.Remedy = \"install Xcode\"\n"
	if lits := stringLiterals(withoutComments(src)); len(lits) != 1 || lits[0] != "install Xcode" {
		t.Fatalf("a comment quoting the retired claim must not become a sentence: %q", lits)
	}
	if lits := stringLiterals(withoutComments("c.Remedy = \"the phone is not offered for runs\"\n")); len(lits) != 1 || !claimsNotOffered(lits[0]) {
		t.Fatalf("a literal carrying the claim must be caught: %q", lits)
	}

	// …and funcBody must brace-match rather than stop at the first `}`.
	body := funcBody("func A() {\n if x { y() }\n z()\n}\nfunc B() {}", "A")
	if !strings.Contains(body, "z()") {
		t.Fatalf("funcBody stopped at a nested brace: %q", body)
	}
}

// ── THE CENSUS: EVERY CHECK THE DOCTOR RUNS, FROM THE EXECUTED Diagnose ───────────────────────────────────────
//
// DISCOVERED, never enumerated. The population is whatever `Diagnose` returns on a machine with both platforms in
// play, so a check added tomorrow joins this census by existing. What it requires of each is the file's own rule,
// which had never been enforced: a requirement traces to a caller, and a state that blocks a run carries a
// sentence a person can act on.
func TestEveryCheckTracesToACallerAndEveryBlockCarriesARemedy(t *testing.T) {
	// TWO MACHINES, ONE WALK — and the guard is INSIDE the census rather than beside it. A first draft ran the
	// rules over ONE executed report and kept its instrument guard in a separate test; replacing this test's
	// population with a hand-written list of checks then PASSED, because the guard was guarding a different call.
	// Requiring two machines to DISAGREE is the thing a constant cannot do.
	bare := doctor.Diagnose(context.Background(), machine{}.env(), bothPlatformsSeen())
	equipped := doctor.Diagnose(context.Background(), equippedMachine().env(), bothPlatformsSeen())

	if len(bare.Checks) < 6 {
		t.Fatalf("the walk must have run the real Diagnose, got %d checks", len(bare.Checks))
	}
	if !equalStrings(names(bare), names(equipped)) {
		t.Fatalf("the same machine shape runs the same checks: %v vs %v", names(bare), names(equipped))
	}
	if equalStrings(states(bare), states(equipped)) {
		t.Fatalf("a bare machine and an equipped one cannot answer identically — this population is not being executed: %v", states(bare))
	}

	// The per-check rule, over BOTH reports: the file's own contract, which had never been enforced.
	for _, report := range []doctor.Report{bare, equipped} {
		seen := map[string]bool{}
		for _, c := range report.Checks {
			if c.Name == "" {
				t.Fatal("a check with no name cannot be searched for by the person reading it")
			}
			if seen[c.Name] {
				t.Fatalf("two checks named %q — a reader cannot tell which answered", c.Name)
			}
			seen[c.Name] = true
			if c.Need == "" {
				t.Fatalf("%q states no requirement; a requirement nobody can trace to a caller is one somebody guessed", c.Name)
			}
			if c.State == "" {
				t.Fatalf("%q established nothing and did not say so — Unknown is the answer for that", c.Name)
			}
			if c.State.Blocking() && c.Remedy == "" {
				t.Fatalf("%q blocks a run and offers no sentence a person can act on", c.Name)
			}
		}
		for _, name := range []string{"Appium driver xcuitest", "Xcode command-line tools", "iOS Developer Mode"} {
			if !seen[name] {
				t.Fatalf("iOS gets the checks Android has; %q is missing", name)
			}
		}
	}

	// …and the disagreement is in NAMED checks, not only in the aggregate.
	if find(t, bare, "Xcode command-line tools").State != doctor.Missing ||
		find(t, equipped, "Xcode command-line tools").State != doctor.Satisfied {
		t.Fatal("the walk decides whether xcodebuild is here, and the two machines differ")
	}
	if find(t, bare, "iOS Developer Mode").State != doctor.Unknown ||
		find(t, equipped, "iOS Developer Mode").State != doctor.Satisfied {
		t.Fatal("the phone's own answer decides, and a machine that cannot ask says unknown")
	}
	if doctor.ExitCode(bare) == 0 {
		t.Fatal("a machine with two phones and no toolchain is blocked, and the census must see it")
	}
	if doctor.ExitCode(equipped) != 0 {
		t.Fatal("a fully equipped machine blocks nothing")
	}
}

// equippedMachine is the operator's Mac as measured on 2026-08-24 — adb, Appium 3.5.0 answering, both drivers,
// Xcode 26.2, and a phone whose Developer Mode is on.
func equippedMachine() machine {
	return machine{
		onPath: map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium", "xcodebuild": "/usr/bin/xcodebuild"},
		run: map[string]string{
			"appium driver list --installed": "- uiautomator2@7.5.2 [installed (npm)]\n- xcuitest@11.9.0 [installed (npm)]",
			"xcodebuild -version":            "Xcode 26.2",
			"xcode-select -p":                "/Applications/Xcode.app/Contents/Developer",
		},
		listening: map[string]bool{"127.0.0.1:5037": true},
		appiumURL: "http://127.0.0.1:4723",
		devMode:   []doctor.DeveloperModeReading{{UDID: "00008030", Enabled: true}},
	}
}

func names(r doctor.Report) []string {
	out := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		out = append(out, c.Name)
	}
	return out
}

func states(r doctor.Report) []string {
	out := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		out = append(out, string(c.State))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// bothPlatformsSeen: an Android phone AND an iPhone attached, so every check is in play.
func bothPlatformsSeen() []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1"}}, nil),
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030", Model: "iPhone 15"}}, nil),
	}
}

// ── THE iOS DRIVER CHECK ──────────────────────────────────────────────────────────────────────────────────────
//
// The instrument is the one Android already uses — `appium driver list --installed` — because the justification
// was never "the agent calls it" but "an Appium session for X needs X's driver installed in Appium", which is
// platform-neutral. These pin the three answers that instrument can give for iOS.
func TestTheIosDriverCheckReadsAppiumsOwnDriverList(t *testing.T) {
	iphone := []inventory.Outcome{
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030", Model: "iPhone 15"}}, nil),
	}
	withDrivers := func(list string) doctor.Check {
		m := machine{
			onPath: map[string]string{"appium": "/usr/local/bin/appium"},
			run:    map[string]string{"appium driver list --installed": list},
		}
		return find(t, doctor.Diagnose(context.Background(), m.env(), iphone), "Appium driver xcuitest")
	}

	// ABSENT: an Appium that is there and has no xcuitest is a real, actionable gap — and it BLOCKS.
	absent := withDrivers("- uiautomator2@7.5.2 [installed (npm)]")
	if absent.State != doctor.Missing {
		t.Fatalf("xcuitest is not in that list, so it is missing, got %q", absent.State)
	}
	if !absent.State.Blocking() {
		t.Fatal("an iPhone attached and no driver to drive it is a machine that cannot run a test")
	}
	if !strings.Contains(absent.Remedy, "appium driver install xcuitest") {
		t.Fatalf("the remedy is the command a person runs, got %q", absent.Remedy)
	}

	// PRESENT: the operator's own machine — xcuitest@11.9.0, measured 2026-08-24.
	present := withDrivers("- uiautomator2@7.5.2 [installed (npm)]\n- xcuitest@11.9.0 [installed (npm)]")
	if present.State != doctor.Satisfied {
		t.Fatalf("xcuitest IS in that list, got %q", present.State)
	}
	if present.State.Blocking() {
		t.Fatal("an installed driver blocks nothing")
	}

	// The SAME instrument as Android, stated so a second one cannot quietly appear.
	if !strings.Contains(find(t, doctor.Diagnose(context.Background(), machine{
		onPath: map[string]string{"appium": "/usr/local/bin/appium"},
		run:    map[string]string{"appium driver list --installed": "- xcuitest@11.9.0 [installed (npm)]"},
	}.env(), iphone), "Appium driver xcuitest").Need, "installed in Appium") {
		t.Fatal("the requirement is Appium's driver list, and the sentence must say so")
	}
}
