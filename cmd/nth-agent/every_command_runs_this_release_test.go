package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ── EVERY COMMAND THE AGENT PRINTS RUNS THIS RELEASE OR A NEWER ONE (NTH_AGENT_0_4_3) ─────────────────────────────
//
// On 2026-10-24 the operator ran `npx nthcase-agent connect …` with no version while 0.4.2 was the latest release, and npx
// ran 0.4.0, a copy it already had. The agent's own lines taught that bare form: 76 of them at v0.4.2, seven of them telling
// a person to start connect again. Now one place spells a command (internal/command), from how this agent was started, and
// every printed line goes through it.
//
// THE CENSUS: no string in the program, outside internal/command, holds a command to type: no `npx <package>`, with or
// without a version, and no bare `nth-agent <verb>`. A line that names a running process rather than a command to type
// is on processLines, word for word, and stays as it is.

// processLines name a process that is running, not a command to type. Each text is a span of the line; the command in it
// is allowed only inside that span.
var processLines = []struct{ file, span string }{
	{"internal/connect/loop.go", "First stop every npx nthcase-agent connect running on this machine"},
	{"internal/state/connectlock.go", "another npx nthcase-agent connect is already running on this machine"},
	{"internal/capture/texts.go", "If npx nthcase-agent connect is running it tells your workspace"},
	{"internal/capture/texts.go", "while npx nthcase-agent connect runs it clears this"},
	{"internal/capture/texts.go", "While npx nthcase-agent connect "},
	{"internal/capture/texts.go", "if npx nthcase-agent connect is running"},
}

// theOnePlace is the package that spells every command.
const theOnePlace = "internal/command"

// commandsIn is where each command to type starts in text: `npx <name>` with any version, and the bare command.
func commandsIn(text, name string, bare *regexp.Regexp) []int {
	npx := regexp.MustCompile(`npx\s+` + regexp.QuoteMeta(name) + `([@\s]|$)`)
	var at []int
	for _, m := range npx.FindAllStringIndex(text, -1) {
		at = append(at, m[0])
	}
	for _, m := range bare.FindAllStringSubmatchIndex(text, -1) {
		at = append(at, m[3]) // the word itself, after the character before it
	}
	return at
}

// unspelledCommands is every command to type in a file's strings that is not inside a span processLines names for it.
func unspelledCommands(rel string, texts []string, name string, bare *regexp.Regexp) []string {
	var problems []string
	for _, text := range texts {
		for _, at := range commandsIn(text, name, bare) {
			allowed := false
			for _, p := range processLines {
				if p.file != rel {
					continue
				}
				for from := 0; ; {
					i := strings.Index(text[from:], p.span)
					if i < 0 {
						break
					}
					i += from
					allowed = allowed || (at >= i && at < i+len(p.span))
					from = i + 1
				}
			}
			if !allowed {
				line, _, _ := strings.Cut(text[at:], "\n")
				problems = append(problems, rel+": "+strings.ReplaceAll(line, "\x00", "…"))
			}
		}
	}
	return problems
}

func TestEveryCommandTheAgentPrintsIsSpelledInOnePlace(t *testing.T) {
	root := repoRoot(t)
	name := packageName(t)
	bare := bareCommand(t)

	// INSTRUMENT: a bare command, a pinned one and a bare verb are seen; a process line and a spelled one are not.
	planted, err := parser.ParseFile(token.NewFileSet(), "planted.go", `package p
const a = "run npx `+name+` doctor to see why"
const b = "restart with: npx `+name+`@0.4.0 connect --appium x"
const c = "(nth-agent " + "capture disable, then …)"
const d = "First stop every npx `+name+` connect running on this machine; then remove the file"
func e() string { return "run " + command.Line("doctor") + " now" }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	found := unspelledCommands("internal/connect/loop.go", stringTexts(planted), name, bare)
	if len(found) != 3 || !strings.Contains(found[0], "npx "+name+" doctor") || !strings.Contains(found[1], "@0.4.0 connect") ||
		!strings.Contains(found[2], "nth-agent capture") {
		t.Fatalf("instrument: the census over a planted source found %d: %q", len(found), found)
	}

	var problems []string
	spans := map[string]int{}
	files := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if base := d.Name(); path != root && (strings.HasPrefix(base, ".") || base == "testdata" || base == "publish") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(rel, theOnePlace+"/") {
			return nil
		}
		files++
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		texts := stringTexts(file)
		problems = append(problems, unspelledCommands(rel, texts, name, bare)...)
		for i, p := range processLines {
			for _, text := range texts {
				if p.file == rel && strings.Contains(text, p.span) {
					spans[p.file+"\x00"+p.span] = i + 1
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 40 {
		t.Fatalf("instrument guard: read %d files — the walk is not reading the program", files)
	}
	for _, p := range processLines {
		if spans[p.file+"\x00"+p.span] == 0 {
			problems = append(problems, p.file+": the process line "+p.span+" is listed and no longer there — take it off the list")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d command(s) the agent prints are spelled outside %s, so they can start an older copy than this one. "+
			"Spell each with command.Line (or command.OnThatMachine, for a sentence read away from this machine), or, if it "+
			"names a running process rather than a command to type, add it to processLines:\n  %s",
			len(problems), theOnePlace, strings.Join(problems, "\n  "))
	}
}
