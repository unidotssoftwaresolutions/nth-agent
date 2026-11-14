package netcap

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ── THE RUNNER'S MASKING RULES, ON THIS MACHINE ─────────────────────────────────────────────────────────────────────
//
// Every exchange a capture records is masked HERE, in memory, before it is written anywhere — and the runner masks it again
// when it arrives. This file is a port of the runner's rules (its sensitive-name rule and its HAR redactor, fields included),
// read from the runner's current source and ported rule by rule, and BOTH are held to one file of
// cases: the runner/tests/SharedVectors/network-capture-mask-vectors.json (shared_vectors_test.go reads it across the
// repository boundary). A rule changed on one side and not the other turns that side's test red.
//
// WHAT IS PORTED, AND HOW CLOSELY:
//   - names (SensitiveNames): the same three lists, the same reading of a name, the same three reaches;
//   - headers: the four always-sensitive names, the five fragments, and every name the rule holds;
//   - the published credential shapes, JWT, Bearer and Basic, and a `"name": value` pair in any text;
//   - query and form pairs, multipart parts, and a JSON body's fields — the JSON reader (jsonspans.go) reads what
//     The runner's Utf8JsonReader reads (comments, trailing commas, several values in one body, a document cut short) and
//     replaces only the masked spans, so a masked body keeps its own bytes and layout, as the runner's does;
//   - `\s` is .NET's, not RE2's: .NET counts every Unicode space as white space, so each class below spells that set.
//
// WHAT IS NOT HERE, ON PURPOSE: the run's saved secrets. They live in the runner, which masks them on arrival in every
// spelling it knows; this agent never asks for them (the decision this slice was given), so nothing here can find them —
// and the shared vectors that carry saved secrets are skipped by this side's test, by that rule and no other.
//
// THE LIMITS ARE THE RUNNER'S LIMITS, said once in its own docblock (the redactor's field rules) and true here too: a value under a
// name no rule holds, with no published shape, survives; so does a credential written inside a string rather than as
// a field, and personal data, which is the app's own content.

// Mask is what a masked value becomes — the runner's LogRedactor.Mask, character for character.
const Mask = "«redacted»"

// maskedJSONString is a masked JSON value as the runner writes it into a body: the mask, quoted.
const maskedJSONString = `"` + Mask + `"`

// netSpace is .NET's `\s`, written as the inside of a character class: [\f\n\r\t\v\x85\p{Z}].
const netSpace = `\f\n\r\t\v\x{85}\p{Z}`

// ── SensitiveNames ───────────────────────────────────────────────────────────────────────────────────────────

var (
	namesWithin  = []string{"password", "passwd", "passphrase", "passcode", "secret", "apikey", "privatekey"}
	namesEnding  = []string{"token", "sessionid", "signature", "credential", "cookie", "accesskey", "authkey", "authcode", "authorizationcode", "authorization", "verifier"}
	namesExactly = []string{"pass", "pwd", "pin", "otp", "totp", "mfacode", "auth", "sig", "hmac", "sid", "sessid", "phpsessid", "connectsid", "session", "jwt", "bearer", "devicecode", "assertion", "clientassertion", "samlresponse", "oobcode", "cvv", "cvc"}
	// namesLastWords is SensitiveNames.LastWords (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 6): a credential's when it is the name's
	// LAST WORD — af_sig, X-Hub-Sig, x-amz-meta-af-auth-v1 — read by nameLastWord.
	namesLastWords = []string{"sig", "signature", "auth"}
)

// reach is how far the value under a name is masked: SensitiveNames.Reach.
type reach int

const (
	reachNone reach = iota
	reachValue
	reachEverything
)

// nameReach is SensitiveNames.Of.
func nameReach(name string) reach {
	read := nameRead(name)
	if read == "" {
		return reachNone
	}
	if nameIsStrong(read) || (len(read) > 1 && read[len(read)-1] == 's' && nameIsStrong(read[:len(read)-1])) {
		return reachEverything
	}
	for _, exact := range namesExactly {
		if read == exact {
			return reachValue
		}
	}
	last := nameLastWord(name)
	for _, word := range namesLastWords {
		if last == word {
			return reachValue
		}
	}
	return reachNone
}

