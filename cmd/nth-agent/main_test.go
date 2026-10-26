package main

import (
	"bytes"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A QUESTION IS NOT A MISTAKE — the difference is the exit code a script reads.

func runParse(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, err bytes.Buffer
	inv, c := parse(args, verbs, &out, &err)
	if inv != nil {
		t.Fatalf("parse(%q) wanted to proceed; these tests only exercise answered lines", args)
	}
	return c, out.String(), err.String()
}

func TestHelpIsAQuestionAnsweredWithExitZero(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"connect", "--help"}, {"devices", "-h"}, {"connect", "--farm", "http://127.0.0.1:5080", "--help"}} {
		code, out, errOut := runParse(t, args...)
		if code != 0 {
			t.Fatalf("%q: a question must exit 0, got %d", args, code)
		}
		if !strings.HasPrefix(out, "nth-agent — the NthCase device agent.") {
			t.Fatalf("%q: the usage belongs on stdout, got %q", args, out)
		}
		if errOut != "" {
			t.Fatalf("%q: a question is not a complaint; stderr must be empty, got %q", args, errOut)
		}
	}
}

func TestVersionIsAQuestion(t *testing.T) {
	code, out, errOut := runParse(t, "--version")
	// Two lines since 0.3.0: the version alone, then where the third-party notices are (TestTheVersionSaysWhereTheNoticesAre).
	if code != 0 || out != "nth-agent dev\n"+noticesLine()+"\n" || errOut != "" {
		t.Fatalf("--version: want exit 0 and the version on stdout, got %d %q %q", code, out, errOut)
	}
}

func TestAnUnknownOptionIsAMistakeAnsweredWithExitTwo(t *testing.T) {
	for _, args := range [][]string{{"connect", "--bogus"}, {"devices", "--verbose"}} {
		code, out, errOut := runParse(t, args...)
		if code != 2 {
			t.Fatalf("%q: a mistake must exit 2, got %d", args, code)
		}
		if !strings.HasPrefix(errOut, "unknown option \"") || !strings.Contains(errOut, "Usage:") {
			t.Fatalf("%q: the complaint names the option and the usage follows, on stderr; got %q", args, errOut)
		}
		if out != "" {
			t.Fatalf("%q: nothing belongs on stdout for a mistake, got %q", args, out)
		}
	}
}

func TestAnUnknownCommandIsNamed(t *testing.T) {
	code, _, errOut := runParse(t, "conect")
	if code != 2 || !strings.HasPrefix(errOut, "unknown command \"conect\"") {
		t.Fatalf("an unknown verb must be named before the usage (it used to print the usage and nothing else), got %d %q", code, errOut)
	}
}

func TestAFlagThatNeedsAValueAndHasNoneIsNamed(t *testing.T) {
	for _, args := range [][]string{{"connect", "--farm"}, {"connect", "--farm", ""}, {"connect", "--farm", "   "}, {"devices", "--skip"}} {
		code, _, errOut := runParse(t, args...)
		if code != 2 || !strings.Contains(errOut, " needs a value") {
			t.Fatalf("%q: the flag was silently DROPPED before this slice; now it must be named, got %d %q", args, code, errOut)
		}
	}
}

func TestNoArgumentsIsUsageAndExitTwo(t *testing.T) {
	// A DECISION (see parse): a bare invocation is usage + 2 — the convention when "what is this?" and
	// "you forgot to say what" are one line.
	code, out, errOut := runParse(t)
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "nth-agent — the NthCase device agent.") {
		t.Fatalf("bare invocation: want usage on stderr and exit 2, got %d %q %q", code, out, errOut)
	}
}

func TestAValidLineProceedsWithItsValues(t *testing.T) {
	var out, errOut bytes.Buffer
	inv, code := parse([]string{"connect", "--farm", "http://127.0.0.1:5080", "--skip", "ios", "--skip", "android"}, verbs, &out, &errOut)
	if inv == nil || code != -1 {
		t.Fatalf("a valid line must proceed, got code %d (stderr %q)", code, errOut.String())
	}
	if inv.Verb != "connect" || inv.first("--farm") != "http://127.0.0.1:5080" || len(inv.Values["--skip"]) != 2 {
		t.Fatalf("values must land by flag, repeatable flags keep every value: %+v", inv)
	}
}

func TestTheExitCodeReachesTheShell(t *testing.T) {
	// A labelled SOURCE PIN: main hands run's code to os.Exit — what the tests above assert is what a script reads.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))") {
		t.Fatal("main must exit with run's code")
	}
}

// ── THE SWEEP: every standard question, every mistake, discovered from the table ────────────────────────────

type cliCase struct {
	Args     []string
	Question bool   // true: expect exit 0 on stdout; false: expect exit 2 on stderr
	Names    string // a mistake must name this in its complaint
}

