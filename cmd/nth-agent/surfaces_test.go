package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TWO SURFACES, ONE KEY — the seal that compares the two mouths of this product.
//
// MEASURED ON A PERSON, 2026-09-03. The operator followed this agent's own documentation to set a BrowserStack
// access key. It failed three times, and the third failure was that he typed HIS COMPUTER'S LOGIN PASSWORD into
// what looked like a password prompt and was not one — a bare `read -rs`, which prints nothing and leaves the
// cursor on an empty line. Nothing was lost (the agent was never started, and `read -rs` input does not reach
// the history file), but had he started it, his machine password would have gone to BrowserStack as an access
// key.
//
// THE DEFECT UNDERNEATH WAS THAT NOTHING COMPARED THE TWO SURFACES. The usage text argued, correctly, that
// `--browserstack-key` puts the value in argv and that an inline `NTH_AGENT_BROWSERSTACK_KEY=…` writes it into
// the shell's history file, and said in as many words that *"prefer the variable" on its own would be poor
// advice*. The README showed exactly that discouraged form, and its flags table said exactly that discouraged
// sentence. **A customer meets the README first.** This file is what stops them disagreeing again.
//
// ⚠️ AND IT IS KEYED ON COMMANDS, NEVER ON THE PROSE AROUND THEM. This programme has met the seal-reads-prose
// shape three times, and the last one made a seal VACUOUS rather than red: a paragraph explaining why a form is
// discouraged necessarily CONTAINS the discouraged form, so a seal that greps the page finds its own
// explanation and is satisfied by it. The rule below is structural on both surfaces — see commandLinesIn.

// The tokens a shell would execute. A LINE THAT STARTS WITH ONE OF THESE IS A COMMAND; a sentence does not
// start with `printf`, and the prose on both surfaces starts with words, backticks or bold markers.
var shellVerbs = []string{"printf ", "read ", "export ", "nth-agent ", "npx "}

// The environment variables whose value is a SECRET. The username and the build id are neither, and are set the
// ordinary way on both surfaces on purpose — a seal that dragged them in would be arguing about the wrong thing.
var secretVars = []string{"NTH_AGENT_BROWSERSTACK_KEY", "NTH_AGENT_TOKEN"}

// commandLinesIn extracts what a surface presents AS A COMMAND, and nothing it presents as prose.
//
// THE TWO SURFACES ARE DIFFERENT MEDIA AND THE RULE IS THE SAME SHAPE ON EACH:
//
//   - Markdown: the line must be inside a fenced block. That is Markdown's own way of saying "this is a command
//     and not a sentence", so the separation is the document's, not this test's guesswork.
//   - the usage text: there are no fences, so the line must simply BEGIN with a shell verb. Prose in that text
//     is sentences and wraps mid-sentence; it never opens a line with `printf`.
//
// THE CONTENT IS A PARAMETER, not a path this function goes and reads — the idiom this suite already relies on
// (see verbsIn in readme_test.go: "six slices in this register broke on a census whose input could not be
// substituted"). The guard below feeds it planted text whose right answer differs from the real one.
func commandLinesIn(content string, fenced bool) []string {
	var commands []string
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		if fenced && strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if fenced && !inFence {
			continue
		}
		trimmed := strings.TrimSpace(line)
		for _, verb := range shellVerbs {
			if strings.HasPrefix(trimmed, verb) {
				commands = append(commands, strings.Join(strings.Fields(trimmed), " "))
				break
			}
		}
	}
	return commands
}

// credentialCommandsIn narrows those to the ones that set a secret — the population the two surfaces must agree
// about, and the only one this seal has an opinion on.
func credentialCommandsIn(content string, fenced bool) []string {
	var out []string
	for _, command := range commandLinesIn(content, fenced) {
		for _, variable := range secretVars {
			if strings.Contains(command, variable) {
				out = append(out, command)
				break
			}
		}
	}
	return out
}

// surface is one mouth of this product, named so a failure can say WHICH one disagrees.
type surface struct {
	name    string
	content string
	fenced  bool
}

