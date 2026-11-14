// Package ui is the agent's terminal voice (AN_AGENT_THAT_SPEAKS_PLAINLY): one palette, one set of marks, one set of width
// rules and one way to say a problem, used by everything the agent prints for a person — connect's narration, doctor,
// devices, the start banner, and the command line's own mistakes.
//
// NO OTHER PACKAGE WRITES AN ESCAPE CODE. A screen asks this package for "the green of what works" or "the mark of what
// stops a run", and this package decides whether that is an escape sequence, a symbol, or two plain letters. A seal holds
// it (TestNoOtherPackageWritesAnEscapeCode).
//
// COLOUR IS A SECOND CHANNEL, NEVER THE ONLY ONE. Every distinction is drawn in text first — the mark, the headline word —
// and colour repeats it for a reader who has colour. Strip the colour from any screen and it must read the same.
package ui

import (
	"io"
	"os"
	"strings"
)

// Mode is how one output is dressed. The zero value is the plain dressing: no colour, the Unicode marks, not a terminal, so
// nothing is folded. A buffer in a test, a pipe and a file all get it.
type Mode struct {
	// Color turns the escape codes on. Decided by For: a terminal, and nothing that turns colour off.
	Color bool
	// ASCII swaps the marks for two plain letters each (ok, !!, xx), for a terminal that cannot show ✓ ! ✗ · →.
	ASCII bool
	// Terminal: the output is a terminal, so a screen folds its lines to Columns and may keep an updating line.
	Terminal bool
	// Columns is the terminal's width when the output was opened. 0 off a terminal.
	Columns int
}

// Asked is what the person asked for on the command line, and where the environment is read from.
type Asked struct {
	// NoColor is --no-color: a person answering the question directly, so it wins over everything.
	NoColor bool
	// JSON is --json: the machine's answer, which takes no dressing at all.
	JSON bool
	// Env reads one environment variable and whether it is set at all. nil = os.LookupEnv. A seam, so the rules below are
	// sealed without changing the test process's own environment.
	Env func(name string) (string, bool)
}

// For decides how w is dressed. Colour is ON ONLY ON A TERMINAL, and off for each of these:
//
//   - NO_COLOR set to anything — presence, not value (no-color.org): honouring the value would make NO_COLOR=0 turn colour
//     on, the surprise the convention exists to prevent;
//   - --no-color;
//   - TERM=dumb, a terminal that says it reads no escape codes;
//   - a pipe or a file — anything that does not answer the terminal-size question;
//   - --json.
//
// The marks are Unicode unless the terminal is dumb or the locale names a character set that is not UTF-8 (see asciiFor).
func For(w io.Writer, asked Asked) Mode {
	env := asked.Env
	if env == nil {
		env = processEnv
	}
	m := Mode{ASCII: asciiFor(env)}
	if f, ok := w.(*os.File); ok {
		if columns, isTerminal := TerminalWidth(f); isTerminal {
			m.Terminal, m.Columns = true, columns
			m.Color = colorAllowed(asked, env) && enableEscapes(f)
		}
	}
	return m
}

// processEnv reads, from this process's environment, each variable the rules above read — BY NAME, so the README's
// census of the variables this agent reads finds every one of them here.
func processEnv(name string) (string, bool) {
	switch name {
	case "NO_COLOR":
		return os.LookupEnv("NO_COLOR")
	case "TERM":
		return os.LookupEnv("TERM")
	case "LC_ALL":
		return os.LookupEnv("LC_ALL")
	case "LC_CTYPE":
		return os.LookupEnv("LC_CTYPE")
	case "LANG":
		return os.LookupEnv("LANG")
	}
	return "", false
}

// colorAllowed is every rule that turns colour off, except "not a terminal", which For checks itself.
func colorAllowed(asked Asked, env func(string) (string, bool)) bool {
	if asked.NoColor || asked.JSON {
		return false
	}
	if _, set := env("NO_COLOR"); set {
		return false
	}
	term, _ := env("TERM")
	return term != "dumb"
}

// asciiFor answers whether the marks must be plain letters: on a dumb terminal, or when the locale (the first of LC_ALL,
// LC_CTYPE and LANG that is set, as the C library reads them) names a character set other than UTF-8. With no locale set
// at all the marks stay Unicode: every sentence this agent prints already carries an em dash and curly quotes, so a
// terminal that could not show ✓ could not show those either.
func asciiFor(env func(string) (string, bool)) bool {
	if term, _ := env("TERM"); term == "dumb" {
		return true
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value, _ := env(name); value != "" {
			lower := strings.ToLower(value)
			return !strings.Contains(lower, "utf-8") && !strings.Contains(lower, "utf8")
		}
	}
	return false
}