// casesFor DISCOVERS the cases from the verb table: questions at the top level and inside every verb, an
// unknown verb, a bare invocation, and — per verb — an unknown option and every valued flag without a value.
func casesFor(table []verbSpec) []cliCase {
	cases := []cliCase{{Args: nil}, {Args: []string{"no-such-verb"}, Names: `unknown command "no-such-verb"`}}
	for _, q := range questions {
		cases = append(cases, cliCase{Args: []string{q}, Question: true})
	}
	for _, v := range table {
		for _, q := range questions {
			cases = append(cases, cliCase{Args: []string{v.Name, q}, Question: true})
		}
		cases = append(cases, cliCase{Args: []string{v.Name, "--no-such-option"}, Names: `unknown option "--no-such-option"`})
		for _, f := range v.Flags {
			if f.TakesValue {
				cases = append(cases,
					cliCase{Args: []string{v.Name, f.Name}, Names: f.Name + " needs a value"},
					cliCase{Args: []string{v.Name, f.Name, ""}, Names: f.Name + " needs a value"})
			}
		}
	}
	return cases
}

func TestEveryQuestionAndMistakeIsAnsweredInItsOwnRegister(t *testing.T) {
	// INSTRUMENT GUARD: the sweep must discover a planted verb and its valued flag — a hand-written case list
	// cannot produce them.
	planted := []verbSpec{{Name: "planted", Flags: []flagSpec{{Name: "--planted-value", TakesValue: true}, {Name: "--planted-bool"}}}}
	seenVerb, seenFlag := false, false
	for _, c := range casesFor(planted) {
		if len(c.Args) >= 1 && c.Args[0] == "planted" {
			seenVerb = true
		}
		if strings.Contains(c.Names, "--planted-value needs a value") {
			seenFlag = true
		}
	}
	if !seenVerb || !seenFlag {
		t.Fatalf("the sweep does not discover the table it is handed (verb %v, flag %v)", seenVerb, seenFlag)
	}

	for _, c := range casesFor(verbs) {
		var out, errOut bytes.Buffer
		inv, code := parse(c.Args, verbs, &out, &errOut)
		if inv != nil {
			t.Fatalf("%q: every swept line must be ANSWERED, not proceed", c.Args)
		}
		switch {
		case c.Question:
			if code != 0 || out.Len() == 0 || errOut.Len() != 0 {
				t.Fatalf("%q is a question: want exit 0, stdout only; got %d stdout=%d stderr=%d", c.Args, code, out.Len(), errOut.Len())
			}
		default:
			if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "Usage:") {
				t.Fatalf("%q is a mistake: want exit 2, stderr with the usage; got %d stdout=%d stderr=%q", c.Args, code, out.Len(), errOut.String())
			}
			if c.Names != "" && !strings.Contains(errOut.String(), c.Names) {
				t.Fatalf("%q: the complaint must name %q, got %q", c.Args, c.Names, errOut.String())
			}
		}
	}
}

// ── A_SESSION_THE_PLATFORM_CANNOT_AUTHENTICATE: half a credential is a mistake, not a state ────────────────
//
// Reading one-of-two as "configured" sends up a session that cannot authenticate; reading it as "absent"
// silently drives the local Appium while the person believes they are on BrowserStack. BOTH readings hide the
// same typo, and the second is worse because it looks like it worked.
func TestHalfABrowserStackCredentialIsRefusedWithBothNamesInTheSentence(t *testing.T) {
	for _, half := range []struct{ flag, value string }{
		{"--browserstack-user", "acme-user"},
		{"--browserstack-key", "some-key"},
	} {
		var stdout, stderr strings.Builder
		code := run([]string{"connect", "--farm", "https://farm.example", "--token", "sk-agent-x", half.flag, half.value},
			strings.NewReader(""), &stdout, &stderr)

		if code != exitMistake {
			t.Fatalf("%s alone must be a mistake, got exit %d", half.flag, code)
		}
		said := stderr.String()
		for _, name := range []string{"--browserstack-user", "--browserstack-key"} {
			if !strings.Contains(said, name) {
				t.Fatalf("the sentence must name BOTH halves so the reader knows what is missing; got %q", said)
			}
		}
		if strings.Contains(said, half.value) {
			t.Fatalf("the mistake message echoed the value it was given: %q", said)
		}
	}
}