func surfaces(t *testing.T) []surface {
	t.Helper()
	root := repoRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	usageSource, err := os.ReadFile(filepath.Join(root, "cmd", "nth-agent", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	return []surface{
		{name: "README.md", content: string(readme), fenced: true},
		{name: "the --help usage text (cmd/nth-agent/main.go)", content: usageBlockOf(string(usageSource)), fenced: false},
	}
}

// usageBlockOf is the printed usage text and only that — the raw string literal a customer sees, not the Go file
// around it. Without this, a comment in main.go explaining the form would be read as if it were the form.
func usageBlockOf(source string) string {
	const opener = "const usageTemplate = `"
	start := strings.Index(source, opener)
	if start < 0 {
		return ""
	}
	rest := source[start+len(opener):]
	if end := strings.Index(rest, "`"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// ── INJECTIONS 1 AND 2: the two mouths must say the same thing, and the failure must name which one ──────────

func TestBothSurfacesTellOneStoryAboutSettingACredential(t *testing.T) {
	found := map[string][]string{}
	for _, s := range surfaces(t) {
		commands := credentialCommandsIn(s.content, s.fenced)
		// VACUITY FIRST: a surface that stopped showing how to set a credential at all would satisfy "they
		// agree" perfectly, and is exactly how this seal would rot.
		if len(commands) == 0 {
			t.Fatalf("%s shows no command that sets a credential at all — a surface that says nothing agrees "+
				"with everything, and this seal would then be guarding nothing", s.name)
		}
		found[s.name] = commands
	}

	var names []string
	for name := range found {
		names = append(names, name)
	}
	first := found[names[0]]
	for _, name := range names[1:] {
		other := found[name]
		if !sameSet(first, other) {
			t.Fatalf("THE TWO SURFACES DISAGREE ABOUT HOW TO SET A CREDENTIAL, and a customer meets the README "+
				"first.\n  %s shows:\n    %s\n  %s shows:\n    %s\nOne product, one story: whichever is right, "+
				"both must say it.", names[0], strings.Join(first, "\n    "), name, strings.Join(other, "\n    "))
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// ── INJECTION 4: a form that asks for a secret without naming it is what put a machine password on screen ────

func TestEveryCommandThatAsksForASecretNamesWhatItIsAskingFor(t *testing.T) {
	for _, s := range surfaces(t) {
		for _, command := range credentialCommandsIn(s.content, s.fenced) {
			if !strings.Contains(command, "read -rs") {
				continue // not a prompted read; the inline seal below is what covers those
			}
			if !strings.HasPrefix(command, "printf '") || !strings.Contains(command, ": ' &&") {
				t.Fatalf("%s asks for a secret without naming it on screen:\n    %s\nA bare read -rs prints "+
					"NOTHING — the cursor sits on an empty line, and on 2026-09-03 a person read that as his "+
					"machine's password prompt and typed his login password into it. The prompt must come from "+
					"printf (no single read serves both bash and zsh) and must say which secret it wants.",
					s.name, command)
			}
		}
	}
}

// ── INJECTION 1's other half: neither surface may teach the form the other one argues against ────────────────

func TestNoSurfaceShowsASecretBeingSetInline(t *testing.T) {
	for _, s := range surfaces(t) {
		for _, command := range credentialCommandsIn(s.content, s.fenced) {
			for _, variable := range secretVars {
				if strings.Contains(command, variable+"=") {
					t.Fatalf("%s shows a secret being set inline:\n    %s\nThat writes the value verbatim into "+
						"the shell's HISTORY FILE, which outlives the process, the terminal and the reboot — "+
						"the longer-lived of the two exposures, and the one this product's own usage text "+
						"already argues against. One product cannot have two mouths on this.", s.name, command)
				}
			}
		}
	}
}

// ── THE WARNING THE FORM CANNOT DO WITHOUT, because the form CANNOT be made paste-safe ───────────────────────
//
// MEASURED, 2026-09-03: pasted as part of a block, `read` consumes the NEXT LINE of the paste as its input — the
// key became the literal text of the following `export …` line and BrowserStack refused it. Making the whole
// form ONE line fixes that case; it does not fix pasting the line together with whatever a person pastes after
// it, and nothing in a shell can. So the warning is required, and Item 1 requires it ON the line.

func TestThePasteWarningRidesOnTheCommandItselfAndNotAParagraphNearby(t *testing.T) {
	for _, s := range surfaces(t) {
		for _, command := range credentialCommandsIn(s.content, s.fenced) {
			if !strings.Contains(command, "read -rs") {
				continue
			}
			if !strings.Contains(command, "# paste this line ALONE") {
				t.Fatalf("%s shows a read form with no paste warning ON IT:\n    %s\nA warning in a paragraph "+
					"above is not on the line a person copies, and this form cannot be made paste-safe: read "+
					"takes whatever follows it as the value.", s.name, command)
			}
			// ⚠️ THE PROPERTY IS THAT THE EXPORT IS ON THE SAME LINE AS THE READ, and it is asserted directly.
			// An earlier version counted `&&` instead, and injection 4 — which removes the PROMPT, dropping one
			// `&&` with it — printed "splits the form across statements", a cause that had not happened. A
			// failure message is a claim, and a proxy for a property is not that property.
			if !strings.Contains(command, "export ") {
				t.Fatalf("%s splits the form across statements:\n    %s\nThe export must be on the SAME line as "+
					"the read — the measured failure was a two-line block whose read swallowed the second line "+
					"as the key.", s.name, command)
			}
		}
	}
}

// ── INJECTION 5: the reason must survive a later simplification ──────────────────────────────────────────────

func TestTheSecurityArgumentSurvivesOnBothSurfaces(t *testing.T) {
	// BOTH EXPOSURES, BY THEIR EFFECTS rather than by a phrase, so a rewording cannot quietly drop one. This
	// reads prose ON PURPOSE and it is the one place that is right: the claim here is that an ARGUMENT is
	// PRESENT, not that a command matches it.
	required := []struct{ what, needle string }{
		{"the argv exposure", "ps"},
		{"the history-file exposure", "HISTORY FILE"},
		{"the refusal of 'prefer the variable' as a complete answer", "on its own would be poor advice"},
	}
	for _, s := range surfaces(t) {
		for _, r := range required {
			if !strings.Contains(s.content, r.needle) {
				t.Fatalf("%s no longer states %s. The form is only as good as the reason for it, and a later "+
					"simplification must not be able to delete the reason and leave the ritual.", s.name, r.what)
			}
		}
	}
}

// ── THE THIRD COPY, which is not a customer surface but drifts the same way ──────────────────────────────────

func TestTheCopyOfTheFormInTheLiveTestDoesNotDrift(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "internal", "browserstack", "live_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Derived from the README rather than typed here — a third copy sealed against a fourth copy would be the
	// same defect with more steps.
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var form string
	for _, command := range credentialCommandsIn(string(readme), true) {
		if strings.Contains(command, "NTH_AGENT_BROWSERSTACK_KEY") && strings.Contains(command, "read -rs") {
			form = command
		}
	}
	if form == "" {
		t.Fatal("the README no longer shows a prompted form for the BrowserStack key")
	}
	if !strings.Contains(strings.Join(strings.Fields(string(body)), " "), strings.TrimPrefix(form, "printf ")) {
		t.Fatalf("internal/browserstack/live_test.go tells a maintainer to run a form the surfaces no longer "+
			"show. It is not a customer surface, which is why it is a separate seal — but a stale copy of a "+
			"SECURITY instruction is the same defect wearing different clothes. The surfaces say:\n    %s", form)
	}
}

// ── THE GUARD: the extractor must be able to get a DIFFERENT answer, or it is not measuring anything ─────────

func TestTheCommandWalkReadsCommandsAndNotTheProseAroundThem(t *testing.T) {
	planted := "" +
		"Some prose that mentions export NTH_AGENT_TOKEN=secret in the middle of a sentence.\n" +
		"export NTH_AGENT_TOKEN=this-one-is-a-command\n" +
		"```\n" +
		"export NTH_AGENT_BROWSERSTACK_KEY=inside-a-fence\n" +
		"```\n" +
		"printf 'x' && read -rs NTH_AGENT_TOKEN\n"

	// Fenced (Markdown): ONLY the line inside the fence counts.
	fenced := credentialCommandsIn(planted, true)
	if len(fenced) != 1 || !strings.Contains(fenced[0], "inside-a-fence") {
		t.Fatalf("on a Markdown surface only fenced lines are commands, got %v", fenced)
	}
	// Unfenced (the usage text): every line that OPENS with a shell verb counts — three here, because without
	// fences the ``` markers are themselves just prose that opens with no verb — and the SENTENCE does not,
	// even though it contains the discouraged form verbatim. THAT IS THE WHOLE POINT: a paragraph explaining
	// why a form is discouraged contains the form, and this walk must be neither satisfied nor reddened by it.
	//
	// ⚠️ THE FIRST VERSION OF THIS GUARD EXPECTED 2 AND THE WALK WAS RIGHT. Kept as three because the count is
	// the cheap half; the assertion below is the one that matters.
	plain := credentialCommandsIn(planted, false)
	if len(plain) != 3 {
		t.Fatalf("a sentence CONTAINING a command is prose and must not be counted; a line STARTING with one "+
			"is a command and must be. got %v", plain)
	}
	for _, command := range plain {
		if strings.HasPrefix(command, "Some prose") {
			t.Fatalf("the walk read a sentence as a command: %q", command)
		}
	}
}
