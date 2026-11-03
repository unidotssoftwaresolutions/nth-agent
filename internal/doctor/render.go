package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// Options is what the CALLER decided about this rendering. A struct rather than positional bools so a new
// decision cannot be silently defaulted at a call site that never heard of it — Go has no default arguments, so
// every caller states both answers.
type Options struct {
	// Mode is how the output is dressed — colour, marks and width — decided by ui.For at the call site, the only place
	// that knows both the real stdout and what the person typed. The zero value is plain, folded at ui.ReportWidth.
	Mode ui.Mode
	// Why prints EVERY caveat, including the ones a green check states. Default off — see the count line in
	// Human, which is what keeps the withheld paragraphs from being invisible.
	Why bool
}

// wrapWidth is the width a report folds to off a terminal (ui.ReportWidth): a fixed width a person can paste and anyone
// else render the same. On a terminal, ui.Mode.Report folds to the terminal, up to ui.Measure.
const wrapWidth = ui.ReportWidth

// Human prints what this machine needs, what it has, and what to do about each gap.
//
// THE MOST IMPORTANT THING FIRST (AN_AGENT_THAT_SPEAKS_PLAINLY). The question a reader opens this to answer is "is
// anything wrong, and what", so the answer is the first line: in red, how many things stop runs here, and each of them in
// the three parts every problem the agent says is said in — what is wrong, why it matters, what to do. Or, in green, that
// nothing here stops a run. The other checks follow, a line each, by urgency: what could not be established and what
// needs a look (yellow, each with what to do), what is ready (green), what this machine does not need (dim).
//
// NOTHING IS SILENTLY WITHHELD: a green check's caveat and every check's reason move behind --why, and the count line
// names them (writeWithheldCount).
func Human(w io.Writer, r Report, opts Options) error {
	m, width := opts.Mode, opts.Mode.Report()
	var b strings.Builder
	say := func(lines ...string) {
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
	}
	pad := strings.Repeat(" ", m.GutterWidth())

	blocking := r.Blocking()
	unknown := 0
	for _, c := range r.Checks {
		if c.State == Unknown {
			unknown++
		}
	}
	// SINGULAR AND PLURAL, WRITTEN OUT: "1 thing stops", "2 things stop" — never "thing(s)".
	switch {
	case len(blocking) == 1:
		say(m.Headline(ui.LevelStop, "1 thing stops runs on this machine"))
	case len(blocking) > 1:
		say(m.Headline(ui.LevelStop, fmt.Sprintf("%d things stop runs on this machine", len(blocking))))
	case unknown > 0:
		// NOT "everything is fine" — some checks are unknown, and saying otherwise would be the confident lie this whole
		// package exists to avoid. So not green either.
		checks := "1 check"
		if unknown > 1 {
			checks = fmt.Sprintf("%d checks", unknown)
		}
		say(m.Headline(ui.LevelLook, "Nothing here stops a run — "+checks+" could not be established"))
		say(ui.Fold(`Anything marked "could not establish" below is unknown, not clear.`, "", "", width)...)
	default:
		say(m.Headline(ui.LevelGood, "Ready: nothing here stops a run"))
	}
	for _, c := range blocking {
		say(m.Problem(ui.Problem{Level: ui.LevelStop, What: checkLine(c), Why: WhatItStops(c), Do: c.Remedy}, width, false)...)
		writeReference(say, m, pad, c, opts, width)
	}

	rest := inUrgencyOrder(r.Checks)[len(blocking):]
	if len(rest) > 0 {
		say("")
		if len(blocking) > 0 {
			say(m.Dim("The other checks"))
		}
	}
	for _, c := range rest {
		switch urgencyGroup(c) {
		case 1, 2:
			say(m.Problem(ui.Problem{Level: ui.LevelLook, What: checkLine(c), Why: WhatItStops(c), Do: c.Remedy}, width, false)...)
		case 4:
			say(m.Marked(ui.MarkNote, checkLine(c), m.Dim, width)...)
			if c.Remedy != "" {
				say(m.DoLine(pad, c.Remedy, width)...)
			}
		default:
			say(m.Marked(ui.MarkOK, checkLine(c), nil, width)...)
			if c.Remedy != "" {
				say(m.DoLine(pad, c.Remedy, width)...)
			}
		}
		writeReference(say, m, pad, c, opts, width)
	}

	writeCapture(say, r.Capture, width)
	writeWithheldCount(say, r, opts, width)
	if len(blocking) > 0 {
		say("")
		say(m.Dim("We do not install anything on your machine. This command reports; you decide."))
	}
	return write(w, b.String())
}

// checkLine is a check's one line: its name, the word for its state, and what was observed.
func checkLine(c Check) string { return c.Name + ": " + headline(c) + detailSuffix(c) }

// writeReference is what a check did not establish, and why it is a requirement at all — dim, under the check.
//
// WHAT THIS DID NOT ESTABLISH. On a FAILING check this is diagnosis and prints by default. On any other it is reference —
// and it moves behind --why, with the count line, because these paragraphs are the opposite of noise: three of the six
// named limits have since turned out to be the actual cause. NOTHING IS DELETED and nothing is ever silently withheld.
//
// WHY THIS IS A REQUIREMENT AT ALL is REFERENCE on every check, failing ones included: the remedy is the action and the
// caveat is the diagnosis, so this prints only behind the flag. Last on purpose.
func writeReference(say func(...string), m ui.Mode, pad string, c Check, opts Options, width int) {
	if c.Limits != "" && (opts.Why || c.Blocks()) {
		say(m.Indented(pad, "not established: "+c.Limits, m.Dim, width)...)
	}
	if c.Need != "" && opts.Why {
		say(m.Indented(pad, "why required: "+c.Need, m.Dim, width)...)
	}
}