// ── THE_BUILD_NOBODY_WAS_ASKED_TO_SUPPLY: credentials with no build stop at startup ────────────────────────
//
// Every session such an agent would forward is one the hub refuses with "Platform and Browser not valid." — a
// sentence that sends a reader to check a platform and a browser that are both fine, and never mentions the
// build. So it is refused HERE, where the sentence can name the real thing and the next command to type.
func TestCredentialsWithNoBuildAreRefusedOnThePathThatForwardsSessions(t *testing.T) {
	var stdout, stderr strings.Builder
	// --appium IS REQUIRED FOR THIS TEST TO MEAN ANYTHING, and that is the finding rather than a detail. The
	// refusal used to sit beside the credential checks, which run on every connect; it now sits inside the block
	// that builds the tunnel, because the tunnel is the only thing that ever forwards a session. Without
	// --appium there is no tunnel, nothing to refuse, and this test would run a real connect loop — which is
	// exactly how it was caught: it hung for ten minutes instead of failing.
	code := run([]string{"connect", "--farm", "https://farm.example", "--token", "sk-agent-x",
		"--appium", "http://127.0.0.1:4723",
		"--browserstack-user", "acme-user", "--browserstack-key", "some-key"},
		strings.NewReader(""), &stdout, &stderr)

	if code != exitMistake {
		t.Fatalf("credentials with no build must be a mistake, got exit %d", code)
	}
	said := stderr.String()
	for _, must := range []string{"--browserstack-app", "NTH_AGENT_BROWSERSTACK_APP", "upload", "bs://"} {
		if !strings.Contains(said, must) {
			t.Fatalf("the refusal must mention %q — a person who reads it should know the next command to type; got %q", must, said)
		}
	}
	if strings.Contains(said, "some-key") {
		t.Fatalf("the refusal echoed the key: %q", said)
	}
}

// AND AN AGENT WITH NO BROWSERSTACK IS UNTOUCHED BY THE RULE — it never asked for the cloud.
func TestAnAgentWithNoBrowserStackIsNotAskedForABuild(t *testing.T) {
	var stdout, stderr strings.Builder
	// No farm/token either, so it exits on THAT mistake — the point is which sentence it prints.
	run([]string{"connect"}, strings.NewReader(""), &stdout, &stderr)
	if strings.Contains(stderr.String(), "browserstack-app") {
		t.Fatalf("an agent with no BrowserStack account was told it needs a BrowserStack build: %q", stderr.String())
	}
}

// THE POSITION OF THE REFUSAL, SEALED STRUCTURALLY — because the behavioural test for the other direction
// cannot exist: an agent with no --appium that is NOT refused goes on to run a real connect loop, so a test
// asserting "it was not refused" would have to hang to pass.
//
// The property: the MissingApp check lives inside the block THAT BUILDS THE TUNNEL, and nowhere else. An agent
// that reports device health and drives nothing must never be told it needs a BrowserStack build.
//
// ⚠️ THE CONDITION WIDENED AND THIS SEAL CAUGHT IT, which is the seal working rather than the seal being wrong.
// It used to match the literal `appiumURL != ""`; a cloud-only agent forwards sessions with no local Appium at
// all, so that block is now `appiumURL != "" || len(cloudSpecs) > 0`. What is being protected was never the
// spelling of the condition — it is that the refusal guards THE FORWARDING PATH — so the match is on a condition
// that mentions appiumURL, which is true of the old form and the new one and false of the credential checks
// above, where the refusal used to sit wrongly.
func TestTheMissingBuildRefusalOnlyGuardsTheForwardingPath(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", source, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	total, guarded := 0, 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "MissingApp" {
			total++
		}
		branch, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		// The block that builds the tunnel: its condition names appiumURL, whatever else it goes on to say.
		mentionsAppium := false
		ast.Inspect(branch.Cond, func(c ast.Node) bool {
			if ident, ok := c.(*ast.Ident); ok && ident.Name == "appiumURL" {
				mentionsAppium = true
			}
			return true
		})
		if !mentionsAppium {
			return true
		}
		ast.Inspect(branch.Body, func(inner ast.Node) bool {
			if sel, ok := inner.(*ast.SelectorExpr); ok && sel.Sel.Name == "MissingApp" {
				guarded++
			}
			return true
		})
		return true
	})

	if total == 0 {
		t.Fatal("instrument guard: no MissingApp check in main.go at all — the sweep is reading the wrong tree")
	}
	if guarded != total {
		t.Fatalf("%d of %d MissingApp checks sit outside the `appiumURL != \"\"` block — an agent that forwards "+
			"nothing would be told it needs a build for a path it is not on", total-guarded, total)
	}
}

// ── THE PLATFORM NOBODY ESTABLISHED: a list we could not read publishes nothing ─────────────────────────────

