package netcap

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ── A JSON BODY'S FIELDS, MASKED WHERE THEY STAND ───────────────────────────────────────────────────────────────
//
// HarRedactor.MaskJsonFields, ported. The runner reads a body with .NET's Utf8JsonReader — comments skipped, trailing commas
// allowed, several values in one body allowed, 256 levels deep — and replaces only the spans it masks, so every other
// byte stays as the app sent it; a body cut at the preview's bound is masked up to the cut, and the text rule reads the
// rest. This is that reader, and that splice.
//
// WHY NOT encoding/json. The spike masked by unmarshalling and re-marshalling, and the vectors it had could not see the
// difference. They can now (the shared vectors' cut, commented and two-value cases): a body json.Unmarshal refuses — cut
// at 256 KB, written with a comment, a Sentry envelope's lines — fell back to the text rule alone, which masks a value by
// its own name and never "everything under a credential's name"; and a two-value body kept only its first value. Each of
// those left a value the runner would have masked.

// maskJSONFields is HarRedactor.MaskJsonFields: the value of a credential's name, everything inside an object or a list
// under a strong one, and a string that itself holds JSON or pairs masked within. Text that is not JSON comes back as it
// was; a document cut short is masked up to the fault.
func maskJSONFields(text string) string {
	first := 0
	for first < len(text) {
		r, size := utf8.DecodeRuneInString(text[first:])
		if !isNetSpace(r) && r != 0xFEFF {
			break
		}
		first += size
	}
	if first == len(text) || (text[first] != '{' && text[first] != '[') {
		return text
	}

	type span struct {
		start, end int
		with       string
	}
	var spans []span
	var beneath []bool // per open object or list: everything inside it is masked
	pending := reachNone
	reader := &jsonReader{s: text, at: first}
	for {
		tok, ok := reader.next()
		if !ok {
			break // the end — or a fault, and what was read before it stands
		}
		inside := len(beneath) > 0 && beneath[len(beneath)-1]
		switch tok.kind {
		case jsonName:
			pending = reachNone
			if tok.valid {
				pending = nameReach(tok.text)
			}
			continue
		case jsonStartObject, jsonStartArray:
			beneath = append(beneath, inside || pending == reachEverything)
		case jsonEndObject, jsonEndArray:
			if len(beneath) > 0 {
				beneath = beneath[:len(beneath)-1]
			}
		case jsonString:
			if inside || pending != reachNone {
				spans = append(spans, span{tok.start, tok.end, maskedJSONString})
			} else if tok.valid {
				if within, changed := maskedWithin(tok.text); changed {
					spans = append(spans, span{tok.start, tok.end, quoteJSON(within)})
				}
			}
		case jsonNumber:
			if inside || pending != reachNone {
				spans = append(spans, span{tok.start, tok.end, maskedJSONString})
			}
		}
		pending = reachNone
	}

	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	at := 0
	for _, s := range spans {
		b.WriteString(text[at:s.start])
		b.WriteString(s.with)
		at = s.end
	}
	b.WriteString(text[at:])
	return b.String()
}

type jsonKind int

const (
	jsonStartObject jsonKind = iota + 1
	jsonEndObject
	jsonStartArray
	jsonEndArray
	jsonName
	jsonString
	jsonNumber
	jsonLiteral
)

// jsonToken is one JSON token and where it stands (held in variables named tok: the farm's census reads every
// identifier named token as the agent's farm token): a string's span includes its quotes, and its text is its value with the
// escapes read — valid false when .NET could not read that value as text (an unpaired surrogate).
type jsonToken struct {
	kind       jsonKind
	start, end int
	text       string
	valid      bool
}

type jsonState int

const (
	expectValue        jsonState = iota // a top-level value, an object member's value
	expectValueOrClose                  // after `[`, or after `,` in a list (a trailing comma is allowed)
	expectNameOrClose                   // after `{`, or after `,` in an object
	expectColon                         // after a name
	expectCommaOrClose                  // after a value inside an object or a list
)

