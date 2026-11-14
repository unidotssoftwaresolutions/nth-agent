package ui

import "strings"

// THE WIDTH RULES — one set, for every screen.
//
//   - ON A TERMINAL a line is folded to the terminal's width, and never wider than Measure: past about a hundred columns
//     the eye loses its place going back to the start of the next line, so a 160-column terminal reads the same sentences
//     at 100.
//   - OFF A TERMINAL a report (doctor, devices), read once and whole, is folded at ReportWidth, the width terminals
//     essentially do not go below — the output a person pastes is then the output anyone else renders. A stream
//     (connect's narration) is never folded off a terminal: in a file, a pipe or a CI log every line stays whole, so it can
//     be found with grep and read by a program the way it was written.
//   - NOTHING A PERSON COPIES IS EVER SPLIT: a word, a span in backticks, a command with its flags and values, an address,
//     a path. One that is longer than the line overflows its line, alone.
//   - A folded line's continuation sits under the text it continues, never at column 0, where it would read as the start
//     of something new.
//   - However deep the indent or narrow the terminal, a line keeps MinColumns for its words.
const (
	Measure     = 100
	ReportWidth = 80
	MinColumns  = 20
)

// Stream is the width connect's narration folds to: the terminal's, up to Measure; 0 (nothing folded) off a terminal.
func (m Mode) Stream() int {
	if !m.Terminal || m.Columns <= 0 {
		return 0
	}
	return min(m.Columns, Measure)
}

// Report is the width a report folds to: the terminal's, up to Measure; ReportWidth off a terminal.
func (m Mode) Report() int {
	if !m.Terminal || m.Columns <= 0 {
		return ReportWidth
	}
	return min(m.Columns, Measure)
}

// Fold is text folded to width columns at its spaces. The first line starts with first, every line after it with rest. A
// width of 0 or less folds nothing: the one line comes back whole. The indents may hold escape codes; only what shows on
// a screen is counted.
func Fold(text, first, rest string, width int) []string {
	text = strings.TrimSpace(text)
	if width <= 0 {
		return []string{first + text}
	}
	pieces := units(text)
	if len(pieces) == 0 {
		return []string{strings.TrimRight(first, " ")}
	}
	room := func(indent string) int { return max(width, Columns(indent)+MinColumns) }
	var lines []string
	line, used, limit := first+pieces[0].text, Columns(first+pieces[0].text), room(first)
	for _, u := range pieces[1:] {
		if used+Columns(u.gap+u.text) > limit {
			lines = append(lines, line)
			line, used, limit = rest+u.text, Columns(rest+u.text), room(rest)
			continue
		}
		line += u.gap + u.text
		used += Columns(u.gap + u.text)
	}
	return append(lines, line)
}

// FoldLine is one line folded as a terminal would want it: its own leading spaces kept, and each continuation two columns
// deeper than the line it continues.
func FoldLine(line string, width int) string {
	if width <= 0 || Columns(line) <= width {
		return line
	}
	text := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(text)]
	return strings.Join(Fold(text, indent, indent+"  ", width), "\n")
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
	word := Plain(words[i])
	switch {
	case strings.HasPrefix(word, "`") && strings.Count(word, "`") == 1:
		for j := i + 1; j < len(words); j++ {
			if strings.Contains(words[j], "`") {
				return j - i + 1
			}
		}
	case word == "npx" || word == "npm" || word == "brew":
		return command(words, i, 3)
	case word == "adb" && i+1 < len(words) && (strings.HasPrefix(words[i+1], "-") || adbVerbs[Plain(words[i+1])]):
		// adb's own words read like prose (shell settings put global …), so its command runs to the end of the clause.
		n := 1
		for n < len(words)-i && !endsClause(words[i+n-1]) {
			n++
		}
		return n
	case strings.HasPrefix(word, "--") && i+1 < len(words) && (placeholder(Plain(words[i+1])) || strings.Contains(words[i+1], "://")):
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
		word, before := Plain(words[i+n]), Plain(words[i+n-1])
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
	word = Plain(word)
	return strings.HasSuffix(word, ",") || strings.HasSuffix(word, ";") || strings.HasSuffix(word, ")") ||
		(strings.HasSuffix(word, ".") && !strings.HasSuffix(word, "..."))
}