// ⚠️ THE RULE WAS ALREADY CHOSEN, AND THIS IS IT APPLIED CONSISTENTLY. A spec is not REFUSED on evidence we could
// not get — "your account does not offer this" would be a claim about a list nobody read. Publishing the rows
// anyway is the same mistake with the opposite sign: a row states a PLATFORM, and the only place that fact exists
// is the list we just failed to read. Refusing on an unread list and asserting on an unread list are one error.
//
// IT IS DERIVED, NOT BRANCHED: the platform comes from the matching catalogue entry, so an empty catalogue
// produces no rows by construction. That is why this is one call in main and not two.
func TestAListWeCouldNotReadPublishesNoCloudRows(t *testing.T) {
	specs := []browserstack.DeviceSpec{
		{DeviceName: "Google Pixel 9", PlatformVersion: "15.0"},
		{DeviceName: "iPhone 15", PlatformVersion: "17.0"},
	}

	if rows := cloudRowsFrom(specs, nil); len(rows) != 0 {
		t.Fatalf("a failed catalogue read must publish nothing, got %d row(s) — each of which would assert a "+
			"platform read from a list nobody could reach", len(rows))
	}
}

// AND WITH A LIST, EACH ROW TAKES THAT LIST'S ANSWER — never a default.
func TestEachRowTakesItsPlatformFromTheEntryThatMatched(t *testing.T) {
	catalogue := []browserstack.CatalogueDevice{
		{OS: "android", OSVersion: "15.0", Device: "Google Pixel 9"},
		{OS: "ios", OSVersion: "17.0", Device: "iPhone 15"},
	}
	rows := cloudRowsFrom([]browserstack.DeviceSpec{
		{DeviceName: "Google Pixel 9", PlatformVersion: "15.0"},
		{DeviceName: "iPhone 15", PlatformVersion: "17.0"},
	}, catalogue)

	if len(rows) != 2 {
		t.Fatalf("both offered specs must produce rows, got %d", len(rows))
	}
	if rows[0].Platform != farm.WireAndroid {
		t.Fatalf("the Pixel must be Android, got %q", rows[0].Platform)
	}
	if rows[1].Platform != farm.WireIos {
		t.Fatalf("the iPhone must be iOS — hard-coding Android here published a row asserting a platform nobody "+
			"established, for a spec BrowserStack really offers; got %q", rows[1].Platform)
	}
	// A spec the list does not contain produces no row, even alongside ones that do.
	if rows := cloudRowsFrom([]browserstack.DeviceSpec{{DeviceName: "Nokia 3310", PlatformVersion: "1.0"}}, catalogue); len(rows) != 0 {
		t.Fatalf("a spec with no matching entry has no established platform and must not be published, got %d", len(rows))
	}
}

// ⚠️ THE ROWS HAVE EXACTLY ONE SOURCE, AND THIS IS THE SEAL AN INJECTION PROVED WAS MISSING.
//
// The two tests above hold `cloudRowsFrom` to its contract, and a faithful injection walked straight past them:
// it left that call alone and filled `cloudRows` a SECOND time, from the specs, with a guessed Android platform,
// on the path where the catalogue read had failed. Nothing could see it — `runConnect` cannot be driven without a
// network, so its body is reachable only as source.
//
// The property is not "cloudRowsFrom is correct" (that is sealed above). It is that NOTHING ELSE FILLS THE
// SLICE: one source means the degraded path stays a consequence of the derivation rather than a second decision
// somebody can add beside it.
func TestTheCloudRowsHaveExactlyOneSource(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", source, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	mentions, fromTheOneFunction := 0, 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			ident, isIdent := lhs.(*ast.Ident)
			if !isIdent || ident.Name != "cloudRows" {
				continue
			}
			mentions++
			// The one permitted right-hand side, and it must be the whole of it: `cloudRowsFrom(...)`.
			if len(assign.Rhs) == 1 {
				if call, isCall := assign.Rhs[0].(*ast.CallExpr); isCall {
					if fn, isFn := call.Fun.(*ast.Ident); isFn && fn.Name == "cloudRowsFrom" {
						fromTheOneFunction++
					}
				}
			}
		}
		return true
	})

	if mentions == 0 {
		t.Fatal("instrument guard: nothing assigns cloudRows in main.go — the sweep is reading the wrong tree")
	}
	if mentions != fromTheOneFunction {
		t.Fatalf("%d of %d assignments to cloudRows do not come from cloudRowsFrom — a second filling is a second "+
			"decision about which devices exist, and the one that gets added is always the one that guesses "+
			"(an append on the path where the catalogue could not be read)", mentions-fromTheOneFunction, mentions)
	}
	// …and a declaration with an initialiser would slip past AssignStmt entirely.
	if !bytes.Contains(source, []byte("var cloudRows []farm.CloudDeviceSpec\n")) {
		t.Fatal("cloudRows must be declared empty and filled once — an initialiser is a second source with a " +
			"different syntax")
	}
}
