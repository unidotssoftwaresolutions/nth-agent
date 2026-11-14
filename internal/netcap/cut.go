package netcap

import (
	"strconv"
	"unicode/utf8"
)

// ── WHAT ONE ENTRY'S TEXTS MAY HOLD: the runner's own bounds, cut as it cuts ─────────────────────────────────────────────
//
// A header value is kept to 16,384 characters and an address to 4,000 — the runner cuts each to the same before it masks
// it, and the farm's own node cuts its capture to the same. A text over its bound keeps its beginning and ends with the
// runner's marker, " [truncated at N chars]", the marker counted inside the bound, so a reader that finds a text exactly
// that long and ending so knows it was cut. Characters are counted as the runner counts them (UTF-16), and a cut never goes
// through one. The value is MASKED FIRST and cut after (entryFor), so a cut only ever shortens what is already masked:
// cut first, the half of a token left before the cut would no longer have a token's shape, and would be kept.

const (
	// MaxHeaderChars is the most characters a header value keeps.
	MaxHeaderChars = 16384
	// MaxAddressChars is the most characters an address (url, redirectURL) keeps.
	MaxAddressChars = 4000
)

// cutText is value, or its beginning and the marker when it is longer than limit characters: limit in all.
func cutText(value string, limit int) string {
	if len(value) <= limit || utf16Length(value) <= limit {
		return value
	}
	marker := " [truncated at " + strconv.Itoa(limit) + " chars]"
	room, used, end := limit-len(marker), 0, 0
	for end < len(value) {
		r, size := utf8.DecodeRuneInString(value[end:])
		if used+utf16Units(r) > room {
			break
		}
		used += utf16Units(r)
		end += size
	}
	return value[:end] + marker
}

// utf16Length is how many characters the runner counts in s: an invalid byte is written as one replacement character.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Units(r)
	}
	return n
}

// utf16Units is how many characters the runner counts in r: two beyond the Basic Multilingual Plane, else one.
func utf16Units(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}