// writeCapture states network capture on this machine after the checks, each line folded at the report's width and kept
// at its own indent.
func writeCapture(say func(...string), status *capture.Status, width int) {
	if status == nil || len(status.Lines) == 0 {
		return
	}
	say("")
	for _, line := range status.Lines {
		for _, piece := range strings.Split(line, "\n") {
			text := strings.TrimLeft(piece, " ")
			indent := strings.Repeat(" ", len(piece)-len(text))
			say(ui.Fold(text, indent, indent, width)...)
		}
	}
}

// WhyFlag is the flag that prints every caveat — named here so the count line, the usage text and the seals all
// spell it the same way.
//
// WHY "--why": at the moment a reader distrusts this output they are asking "why did it say ready?", and that is
// the question these paragraphs answer — each one states what a green answer did NOT establish. It is also the
// shortest thing to type at exactly that moment. The count line always spells it out, so the name never has to
// be guessed at.
const WhyFlag = "--why"

// writeWithheldCount is the promise that nothing is silently gone: whenever anything was withheld, the report
// says WHAT KINDS and WHICH FLAG reveals them. A reader who never types the flag still knows the paragraphs
// exist. TWO kinds are withheld by default — a non-blocking check's caveat, and every check's "why required"
// sentence — and the line accounts for both, because a second kind of silently-absent thing would break this
// function's own promise.
func writeWithheldCount(say func(...string), r Report, opts Options, width int) {
	if opts.Why {
		return
	}
	withheld, needs := 0, 0
	for _, c := range r.Checks {
		if c.Limits != "" && !c.Blocks() {
			withheld++
		}
		if c.Need != "" {
			needs++
		}
	}
	if withheld == 0 && needs == 0 {
		return
	}
	say("")
	if withheld == 0 {
		// Only the reasons are withheld (every real check carries one).
		say(ui.Fold(fmt.Sprintf("Every check knows why it is required — run `%s` to read the reasons.", command.Line("doctor "+WhyFlag)), "", "", width)...)
		return
	}
	// "CHECKS THAT DO NOT STOP A RUN", not "passing checks": an ADVISORY check is `missing` and withholds its
	// caveat too, so "passing" would be false about exactly the check the previous slice existed to distinguish.
	noun, verb := "checks", "state"
	if withheld == 1 {
		noun, verb = "check", "states"
	}
	sentence := fmt.Sprintf("%d %s that do not stop a run %s what they did NOT establish", withheld, noun, verb)
	if needs > 0 {
		sentence += ", and every check knows why it is required"
	}
	say(ui.Fold(sentence+fmt.Sprintf(" — run `%s` to read them.", command.Line("doctor "+WhyFlag)), "", "", width)...)
}

// inUrgencyOrder groups the checks by what a reader needs first, PRESERVING the declared order inside each group
// — the checks are emitted in dependency order (adb before adb server before Appium), and losing that inside a
// group would turn a sequence into a list.
//
// The groups, and why in this order: what STOPS a run; what could not be ESTABLISHED (not a failure, but the one
// thing a reader cannot act on without knowing); what is true and does NOT stop a run; what is fine; what this
// machine does not need.
func inUrgencyOrder(checks []Check) []Check {
	groups := make([][]Check, 5)
	for _, c := range checks {
		groups[urgencyGroup(c)] = append(groups[urgencyGroup(c)], c)
	}
	out := make([]Check, 0, len(checks))
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func urgencyGroup(c Check) int {
	switch {
	case c.Blocks():
		return 0
	case c.State == Unknown:
		return 1
	case c.Advisory:
		return 2
	case c.State == NotRequired:
		return 4
	default:
		return 3
	}
}

func headline(c Check) string {
	switch c.State {
	case Satisfied:
		return "ready"
	case Missing:
		// ADVISORY IS SAID IN WORDS, NOT ONLY IN COLOUR AND NOT ONLY IN THE GUTTER (seal 5). Lower case on
		// purpose: capitals are how this output shouts, and a check that stops nothing has nothing to shout
		// about. The previous slice made the STATE say what is true and Advisory say what it costs; a renderer
		// that printed both as "MISSING" would undo that decision at the presentation layer.
		if c.Advisory {
			return "missing (does not stop a run)"
		}
		return "MISSING"
	case NotRunning:
		return "INSTALLED, NOT RUNNING"
	case Unreachable:
		return "NOT REACHABLE FROM HERE"
	case NotRequired:
		return "not needed on this machine"
	default:
		return "could not establish"
	}
}

func detailSuffix(c Check) string {
	if c.Detail == "" {
		return ""
	}
	return " — " + c.Detail
}

func write(w io.Writer, text string) error {
	_, err := io.WriteString(w, text)
	return err
}

// JSON is the same facts, machine-readable — the shape `devices --json` already established.
func JSON(w io.Writer, r Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

// ExitCode follows the README's existing contract, read onto this command's subject:
//
//	0 — this command established the truth about the machine and nothing is blocking a run.
//	1 — something IS blocking a run here. A person has to act; the remedies say what.
//
// AN UNKNOWN IS NOT A FAILURE. A check that could not establish its answer leaves the code at 0 with the
// uncertainty stated in the output, exactly as `devices` treats an answered-with-caveat: reporting "I could not
// tell" as a failure would train a reader to ignore the code.
func ExitCode(r Report) int {
	if len(r.Blocking()) > 0 {
		return 1
	}
	return 0
}
