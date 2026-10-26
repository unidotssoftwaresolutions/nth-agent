package doctor

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// NINE_GREENS_AND_THE_TWO_THAT_MATTER — the doctor is the most-read surface this agent has and it read like a
// log file: eleven checks at one visual weight, nine of them `ready`, the two that mattered found by reading all
// eleven, and ~600-character caveats folded by the terminal at column 0 into the next check's name.
//
// THE DIFFICULTY, AND WHAT THESE SEALS PROTECT: the caveats are not noise. Three of the six named limits have
// since turned out to be the actual cause and were acted on. So "tidy the output" may not become "hide the
// register" — a caveat on a FAILING check stays, one on a check that stops nothing moves behind a flag, and a
// count line naming the flag is printed whenever anything is withheld. Nothing is ever silently gone.
//
// The fixture is tonight's real report, names and caveat text verbatim.

// theRealCaveat is the provisioning check's actual Limits paragraph — ~600 characters, the longest this output
// has, and the one a design that only looks good on short strings has never been tested against.
const theRealCaveat = "reading a profile off this disk is not running the build: it does not establish that Xcode " +
	"can reach Apple, and it CANNOT say whether the developer certificate is trusted on the phone — iOS exposes no " +
	"such query (go-ios v1.3.2 has none, and its misagent profile copy errors), so the first session on a phone may " +
	"still ask you on the device. A development profile also LISTS THE DEVICES it covers, and this check reads that " +
	"list: a udid missing from every matching profile fails the build by name before the phone is touched."

// theRealPath is the WebDriverAgent project's real location — one token, 95 characters, and a reader copies it.
// Breaking it to satisfy a margin would make the output prettier and the thing it prints unusable.
const theRealPath = "~/.appium/node_modules/appium-xcuitest-driver/node_modules/appium-webdriveragent/WebDriverAgent.xcodeproj"

// tonight is the shape the operator actually saw: a blocker, an unknown that blocks, the advisory the previous
// slice created, and greens — two of which state a caveat.
func tonight() Report {
	return Report{Checks: []Check{
		{Name: "adb", State: Satisfied, Detail: "Android Debug Bridge version 1.0.41"},
		{Name: "Appium endpoint", State: Unknown, Detail: "no endpoint was given (--appium or NTH_AGENT_APPIUM)",
			Remedy: "start Appium and pass its URL"},
		{Name: "iOS Developer Mode", State: Satisfied, Detail: "on for 2 attached device(s)"},
		{Name: "iOS provisioning profile", State: Missing,
			Detail: "no profile on this machine covers com.qanguru.WebDriverAgentRunner.xctrunner for team TEAM7QZ4X2",
			// THE REAL REMEDY, INCLUDING THE PATH — a 95-character token no margin may break, and the case this
			// fixture was silently missing: the first draft of these seals passed only because no fixture string
			// contained a word longer than the line budget, while the real output has two.
			Remedy: "run 'nth-agent setup-ios', or open " + theRealPath + " in Xcode and set your team",
			Limits: theRealCaveat},
		{Name: "iOS WebDriverAgent project", State: Missing, Advisory: true,
			Detail: "WebDriverAgentLib is on team TEAM7QZ4X2 — this does not stop a run here",
			Remedy: "run 'nth-agent setup-ios'",
			Limits: "this reads what the PROJECT stores; a build that passes DEVELOPMENT_TEAM on the command line overrides all of it"},
		{Name: "iOS WebDriverAgent port", State: Satisfied, Detail: "the derived port is available",
			Limits: "this checks THIS machine only, and only right now"},
		{Name: "iOS signing identity", State: NotRequired, Detail: "no iOS device was seen on this machine"},
	}}
}

func render(t *testing.T, r Report, opts Options) string {
	t.Helper()
	var out strings.Builder
	if err := Human(&out, r, opts); err != nil {
		t.Fatalf("render: %v", err)
	}
	return out.String()
}

// ── seal 1 — a blocking check's caveat is printed by default ────────────────────────────────────────────────

