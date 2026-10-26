package browserstack

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// ── A HUB ANSWER NEVER CARRIES THE CUSTOMER'S KEY TO THE FARM ──────────────────────────────────────────
//
// The session create is the one request into which this agent merges the access key, and BrowserStack's answer to it —
// and to every later frame of a cloud session — is relayed to the farm, which relays it to the lease holder. Whether the
// hub ever echoes the key (in value.capabilities.bstack:options, in an echo of the requested capabilities, in an error
// quoting the request) is BrowserStack's behaviour, and no successful create answer has been recorded here. So "the key
// is never sent to us" does not rest on it: every hub answer has the key replaced, in every spelling an answer could
// carry it in, before it is relayed. The answer keeps its status, its Content-Type and every other byte, so routing,
// the farm's read of the session id and the driver's own error handling are unchanged.
//
// THE USER NAME IS NOT A SECRET (README), and is left wherever it stands — except as the account's name beside the key,
// a JSON field naming a user whose value is exactly the configured name, which with the key is the credential.

// KeyMask is what the customer's access key becomes in a hub answer the agent relays — the capture's own mask
// (netcap.Mask), character for character, so a lease holder reads one placeholder for a hidden value wherever it is.
const KeyMask = "«redacted»"

// shortestMiddle is the shortest piece of the key's base64, taken from the middle of a longer run, that is replaced: a
// shorter one could match an unrelated run of base64 (a screenshot) and is not, because it cannot spell the key either.
const shortestMiddle = 8

// ScrubAnswer is a hub answer with the configured access key replaced by KeyMask wherever it appears: as written, inside
// any JSON string whatever its escapes, percent-encoded, in hex, and in base64 — alone, as the user:key of a basic
// credential, and in the middle of a longer base64 run. An answer without the key comes back as it was, byte for byte.
func ScrubAnswer(answer []byte, creds Credentials) []byte {
	if creds.Key == "" || len(answer) == 0 {
		return answer
	}
	spellings := keySpellings(creds)
	scrubbed := scrubJSONStrings(answer, creds, spellings)
	for _, spelling := range spellings {
		scrubbed = bytes.ReplaceAll(scrubbed, []byte(spelling), []byte(KeyMask))
	}
	return scrubbed
}

// ScrubHeader is a header value of a hub answer with the key replaced, as ScrubAnswer replaces it in the body.
func ScrubHeader(value string, creds Credentials) string {
	return string(ScrubAnswer([]byte(value), creds))
}

// keySpellings is every byte sequence that spells the key in an answer, longest first, so a longer spelling that holds a
// shorter one is replaced whole.
func keySpellings(creds Credentials) []string {
	secret := []byte(creds.Key)
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(creds.Key)
	add(url.QueryEscape(creds.Key))
	add(url.PathEscape(creds.Key))
	every, everyLower := "", ""
	for _, b := range secret {
		every += "%" + strings.ToUpper(hex.EncodeToString([]byte{b}))
		everyLower += "%" + hex.EncodeToString([]byte{b})
	}
	add(every)
	add(everyLower)
	add(hex.EncodeToString(secret))
	add(strings.ToUpper(hex.EncodeToString(secret)))
	encodings := []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding}
	for _, plain := range [][]byte{secret, []byte(creds.User + ":" + creds.Key)} {
		for _, encoding := range encodings {
			add(encoding.EncodeToString(plain))
		}
	}
	// THE KEY IN THE MIDDLE OF A LONGER BASE64 RUN: what is the same whichever byte of a group of three it starts at.
	for offset := 0; offset < 3; offset++ {
		run := append(make([]byte, offset), secret...)
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
			encoded := encoding.EncodeToString(run)
			start, end := 0, len(encoded)
			if offset > 0 {
				start = 4 // the first group mixes the bytes before the key with its own
			}
			if len(run)%3 != 0 {
				end -= 4 // the last group mixes the key's last bytes with what follows it
			}
			if end-start >= shortestMiddle {
				add(encoded[start:end])
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// scrubJSONStrings replaces, inside every JSON string in answer, the key however the string escapes it — and, under a
// name that names a user, a value that is exactly the configured user name. Each string that changes is written again,
// escaping only what JSON requires; every other byte stays where it was. Text that is not JSON has no string to read and
// comes back as it was.
func scrubJSONStrings(answer []byte, creds Credentials, spellings []string) []byte {
	var out []byte
	copied := 0
	lastName := ""
	for at := 0; at < len(answer); at++ {
		if answer[at] != '"' {
			continue
		}
		end := stringEnd(answer, at)
		if end < 0 {
			break
		}
		raw := answer[at : end+1]
		next := end + 1
		for next < len(answer) && (answer[next] == ' ' || answer[next] == '\t' || answer[next] == '\n' || answer[next] == '\r') {
			next++
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || !utf8.ValidString(text) {
			at = end
			continue
		}
		isName := next < len(answer) && answer[next] == ':'
		replaced := text
		for _, spelling := range spellings {
			replaced = strings.ReplaceAll(replaced, spelling, KeyMask)
		}
		if !isName && creds.User != "" && text == creds.User && namesAUser(lastName) {
			replaced = KeyMask
		}
		if replaced != text {
			out = append(out, answer[copied:at]...)
			out = append(out, quoted(replaced)...)
			copied = end + 1
		}
		if isName {
			lastName = text
		} else {
			lastName = ""
		}
		at = end
	}
	if out == nil {
		return answer
	}
	return append(out, answer[copied:]...)
}

// stringEnd is the index of the quote that closes the JSON string opened at start, or -1.
func stringEnd(s []byte, start int) int {
	for at := start + 1; at < len(s); at++ {
		switch s[at] {
		case '\\':
			at++
		case '"':
			return at
		}
	}
	return -1
}

// namesAUser: userName, user, username, browserstack.user, browserstack.userName — the names an account's user stands under.
func namesAUser(name string) bool {
	var kept strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			kept.WriteRune(r)
		}
	}
	switch strings.TrimPrefix(kept.String(), "browserstack") {
	case "user", "username", "userid":
		return true
	}
	return false
}

func quoted(s string) []byte {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		return []byte(`"` + KeyMask + `"`)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
