package ui

import (
	"regexp"
	"unicode/utf8"
)

// THE PALETTE — five styles, and the 8 basic colours rather than 256 or truecolor: these render on every terminal a
// developer has, a bare tty and a CI viewer included.
//
//	green   what works                  Good
//	yellow  what needs a look           Look
//	red     what stops a run            Stop
//	dim     detail                      Dim
//	bold    the one thing to read       Bold
//
// THE ONLY ESCAPE CODES IN THIS PROGRAM are the five below, the reset, and the two that keep an updating line (console.go).
const (
	codeReset  = "\033[0m"
	codeGreen  = "\033[32m"
	codeYellow = "\033[33m"
	codeRed    = "\033[31m"
	codeDim    = "\033[2m"
	codeBold   = "\033[1m"
)

// paint wraps text in codes, or returns it untouched when colour is off: ONE branch, so "colour is off" is not a rule every
// call site has to remember.
func (m Mode) paint(text string, codes ...string) string {
	if !m.Color || text == "" {
		return text
	}
	prefix := ""
	for _, code := range codes {
		prefix += code
	}
	return prefix + text + codeReset
}

// Good is the green of what works.
func (m Mode) Good(text string) string { return m.paint(text, codeGreen) }

// Look is the yellow of what needs a look.
func (m Mode) Look(text string) string { return m.paint(text, codeYellow) }

// Stop is the red of what stops a run.
func (m Mode) Stop(text string) string { return m.paint(text, codeRed) }

// Dim is detail: what a reader may skip.
func (m Mode) Dim(text string) string { return m.paint(text, codeDim) }

// Bold is the one thing on a screen to read first.
func (m Mode) Bold(text string) string { return m.paint(text, codeBold) }

// Headline is a summary's first line: bold, in the colour of its level.
func (m Mode) Headline(level Level, text string) string {
	switch level {
	case LevelStop:
		return m.paint(text, codeBold, codeRed)
	case LevelLook:
		return m.paint(text, codeBold, codeYellow)
	default:
		return m.paint(text, codeBold, codeGreen)
	}
}

// Level is how much a line matters: what works, what needs a look, what stops a run.
type Level int

const (
	LevelGood Level = iota
	LevelLook
	LevelStop
)

// In paints text in its level's colour.
func (m Mode) In(level Level, text string) string {
	switch level {
	case LevelStop:
		return m.Stop(text)
	case LevelLook:
		return m.Look(text)
	default:
		return m.Good(text)
	}
}

// escapeCode is any CSI sequence: what Plain strips. Wider than the codes above on purpose, so the updating line's own codes
// go too.
var escapeCode = regexp.MustCompile("\033\\[[0-9;?]*[A-Za-z]")

// Plain is text with every escape code taken out: what a reader sees, and what a seal compares.
func Plain(text string) string { return escapeCode.ReplaceAllString(text, "") }

// Columns is how many columns text takes on a screen: runes, escape codes not counted.
func Columns(text string) int { return utf8.RuneCountInString(Plain(text)) }
