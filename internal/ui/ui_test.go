package ui_test

import (
	"bytes"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// ── AN_AGENT_THAT_SPEAKS_PLAINLY: one palette, one set of marks, one set of width rules ─────────────────────────────

// environment is a fixed environment for ui.Asked.Env: the rules are sealed without touching the test process's own.
func environment(set map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := set[name]
		return value, ok
	}
}

// COLOUR IS OFF FOR ANYTHING THAT IS NOT A TERMINAL, whatever else is asked: a pipe, a file, the null device, a buffer.
func TestColourIsOffForAPipeAFileAndABuffer(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "doctor.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer pipe.Close()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	asked := ui.Asked{Env: environment(map[string]string{"TERM": "xterm-256color"})}
	for name, w := range map[string]interface{ Write([]byte) (int, error) }{
		"a file": file, "a pipe": pipe, "the null device": null, "a buffer": &bytes.Buffer{},
	} {
		if mode := ui.For(w, asked); mode.Color || mode.Terminal {
			t.Errorf("%s is no terminal: no colour and nothing kept updating, got %+v", name, mode)
		}
	}
}

// WITH COLOUR OFF NOT ONE ESCAPE CODE IS WRITTEN — every style hands its text back untouched.
func TestThePlainModeWritesNoEscapeCode(t *testing.T) {
	m := ui.Mode{}
	for _, styled := range []string{
		m.Good("works"), m.Look("look"), m.Stop("stops"), m.Dim("detail"), m.Bold("read me"),
		m.Headline(ui.LevelStop, "1 thing stops runs on this machine"), m.Mark(ui.MarkStop), m.Mark(ui.MarkDo),
		strings.Join(m.Problem(ui.Problem{Level: ui.LevelStop, What: "w", Why: "y", Do: "d", Raw: "r"}, 60, true), "\n"),
		strings.Join(m.Table("", []string{"NAME", "STATE"}, [][]ui.Cell{{{Text: "Pixel 8", Paint: m.Good}, {Text: "offered", Paint: m.Good}}}), "\n"),
	} {
		if strings.Contains(styled, "\033") {
			t.Fatalf("colour is off, and an escape code got through: %q", styled)
		}
	}
}

// THE PALETTE: green works, yellow needs a look, red stops a run, dim is detail, bold is the one thing to read.
func TestThePaletteIsTheFiveStylesAndNothingElse(t *testing.T) {
	m := ui.Mode{Color: true}
	for name, got := range map[string][2]string{
		"green":  {m.Good("x"), "\033[32mx\033[0m"},
		"yellow": {m.Look("x"), "\033[33mx\033[0m"},
		"red":    {m.Stop("x"), "\033[31mx\033[0m"},
		"dim":    {m.Dim("x"), "\033[2mx\033[0m"},
		"bold":   {m.Bold("x"), "\033[1mx\033[0m"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: got %q, want %q", name, got[0], got[1])
		}
	}
}

// THE MARKS: ✓ ! ✗ · →, and two plain letters each when the terminal cannot show them.
func TestTheMarksAndTheirPlainLetters(t *testing.T) {
	unicode, ascii := ui.Mode{}, ui.Mode{ASCII: true}
	for mark, want := range map[ui.Mark][2]string{
		ui.MarkOK: {"✓", "ok"}, ui.MarkLook: {"!", "!!"}, ui.MarkStop: {"✗", "xx"}, ui.MarkNote: {"·", "--"}, ui.MarkDo: {"→", "->"},
	} {
		if got := unicode.Symbol(mark); got != want[0] {
			t.Errorf("mark %d: got %q, want %q", mark, got, want[0])
		}
		if got := ascii.Symbol(mark); got != want[1] {
			t.Errorf("mark %d in ASCII: got %q, want %q", mark, got, want[1])
		}
	}
	for name, env := range map[string]map[string]string{
		"TERM=dumb":              {"TERM": "dumb"},
		"LANG=C":                 {"LANG": "C"},
		"LC_ALL=POSIX":           {"LC_ALL": "POSIX", "LANG": "en_US.UTF-8"},
		"LC_CTYPE=en_US.ISO8859": {"LC_CTYPE": "en_US.ISO8859-1"},
	} {
		if !ui.For(&bytes.Buffer{}, ui.Asked{Env: environment(env)}).ASCII {
			t.Errorf("%s: the marks are plain letters", name)
		}
	}
	for name, env := range map[string]map[string]string{
		"LANG=en_US.UTF-8": {"LANG": "en_US.UTF-8"},
		"LC_ALL=C.utf8":    {"LC_ALL": "C.utf8"},
		"no locale at all": {},
	} {
		if ui.For(&bytes.Buffer{}, ui.Asked{Env: environment(env)}).ASCII {
			t.Errorf("%s: the marks are Unicode", name)
		}
	}
}

