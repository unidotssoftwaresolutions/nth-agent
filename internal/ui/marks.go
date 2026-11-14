package ui

// Mark is a line's symbol in the left margin: what kind of line it is, said before a word of it is read.
type Mark int

const (
	// MarkOK: this works.
	MarkOK Mark = iota
	// MarkLook: this needs a look — it does not stop a run, or it could not be established.
	MarkLook
	// MarkStop: this stops a run.
	MarkStop
	// MarkNote: detail, or something this machine does not need.
	MarkNote
	// MarkDo: what to do — the line a person copies.
	MarkDo
)

// symbols are the marks, Unicode and ASCII. The ASCII ones are TWO letters each so they cannot be read as punctuation in a
// sentence: `ok`, `!!`, `xx`, and `--`, `->` for the two that are not a state.
var symbols = map[Mark][2]string{
	MarkOK:   {"✓", "ok"},
	MarkLook: {"!", "!!"},
	MarkStop: {"✗", "xx"},
	MarkNote: {"·", "--"},
	MarkDo:   {"→", "->"},
}

// Symbol is the mark itself, uncoloured.
func (m Mode) Symbol(mark Mark) string {
	s := symbols[mark]
	if m.ASCII {
		return s[1]
	}
	return s[0]
}

// Mark is the mark in its colour: green, yellow, red, dim, and the arrow bold — it leads to the line a person acts on.
func (m Mode) Mark(mark Mark) string {
	symbol := m.Symbol(mark)
	switch mark {
	case MarkOK:
		return m.Good(symbol)
	case MarkLook:
		return m.Look(symbol)
	case MarkStop:
		return m.Stop(symbol)
	case MarkDo:
		return m.Bold(symbol)
	default:
		return m.Dim(symbol)
	}
}

// Gutter is the mark and the space after it: where a marked line's text starts.
func (m Mode) Gutter(mark Mark) string { return m.Mark(mark) + " " }

// GutterWidth is how many columns Gutter takes, so a folded line's continuation sits under its text and not under its mark.
func (m Mode) GutterWidth() int {
	if m.ASCII {
		return 3
	}
	return 2
}

// LevelMark is a level's mark.
func LevelMark(level Level) Mark {
	switch level {
	case LevelStop:
		return MarkStop
	case LevelLook:
		return MarkLook
	default:
		return MarkOK
	}
}
