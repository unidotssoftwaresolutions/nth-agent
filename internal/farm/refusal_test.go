package farm_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// AN_AGENT_THAT_GUESSES_WHY_IT_WAS_REFUSED — the farm's side of it: a 401's body is read, a little of it, and what
// the farm said leaves post() on a RefusedError, made safe for the terminal it is headed to. Every message here says
// what the person running the agent would have seen.

const secretToken = "sk-agent-VERYSECRET-0123456789"

// The farm's own sentence for a token that reached its end (the farm's ApiKeyAuthMiddleware.ExpiredAgentTokenMessage).
const expiredSentence = "This agent token has expired. Create a new one in Settings → Agent tokens and start the agent with it."

// refusedBy runs one registration against a farm that answers 401 with body, and returns what came out of post().
func refusedBy(t *testing.T, body string) (farm.CallStatus, *farm.RefusedError) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client := &farm.Client{BaseURL: server.URL, Token: secretToken, HTTP: server.Client()}
	_, status, err := client.Register(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "host", farm.Report{})
	var refused *farm.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("a 401 must come out of post() as a *farm.RefusedError, so the loop can read what the farm said — got %T: %v", err, err)
	}
	return status, refused
}

func envelopeSaying(message string) string {
	return `{"success":false,"data":null,"message":` + jsonString(message) + `,"errors":["x"]}`
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e):
			// JSON escapes these as \u00XX — the way a server would send them.
			const hex = "0123456789abcdef"
			b.WriteString(`\` + "u" + string([]byte{hex[r>>12&0xf], hex[r>>8&0xf], hex[r>>4&0xf], hex[r&0xf]}))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// SEAL 6 — THE STATUS IS UNCHANGED, AND THE FARM'S WORDS COME OUT WITH IT.
func TestARefusalCarriesTheFarmsOwnWordsAndIsStillTerminal(t *testing.T) {
	status, refused := refusedBy(t, envelopeSaying(expiredSentence))
	if status != farm.CallUnauthorized {
		t.Fatalf("a 401 came out as %v, not CallUnauthorized — the loop would treat a refused token as something to retry, and a person's laptop would hammer the farm with a credential that will never work again", status)
	}
	if refused.Reason != expiredSentence {
		t.Fatalf("the farm said %q and the agent carried %q — a person whose token simply ended would be told something else", expiredSentence, refused.Reason)
	}
}

// SEAL 5 — THE READ IS BOUNDED.
func TestTheReadOfARefusalIsBounded(t *testing.T) {
	body := &countingBody{size: 64 << 20} // 64 MiB of a server that will not stop talking
	client := &farm.Client{BaseURL: "http://127.0.0.1:1", Token: secretToken, HTTP: &http.Client{
		Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: body, Header: http.Header{}}, nil
		}),
	}}
	_, status, err := client.Register(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "host", farm.Report{})

	if body.read > farm.RefusalBodyLimit {
		t.Fatalf("the agent read %d bytes of a refusal's body (the cap is %d) — a server answering a refused token with an endless body would hold the person's agent reading it, and the memory it fills", body.read, farm.RefusalBodyLimit)
	}
	if status != farm.CallUnauthorized {
		t.Fatalf("an oversized refusal came out as %v — it is still a refused token, and still terminal", status)
	}
	var refused *farm.RefusedError
	if !errors.As(err, &refused) || refused.Reason != "" {
		t.Fatalf("a body cut at the cap does not parse, so there is no reason to print — got %v", err)
	}
}

// SEAL 4 — THE TERMINAL IS SAFE: escape sequences, control characters and direction overrides from the farm's
// sentence never reach it, and the words around them do.
func TestTheFarmsWordsCannotRewriteThePersonsTerminal(t *testing.T) {
	c1CSI := string(rune(0x9b))
	rightToLeft := string(rune(0x202e))
	hostile := "\x1b[2J\x1b[H" + // clear the screen, cursor home
		"\x1b]0;owned\x07" + // set the window title
		"\x1b[31mToken\x1b[0m " + // colour
		c1CSI + "1mrefused" + // the one-character form of the same introducer
		"\r\nregistered as node 11111111\x00\x08\x7f " + // a forged next line, NUL, backspace, DEL
		rightToLeft + "reversed"

	_, refused := refusedBy(t, envelopeSaying(hostile))

	var leaked []string
	for _, r := range refused.Reason {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x061c || r == 0x200e || r == 0x200f ||
			(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			leaked = append(leaked, "U+"+hexOf(r))
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("the farm's sentence reached the terminal carrying %v — a server answering a refused token could clear the person's screen, retitle their window, or forge a line of the agent's own output", leaked)
	}
	for _, word := range []string{"Token", "refused", "registered as node 11111111", "reversed"} {
		if !strings.Contains(refused.Reason, word) {
			t.Fatalf("stripping the controls also took the words %q with them — got %q", word, refused.Reason)
		}
	}
	if strings.Contains(refused.Reason, "[2J") || strings.Contains(refused.Reason, "31m") || strings.Contains(refused.Reason, "owned") {
		t.Fatalf("an escape sequence's parameters survived as visible debris: %q", refused.Reason)
	}
}

// SEAL 4, the other half — an oversized sentence is cut to the cap.
func TestAnOversizedReasonIsCutToTheCap(t *testing.T) {
	_, refused := refusedBy(t, envelopeSaying(strings.Repeat("a", 2000)))
	if got := utf8.RuneCountInString(refused.Reason); got != farm.ReasonLimit {
		t.Fatalf("a 2000-character reason reached the terminal as %d characters (the cap is %d) — a server could fill the person's terminal with one refusal", got, farm.ReasonLimit)
	}
	if !strings.HasSuffix(refused.Reason, "…") {
		t.Fatalf("a cut reason must say it was cut — got one ending %q", refused.Reason[len(refused.Reason)-5:])
	}
}

// The README promises the token is never written into any output. A server that echoes it back into its refusal —
// whole, or split by a character meant to slip it past a search — must not be how that promise breaks.
//
// ⚠️ THE LEAK IS CHECKED AS IT IS READ, NOT AS IT IS STORED. A token split by a zero-width space does not contain the
// string "VERYSECRET", so a check on the stored reason passes — while the terminal draws nothing for the zero-width
// space and the person reads the whole token. So the reason is first reduced to what is SEEN (every format character,
// Cf, taken out) and that is what is searched.
//
// NOT COVERED, and said so: an invisible character outside Cc and Cf still splits the token past the redaction — a
// combining grapheme joiner or a variation selector (both Mn), a Hangul filler (Lo). Those carry meaning in legitimate
// text, so removing them is not the fix; matching the token while ignoring Unicode's default-ignorable code points is.
func TestAServerCannotMakeTheAgentPrintItsOwnToken(t *testing.T) {
	zeroWidthSpace := string(rune(0x200b)) // written as a number: an escape typed here has arrived as the raw character before
	for name, message := range map[string]string{
		"echoed":                    "token " + secretToken + " is not accepted",
		"split by control":          "token sk-agent-VERY\x1b[0mSECRET-0123456789 is not accepted",
		"split by zero-width space": "token sk-agent-VERY" + zeroWidthSpace + "SECRET-0123456789 is not accepted",
	} {
		_, refused := refusedBy(t, envelopeSaying(message))
		seen := strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, refused.Reason)
		if strings.Contains(seen, "VERYSECRET") {
			t.Fatalf("%s: the farm's sentence carried the agent's own token onto the person's terminal — stored as %q, read as %q", name, refused.Reason, seen)
		}
		if !strings.Contains(refused.Reason, "«token»") {
			t.Fatalf("%s: the token was removed without saying so — got %q", name, refused.Reason)
		}
	}
}

// SEAL 3, at the source — a refusal with nothing readable in it carries no reason, and still is a refusal.
func TestARefusalWithNoReadableReasonCarriesNone(t *testing.T) {
	for name, body := range map[string]string{
		"empty body":       "",
		"not JSON":         "<html><body>401 Unauthorized</body></html>",
		"no message":       `{"success":false,"data":null}`,
		"empty once clean": envelopeSaying("\x1b[0m\x00  \t"),
	} {
		status, refused := refusedBy(t, body)
		if status != farm.CallUnauthorized {
			t.Fatalf("%s: came out as %v — a refusal without a reason is still a refusal", name, status)
		}
		if refused.Reason != "" {
			t.Fatalf("%s: carried the reason %q out of a body that gave none — the agent would print it as if the farm had said it", name, refused.Reason)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// countingBody serves `size` bytes and counts how many were asked for.
type countingBody struct{ size, read int }

func (b *countingBody) Read(p []byte) (int, error) {
	if b.read >= b.size {
		return 0, io.EOF
	}
	n := len(p)
	if n > b.size-b.read {
		n = b.size - b.read
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	b.read += n
	return n, nil
}

func (b *countingBody) Close() error { return nil }

func hexOf(r rune) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{hex[r>>12&0xf], hex[r>>8&0xf], hex[r>>4&0xf], hex[r&0xf]})
}
