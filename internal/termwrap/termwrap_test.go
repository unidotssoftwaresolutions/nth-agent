package termwrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/termwrap"
)

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 4: connect's narration, wrapped where a person reads it ────────────────────
//
// Filmed on 2026-10-20: a long sentence from `connect` broke in the middle of a word, where the terminal ran out of
// columns. In a terminal each line is now folded at a space to the terminal's width; a command, an address or a path is
// never split. Anywhere else (a file, a pipe, a CI log) nothing is folded, so every line can still be found whole.

// narration is what connect really says, at its longest: each sentence with the commands, addresses and paths in it.
var narration = []struct {
	line  string
	whole []string
}{
	{"the farm refused this node's identity: this id is already registered at the farm, but not to the workspace of this agent's token. First stop every npx nthcase-agent connect running on this machine; then remove the node-id file in the agent's config folder to mint a fresh identity, or contact your operator.",
		[]string{"npx nthcase-agent connect"}},
	{"  what to do: run Appium on this machine and restart with its local address: npx nthcase-agent connect --appium http://127.0.0.1:4723",
		[]string{"npx nthcase-agent connect --appium http://127.0.0.1:4723"}},
	{"  to offer these too, restart with: npx nthcase-agent connect … --offer-uncabled-devices",
		[]string{"npx nthcase-agent connect … --offer-uncabled-devices"}},
	{"  what to do: run `npx nthcase-agent doctor` to see what this machine is missing, then restart with --appium URL",
		[]string{"`npx nthcase-agent doctor`", "--appium URL"}},
	{"Plug it back in, unlock it and allow USB debugging if it asks: while npx nthcase-agent connect runs it clears this within half a minute, and otherwise when it starts. Or clear it yourself now:  adb -s R5CT30ABCDE shell settings put global http_proxy :0",
		[]string{"npx nthcase-agent connect", "adb -s R5CT30ABCDE shell settings put global http_proxy :0"}},
	{"no capture: phone R5CT30ABCDE has not been set up for capture on the machine it is plugged into (on that machine: npx nthcase-agent capture setup R5CT30ABCDE)",
		[]string{"npx nthcase-agent capture setup R5CT30ABCDE)"}},
	{"  to update, stop it (Ctrl-C), run npm install -g nthcase-agent@latest, and start it the way you do now",
		[]string{"npm install -g nthcase-agent@latest,"}},
	{"  to update, stop it (Ctrl-C) and start it with the flags you use now: npx nthcase-agent@latest connect …",
		[]string{"npx nthcase-agent@latest connect …"}},
	{"  to update, stop it (Ctrl-C), run npm install nthcase-agent@latest in /Users/ada/work/a-project-with-a-long-name/mobile, and start it the way you do now",
		[]string{"npm install nthcase-agent@latest", "/Users/ada/work/a-project-with-a-long-name/mobile,"}},
	{"drive tunnel established — wss://farm.nthcase.com/v1/nodes/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/tunnel",
		[]string{"wss://farm.nthcase.com/v1/nodes/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/tunnel"}},
}

func wrapped(t *testing.T, width int, text string) string {
	t.Helper()
	var out strings.Builder
	w := &termwrap.Writer{Out: &out, Width: func() int { return width }}
	n, err := w.Write([]byte(text))
	if err != nil || n != len(text) {
		t.Fatalf("Write took %d of %d bytes: %v", n, len(text), err)
	}
	return out.String()
}

func indentOf(line string) string { return line[:len(line)-len(strings.TrimLeft(line, " "))] }

func TestALongLineIsFoldedAtASpaceToTheTerminalsWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80} {
		for _, said := range narration {
			got := wrapped(t, width, said.line+"\n")
			if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
				t.Fatalf("width %d: the line keeps its one line ending: %q", width, got)
			}
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(said.line), " ") {
				t.Fatalf("width %d: folding lost or split a word:\n%q\n%q", width, said.line, got)
			}
			for _, whole := range said.whole {
				found := false
				for _, line := range lines {
					found = found || strings.Contains(line, whole)
				}
				if !found {
					t.Errorf("width %d: %q is split across lines:\n%s", width, whole, got)
				}
			}
			for i, line := range lines {
				if i > 0 && indentOf(line) != indentOf(lines[0])+"  " {
					t.Errorf("width %d: a folded line is indented two deeper than its sentence: %q", width, line)
				}
				if n := len([]rune(line)); n > width && strings.Contains(strings.TrimSpace(line), " ") && !holdsOneWhole(line, said.whole) {
					t.Errorf("width %d: a line runs to %d columns and could have been folded: %q", width, n, line)
				}
			}
		}
	}
}

// holdsOneWhole is a line that is only one command, address or path, too long for the width on its own.
func holdsOneWhole(line string, whole []string) bool {
	for _, w := range whole {
		if strings.TrimSpace(line) == w {
			return true
		}
	}
	return false
}

func TestALineThatFitsIsLeftAsItIs(t *testing.T) {
	text := "registered as node aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\n  android: 1 usable, 0 attached but unusable\npartial"
	if got := wrapped(t, 80, text); got != text {
		t.Fatalf("lines within the width are not touched:\n%q\n%q", text, got)
	}
}

// NOT A TERMINAL, NOT FOLDED: a file, a pipe, the null device or a buffer gets the very writer it was.
func TestNothingButATerminalIsFolded(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "connect.log"))
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
	var buffer strings.Builder
	for name, w := range map[string]interface{ Write([]byte) (int, error) }{
		"a file": file, "a pipe": pipe, "the null device": null, "a buffer": &buffer,
	} {
		if got := termwrap.For(w); got != w {
			t.Errorf("%s is not a terminal and must be written as it is, got %T", name, got)
		}
	}
	if _, err := termwrap.For(file).Write([]byte(narration[0].line + "\n")); err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(file.Name())
	if string(logged) != narration[0].line+"\n" {
		t.Fatalf("a log holds every line whole:\n%q", logged)
	}
}
