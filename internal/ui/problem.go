package ui

import "strings"

// Problem is EVERY PROBLEM THE AGENT SAYS, in the same three parts, in this order:
//
//	✗ what is wrong                         one line, red (stops a run) or yellow (needs a look)
//	  why it matters                        one short line, dim: what it stops
//	  → what to do                          a command or a step to copy, on its own line
//
// and under --why / --verbose, dim, the raw error as it came — never on its own, and never instead of the three parts.
type Problem struct {
	Level Level // LevelStop or LevelLook
	What  string
	// Items are what the problem is about, one line each under what is wrong: the phones it names, for one.
	Items []string
	Why   string
	Do    string
	// Raw is the error as a library or a server gave it. Shown only when asked for: a Go error is not a sentence.
	Raw string
}

// Problem is the problem as lines folded to width (0 = not folded), the raw error included only when verbose.
func (m Mode) Problem(p Problem, width int, verbose bool) []string {
	pad := strings.Repeat(" ", m.GutterWidth())
	level := p.Level
	if level == LevelGood {
		level = LevelLook
	}
	var out []string
	out = append(out, m.Marked(LevelMark(level), p.What, func(s string) string { return m.In(level, s) }, width)...)
	for _, item := range p.Items {
		out = append(out, m.Indented(pad, item, nil, width)...)
	}
	if p.Why != "" {
		out = append(out, m.Indented(pad, p.Why, m.Dim, width)...)
	}
	if p.Do != "" {
		out = append(out, m.DoLine(pad, p.Do, width)...)
	}
	if verbose && p.Raw != "" {
		out = append(out, m.Indented(pad, "as it came: "+p.Raw, m.Dim, width)...)
	}
	return out
}

// Marked is text after a mark, folded, each line painted on its own (a colour must not run across a line break into the
// next line's indent) and every continuation under the text rather than under the mark.
func (m Mode) Marked(mark Mark, text string, paint func(string) string, width int) []string {
	pad := strings.Repeat(" ", m.GutterWidth())
	plain := Fold(text, pad, pad, width)
	out := make([]string, len(plain))
	for i, line := range plain {
		body := paintBody(strings.TrimPrefix(line, pad), paint)
		if i == 0 {
			out[i] = m.Gutter(mark) + body
			continue
		}
		out[i] = pad + body
	}
	return out
}

// Indented is text at indent, folded, each line painted on its own.
func (m Mode) Indented(indent, text string, paint func(string) string, width int) []string {
	plain := Fold(text, indent, indent, width)
	out := make([]string, len(plain))
	for i, line := range plain {
		out[i] = indent + paintBody(strings.TrimPrefix(line, indent), paint)
	}
	return out
}

// DoLine is what to do: the arrow at indent, then the step, folded under the step and never under the arrow.
func (m Mode) DoLine(indent, text string, width int) []string {
	arrow := m.Symbol(MarkDo)
	hang := indent + strings.Repeat(" ", Columns(arrow)+1)
	plain := Fold(text, hang, hang, width)
	out := make([]string, len(plain))
	for i, line := range plain {
		if i == 0 {
			out[i] = indent + m.Mark(MarkDo) + " " + strings.TrimPrefix(line, hang)
			continue
		}
		out[i] = line
	}
	return out
}

func paintBody(text string, paint func(string) string) string {
	if paint == nil {
		return text
	}
	return paint(text)
}