func TestSeal1_ABlockingChecksCaveatIsPrintedByDefault(t *testing.T) {
	text := render(t, tonight(), Options{})

	if !strings.Contains(unwrapped(text), theRealCaveat) {
		t.Fatalf("a failing check's caveat is DIAGNOSIS and prints by default; got:\n%s", text)
	}
}

// ── seal 2 — a green check's caveat is withheld, and the count line names the flag ───────────────────────────

func TestSeal2_AGreenChecksCaveatIsWithheldAndTheCountLineNamesTheFlag(t *testing.T) {
	text := render(t, tonight(), Options{})
	flat := unwrapped(text)

	// The port check is `ready` and states a caveat: reference, not diagnosis.
	if strings.Contains(flat, "this checks THIS machine only") {
		t.Fatalf("a passing check's caveat is reference and moves behind %s; got:\n%s", WhyFlag, text)
	}
	// …and so is the ADVISORY one, which is `missing` and stops nothing.
	if strings.Contains(flat, "a build that passes DEVELOPMENT_TEAM on the command line overrides all of it") {
		t.Fatalf("an advisory check stops nothing, so its caveat is reference too; got:\n%s", text)
	}
	// THE PROMISE: never silently gone. The count must be there, must be right, and must name the flag.
	if !strings.Contains(flat, "2 checks that do not stop a run state what they did NOT establish") {
		t.Fatalf("the count line must state how many were withheld; got:\n%s", text)
	}
	if !strings.Contains(flat, WhyFlag) {
		t.Fatalf("the count line must name the flag, or a reader cannot reach the paragraphs; got:\n%s", text)
	}
}

func TestSeal2_WithNothingWithheldThereIsNoCountLine(t *testing.T) {
	r := Report{Checks: []Check{{Name: "adb", State: Satisfied, Detail: "ready"}}}

	if strings.Contains(render(t, r, Options{}), "did NOT establish") {
		t.Fatal("a count of zero is not a sentence anybody needs")
	}
}

// ── seal 3 — --why prints every caveat, and the set equals the set the report holds ──────────────────────────

func TestSeal3_WhyPrintsEveryCaveatTheReportHolds(t *testing.T) {
	r := tonight()
	flat := unwrapped(render(t, r, Options{Why: true}))

	// NO CAVEAT MAY EXIST THAT NO FLAG REVEALS — walked over the report itself, so a check added later is
	// covered by this seal without anybody remembering to add it here.
	for _, c := range r.Checks {
		if c.Limits == "" {
			continue
		}
		if !strings.Contains(flat, c.Limits) {
			t.Fatalf("%s's caveat is reachable through no flag at all; got:\n%s", c.Name, flat)
		}
	}
	// …and with everything shown there is nothing to count.
	if strings.Contains(flat, "did NOT establish — run") {
		t.Fatal("--why withholds nothing, so it must not advertise a flag that would show nothing new")
	}
}

// ── seal 4 — --json is byte-identical ───────────────────────────────────────────────────────────────────────

// TheJsonBytesBeforeThisSlice is doctor.JSON's output for the fixture below, captured from `main` before the
// renderer was touched. --json is the MACHINE's answer: it already carries every field, caveats included, and no
// part of this slice's dressing may reach it.
const theJsonBytesBeforeThisSlice = `{
  "checks": [
    {
      "name": "adb",
      "need": "the probe shells out to adb",
      "state": "satisfied",
      "detail": "version 1.0.41"
    },
    {
      "name": "iOS WebDriverAgent project",
      "need": "xcodebuild signs every target",
      "state": "missing",
      "detail": "no team is set",
      "remedy": "run setup-ios",
      "limits": "this reads what the PROJECT stores",
      "advisory": true
    }
  ]
}
`

