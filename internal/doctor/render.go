package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// Human prints what this machine needs, what it has, and what to do about each gap.
//
// WHAT IT STILL SHARES WITH `devices`, AND WHAT IT NO LONGER DOES — the docblock this replaces claimed "the
// shape follows `devices` … a reader who has seen one output can read the other", and after this slice half of
// that is true, so it says which half rather than going on asserting the whole.
//
// SHARED, and a reader who has seen one still reads the other: one line per subject, the subject's name first,
// an indented "what to do:" under anything that wants a person, and the same refusal to call an unknown a
// failure.
//
// THIS COMMAND'S OWN, and deliberately NOT carried to the other two: a state marker in the left gutter, urgency
// order, wrapped paragraphs, colour, and the caveat flag. `devices` and the connect loop have neither six states
// nor caveats — there is nothing there for a gutter to mark or a flag to withhold — and the connect loop STREAMS,
// so a trailing count of what it withheld is a sentence it can never reach. A doctor is read ONCE, whole, at the
// moment something is wrong; those two are a listing and a running commentary. (What could still move is the
// wrapping and the colour; that is a slice of its own and is recorded rather than half-done.)
//
// THE QUESTION A READER OPENS THIS TO ANSWER is "is anything wrong, and what". Eleven checks printed at one
// weight cost eleven lines of prose to answer it; the answer is now the FIRST LINE, because the checks are
// grouped by urgency and a blocker sorts to the top.
func Human(w io.Writer, r Report, opts Options) error {
	var b strings.Builder
	for _, c := range inUrgencyOrder(r.Checks) {
		writeCheckLine(&b, c, opts)
		if c.Remedy != "" {
			writeParagraph(&b, "what to do: "+c.Remedy)
		}
		// WHAT THIS DID NOT ESTABLISH. On a FAILING check this is diagnosis and prints by default. On a green one
		// it is reference — and it moves behind --why, with the count line below, because these paragraphs are
		// the opposite of noise: three of the six named limits have since turned out to be the actual cause (the
		// device-coverage check, the developer-trust inference, the advisory decision). NOTHING IS DELETED and
		// nothing is ever silently withheld.
		if c.Limits != "" && (opts.Why || c.Blocks()) {
			writeParagraph(&b, "not established: "+c.Limits)
		}
		// WHY THIS IS A REQUIREMENT AT ALL — the other half of the question --why exists for. Every check has
		// carried this in `Need` since the checks were written, and no renderer ever showed it; --json always
		// has. It is REFERENCE on every check, failing ones included: the remedy is the action and the caveat is
		// the diagnosis, so this prints only behind the flag — a sentence added to every check unconditionally is
		// exactly the one-visual-weight regression the nine-greens seals exist to stop. Last on purpose: what is
		// wrong, what to do, what was not established, and only then why we ever asked.
		if c.Need != "" && opts.Why {
			writeParagraph(&b, "why required: "+c.Need)
		}
	}

	writeCapture(&b, r.Capture)

	blocking := r.Blocking()
	fmt.Fprintln(&b)
	if len(blocking) == 0 {
		// NOT "everything is fine" — some checks may be unknown, and saying otherwise would be the confident
		// lie this whole package exists to avoid.
		writeProse(&b, "Nothing here is stopping a run. Anything above marked \"could not establish\" is exactly that — unknown, not clear.")
		writeWithheldCount(&b, r, opts)
		return write(w, b.String())
	}
	// SINGULAR AND PLURAL, WRITTEN OUT. This read "%d thing(s) stop a run happening on this machine" — a
	// parenthesised plural is a note to the reader that the program could not be bothered to agree with itself, and
	// "stop" was simply wrong for one. This is the sentence a person sees at the moment something is broken; it
	// should read like a sentence.
	if len(blocking) == 1 {
		fmt.Fprintln(&b, paint(opts.Color, ansiBold, "One thing stops a run happening on this machine:"))
	} else {
		fmt.Fprintln(&b, paint(opts.Color, ansiBold, fmt.Sprintf("%d things stop a run happening on this machine:", len(blocking))))
	}
	for _, c := range blocking {
		for i, line := range wrap(c.Name+" — "+c.Remedy, "    ", wrapWidth) {
			if i == 0 {
				fmt.Fprintf(&b, "  - %s\n", strings.TrimLeft(line, " "))
				continue
			}
			fmt.Fprintln(&b, line)
		}
	}
	writeWithheldCount(&b, r, opts)
	fmt.Fprintln(&b)
	writeProse(&b, "We do not install anything on your machine. This command reports; you decide.")
	return write(w, b.String())
}

