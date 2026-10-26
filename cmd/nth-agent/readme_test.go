package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// WHAT_WE_LEAVE_RUNNING_ON_YOUR_PHONE item 4 — THE REGISTER A REPO CAN SEAL ALONE.
//
// The binary's verb table IS the population. Two documents claimed to describe it and neither was it: this
// README was headed "The one command" and documented `devices` while the binary had four, and the web app's docs
// site listed three (and not `devices`). The README and the table are in ONE TREE, so that half is sealable here
// — and the other half deliberately is not: a cross-repo seal would join the ones the farm was running with no
// siblings and the ones the web app excludes, and A SEAL THAT NEVER RUNS READS GREEN WHILE THE REGISTERS SAY IT IS
// GUARDED.
//
// ⚠️ THAT WARNING WAS RIGHT AND THE NUMBERS IN IT WENT STALE — it said "the 11 the web app excludes" when there
// were 15, so a register about seals nobody runs had itself drifted about how many there are. THE NUMBERS ARE
// GONE rather than corrected: a count in a comment is a claim that decays, and this file's own neighbours already
// say so ("a count in a comment is stale from the moment it is typed"). It is now derived, every run, by the
// thing that runs the cross-repository seals, beside
// `go test`, which counts what is declared, counts what executed, and fails when the second is smaller.
//
// ⚠️ AND THE COST OF NOT SEALING THIS HALF WAS PAID. `upload` was added here on 2026-09-02 while the web app's docs
// page went stale, and the seal on the other side stayed red for three slices because each was scoped to
// repositories that did not include the one holding it. The reasoning below is unchanged — this repository still
// cannot seal a document it does not contain — but the runner is now the answer to "then who does".

// verbsIn reads the verb table out of a source file. THE PATH IS A PARAMETER, which is the whole guard — six
// slices in this register broke on a census whose input could not be substituted, and the guard below hands this
// a planted table whose correct answer differs from the real one.
func verbsIn(t *testing.T, sourcePath string) []string {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("reading %s: %v", sourcePath, err)
	}
	start := strings.Index(string(source), "var verbs = []verbSpec{")
	if start < 0 {
		return nil
	}
	block := string(source)[start:]
	if end := strings.Index(block, "\n}\n"); end > 0 {
		block = block[:end]
	}
	// `, Flags:` is what separates a VERB from a flagSpec — flag entries are `{Name: "--json"}` with no Flags
	// member of their own, and their names begin with a dash besides.
	found := regexp.MustCompile(`\{Name: "([a-z][a-z-]*)", Flags:`).FindAllStringSubmatch(block, -1)
	verbs := make([]string, 0, len(found))
	for _, m := range found {
		verbs = append(verbs, m[1])
	}
	return verbs
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

func TestEveryVerbInTheTableIsDocumentedInTheReadme(t *testing.T) {
	root := repoRoot(t)
	verbs := verbsIn(t, filepath.Join(root, "cmd", "nth-agent", "cli.go"))
	if len(verbs) < 4 {
		t.Fatalf("the walk must have read the real table, got %v", verbs)
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range verbs {
		// `npx {{PACKAGE}}@latest <verb>` — the form a person can copy after the README's own install, not a bare word that
		// might be prose. The bare `nth-agent <verb>` is not on their PATH after npx, and `npx nth-agent` is another package.
		// @latest (NTH_AGENT_0_4_3): with no version npx runs whatever copy it already has.
		if !strings.Contains(string(readme), "npx {{PACKAGE}}@latest "+verb) {
			t.Fatalf("the binary has a %q verb and the README does not document `npx {{PACKAGE}}@latest %s` — "+
				"this is the first page a developer meets, and the whole proposition is that they do not have to "+
				"learn our know-how", verb, verb)
		}
	}

	// AND THE HEADING STOPPED CLAIMING THERE IS ONE. A count in a heading is a claim like any other.
	if strings.Contains(string(readme), "## The one command") {
		t.Fatalf("the README is headed \"The one command\" and the binary has %d", len(verbs))
	}

	// …AND THE LIST UNDER "The commands" IS THE TABLE, NOT A PREFIX OF IT (AN_AGENT_THAT_SHIPS_ITS_NOTICES). A verb mentioned
	// somewhere below satisfies the walk above, while the list a reader takes for the whole surface misses it — `notices`
	// arrived in 0.3.0, and this is what makes it arrive in the list too. One line per verb, and no line for a verb the
	// binary does not have.
	_, block, found := strings.Cut(string(readme), "## The commands\n\n```\n")
	if !found {
		t.Fatal("the README has no command list under `## The commands` — the first thing a reader looks for")
	}
	block, _, _ = strings.Cut(block, "\n```")
	listed := map[string]int{}
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "npx" || fields[1] != "{{PACKAGE}}@latest" {
			t.Fatalf("the command list has a line that is not `npx {{PACKAGE}}@latest <verb>`: %q", line)
		}
		listed[fields[2]]++
	}
	for _, verb := range verbs {
		if listed[verb] != 1 {
			t.Errorf("the command list names `npx {{PACKAGE}}@latest %s` %d time(s) — once, like every verb in the table", verb, listed[verb])
		}
		delete(listed, verb)
	}
	for extra := range listed {
		t.Errorf("the command list names `npx {{PACKAGE}}@latest %s`, and the binary has no such verb", extra)
	}
}