// nameSensitive is SensitiveNames.IsSensitive.
func nameSensitive(name string) bool { return nameReach(name) != reachNone }

func nameIsStrong(read string) bool {
	for _, word := range namesWithin {
		if strings.Contains(read, word) {
			return true
		}
	}
	for _, word := range namesEnding {
		if strings.HasSuffix(read, word) {
			return true
		}
	}
	return false
}

// nameRead is SensitiveNames.Read: the last bracket of a form key, then ASCII letters and digits only, lower-cased, with
// trailing digits dropped.
func nameRead(name string) string {
	if strings.TrimFunc(name, isNetSpace) == "" {
		return ""
	}
	text := lastBracket(name)
	var kept strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'A' && c <= 'Z':
			kept.WriteByte(c + ('a' - 'A'))
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			kept.WriteByte(c)
		}
	}
	read := kept.String()
	end := len(read)
	for end > 0 && read[end-1] >= '0' && read[end-1] <= '9' {
		end--
	}
	return read[:end]
}

// nameLastWord is SensitiveNames.LastWord: the words of nameWords, a trailing version or number set aside (v1, V2, 256), in
// lower case without trailing digits. af_sig → sig; X-Hub-Sig → sig; x-amz-meta-af-auth-v1 → auth; X-Hub-Signature-256 →
// signature; signatureMethod → method; zulaessig → zulaessig. Empty when the name has no word.
func nameLastWord(name string) string {
	words := nameWords(name)
	end := len(words)
	for end > 0 && isVersionWord(words[end-1]) {
		end--
	}
	if end == 0 {
		return ""
	}
	last := strings.ToLower(words[end-1])
	cut := len(last)
	for cut > 0 && last[cut-1] >= '0' && last[cut-1] <= '9' {
		cut--
	}
	return last[:cut]
}

// nameWords is SensitiveNames.Words: a name's words, read from its last bracket for a form key — split at every byte that is
// not an ASCII letter or digit (_, -, ., a space), before a capital that follows a lower-case letter or a digit (afSig → af
// Sig), and before the last capital of a run of capitals that a lower-case letter follows (HMACSig → HMAC Sig). Digits stay
// with the letters before them (v1, sig2).
func nameWords(name string) []string {
	if strings.TrimFunc(name, isNetSpace) == "" {
		return nil
	}
	text := lastBracket(name)
	var words []string
	var current []byte
	flush := func() {
		if len(current) > 0 {
			words = append(words, string(current))
		}
		current = current[:0]
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !isASCIILetterOrDigit(c) {
			flush()
			continue
		}
		if len(current) > 0 && isASCIIUpper(c) {
			before := current[len(current)-1]
			lowerNext := i+1 < len(text) && isASCIILower(text[i+1])
			if isASCIILower(before) || isASCIIDigit(before) || (isASCIIUpper(before) && lowerNext) {
				flush()
			}
		}
		current = append(current, c)
	}
	flush()
	return words
}

// isVersionWord: a version or a number on its own — v1, V2, 256.
func isVersionWord(word string) bool {
	digits := word
	if len(word) > 1 && (word[0] == 'v' || word[0] == 'V') {
		digits = word[1:]
	}
	return digits != "" && allDigits(digits)
}

func isASCIIUpper(c byte) bool         { return c >= 'A' && c <= 'Z' }
func isASCIILower(c byte) bool         { return c >= 'a' && c <= 'z' }
func isASCIIDigit(c byte) bool         { return c >= '0' && c <= '9' }
func isASCIILetterOrDigit(c byte) bool { return isASCIIUpper(c) || isASCIILower(c) || isASCIIDigit(c) }