// writeCapture states network capture on this machine after the checks, each line wrapped at the doctor's width and kept
// at its own indent.
func writeCapture(b *strings.Builder, status *capture.Status) {
	if status == nil || len(status.Lines) == 0 {
		return
	}
	fmt.Fprintln(b)
	for _, line := range status.Lines {
		for _, piece := range strings.Split(line, "\n") {
			text := strings.TrimLeft(piece, " ")
			indent := strings.Repeat(" ", len(piece)-len(text))
			for _, wrapped := range wrap(text, indent, wrapWidth) {
				fmt.Fprintln(b, wrapped)
			}
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
func writeWithheldCount(b *strings.Builder, r Report, opts Options) {
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
	fmt.Fprintln(b)
	if withheld == 0 {
		// Only the reasons are withheld (every real check carries one).
		writeProse(b, fmt.Sprintf("Every check knows why it is required — run `%s` to read the reasons.", command.Line("doctor "+WhyFlag)))
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
	writeProse(b, sentence+fmt.Sprintf(" — run `%s` to read them.", command.Line("doctor "+WhyFlag)))
}

// writeCheckLine prints the check's own line: the gutter, the name, the headline word and the detail — wrapped
// like everything else here, with continuations at FOUR spaces.
//
// THREE DEPTHS, AND THEY ARE THE HIERARCHY: column 0 is a new check (and only a gutter marker ever sits there),
// four is this check still talking, six is one of its paragraphs. The old renderer had one depth and no wrapping,
// so a 180-character detail folded at column 0 and read as the start of the next check.
//
// The gutter and the name are coloured; the detail is not. Colour marks WHICH CHECK and WHAT STATE — a coloured
// sentence would be decoration, and decoration is the thing that cannot survive being piped to a file.
func writeCheckLine(b *strings.Builder, c Check, opts Options) {
	head := c.Name + ": " + headline(c) + detailSuffix(c)
	lines := wrap(head, "    ", wrapWidth)
	first := strings.TrimLeft(lines[0], " ")
	// The name is painted inside the already-wrapped first line, so the escape codes cannot affect the wrapping
	// arithmetic — a coloured string is longer in bytes than it is on screen, and wrapping it would indent by the
	// length of an escape sequence.
	first = strings.Replace(first, c.Name, paint(opts.Color, ansiBold, c.Name), 1)
	fmt.Fprintf(b, "%s %s\n", gutter(c, opts.Color), first)
	for _, line := range lines[1:] {
		fmt.Fprintln(b, line)
	}
}

// writeProse wraps one un-indented sentence at the same width as everything else — the summary lines are prose
// too, and a summary that ran past the margin the paragraphs respect would be the only thing on the page the
// terminal still folded at column 0.
func writeProse(b *strings.Builder, text string) {
	for _, line := range wrap(text, "", wrapWidth) {
		fmt.Fprintln(b, line)
	}
}

// writeParagraph prints one indented, wrapped paragraph under a check. SIX SPACES, two deeper than the check's
// own line, so a wrapped continuation cannot be mistaken for the next check: the old renderer printed these
// unwrapped at four spaces and let the terminal fold them at column 0, where a 600-character caveat ran straight
// into the next check's name.
func writeParagraph(b *strings.Builder, text string) {
	for _, line := range wrap(text, "      ", wrapWidth) {
		fmt.Fprintln(b, line)
	}
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

// gutter is the state, in the left margin, IN TEXT — three characters wide so the names line up and a reader
// scans one column instead of eleven sentences. Colour repeats what the marker already says; it never adds a
// distinction of its own, which is what makes a piped or NO_COLOR run lose nothing.
//
// The palette, and the one it had to invent:
//
//	!!  blocks a run          red     — the only thing that needs doing now
//	??  could not establish   yellow  — neither failure nor pass, and it has been load-bearing since day one
//	fyi true, does not block  blue    — the INVENTED one: not a shade of red, because a check that does not stop
//	                                    a run must not look like one that does (the advisory decision was made in
//	                                    the previous slice and the renderer would otherwise undo it); blue reads
//	                                    as a note in every convention a developer already knows
//	ok  ready                 green
//	n/a not needed here       dim
func gutter(c Check, color bool) string {
	switch urgencyGroup(c) {
	case 0:
		return paint(color, ansiRed, "!! ")
	case 1:
		return paint(color, ansiYellow, "?? ")
	case 2:
		return paint(color, ansiBlue, "fyi")
	case 4:
		return paint(color, ansiDim, "n/a")
	default:
		return paint(color, ansiGreen, "ok ")
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
