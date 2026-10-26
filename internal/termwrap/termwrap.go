// Package termwrap folds what connect says to the width of the terminal it says it in (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 4).
//
// A terminal folds a line that is too long for it wherever its columns run out, which is usually in the middle of a word.
// So in a terminal each line is folded here first, at a space, to the terminal's width as it is at that moment; the lines
// after the first are indented two spaces deeper than the line they continue. A command, an address or a path is never
// split: a person copies those whole.
//
// ANYTHING THAT IS NOT A TERMINAL IS NOT FOLDED. In a file, a pipe or a CI log every line stays whole, so it can be found
// with grep and read by a program the way it was written.
package termwrap

import (
	"io"
	"os"
	"strings"
)

// For is w folded to its terminal's width when w is a terminal, and w itself when it is anything else.
func For(w io.Writer) io.Writer {
	f, ok := w.(*os.File)
	if !ok {
		return w
	}
	if _, isTerminal := terminalWidth(f); !isTerminal {
		return w
	}
	return &Writer{Out: w, Width: func() int {
		width, _ := terminalWidth(f)
		return width
	}}
}

// Writer folds each line written to it to Width() columns before it passes it on. A width of zero or less folds nothing.
//
// Every write is folded on its own: connect writes a whole line, or a few, at a time, from more than one goroutine, so
// nothing is held back for a later write to finish.
type Writer struct {
	Out   io.Writer
	Width func() int
}

// minimumColumns is the least a line is given, however deep its indent or narrow the terminal: below it a sentence would
// become one word per line.
const minimumColumns = 20

func (w *Writer) Write(p []byte) (int, error) {
	width := w.Width()
	if width <= 0 {
		return w.Out.Write(p)
	}
	lines := strings.Split(string(p), "\n")
	for i, line := range lines {
		lines[i] = fold(line, width)
	}
	if _, err := io.WriteString(w.Out, strings.Join(lines, "\n")); err != nil {
		return 0, err
	}
	return len(p), nil
}

// fold is one line folded to width columns at its spaces, keeping each unit (see units) on one line.
func fold(line string, width int) string {
	if len([]rune(line)) <= width {
		return line
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	pieces := units(strings.TrimLeft(line, " "))
	if len(pieces) == 0 {
		return line
	}
	limit := func(indent string) int { return max(width, len([]rune(indent))+minimumColumns) }
	next := indent + "  "
	var b strings.Builder
	b.WriteString(indent + pieces[0].text)
	used, room := len([]rune(indent+pieces[0].text)), limit(indent)
	for _, u := range pieces[1:] {
		if used+len([]rune(u.gap+u.text)) > room {
			b.WriteString("\n" + next + u.text)
			used, room = len([]rune(next+u.text)), limit(next)
			continue
		}
		b.WriteString(u.gap + u.text)
		used += len([]rune(u.gap + u.text))
	}
	return b.String()
}

// unit is a run of words a line may not be folded inside, and the spaces that came before it.
type unit struct{ gap, text string }

// units splits a line's text into what it may be folded between: words, except that these stay whole —
//
//   - a span in backticks;
//   - a command: npx, npm or adb, the words that name what it runs, and every word after them that reads as part of a
//     command (a flag and its value, an address, a path, a placeholder, `…`), up to a word that ends a clause;
//   - a flag with the placeholder or address that follows it (--appium URL).
func units(text string) []unit {
	var words, gaps []string
	for len(text) > 0 {
		trimmed := strings.TrimLeft(text, " ")
		gap := text[:len(text)-len(trimmed)]
		if trimmed == "" {
			break
		}
		end := strings.IndexByte(trimmed, ' ')
		if end < 0 {
			end = len(trimmed)
		}
		words, gaps = append(words, trimmed[:end]), append(gaps, gap)
		text = trimmed[end:]
	}
	var out []unit
	for i := 0; i < len(words); {
		j := i + wholeFrom(words, i)
		u := unit{gap: gaps[i], text: words[i]}
		for k := i + 1; k < j; k++ {
			u.text += gaps[k] + words[k]
		}
		out = append(out, u)
		i = j
	}
	return out
}

// wholeFrom is how many words, from words[i], may not be folded apart.
func wholeFrom(words []string, i int) int {
	word := words[i]
	switch {
	case strings.HasPrefix(word, "`") && strings.Count(word, "`") == 1:
		for j := i + 1; j < len(words); j++ {
			if strings.Contains(words[j], "`") {
				return j - i + 1
			}
		}
	case word == "npx" || word == "npm":
		return command(words, i, 3)
	case word == "adb" && i+1 < len(words) && (strings.HasPrefix(words[i+1], "-") || adbVerbs[words[i+1]]):
		// adb's own words read like prose (shell settings put global …), so its command runs to the end of the clause.
		n := 1
		for n < len(words)-i && !endsClause(words[i+n-1]) {
			n++
		}
		return n
	case strings.HasPrefix(word, "--") && i+1 < len(words) && (placeholder(words[i+1]) || strings.Contains(words[i+1], "://")):
		return 2
	}
	return 1
}

var adbVerbs = map[string]bool{"shell": true, "reverse": true, "forward": true, "devices": true, "connect": true}

// command is how many words from words[i] are one command: its first `head` words (the program, and what it runs), then
// every word that reads as part of a command, up to and including a word that ends a clause.
func command(words []string, i, head int) int {
	n := 1
	for i+n < len(words) && !endsClause(words[i+n-1]) {
		word, before := words[i+n], words[i+n-1]
		if n >= head && !partOfACommand(word, before) {
			break
		}
		n++
	}
	return n
}

// partOfACommand is a word that reads as part of the command before it rather than as the sentence going on.
func partOfACommand(word, before string) bool {
	bare := strings.TrimRight(word, ",;:.)")
	switch {
	case strings.HasPrefix(word, "-"), word == "…", word == "...", strings.HasPrefix(word, "["):
		return true
	case strings.Contains(word, "://"), strings.Contains(word, "/"), strings.Contains(word, "@"):
		return true
	case placeholder(bare):
		return true
	case before == "capture" && (bare == "enable" || bare == "setup" || bare == "disable"):
		return true
	case before == "setup" && strings.ContainsAny(bare, "0123456789"):
		return true // a phone's serial
	case strings.HasPrefix(before, "--") && !strings.HasPrefix(word, "-"):
		return true // a flag's value
	}
	return false
}

// placeholder is a word in capitals that stands for what a person fills in (URL, SERIAL).
func placeholder(word string) bool {
	if len(word) < 2 {
		return false
	}
	for _, r := range word {
		if (r < 'A' || r > 'Z') && r != '_' {
			return false
		}
	}
	return true
}

// endsClause is a word the sentence pauses after: a command it ends stops there.
func endsClause(word string) bool {
	return strings.HasSuffix(word, ",") || strings.HasSuffix(word, ";") || strings.HasSuffix(word, ")") ||
		(strings.HasSuffix(word, ".") && !strings.HasSuffix(word, "..."))
}