// jsonMaxDepth is the runner's JsonReaderOptions.MaxDepth.
const jsonMaxDepth = 256

// jsonReader reads JSON the way the runner's Utf8JsonReader is configured to (HarRedactor.JsonFields): comments skipped,
// trailing commas allowed, several top-level values allowed, at most 256 levels deep. Anything else is a fault, and a
// fault ends the reading.
type jsonReader struct {
	s     string
	at    int
	stack []byte // 'o' for an object, 'a' for a list
	state jsonState
}

func (r *jsonReader) next() (jsonToken, bool) {
	for {
		if !r.skipSpaceAndComments() || r.at >= len(r.s) {
			return jsonToken{}, false
		}
		c := r.s[r.at]
		switch r.state {
		case expectColon:
			if c != ':' {
				return jsonToken{}, false
			}
			r.at++
			r.state = expectValue
			continue
		case expectCommaOrClose:
			switch {
			case c == ',':
				r.at++
				if r.top() == 'o' {
					r.state = expectNameOrClose
				} else {
					r.state = expectValueOrClose
				}
				continue
			case c == '}' && r.top() == 'o':
				return r.closeContainer(jsonEndObject), true
			case c == ']' && r.top() == 'a':
				return r.closeContainer(jsonEndArray), true
			}
			return jsonToken{}, false
		case expectNameOrClose:
			if c == '}' {
				return r.closeContainer(jsonEndObject), true
			}
			if c != '"' {
				return jsonToken{}, false
			}
			tok, ok := r.readString()
			if !ok {
				return jsonToken{}, false
			}
			tok.kind = jsonName
			r.state = expectColon
			return tok, true
		case expectValueOrClose:
			if c == ']' {
				return r.closeContainer(jsonEndArray), true
			}
		}
		return r.readValue(c)
	}
}

func (r *jsonReader) readValue(c byte) (jsonToken, bool) {
	start := r.at
	switch {
	case c == '{' || c == '[':
		if len(r.stack) >= jsonMaxDepth {
			return jsonToken{}, false
		}
		r.at++
		if c == '{' {
			r.stack = append(r.stack, 'o')
			r.state = expectNameOrClose
			return jsonToken{kind: jsonStartObject, start: start, end: r.at}, true
		}
		r.stack = append(r.stack, 'a')
		r.state = expectValueOrClose
		return jsonToken{kind: jsonStartArray, start: start, end: r.at}, true
	case c == '"':
		tok, ok := r.readString()
		if !ok {
			return jsonToken{}, false
		}
		tok.kind = jsonString
		r.afterValue()
		return tok, true
	case c == '-' || (c >= '0' && c <= '9'):
		end, ok := numberEnd(r.s, r.at)
		if !ok || !delimitedAt(r.s, end) {
			return jsonToken{}, false
		}
		r.at = end
		r.afterValue()
		return jsonToken{kind: jsonNumber, start: start, end: end}, true
	default:
		for _, literal := range []string{"true", "false", "null"} {
			if strings.HasPrefix(r.s[r.at:], literal) && delimitedAt(r.s, r.at+len(literal)) {
				r.at += len(literal)
				r.afterValue()
				return jsonToken{kind: jsonLiteral, start: start, end: r.at}, true
			}
		}
		return jsonToken{}, false
	}
}

func (r *jsonReader) top() byte {
	if len(r.stack) == 0 {
		return 0
	}
	return r.stack[len(r.stack)-1]
}

func (r *jsonReader) closeContainer(kind jsonKind) jsonToken {
	start := r.at
	r.at++
	r.stack = r.stack[:len(r.stack)-1]
	r.afterValue()
	return jsonToken{kind: kind, start: start, end: r.at}
}