func TestSeal4_JsonOutputIsByteIdenticalToBeforeThisSlice(t *testing.T) {
	r := Report{Checks: []Check{
		{Name: "adb", Need: "the probe shells out to adb", State: Satisfied, Detail: "version 1.0.41"},
		{Name: "iOS WebDriverAgent project", Need: "xcodebuild signs every target", State: Missing, Advisory: true,
			Detail: "no team is set", Remedy: "run setup-ios", Limits: "this reads what the PROJECT stores"},
	}}

	var out bytes.Buffer
	if err := JSON(&out, r); err != nil {
		t.Fatalf("json: %v", err)
	}

	if out.String() != theJsonBytesBeforeThisSlice {
		t.Fatalf("--json must be byte-identical to before this slice.\n got:\n%s\nwant:\n%s", out.String(), theJsonBytesBeforeThisSlice)
	}
}

// ── seal 5 — advisory is visually distinct from a blocker, WITH COLOUR OFF ──────────────────────────────────

func TestSeal5_AnAdvisoryMissingIsDistinctFromABlockingMissingWithoutColour(t *testing.T) {
	blocker := Check{Name: "iOS provisioning profile", State: Missing, Detail: "no profile covers it"}
	advisory := Check{Name: "iOS WebDriverAgent project", State: Missing, Advisory: true, Detail: "no team is set"}
	text := render(t, Report{Checks: []Check{blocker, advisory}}, Options{})

	lines := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		for _, c := range []Check{blocker, advisory} {
			if strings.Contains(line, c.Name+":") {
				lines[c.Name] = line
			}
		}
	}
	// The GUTTER differs…
	if !strings.HasPrefix(lines[blocker.Name], "!! ") {
		t.Fatalf("a blocker must be marked as one; got %q", lines[blocker.Name])
	}
	if !strings.HasPrefix(lines[advisory.Name], "fyi") {
		t.Fatalf("a check that stops nothing must not wear the blocker's marker; got %q", lines[advisory.Name])
	}
	// …and so does the HEADLINE WORD, so the distinction survives even a reader who ignores the margin.
	if !strings.Contains(lines[blocker.Name], "MISSING") || strings.Contains(lines[blocker.Name], "does not stop") {
		t.Fatalf("the blocker's headline is unchanged; got %q", lines[blocker.Name])
	}
	if !strings.Contains(lines[advisory.Name], "missing (does not stop a run)") {
		t.Fatalf("the advisory says so IN WORDS — the previous slice's decision must not be undone by the renderer; got %q", lines[advisory.Name])
	}
	// AND THE COUNT AGREES: only one of them is in the "stops a run" list.
	if !strings.Contains(text, "One thing stops a run happening on this machine:") {
		t.Fatalf("exactly one of these blocks; got:\n%s", text)
	}
}

// ── seal 6 — Unknown is neither failure nor pass, colour off and on ─────────────────────────────────────────

func TestSeal6_UnknownRendersAsNeitherFailureNorPass(t *testing.T) {
	r := Report{Checks: []Check{
		{Name: "Appium endpoint", State: Unknown, Detail: "no endpoint was given"},
		{Name: "adb", State: Satisfied, Detail: "ready"},
		{Name: "iOS provisioning profile", State: Missing, Detail: "no profile"},
	}}

	for _, colour := range []bool{false, true} {
		text := render(t, r, Options{Color: colour})
		// Found on the STRIPPED text and kept RAW: with colour on, the name is wrapped in escape codes, so
		// "Appium endpoint:" is not a contiguous substring of the line it appears on.
		var line string
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(stripANSI(l), "Appium endpoint:") {
				line = l
			}
		}
		if !strings.Contains(stripANSI(line), "?? ") {
			t.Fatalf("colour=%v: unknown has its own marker, not the blocker's and not the pass's; got %q", colour, line)
		}
		if !strings.Contains(stripANSI(line), "could not establish") {
			t.Fatalf("colour=%v: and its own words; got %q", colour, line)
		}
		if colour && !strings.Contains(line, ansiYellow) {
			t.Fatalf("with colour on, unknown is yellow — neither the red of a blocker nor the green of a pass; got %q", line)
		}
		if colour && (strings.Contains(line, ansiRed) || strings.Contains(line, ansiGreen)) {
			t.Fatalf("colour=%v: unknown must not wear failure's or success's colour; got %q", colour, line)
		}
		// It is not counted as stopping anything, which is the load-bearing half.
		if strings.Contains(text, "2 things stop") {
			t.Fatalf("colour=%v: an unknown is not a failure; got:\n%s", colour, text)
		}
	}
}

