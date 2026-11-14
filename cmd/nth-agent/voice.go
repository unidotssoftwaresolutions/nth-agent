package main

import (
	"fmt"
	"io"
	"slices"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// THE COMMAND LINE'S OWN PROBLEMS, IN THE AGENT'S ONE VOICE (AN_AGENT_THAT_SPEAKS_PLAINLY): a mistake on the command line,
// and a refusal before connect starts, are said like every other problem — what is wrong, why it matters, what to do — on
// the output they are written to, dressed for it (internal/ui). Every one goes through refuse: a census reads this
// package for any other way of writing a problem to stderr (TestEveryRefusalOnTheCommandLineSaysWhatToDo).

// nothingRan is why a command-line mistake matters: it is said before anything starts.
const nothingRan = "Nothing was run."

// refuse writes p to w in its three parts. noColor is --no-color when the person typed it; NO_COLOR, TERM=dumb and a
// pipe are read by ui.For as for every other output.
func refuse(w io.Writer, noColor bool, p ui.Problem) {
	if p.Level == ui.LevelGood {
		p.Level = ui.LevelStop
	}
	m := ui.For(w, ui.Asked{NoColor: noColor})
	for _, line := range m.Problem(p, m.Report(), false) {
		fmt.Fprintln(w, line)
	}
}

// asksNoColor is whether --no-color is among the arguments, read before they are parsed: a mistake in parsing them is
// still written the way the person asked.
func asksNoColor(args []string) bool { return slices.Contains(args, "--no-color") }

// sayTo writes one line after its mark to w — the console connect speaks into, or a plain output.
func sayTo(w io.Writer, mark ui.Mark, text string) {
	m := ui.Mode{}
	width := 0
	if console, ok := w.(*ui.Console); ok {
		m, width = console.Mode(), console.Width()
	}
	for _, line := range m.Marked(mark, text, nil, width) {
		fmt.Fprintln(w, line)
	}
}

// skipMistake is a --skip that names no platform this agent knows.
func skipMistake(err error) ui.Problem {
	return ui.Problem{What: err.Error(), Why: nothingRan, Do: "name a platform this agent asks about: --skip android, or --skip ios"}
}

// reportNotWritten is a report that could not be written to its output.
func reportNotWritten(err error) ui.Problem {
	return ui.Problem{What: "could not write the report: " + err.Error(), Why: "What this machine has was established and could not be shown.",
		Do: "check where the output goes (a full disk, a pipe closed early), then run the command again"}
}