// afterValue: inside a container a value is followed by a comma or the close; at the top another value may follow.
func (r *jsonReader) afterValue() {
	if len(r.stack) == 0 {
		r.state = expectValue
	} else {
		r.state = expectCommaOrClose
	}
}

// skipSpaceAndComments steps over JSON white space and comments; false for a comment that never ends or a `/` that
// starts none.
func (r *jsonReader) skipSpaceAndComments() bool {
	for r.at < len(r.s) {
		switch c := r.s[r.at]; c {
		case ' ', '\t', '\n', '\r':
			r.at++
		case '/':
			if r.at+1 >= len(r.s) {
				return false
			}
			switch r.s[r.at+1] {
			case '/':
				end := strings.IndexAny(r.s[r.at+2:], "\r\n")
				if end < 0 {
					r.at = len(r.s)
					return true
				}
				r.at += 2 + end
			case '*':
				end := strings.Index(r.s[r.at+2:], "*/")
				if end < 0 {
					return false
				}
				r.at += 2 + end + 2
			default:
				return false
			}
		default:
			return true
		}
	}
	return true
}

// readString reads the string whose opening quote is at r.at.
func (r *jsonReader) readString() (jsonToken, bool) {
	start := r.at
	escaped := false
	for i := start + 1; i < len(r.s); {
		c := r.s[i]
		switch {
		case c == '"':
			raw := r.s[start+1 : i]
			r.at = i + 1
			tok := jsonToken{start: start, end: r.at, text: raw, valid: true}
			if escaped {
				tok.text, tok.valid = unescapeJSONString(raw)
			}
			return tok, true
		case c == '\\':
			escaped = true
			if i+1 >= len(r.s) {
				return jsonToken{}, false
			}
			switch r.s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+6 > len(r.s) || !isHex(r.s[i+2]) || !isHex(r.s[i+3]) || !isHex(r.s[i+4]) || !isHex(r.s[i+5]) {
					return jsonToken{}, false
				}
				i += 6
			default:
				return jsonToken{}, false
			}
		case c < 0x20:
			return jsonToken{}, false
		default:
			i++
		}
	}
	return jsonToken{}, false
}

// numberEnd is where a strict JSON number starting at i ends: -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func numberEnd(s string, i int) (int, bool) {
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return 0, false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	return i, true
}

// delimitedAt says whether a number or a literal may end at i: at the end, or before white space, a separator, a close
// or a comment.
func delimitedAt(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	switch s[i] {
	case ' ', '\t', '\n', '\r', ',', ']', '}', '/':
		return true
	}
	return false
}

// unescapeJSONString reads a JSON string's escapes. ok is false for anything .NET would not read as text: an invalid
// escape, a raw control character, or an unpaired surrogate.
func unescapeJSONString(raw string) (string, bool) {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); {
		c := raw[i]
		if c < 0x20 {
			return "", false
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(raw) {
			return "", false
		}
		switch raw[i+1] {
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case '/':
			b.WriteByte('/')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			unit, ok := hex4(raw, i+2)
			if !ok {
				return "", false
			}
			i += 6
			if utf16.IsSurrogate(rune(unit)) {
				if unit >= 0xDC00 { // a low surrogate with no high one before it
					return "", false
				}
				low, ok := hex4(raw, i+2)
				if !ok || i+1 >= len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
					return "", false
				}
				pair := utf16.DecodeRune(rune(unit), rune(low))
				if pair == utf8.RuneError {
					return "", false
				}
				b.WriteRune(pair)
				i += 6
				continue
			}
			b.WriteRune(rune(unit))
			continue
		default:
			return "", false
		}
		i += 2
	}
	return b.String(), true
}

func hex4(s string, at int) (uint16, bool) {
	if at+4 > len(s) {
		return 0, false
	}
	var v uint16
	for i := at; i < at+4; i++ {
		if !isHex(s[i]) {
			return 0, false
		}
		v = v<<4 | uint16(unhex(s[i]))
	}
	return v, true
}