// lastBracket is SensitiveNames.LastBracket: user[password] → password; data[0][token] → token; password[] → password.
func lastBracket(name string) string {
	open := strings.IndexByte(name, '[')
	if open < 0 {
		return name
	}
	last, found := "", false
	at := open
	for at >= 0 && at < len(name) {
		closeAt := strings.IndexByte(name[at+1:], ']')
		if closeAt < 0 {
			break
		}
		closeAt += at + 1
		inside := name[at+1 : closeAt]
		if inside != "" && !allDigits(inside) {
			last, found = inside, true
		}
		next := strings.IndexByte(name[closeAt+1:], '[')
		if next < 0 {
			break
		}
		at = closeAt + 1 + next
	}
	if found {
		return last
	}
	return name[:open]
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isNetSpace is .NET's char.IsWhiteSpace: the separators (Unicode category Z) and \t \n \v \f \r and U+0085.
func isNetSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', 0x85:
		return true
	}
	return unicode.Is(unicode.Z, r)
}

// ── headers ──────────────────────────────────────────────────────────────────────────────────────────────────

var (
	alwaysSensitiveHeaders = []string{"authorization", "proxy-authorization", "cookie", "set-cookie"}
	sensitiveNameFragments = []string{"token", "secret", "api-key", "apikey", "api_key"}
)

// sensitiveHeader is HarRedactor.IsSensitiveHeader.
func sensitiveHeader(name string) bool {
	lower := strings.ToLower(name)
	for _, always := range alwaysSensitiveHeaders {
		if lower == always {
			return true
		}
	}
	for _, fragment := range sensitiveNameFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return nameSensitive(name)
}

// maskHeaders masks every value: a sensitive header's whole, one that holds an address as an address, any other's by
// the text rule.
func maskHeaders(headers []Header) []Header {
	out := make([]Header, len(headers))
	for i, h := range headers {
		if sensitiveHeader(h.Name) {
			out[i] = Header{Name: h.Name, Value: Mask}
		} else if holdsAnAddress(h.Name) {
			out[i] = Header{Name: h.Name, Value: maskAddress(h.Value)}
		} else {
			out[i] = Header{Name: h.Name, Value: maskText(h.Value)}
		}
	}
	return out
}

// ── the shapes that announce a credential (HarRedactor.CredentialFormats, JWT, Bearer, Basic) ─────────────────

var (
	jwtPattern    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	bearerPattern = regexp.MustCompile(`(?i)Bearer[` + netSpace + `]+[A-Za-z0-9\-._~+/]+=*`)
	basicPattern  = regexp.MustCompile(`(?i)\bBasic[` + netSpace + `]+[A-Za-z0-9+/]{16,}={0,2}`)

	// credentialFormats is HarRedactor.CredentialFormats, issuer by issuer and in its order.
	credentialFormats = []*regexp.Regexp{
		regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),                                                         // Google API key
		regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`),                                                      // AWS access key id
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),                                                     // GitHub token
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),                                                   // GitHub fine-grained token
		regexp.MustCompile(`glpat-[0-9A-Za-z_\-]{20,}`),                                                      // GitLab personal access token
		regexp.MustCompile(`xox[baprs]-[0-9A-Za-z\-]{10,}`),                                                  // Slack token
		regexp.MustCompile(`[sr]k_(?:live|test)_[0-9A-Za-z]{16,}`),                                           // Stripe secret key
		regexp.MustCompile(`\bsk-(?:ant-)?[0-9A-Za-z_\-]{20,}`),                                              // Anthropic/OpenAI key
		regexp.MustCompile(`\bSG\.[0-9A-Za-z_\-]{16,}\.[0-9A-Za-z_\-]{16,}`),                                 // SendGrid key
		regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END[A-Z ]*PRIVATE KEY-----|$)`), // PEM private key
	}

	// secretReference is the runner's SecretReferences.AnyReference: a `{{secret:NAME}}` reference, masked wherever it stands.
	secretReference = regexp.MustCompile(`\{\{[` + netSpace + `]*secret:[^{}]+?[` + netSpace + `]*\}\}`)
)