func TestSeal6_EveryDistinctionSurvivesColourBeingOff(t *testing.T) {
	// THE RULE: if a difference exists only in colour, it is a bug. Strip the escapes from the coloured render
	// and it must equal the plain one exactly.
	coloured := stripANSI(render(t, tonight(), Options{Color: true}))
	plain := render(t, tonight(), Options{})

	if coloured != plain {
		t.Fatalf("colour may only repeat what the text says.\ncoloured-stripped:\n%s\nplain:\n%s", coloured, plain)
	}
}

// ── seal 7 — no ANSI reaches a non-terminal writer, or any writer under NO_COLOR ─────────────────────────────

func TestSeal7_NoAnsiReachesANonTerminalWriterOrAnyWriterUnderNoColor(t *testing.T) {
	// A buffer is not a terminal.
	if ColorFor(&bytes.Buffer{}, false) {
		t.Fatal("a buffer is not a terminal")
	}
	// A pipe is not a terminal either — the case a person hits with `doctor | less` or a CI log.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()
	if ColorFor(pw, false) {
		t.Fatal("a pipe is not a terminal: escape sequences in a file are noise a person greps around")
	}
	// NO_COLOR by PRESENCE, not value — honouring the value would mean NO_COLOR=0 turns colour ON.
	for _, value := range []string{"", "0", "false", "1"} {
		t.Setenv("NO_COLOR", value)
		if ColorFor(os.Stdout, false) {
			t.Fatalf("NO_COLOR=%q is set, so colour is off whatever it says", value)
		}
	}
	os.Unsetenv("NO_COLOR")
	// …and the flag wins over everything.
	if ColorFor(os.Stdout, true) {
		t.Fatal("--no-color is a person answering this question directly")
	}
	// And the renderer itself emits nothing when told not to.
	if strings.Contains(render(t, tonight(), Options{}), "\033[") {
		t.Fatal("no escape sequence may reach a writer that was not told to take colour")
	}
}

// ── seal 8 — every caveat paragraph is wrapped and indented ─────────────────────────────────────────────────

func TestSeal8_NoCaveatLineCanBeginAtColumnZero(t *testing.T) {
	text := render(t, tonight(), Options{Why: true})

	inParagraph := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case strings.HasPrefix(trimmed, "not established:") || strings.HasPrefix(trimmed, "what to do:"):
			inParagraph = true
		case trimmed == "" || isCheckLine(line):
			inParagraph = false
			continue
		}
		if !inParagraph {
			continue
		}
		// THE LOAD-BEARING CLAIM IS THE INDENT, not the width: what made a 600-character caveat run into the next
		// check's name was that the terminal folded the overflow at COLUMN 0.
		if !strings.HasPrefix(line, "      ") {
			t.Fatalf("every paragraph line must sit under its check, never at column 0: %q", line)
		}
		// COLUMNS, NOT BYTES (this output is full of three-byte em dashes) — and a line may exceed the budget
		// for exactly one reason: it holds a single token longer than the budget. A path or a URL is a thing a
		// reader copies, so it overflows alone rather than being broken.
		if width := len([]rune(line)); width > wrapWidth {
			if fields := strings.Fields(line); len(fields) != 1 {
				t.Fatalf("a paragraph line is %d columns and holds %d words, so it could have been wrapped: %q",
					width, len(fields), line)
			}
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────────────────

// isCheckLine reports whether a line starts a new check — the only lines allowed at column 0 with a gutter.
func isCheckLine(line string) bool {
	for _, marker := range []string{"!! ", "?? ", "fyi", "ok ", "n/a"} {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return false
}

// unwrapped joins wrapped lines back into one string so a test can assert about a PARAGRAPH rather than about
// where this renderer happened to break it — the width is a rendering decision and the seals about content must
// not become seals about the width.
func unwrapped(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func stripANSI(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '\033' {
			for i < len(text) && text[i] != 'm' {
				i++
			}
			i++ // the 'm'
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}