// ── DOCTOR'S LINES, AS THE DOCTOR PRINTS THEM (AN_AGENT_THAT_SHIPS_ITS_NOTICES) ─────────────────────────────────────
//
// The README said doctor checks "Eight requirements" while Diagnose appended thirteen, plus one live-view line per phone:
// a count typed into prose on the day it was true. Now the paragraph names each check by the name the doctor prints, and
// this holds it to checks.go — every `Name:` a check is built with, and each Appium driver Diagnose asks about — and the
// number of states to the State constants in doctor.go.

// checkNamesIn reads the names doctor's checks are built with out of a source file. THE PATH IS A PARAMETER: the guard
// below hands it a planted file whose answer differs from the real one.
func checkNamesIn(t *testing.T, sourcePath string) []string {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("reading %s: %v", sourcePath, err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`Name:\s+"([^"]+)"`).FindAllStringSubmatch(string(source), -1) {
		names = append(names, strings.TrimSpace(m[1]))
	}
	for _, m := range regexp.MustCompile(`appiumDriver\(ctx, env, "([a-z0-9]+)"`).FindAllStringSubmatch(string(source), -1) {
		names = append(names, "Appium driver "+m[1])
	}
	return names
}

var numberWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}

func TestTheReadmeNamesEveryCheckTheDoctorPrints(t *testing.T) {
	root := repoRoot(t)
	names := checkNamesIn(t, filepath.Join(root, "internal", "doctor", "checks.go"))
	if len(names) < 13 {
		t.Fatalf("the walk must have read the real checks, got %d: %v", len(names), names)
	}
	section := readmeSection(t, "### `doctor` — what this machine is missing")
	if end := strings.Index(section[1:], "\n### "); end >= 0 {
		section = section[:end+1]
	}
	plain := strings.ToLower(fold(strings.ReplaceAll(section, "**", "")))
	for _, name := range names {
		if !strings.Contains(plain, strings.ToLower(name)) {
			t.Errorf("doctor prints a check called %q and the README's doctor paragraph does not name it — a reader meets "+
				"a line in the output that the page that introduces it never mentions", name)
		}
	}

	doctorGo, err := os.ReadFile(filepath.Join(root, "internal", "doctor", "doctor.go"))
	if err != nil {
		t.Fatal(err)
	}
	states := len(regexp.MustCompile(`(?m)^\s+[A-Z]\w*\s+State\s*=\s*"`).FindAllString(string(doctorGo), -1))
	if states < 3 || states >= len(numberWords) {
		t.Fatalf("instrument guard: %d State constants read out of doctor.go", states)
	}
	if !strings.Contains(plain, numberWords[states]+" states") {
		t.Errorf("doctor reports each check in one of %d states, and the README's doctor paragraph does not say %q", states,
			numberWords[states]+" states")
	}
	if regexp.MustCompile(`(?i)\b(eight|nine|ten|eleven|twelve|thirteen|fourteen) requirements\b`).MatchString(plain) {
		t.Error("the doctor paragraph counts its requirements in prose again — the count went stale the last time")
	}
}