// applyHeuristics is HarRedactor.ApplyHeuristics.
func applyHeuristics(input string) string {
	if input == "" {
		return input
	}
	s := jwtPattern.ReplaceAllLiteralString(input, Mask)
	s = bearerPattern.ReplaceAllLiteralString(s, "Bearer "+Mask)
	s = basicPattern.ReplaceAllLiteralString(s, "Basic "+Mask)
	for _, format := range credentialFormats {
		s = format.ReplaceAllLiteralString(s, Mask)
	}
	return maskJSONText(s)
}

// ── pairs, forms, multipart, and "name": value in text (HarRedactor.Fields) ────────────────────────────────────

var (
	formShape         = regexp.MustCompile(`^[^` + netSpace + `=&]+=[^` + netSpace + `&]*(?:&[^` + netSpace + `=&]+=[^` + netSpace + `&]*)*&?[` + netSpace + `]*\z`)
	multipartPart     = regexp.MustCompile(`(?i)(content-disposition:[ \t]*form-data[^\r\n]*?;[ \t]*name="([^"\r\n]*)"[^\r\n]*(?:\r?\n[^\r\n]+)*\r?\n\r?\n)([\s\S]*?)(\r?\n--)`)
	jsonPairInText    = regexp.MustCompile(`"((?:[^"\\\r\n]|\\.){1,128})"[` + netSpace + `]*:[` + netSpace + `]*(?:"((?:[^"\\]|\\.)*)"|(-?\p{Nd}+(?:\.\p{Nd}+)?(?:[eE][+\-]?\p{Nd}+)?))`)
	escapedPairInText = regexp.MustCompile(`\\"([^"\\\r\n]{1,128})\\"[` + netSpace + `]*:[` + netSpace + `]*\\"([^"\\]*)\\"`)
)

// ── A PAIR'S VALUE, AS FAR AS THE PLACE IT STANDS IN SAYS IT RUNS ─────────────────────────────────────
//
// HarRedactor.MaskPairs reads every value with one grammar: it ends at white space, ;, a quote, an angle bracket or a
// backslash. That is where a word ends in free text, and it is not where a value ends in an address or a form: there a
// value runs to the next & whatever it holds, so `password=it'sX` was masked up to the apostrophe, `password='X` not at
// all, and a pair nested in another pair's value (`next=/reset?token=X`) was never read, because a value already matched
// is not matched again. Here a value is read as the place it stands in defines it, every lead is visited — nested ones
// too — and a held name's value is empty only when a separator or the end follows its `=`. Each reading masks AT LEAST
// what the runner's grammar masks, so the agent's record is never less masked than the runner's pass would leave it; the one
// table both readings are held to is a_pair_masked_whole_test.go.

// pairReading is how far a held name's value runs.
type pairReading int

const (
	// readAsText is free text — a header that holds no address, a body's last pass: a value ends where a word does, at
	// white space, &, #, ;, a double quote, an angle bracket or an escaped quote (`\"`), and a value that opens with a
	// quote runs to its closing quote.
	readAsText pairReading = iota
	// readAsForm is an x-www-form-urlencoded body: a value runs to the next &, whatever it holds, and the body's first
	// pair follows nothing.
	readAsForm
	// readAsAddress is a URL: in the query a value runs to the next & or to the fragment's #, in the fragment to the next
	// &, and a path parameter's to the next ;, /, ? or #.
	readAsAddress
)

// maskPairs is HarRedactor.MaskPairs: every name=value after ?, &, # or ; — and, with whole, a form body whose first
// pair follows nothing — masked when its name (form-decoded) is a credential's; the value read as free text, or with
// whole as a form's.
func maskPairs(text string, whole bool) string {
	if whole {
		return maskPairsAs(text, readAsForm)
	}
	return maskPairsAs(text, readAsText)
}

