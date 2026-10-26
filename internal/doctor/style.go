package doctor

import (
	"io"
	"os"
	"strings"
)

// HOW THIS OUTPUT IS DRESSED — and the one rule the dressing obeys: COLOUR IS A SECOND CHANNEL, NEVER THE ONLY
// ONE. Every distinction this renderer draws is drawn in TEXT first (the gutter marker and the headline word);
// colour repeats it for a reader who has one. Pipe this to a file, run it under NO_COLOR, read it in a CI log,
// and not one distinction is lost — which is why the seals assert the same facts with colour off.

// Options is what the CALLER decided about this rendering. A struct rather than positional bools so a new
// decision cannot be silently defaulted at a call site that never heard of it — Go has no default arguments, so
// every caller states both answers.
type Options struct {
	// Color turns the ANSI channel on. Decide it with ColorFor: a terminal, and no NO_COLOR, and no --no-color.
	Color bool
	// Why prints EVERY caveat, including the ones a green check states. Default off — see the count line in
	// Human, which is what keeps the withheld paragraphs from being invisible.
	Why bool
}

// ColorFor answers whether this writer may be coloured, and it says no unless it is sure.
//
//   - NO_COLOR set AT ALL — presence, not value — is no. That is the convention (no-color.org), and honouring
//     the VALUE would mean NO_COLOR=0 turns colour on, which is exactly the surprise the convention exists to
//     prevent.
//   - --no-color (the caller's own flag) wins over everything, because a person typing it is answering this
//     question directly.
//   - Anything that is not a character device is no: a pipe, a file, a CI log. Escape sequences in a file are
//     noise a person later greps around.
//
// NO DEPENDENCY AND NO SYSCALL OF OUR OWN: os.File.Stat and a mode check, which is what the standard library
// already gives us.
func ColorFor(w io.Writer, noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false // not a file at all (a buffer, a pipe wrapper) — no terminal to colour
	}
	info, err := f.Stat()
	if err != nil {
		return false // could not establish ⇒ do not colour, the same discipline the checks keep
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// The ANSI codes used, and nothing else. Deliberately the 8 basic colours rather than 256-colour or truecolor:
// these render on every terminal a developer has, including a bare tty and a CI viewer, and the palette is not
// where this design is doing its work.
const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiGreen  = "\033[32m"
	ansiBlue   = "\033[34m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
)

// paint wraps text in a code, or returns it untouched when colour is off. ONE function, so "colour is off" is one
// branch rather than a rule every call site has to remember.
func paint(on bool, code, text string) string {
	if !on || text == "" {
		return text
	}
	return code + text + ansiReset
}

// wrapWidth is the total line width this renderer wraps to, INCLUDING the indent.
//
// FIXED, NOT DETECTED, and the reason is the defect being fixed. A width read from the terminal cannot be
// reproduced in a test without a pty, cannot be reproduced in a bug report at all, and differs between the
// operator's terminal and CI — so the output a person pastes is not the output anyone else renders.
//
// EIGHTY, because a fixed width that is WIDER than the reader's terminal re-creates exactly the defect this item
// exists to fix: the terminal soft-wraps the overflow at column 0, with no indent, and the caveat runs into the
// next check's name. 80 is the width terminals essentially do not go below. On a wider terminal the paragraphs
// are merely narrower than they could be, which costs a reader nothing.
const wrapWidth = 80

// wrap breaks text into lines of at most width runes, each prefixed by indent, WITHOUT EVER SPLITTING A WORD.
//
// A path, a URL or a bundle id is a single token a reader copies; breaking one to satisfy a margin would make the
// output prettier and the thing it prints unusable. An over-long token therefore overflows its line, alone, and
// the line after it starts at the indent again.
func wrap(text, indent string, width int) []string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	budget := width - len([]rune(indent))
	if budget < 20 {
		budget = 20 // a pathological indent must not turn into one word per line
	}

	var lines []string
	line := fields[0]
	for _, word := range fields[1:] {
		if len([]rune(line))+1+len([]rune(word)) > budget {
			lines = append(lines, indent+line)
			line = word
			continue
		}
		line += " " + word
	}
	return append(lines, indent+line)
}
