package ui

import (
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// Console is one output that more than one part of the agent speaks into at once — connect's narration, the drive tunnel,
// network capture — so that their lines never cut into each other or into the updating line.
//
// THE UPDATING LINE. On a terminal, what repeats (connecting, a heartbeat, a reconnect) is one line at the bottom, rewritten
// in place; every other line is printed above it and it is drawn again under them. Off a terminal there is no such line: a
// progress line is an ordinary line, one each, and a status that only a terminal shows is not printed at all.
type Console struct {
	mu      sync.Mutex
	out     io.Writer
	mode    Mode
	columns func() int
	status  string // the updating line on the screen now, "" for none
	// Verbose prints the raw error under every problem (--verbose), dim.
	Verbose bool
}

// NewConsole is a console writing to out, dressed as mode says. On a terminal its width is read again for every line, so a
// window dragged wider is folded at its new width.
func NewConsole(out io.Writer, mode Mode) *Console {
	c := &Console{out: out, mode: mode, columns: func() int { return mode.Columns }}
	if f, ok := out.(*os.File); ok && mode.Terminal {
		c.columns = func() int {
			if columns, isTerminal := TerminalWidth(f); isTerminal {
				return columns
			}
			return mode.Columns
		}
	}
	return c
}

// Mode is how this console's lines are dressed.
func (c *Console) Mode() Mode { return c.mode }

// Width is the width a streamed line folds to now: the terminal's, up to Measure; 0 off a terminal (nothing is folded).
func (c *Console) Width() int {
	if !c.mode.Terminal {
		return 0
	}
	return min(c.columns(), Measure)
}

// Say prints lines, each folded to Width. A line that already fits is printed as it is.
func (c *Console) Say(lines ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.printLocked(lines)
}

// Write is the console as an io.Writer, for a part of the agent that prints its own lines (the tunnel, capture): each line
// is folded like any other and printed above the updating line.
//
// A write that does not end its line (a prompt) is passed on as it is, with the updating line taken down first and drawn
// again only after a line that ends: a line drawn over it would erase what was just written.
func (c *Console) Write(p []byte) (int, error) {
	text := string(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !strings.HasSuffix(text, "\n") {
		lines := strings.Split(text, "\n")
		last := lines[len(lines)-1]
		if len(lines) > 1 {
			if err := c.printLocked(lines[:len(lines)-1]); err != nil {
				return 0, err
			}
		}
		if c.status != "" {
			io.WriteString(c.out, clearLine)
		}
		if _, err := io.WriteString(c.out, last); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if err := c.printLocked(strings.Split(strings.TrimSuffix(text, "\n"), "\n")); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Progress is a line that repeats: on a terminal it becomes the updating line; off one it is printed, one line each.
func (c *Console) Progress(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.mode.Terminal {
		c.printLocked([]string{text})
		return
	}
	c.status = text
	c.drawLocked()
}

// Status is the updating line on a terminal, and nothing at all off one: what a pipe was never told every ten seconds is not
// started being told now.
func (c *Console) Status(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.mode.Terminal {
		return
	}
	c.status = text
	c.drawLocked()
}

// Done takes the updating line off the screen, so whatever comes after the agent — the shell's prompt — starts on a clean
// line.
func (c *Console) Done() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status != "" {
		io.WriteString(c.out, clearLine)
		c.status = ""
	}
}

// clearLine returns to the start of the line and clears it: the two codes that keep an updating line.
const clearLine = "\r\033[K"

func (c *Console) printLocked(lines []string) error {
	var b strings.Builder
	if c.status != "" {
		b.WriteString(clearLine)
	}
	width := c.Width()
	for _, line := range lines {
		b.WriteString(FoldLine(line, width))
		b.WriteString("\n")
	}
	if _, err := io.WriteString(c.out, b.String()); err != nil {
		return err
	}
	c.drawLocked()
	return nil
}

// drawLocked draws the updating line, cut to fit: a line longer than the terminal would wrap, and the return to its start
// would then rewrite only its last row.
func (c *Console) drawLocked() {
	if c.status == "" {
		return
	}
	io.WriteString(c.out, clearLine+Cut(c.status, c.columns()-1))
}

// Cut is text cut to width columns, an ellipsis standing for what was cut. Escape codes are kept whole and not counted;
// a reset closes whatever was open when the cut fell inside a colour.
func Cut(text string, width int) string {
	if width <= 0 || Columns(text) <= width {
		return text
	}
	var b strings.Builder
	shown := 0
	for i := 0; i < len(text); {
		if loc := escapeCode.FindStringIndex(text[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(text[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if shown == width-1 {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		shown++
		i += size
	}
	if strings.Contains(b.String(), "\033[") {
		b.WriteString(codeReset)
	}
	return b.String()
}