// maskPairsAs masks every held pair in text, its value read as reading says.
func maskPairsAs(text string, reading pairReading) string {
	if text == "" || !strings.Contains(text, "=") {
		return text
	}
	query, fragment := len(text), len(text)
	if reading == readAsAddress {
		if i := strings.IndexByte(text, '#'); i >= 0 {
			fragment = i
		}
		query = fragment // no query: the path runs to the fragment
		if i := strings.IndexByte(text[:fragment], '?'); i >= 0 {
			query = i
		}
	}
	var b strings.Builder
	copied := 0
	for at := 0; at < len(text); {
		nameStart, isLead := pairLead(text, at, reading)
		if !isLead {
			at++
			continue
		}
		nameEnd, isPair := pairName(text, nameStart)
		if !isPair {
			at++
			continue
		}
		from, to := heldValue(text, nameEnd+1, reading, query, fragment)
		if to > from && nameSensitive(formDecoded(text[nameStart:nameEnd])) {
			b.WriteString(text[copied:from])
			b.WriteString(Mask)
			copied, at = to, to
			continue
		}
		at++ // NOT past the value: a pair nested in it is read too
	}
	if copied == 0 {
		return text
	}
	b.WriteString(text[copied:])
	return b.String()
}

// pairLead says whether a pair's name may start after the byte at `at` — ?, &, # or ; (and the spaces after a ;), or,
// in a form, the very start — and where the name starts.
func pairLead(text string, at int, reading pairReading) (int, bool) {
	switch text[at] {
	case '?', '&', '#':
		return at + 1, true
	case ';':
		next := at + 1
		for next < len(text) && (text[next] == ' ' || text[next] == '\t') {
			next++
		}
		return next, true
	}
	return at, at == 0 && reading == readAsForm
}