// THE WIDTH RULES on a narrow terminal (60 columns) and a wide one (160): folded to the terminal, never past the measure,
// a report at 80 off a terminal and a stream not at all, and nothing a person copies split.
func TestTheWidthRulesOnANarrowAndAWideTerminal(t *testing.T) {
	narrow, wide, off := ui.Mode{Terminal: true, Columns: 60}, ui.Mode{Terminal: true, Columns: 160}, ui.Mode{}
	for name, got := range map[string][2]int{
		"narrow stream": {narrow.Stream(), 60}, "narrow report": {narrow.Report(), 60},
		"wide stream": {wide.Stream(), ui.Measure}, "wide report": {wide.Report(), ui.Measure},
		"off a terminal, a stream": {off.Stream(), 0}, "off a terminal, a report": {off.Report(), ui.ReportWidth},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: folded at %d, want %d", name, got[0], got[1])
		}
	}

	problem := ui.Problem{Level: ui.LevelStop,
		What: "this machine's Appium at http://127.0.0.1:4723 is not answering, so nothing attached here can be driven",
		Why:  "No phone on this machine is offered for a run until Appium answers.",
		Do:   "start Appium (appium), then restart: npx nthcase-agent@latest connect --appium http://127.0.0.1:4723"}
	for _, m := range []ui.Mode{narrow, wide} {
		width := m.Stream()
		lines := m.Problem(problem, width, false)
		for _, line := range lines {
			if n := ui.Columns(line); n > width && !strings.Contains(line, "npx nthcase-agent@latest connect --appium http://127.0.0.1:4723") {
				t.Errorf("%d columns: a %d-column line that could have been folded: %q", width, n, line)
			}
			if strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "  ") {
				t.Errorf("%d columns: a continuation must sit under the text, not under the mark: %q", width, line)
			}
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "npx nthcase-agent@latest connect --appium http://127.0.0.1:4723") {
			t.Errorf("%d columns: the command a person copies was split:\n%s", width, joined)
		}
	}
	// 160 columns, and a sentence of 130 still folds: the measure, not the terminal, is the limit.
	if lines := wide.Problem(problem, wide.Stream(), false); len(lines) < 4 {
		t.Errorf("a 160-column terminal still folds prose at %d columns:\n%s", ui.Measure, strings.Join(lines, "\n"))
	}
}