func TestTheCheckWalkReadsTheFileItIsGiven(t *testing.T) {
	planted := filepath.Join(t.TempDir(), "checks.go")
	if err := os.WriteFile(planted, []byte("func a() Check { return Check{Name: \"planted check\"} }\n"+
		"report.Checks = append(report.Checks, appiumDriver(ctx, env, \"planteddriver\", x, y))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := checkNamesIn(t, planted)
	if len(got) != 2 || got[0] != "planted check" || got[1] != "Appium driver planteddriver" {
		t.Fatalf("the walk must read the checks of the file it is given, got %v", got)
	}
}

// ── LIVE VIEW, AS `devices` AND `doctor` PRINT IT ────────────────────────────────────────────────────────────────────
//
// 0.2.0 carried each phone's live view and the README never said so: `devices` printed a `live view:` line under every
// phone that no page explained. The section names the line by what render.Human actually prints, and the iPhone's driver
// floor by the constant the judgement reads.
func TestTheReadmeSaysWhatTheLiveViewLinesMean(t *testing.T) {
	section := readmeSection(t, "## Watching a run on your phone — live view")
	customer, _, _ := strings.Cut(readmeText(t), readmeBoundary)
	if !strings.Contains(customer, section) {
		t.Fatal("the live view section is below the registry boundary — a customer never reads it")
	}

	var rendered bytes.Buffer
	phone := inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT30ABCDE", Model: "SM-A546B"}}, nil)
	if err := render.Human(&rendered, []inventory.Outcome{phone}, func(inventory.Platform, string) liveview.Availability {
		return liveview.Availability{Available: true, Port: 14723, Reason: "planted"}
	}); err != nil {
		t.Fatal(err)
	}
	label := ""
	for _, line := range strings.Split(rendered.String(), "\n") {
		if before, _, ok := strings.Cut(line, ": yes"); ok {
			label = strings.TrimSpace(before)
		}
	}
	if label == "" {
		t.Fatalf("instrument guard: `devices` printed no live view line for a phone with one:\n%s", rendered.String())
	}
	if !strings.Contains(section, "`"+label+":`") {
		t.Errorf("`devices` prints %q under each phone and the live view section does not name that line", label+":")
	}
	floor := "XCUITest " + liveview.XcuitestForwardsFrom.Version() + " or later"
	if !strings.Contains(fold(section), floor) {
		t.Errorf("the live view section does not say %q — the driver the judgement requires for an iPhone", floor)
	}
	if !strings.Contains(fold(section), "never stops a run") {
		t.Error("the live view section does not say that live view never stops a run — doctor's live view lines are advisory")
	}
}

// THE INSTRUMENT GUARD: the walk reads the file it is given.
func TestTheVerbWalkReadsTheTableItIsGiven(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted.go")
	if err := os.WriteFile(planted, []byte(`
var verbs = []verbSpec{
	{Name: "invented", Flags: []flagSpec{{Name: "--json"}}},
	{Name: "alsoinvented", Flags: []flagSpec{}},
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := verbsIn(t, planted)
	want := []string{"invented", "alsoinvented"}
	if len(got) != len(want) {
		t.Fatalf("the walk must read the table it is given, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("planted table: want %v, got %v", want, got)
		}
	}
	// …and ONLY that one. A walk answering about the repository would find the real verbs here too.
	for _, real := range []string{"devices", "doctor", "connect", "setup-ios"} {
		for _, g := range got {
			if g == real {
				t.Fatalf("the walk returned %q from a planted table that does not contain it", real)
			}
		}
	}
}

// ── THE_PORT_BELONGS_TO_THE_MACHINE_TOO: the probe's port is the RULE's, not a constant ───────────────────────

// wdaPortArgumentsIn returns the second argument of every ios.WithWdaPort call in a source file. THE PATH IS A
// PARAMETER, and the guard below plants a file whose answer differs from the real one.
func wdaPortArgumentsIn(t *testing.T, sourcePath string) []string {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("reading %s: %v", sourcePath, err)
	}
	found := regexp.MustCompile(`ios\.WithWdaPort\([^,]+,\s*([^,]+),`).FindAllStringSubmatch(string(source), -1)
	args := make([]string, 0, len(found))
	for _, m := range found {
		args = append(args, strings.TrimSpace(m[1]))
	}
	return args
}

// ONE CHOOSER, TWO READERS. The port a forward asks on must be the one PASSED IN — the number this machine
// DECLARED to the farm — never a constant and never a second derivation beside the first.
//
// The shipped version probed a library constant, 8100, and its "nothing is listening" was read as the probe
// working: measured on the operator's phone, 8100 answered "connection reset by peer" while the port the run
// actually configured answered HTTP 200. The slice after it derived the number in two places instead, which is
// two chances to be wrong about the same thing; now the machine picks once and everything carries it.
func TestEveryWdaForwardUsesTheDeclaredPortAndNeverItsOwnDerivation(t *testing.T) {
	main := filepath.Join(repoRoot(t), "cmd", "nth-agent", "main.go")
	args := wdaPortArgumentsIn(t, main)

	if len(args) < 2 {
		t.Fatalf("the walk must have found the probe and the stop, got %v", args)
	}
	for _, arg := range args {
		if arg != "port" {
			t.Fatalf("a WebDriverAgent forward asks for port %q — it must be the DECLARED port passed in, not a "+
				"constant and not a derivation of its own. Two derivations are two chances to be wrong about one "+
				"number, and a constant is how the probe spent a slice answering about a port nothing was on.", arg)
		}
	}

	// …AND THERE IS EXACTLY ONE PLACE THAT DECIDES. The chooser is what the registration publishes from and what
	// the probe asks on; a second construction of it would be two machines' opinions inside one process.
	source, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(source), "&wda.PortChooser{"); n != 1 {
		t.Fatalf("main must construct exactly one PortChooser, found %d", n)
	}
	if !strings.Contains(string(source), "WdaPortFor:   chooser.For") {
		t.Fatal("the loop must declare ports from the same chooser the probe reads — one home, or it is not one number")
	}
	if strings.Contains(string(source), "wda.PortFor(") {
		t.Fatal("main must not derive the port itself: the chooser owns the rule, because a derived number that " +
			"was never checked against this machine is exactly what this slice replaced")
	}
}

func TestTheWdaPortWalkReadsTheFileItIsGiven(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted.go")
	if err := os.WriteFile(planted, []byte("_ = ios.WithWdaPort(udid, 8100, func(string) error { return nil })\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := wdaPortArgumentsIn(t, planted)
	if len(args) != 1 || args[0] != "8100" {
		t.Fatalf("the walk must read the file it is given, got %v", args)
	}
	for _, a := range args {
		if strings.Contains(a, "PortFor") {
			t.Fatalf("the walk returned a real call from a planted file: %v", args)
		}
	}
}

// ── THE_FLAGS_NOBODY_WROTE_DOWN: the README documents every input, held against the CODE ──────────────────────
//
// The verb seal above covered the verb NAMES and stopped there: 6 of 16 flags and 7 of 8 environment reads were
// absent from the README, discovered by measurement. Same shape the web app just fixed — a guard whose far end is
// attached and whose reach is too short. These extend the same pattern: the verbs table is the flag population,
// the source tree's explicit env reads are the variable population, and the README is held against BOTH, in BOTH
// directions — an undocumented input fails, and so does a documented input the code does not read.

// flagsIn reads every flag name out of the verbs table in a source file. THE PATH IS A PARAMETER, same guard as
// verbsIn: the companion below hands it a planted table whose correct answer differs from the real one.
func flagsIn(t *testing.T, sourcePath string) []string {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("reading %s: %v", sourcePath, err)
	}
	start := strings.Index(string(source), "var verbs = []verbSpec{")
	if start < 0 {
		return nil
	}
	block := string(source)[start:]
	if end := strings.Index(block, "\n}\n"); end > 0 {
		block = block[:end]
	}
	found := regexp.MustCompile(`\{Name: "(--[a-z][a-z-]*)"`).FindAllStringSubmatch(block, -1)
	seen := map[string]bool{}
	flags := make([]string, 0, len(found))
	for _, m := range found {
		if !seen[m[1]] {
			seen[m[1]] = true
			flags = append(flags, m[1])
		}
	}
	return flags
}

// questionsIn reads the standard questions ("--help", "--version") out of the source, so the reverse direction
// below can allow them without a hand-kept list drifting from the code.
func questionsIn(t *testing.T, sourcePath string) []string {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("reading %s: %v", sourcePath, err)
	}
	m := regexp.MustCompile(`var questions = \[\]string\{([^}]*)\}`).FindStringSubmatch(string(source))
	if m == nil {
		return nil
	}
	return regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(m[1], -1)
}

func TestEveryFlagInTheTableIsDocumentedInTheReadmeAndNoOtherFlagIs(t *testing.T) {
	root := repoRoot(t)
	declared := flagsIn(t, filepath.Join(root, "cmd", "nth-agent", "cli.go"))
	if len(declared) < 10 {
		t.Fatalf("the walk must have read the real table, got %d flags: %v", len(declared), declared)
	}

	readmeBytes, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(readmeBytes)

	// FORWARD: every declared flag is documented. The boundary matters — `--why` must not be satisfied by
	// `--why-not` — so the flag is required to appear followed by a non-name character.
	for _, flag := range declared {
		if !regexp.MustCompile(regexp.QuoteMeta(flag) + `([^a-z-]|$)`).MatchString(readme) {
			t.Fatalf("the binary declares %s and the README does not document it — a flag a developer can pass "+
				"and cannot read about is know-how, and the whole proposition is that they need none", flag)
		}
	}

	// REVERSE: every flag-shaped token the README uses is one the binary declares — a documented ghost is a
	// promise the parser will answer with "unknown option". The standard questions are allowed (read from the
	// source, not a list here), and so is ONE stated exclusion:
	//   --platform: the README's sentence is a DENIAL — "no required `--platform`" — documenting that the flag
	//   does not exist. Requiring denials to be declared would delete the denial.
	allowed := map[string]bool{
		"--platform": true,
		// ⚠️ SOMEBODY ELSE'S FLAG, on somebody else's command. The §Getting it section tells a person to verify
		// their download with `shasum -a 256 -c SHA256SUMS --ignore-missing` — needed because they will have
		// downloaded one archive of three, and without it shasum reports the other two as failures. Requiring
		// this repository to DECLARE a flag it does not own would mean either deleting the verification step or
		// inventing a flag; the census's population is THIS BINARY's surface, and this is not on it.
		"--ignore-missing": true,
		// ⚠️ APPIUM'S FLAGS, NAMED SO NOBODY USES THEM (AN_AGENT_READY_TO_BE_READ). The README's promise tells a customer
		// not to start Appium with these: they switch on Appium features that run commands on this computer. The sentence
		// is a warning about somebody else's command, like the two above, not a flag of this binary.
		"--relaxed-security": true,
		"--allow-insecure":   true,
	}
	for _, q := range questionsIn(t, filepath.Join(root, "cmd", "nth-agent", "cli.go")) {
		allowed[q] = true
	}
	for _, f := range declared {
		allowed[f] = true
	}
	for _, tok := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(readme, -1) {
		if !allowed[tok] {
			t.Fatalf("the README documents %s and the binary does not declare it — a reader who types it will be "+
				"told 'unknown option', which is worse than the flag not being mentioned", tok)
		}
	}
}

func TestTheFlagWalkReadsTheTableItIsGiven(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted.go")
	if err := os.WriteFile(planted, []byte(`
var verbs = []verbSpec{
	{Name: "invented", Flags: []flagSpec{{Name: "--invented-flag"}, {Name: "--also", TakesValue: true}}},
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := flagsIn(t, planted)
	want := []string{"--invented-flag", "--also"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("the walk must read the table it is given: want %v, got %v", want, got)
	}
	// A WALK THAT FINDS NOTHING MUST NOT PASS — the census's floor depends on this returning nil, not [].
	if flagsIn(t, filepath.Join(dir, func() string {
		empty := filepath.Join(dir, "empty.go")
		_ = os.WriteFile(empty, []byte("package planted\n"), 0o600)
		return "empty.go"
	}())) != nil {
		t.Fatal("a file with no verbs table must yield nil, so the real census's floor can refuse it")
	}
}

// envReadsIn walks every non-test .go file under root and returns each distinct name read via os.Getenv or
// os.LookupEnv. BOTH forms, deliberately: the measurement that started this slice used a Getenv-only regex and
// missed NO_COLOR, which is read via LookupEnv (presence, not value — no-color.org). THE ROOT IS A PARAMETER.
func envReadsIn(t *testing.T, root string) []string {
	t.Helper()
	pattern := regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\("([A-Z][A-Z0-9_]*)"\)`)
	seen := map[string]bool{}
	var names []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range pattern.FindAllStringSubmatch(string(source), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				names = append(names, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// THE ONE STATED EXCLUSION — PATH. It is an input in the honest sense: the doctor resolves `adb`, `appium` and
// `xcodebuild` on it (exec.LookPath), and a different PATH is a different diagnosis. But it is the platform's
// resolution mechanism, not this agent's variable — the agent never reads it by name, so it never matches the
// pattern above. That absence is a DECISION, written here so it cannot be mistaken for a regex accident: the
// census population is "variables this agent reads explicitly", and the README's inputs section says the same
// thing in its PATH sentence. (APPIUM_HOME, by contrast, IS ours — setup and connect read it by name to find
// Appium's WebDriverAgent project — so it is in the population and documented.)
func TestEveryEnvironmentVariableTheAgentReadsIsDocumentedAndNoOtherIsClaimed(t *testing.T) {
	root := repoRoot(t)
	reads := envReadsIn(t, root)
	if len(reads) < 5 {
		t.Fatalf("the walk must have found the real reads, got %d: %v", len(reads), reads)
	}

	readmeBytes, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(readmeBytes)

	// FORWARD: every variable the agent reads is documented.
	for _, name := range reads {
		if !strings.Contains(readme, name) {
			t.Fatalf("the agent reads %s and the README does not document it — a variable that changes behaviour "+
				"and cannot be read about is discovered by experiment, one developer at a time", name)
		}
	}

	// REVERSE: every variable the README claims, in the families this agent owns or borrows, is really read.
	// Bounded to those families on purpose: the README legitimately names words like PATH, CI and ANSI that are
	// not environment claims, and a census that flagged prose would teach people to stop writing it.
	inReads := map[string]bool{}
	for _, r := range reads {
		inReads[r] = true
	}
	for _, tok := range regexp.MustCompile(`\b(?:NTH_AGENT_[A-Z0-9_]+|APPIUM_[A-Z0-9_]+|NO_COLOR)\b`).FindAllString(readme, -1) {
		if !inReads[tok] {
			t.Fatalf("the README documents %s and nothing in the source reads it — a variable a developer exports "+
				"to no effect is a silent lie", tok)
		}
	}
}

func TestTheEnvWalkReadsTheTreeItIsGiven(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(
		"package planted\nvar a = os.Getenv(\"PLANTED_ONE\")\nvar b, _ = os.LookupEnv(\"PLANTED_TWO\")\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A _test.go read must NOT count: tests set what they like.
	if err := os.WriteFile(filepath.Join(dir, "planted_test.go"), []byte(
		"package planted\nvar c = os.Getenv(\"PLANTED_TEST_ONLY\")\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := envReadsIn(t, dir)
	if len(got) != 2 || got[0] != "PLANTED_ONE" || got[1] != "PLANTED_TWO" {
		t.Fatalf("the walk must read Getenv AND LookupEnv from the tree it is given, and skip tests: got %v", got)
	}
	// A WALK THAT FINDS NOTHING MUST NOT PASS the real census: prove the empty answer is representable…
	empty := t.TempDir()
	if n := len(envReadsIn(t, empty)); n != 0 {
		t.Fatalf("an empty tree must yield no reads, got %d", n)
	}
	// …because the census's floor (>= 5) is what refuses it there.
}