// pairName is the end of the name that starts at start — one to 128 characters that are not white space, =, &, #, ;, ?,
// a quote, an angle bracket or a backslash — when an = follows it.
func pairName(text string, start int) (int, bool) {
	at, count := start, 0
	for at < len(text) && count <= 128 {
		r, size := utf8.DecodeRuneInString(text[at:])
		if r == '=' {
			return at, count > 0
		}
		if isNetSpace(r) || strings.ContainsRune(`&#;?"'<>\`, r) {
			return at, false
		}
		at += size
		count++
	}
	return at, false
}

// heldValue is the span a held name's value covers, read from the byte after its `=`.
func heldValue(text string, start int, reading pairReading, query, fragment int) (int, int) {
	switch reading {
	case readAsForm:
		end := indexFrom(text, start, "&")
		if end == len(text) { // a body's trailing line end is not its last value's
			end = start + len(strings.TrimRightFunc(text[start:end], isNetSpace))
		}
		return start, end
	case readAsAddress:
		switch {
		case start <= query: // a path parameter
			return start, min(indexFrom(text, start, ";/?#"), query)
		case start > fragment:
			return start, indexFrom(text, start, "&")
		default:
			return start, min(indexFrom(text, start, "&"), fragment)
		}
	}
	if start == len(text) {
		return start, start
	}
	switch c := text[start]; {
	case c == '"' || c == '\'':
		if close := strings.IndexByte(text[start+1:], c); close >= 0 {
			return start + 1, start + 1 + close
		}
		return start + 1, runEnd(text, start+1)
	case c == '\\' && start+1 < len(text) && (text[start+1] == '"' || text[start+1] == '\''):
		if close := strings.Index(text[start+2:], text[start:start+2]); close >= 0 {
			return start + 2, start + 2 + close
		}
		return start + 2, runEnd(text, start+2)
	case c == '<' || c == '>':
		return start, runEnd(text, start)
	}
	return start, wordEnd(text, start)
}

// runEnd is where a run of text ends: at white space, &, #, ; or the end.
func runEnd(text string, at int) int {
	for at < len(text) {
		r, size := utf8.DecodeRuneInString(text[at:])
		if isNetSpace(r) || r == '&' || r == '#' || r == ';' {
			return at
		}
		at += size
	}
	return at
}

// wordEnd is where a value in free text ends: where a run does, or at a double quote, an angle bracket or an escaped
// double quote — any other backslash escape is part of the value.
func wordEnd(text string, at int) int {
	for at < len(text) {
		r, size := utf8.DecodeRuneInString(text[at:])
		switch {
		case isNetSpace(r) || r == '&' || r == '#' || r == ';' || r == '"' || r == '<' || r == '>':
			return at
		case r == '\\' && at+1 < len(text):
			if text[at+1] == '"' {
				return at
			}
			at += 2
			continue
		}
		at += size
	}
	return at
}

// indexFrom is the first byte at or after start that is one of chars, or the end.
func indexFrom(text string, start int, chars string) int {
	if i := strings.IndexAny(text[start:], chars); i >= 0 {
		return start + i
	}
	return len(text)
}

// readingOf is how a JSON string that holds pairs is read: as an address when it is one, as a form when it starts with
// a pair, and otherwise as free text.
func readingOf(s string) pairReading {
	switch {
	case addressShape.MatchString(s):
		return readAsAddress
	case formShape.MatchString(s):
		return readAsForm
	}
	if end, isPair := pairName(s, 0); isPair && end > 0 {
		return readAsForm
	}
	return readAsText
}

// addressShape is a string that is an address: a scheme and //, or a path or a query on its own.
var addressShape = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.\-]*://|/|\?)`)

// addressHeaders hold an address, and their values are read as one.
var addressHeaders = []string{"location", "content-location", "referer", "origin"}

func holdsAnAddress(header string) bool {
	lower := strings.ToLower(header)
	for _, name := range addressHeaders {
		if lower == name {
			return true
		}
	}
	return false
}

// maskMultipart is HarRedactor.MaskMultipart: a part masked by its name. RE2 has no look-ahead, so the boundary the part
// ends at is matched and put back as it was.
func maskMultipart(text string) string {
	if !containsFold(text, "form-data") {
		return text
	}
	return replaceMatches(multipartPart, text, func(s string, m []int) string {
		if nameSensitive(s[m[4]:m[5]]) {
			return s[m[2]:m[3]] + Mask + s[m[8]:m[9]]
		}
		return s[m[0]:m[1]]
	})
}

// maskJSONText is HarRedactor.MaskJsonText: a `"name": value` pair anywhere in text, and the same pair escaped inside a
// JSON string that holds JSON.
func maskJSONText(text string) string {
	if text == "" || !strings.Contains(text, ":") {
		return text
	}
	masked := replaceMatches(jsonPairInText, text, func(s string, m []int) string {
		if !nameSensitive(jsonDecoded(s[m[2]:m[3]])) {
			return s[m[0]:m[1]]
		}
		if m[4] >= 0 { // a string value: its contents become the mask, its quotes stay
			return s[m[0]:m[4]] + Mask + s[m[5]:m[1]]
		}
		return s[m[0]:m[6]] + maskedJSONString + s[m[7]:m[1]]
	})
	return replaceMatches(escapedPairInText, masked, func(s string, m []int) string {
		if nameSensitive(s[m[2]:m[3]]) {
			return s[m[0]:m[4]] + Mask + s[m[5]:m[1]]
		}
		return s[m[0]:m[1]]
	})
}

// isAForm is HarRedactor.IsAForm.
func isAForm(mimeType, text string) bool {
	if containsFold(mimeType, "x-www-form-urlencoded") {
		return true
	}
	return text != "" && text[0] != '{' && text[0] != '[' && formShape.MatchString(text)
}

// ── the composed maskers ─────────────────────────────────────────────────────────────────────────────────────

// maskText is HarRedactor.MaskText for a run whose saved secrets this agent does not hold: pairs by name, any
// `{{secret:NAME}}` reference, the published shapes and `"name": value` pairs, and no NUL.
func maskText(value string) string { return maskTextAs(value, readAsText) }

func maskTextAs(value string, reading pairReading) string {
	if value == "" {
		return value
	}
	masked := maskPairsAs(value, reading)
	masked = secretReference.ReplaceAllLiteralString(masked, Mask)
	return stripNul(applyHeuristics(masked))
}

// maskAddress is HarRedactor.MaskAddress without saved secrets: the address through the text rule, query, fragment and
// path parameters by name included — each value read as URL syntax defines it.
func maskAddress(url string) string {
	if url == "" {
		return url
	}
	return maskTextAs(url, readAsAddress)
}

// maskBodyText is HarRedactor.MaskBodyText for a body read as text: its JSON fields, its form fields or multipart parts
// when it is one, then the text rule. (A body this agent cannot read as text is never recorded, so the base64 branch
// the runner keeps for a node's binary bodies has nothing to do here.)
func maskBodyText(preview, mimeType string) string {
	masked := maskJSONFields(preview)
	if isAForm(mimeType, masked) {
		masked = maskPairs(masked, true)
	}
	if containsFold(mimeType, "multipart/") || containsFold(masked, "form-data") {
		masked = maskMultipart(masked)
	}
	return maskText(masked)
}

// maskedWithin is HarRedactor.MaskedWithin: a string's own content masked — JSON inside it, or pairs — reporting
// whether anything changed.
func maskedWithin(value string) (string, bool) {
	within := value
	trimmed := strings.TrimLeftFunc(within, isNetSpace)
	if trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') {
		within = maskJSONFields(within)
	}
	if strings.Contains(within, "=") {
		within = maskPairsAs(within, readingOf(within))
	}
	return within, within != value
}

// ── small helpers ────────────────────────────────────────────────────────────────────────────────────────────

// replaceMatches is .NET's Regex.Replace with an evaluator: every non-overlapping match, left to right, replaced by what
// the function returns for it (given the whole subject and the match's submatch indexes).
func replaceMatches(re *regexp.Regexp, s string, replace func(s string, m []int) string) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	at := 0
	for _, m := range matches {
		b.WriteString(s[at:m[0]])
		b.WriteString(replace(s, m))
		at = m[1]
	}
	b.WriteString(s[at:])
	return b.String()
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// stripNul is HarRedactor.StripNul: a NUL never reaches the record.
func stripNul(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, string(rune(0)), "")
}