// EVERY PROBLEM IS SAID IN THREE PARTS, IN THIS ORDER — and the raw error only when asked for.
func TestAProblemIsWhatThenWhyThenWhatToDo(t *testing.T) {
	p := ui.Problem{Level: ui.LevelStop, What: "the farm refused this agent's token",
		Why: "Nothing on this machine is offered while the token is refused.",
		Do:  "put a new token in NTH_AGENT_TOKEN, then restart the agent", Raw: "401 Unauthorized"}
	lines := ui.Mode{}.Problem(p, 0, false)
	want := []string{
		"✗ the farm refused this agent's token",
		"  Nothing on this machine is offered while the token is refused.",
		"  → put a new token in NTH_AGENT_TOKEN, then restart the agent",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if verbose := strings.Join(ui.Mode{}.Problem(p, 0, true), "\n"); !strings.HasSuffix(verbose, "  as it came: 401 Unauthorized") {
		t.Fatalf("under --verbose the raw error comes last, dim:\n%s", verbose)
	}
	coloured := ui.Mode{Color: true}.Problem(p, 0, true)
	if !strings.HasPrefix(coloured[0], "\033[31m✗\033[0m \033[31m") || !strings.HasPrefix(coloured[1], "  \033[2m") || !strings.HasPrefix(coloured[3], "  \033[2m") {
		t.Fatalf("what is wrong in red, why and the raw error dim:\n%q", coloured)
	}
	if ui.Plain(strings.Join(coloured, "\n")) != strings.Join(ui.Mode{}.Problem(p, 0, true), "\n") {
		t.Fatal("colour may only repeat what the text says")
	}
}

// THE UPDATING LINE: on a terminal a progress line is rewritten in place and every other line goes above it; in a pipe it
// is a line of its own, and a terminal-only status is not printed at all.
func TestProgressCollapsesOnATerminalAndStaysOneLineEachInAPipe(t *testing.T) {
	var pipe bytes.Buffer
	piped := ui.NewConsole(&pipe, ui.Mode{})
	piped.Progress("connecting to https://farm.example")
	piped.Status("heartbeat answered at 12:00:10")
	piped.Say("registered as node aaaa")
	piped.Progress("cannot reach the farm — retrying with backoff")
	piped.Done()
	if want := "connecting to https://farm.example\nregistered as node aaaa\ncannot reach the farm — retrying with backoff\n"; pipe.String() != want {
		t.Fatalf("a pipe gets a line each and no status:\n%q\nwant\n%q", pipe.String(), want)
	}

	var screen bytes.Buffer
	terminal := ui.NewConsole(&screen, ui.Mode{Terminal: true, Columns: 80})
	terminal.Progress("connecting to https://farm.example")
	terminal.Status("heartbeat answered at 12:00:10")
	terminal.Say("registered as node aaaa")
	terminal.Done()
	shown := screen.String()
	if strings.Count(shown, "\n") != 1 {
		t.Fatalf("on a terminal only the said line ends a line; progress and status rewrite one line:\n%q", shown)
	}
	if !strings.Contains(shown, "\r\033[Kregistered as node aaaa\n\r\033[Kheartbeat answered at 12:00:10\r\033[K") {
		t.Fatalf("a said line clears the updating line, prints above it, the updating line is drawn again, and Done clears it:\n%q", shown)
	}
	// AN UPDATING LINE THAT DOES NOT FIT IS CUT, never folded: a folded one could not be rewritten in place.
	var narrow bytes.Buffer
	ui.NewConsole(&narrow, ui.Mode{Terminal: true, Columns: 20}).Progress(strings.Repeat("long ", 10))
	if got := strings.TrimPrefix(narrow.String(), "\r\033[K"); ui.Columns(got) > 19 || !strings.HasSuffix(got, "…") {
		t.Fatalf("cut to the terminal, with an ellipsis: %q", got)
	}
}

// A TABLE'S COLUMNS ARE COUNTED BEFORE IT IS COLOURED, so colour cannot move a column.
func TestATablesColumnsDoNotMoveWithColour(t *testing.T) {
	rows := func(m ui.Mode) [][]ui.Cell {
		return [][]ui.Cell{
			{{Text: "Pixel 8"}, {Text: "Android"}, {Text: "15"}, {Text: "offered", Paint: m.Good}},
			{{Text: "R5CT31ABCDE"}, {Text: "Android"}, {Text: "—"}, {Text: "left out: unauthorized", Paint: m.Stop}},
		}
	}
	header := []string{"NAME", "PLATFORM", "OS", "STATE"}
	plain := ui.Mode{}.Table("  ", header, rows(ui.Mode{}))
	coloured := ui.Mode{Color: true}
	if got := ui.Plain(strings.Join(coloured.Table("  ", header, rows(coloured)), "\n")); got != strings.Join(plain, "\n") {
		t.Fatalf("coloured and plain must line up the same:\n%s\n%s", got, strings.Join(plain, "\n"))
	}
	if plain[1] != "  Pixel 8      Android   15  offered" {
		t.Fatalf("columns two spaces apart: %q", plain[1])
	}
}

// NO OTHER PACKAGE WRITES AN ESCAPE CODE: every string or rune literal in this program whose VALUE holds the escape
// character is in this package. Read over every non-test Go file of the module with Go's own scanner, so a file added
// tomorrow is read too, and a pattern that only names the character to strip it (farm's terminal filter, a raw-string
// regular expression) is not mistaken for writing one.
func TestNoOtherPackageWritesAnEscapeCode(t *testing.T) {
	root := filepath.Join("..", "..")
	read, inUI := 0, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		read++
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ours := filepath.Base(filepath.Dir(path)) == "ui" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "internal"
		files := token.NewFileSet()
		var s scanner.Scanner
		s.Init(files.AddFile(path, -1, len(text)), text, nil, 0)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok != token.STRING && tok != token.CHAR {
				continue
			}
			value := lit
			if tok == token.STRING {
				if unquoted, err := strconv.Unquote(lit); err == nil {
					value = unquoted
				}
			} else if r, _, _, err := strconv.UnquoteChar(strings.Trim(lit, "'"), '\''); err == nil {
				value = string(r)
			}
			if !strings.ContainsRune(value, 0x1b) {
				continue
			}
			if ours {
				inUI++
				continue
			}
			t.Errorf("%s: a literal holding the escape character — only internal/ui may write one", files.Position(pos))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if read < 100 || inUI == 0 {
		t.Fatalf("read %d Go files and found %d escape codes in internal/ui — the walk is not reading what it seals", read, inUI)
	}
}