// formDecoded is HarRedactor.FormDecoded: `+` is a space, then percent-decoding — leniently, as .NET's
// Uri.UnescapeDataString does: a `%` that does not start a valid escape stays as written.
func formDecoded(text string) string { return percentDecoded(strings.ReplaceAll(text, "+", " ")) }

// percentDecoded decodes every run of %XX escapes as UTF-8; an escaped byte that is not part of valid UTF-8 stays as the
// three characters it was written as — what .NET's Uri.UnescapeDataString does, so a name reads the same on both sides.
func percentDecoded(text string) string {
	if !strings.Contains(text, "%") {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if !isEscape(text, i) {
			b.WriteByte(text[i])
			i++
			continue
		}
		start := i
		var run []byte
		for isEscape(text, i) {
			run = append(run, unhex(text[i+1])<<4|unhex(text[i+2]))
			i += 3
		}
		for at := 0; at < len(run); {
			r, size := utf8.DecodeRune(run[at:])
			if r == utf8.RuneError && size <= 1 {
				b.WriteString(text[start+3*at : start+3*at+3])
				at++
				continue
			}
			b.WriteRune(r)
			at += size
		}
	}
	return b.String()
}

func isEscape(text string, i int) bool {
	return i+2 < len(text) && text[i] == '%' && isHex(text[i+1]) && isHex(text[i+2])
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// jsonDecoded is HarRedactor.JsonDecoded: a JSON name with its escapes read; as written when they cannot be.
func jsonDecoded(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	if decoded, ok := unescapeJSONString(raw); ok {
		return decoded
	}
	return raw
}

// quoteJSON writes a string the way the runner re-writes one it changed: escaping only what JSON requires.
func quoteJSON(s string) string {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		return maskedJSONString
	}
	return strings.TrimSuffix(b.String(), "\n")
}
