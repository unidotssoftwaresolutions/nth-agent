package farm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A REFUSAL CARRIES THE FARM'S OWN WORDS (AN_AGENT_THAT_GUESSES_WHY_IT_WAS_REFUSED, and every other answer since
// AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO: see AnswerError).
//
// The farm knows why it refused this agent; the agent knows what the person should do next. Until this file existed
// the agent threw the farm's sentence away — a 401's body was never read — and printed a cause of its own: "it may
// have been revoked". Agent tokens can END now, so the commonest refusal is a token that simply lived out the week it
// was given, and a person told "revoked" goes looking for whoever revoked it and for the leak that would explain why.
//
// So a 401's body is read — a little of it, never the whole of whatever arrives — and the farm's `message` travels
// out beside CallUnauthorized on a RefusedError. The status does not change: a refused credential is still terminal
// and still not retried.
//
// ⚠️ THE FARM'S SENTENCE IS UNTRUSTED TEXT BOUND FOR A TERMINAL. `--farm` is a URL the customer types, so the server
// answering is not necessarily ours. SafeForTerminal is the one door it passes through: escape sequences and control
// characters out (a server must not be able to rewrite the person's terminal by refusing them), the agent's own
// token redacted (the README promises it is never written into any output, and a server that echoes it back must not
// be the way that promise breaks), and a length cap.

// refusalBodyLimit is the most of a refused call's body this agent reads. The farm's refusal is its standard envelope
// around one sentence — about two hundred bytes — so four kilobytes is twenty times the real thing and nothing like
// the unbounded stream a hostile or broken server could answer with. A body cut at the limit simply does not parse,
// which reads as "no reason given": safe, and said as such.
const refusalBodyLimit = 4 << 10

// reasonLimit is the most of the farm's sentence that reaches a terminal, in characters. The longest sentence the farm
// has is about a hundred; a reason longer than three hundred is not a reason, it is a page.
const reasonLimit = 300

// RefusedError is what a 401 carries out of post(). The status stays CallUnauthorized; this adds what the farm SAID.
type RefusedError struct {
	// Reason is the farm's own sentence for the refusal, already made safe to print — "" when the farm gave none this
	// agent could read (no body, not JSON, no message, or nothing left after sanitising).
	Reason string
}

func (e *RefusedError) Error() string {
	if e.Reason == "" {
		return "the farm did not accept the credential (401)"
	}
	return "the farm did not accept the credential (401): " + e.Reason
}

// AnswerError is what every other answer the farm did not take a call with carries out of post() — a 404, a 429, a 503,
// and the statuses this client has no name for (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO). The CallStatus beside it
// decides what the loop does; this says what the farm said.
//
// ONE TYPE FOR ALL OF THEM, AND NOT RefusedError'S: a 401 is the one terminal refusal, its words are about a credential and
// the refusal census holds them, and it does not change. Every other answer is worded the same way — its status, and the
// farm's sentence or the fact that it gave none — so one carrier, with the status in it, is one shape for the loop, the
// goodbye and the tunnel to read.
type AnswerError struct {
	// Status is the HTTP status the farm answered with.
	Status int
	// Reason is the farm's own sentence, already made safe to print — "" when it gave none this agent could read: nginx's
	// 413, the farm's envelope with no message, any other body that is not the farm's envelope. (A 502, 503 or 504 with no
	// envelope is not here at all: see GatewayError.)
	Reason string
}

func (e *AnswerError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("the farm answered %d and gave no reason this agent could read", e.Status)
	}
	return fmt.Sprintf("the farm answered %d: %s", e.Status, e.Reason)
}

// UnusableAnswerError is a 2xx this agent could not use (NTH_AGENT_0_4_3 item 5): the farm took the call and answered, and
// the answer could not be read, was larger than AnswerBodyLimit, was not the farm's envelope, or had another shape. It is
// not an answer the farm did not take a call with, so WhatTheFarmSaid does not read it.
type UnusableAnswerError struct {
	// Status is the 2xx the farm answered with.
	Status int
	// What is what was wrong with the answer, as the end of "the farm's answer …": "was not readable".
	What string
	// Err is the reader's own error, when there is one.
	Err error
}

func (e *UnusableAnswerError) Error() string {
	if e.Err == nil {
		return "the farm's answer " + e.What + ", so it was not used"
	}
	return "the farm's answer " + e.What + ": " + e.Err.Error()
}

func (e *UnusableAnswerError) Unwrap() error { return e.Err }

// GatewayError is a 502, 503 or 504 that is not in the farm's envelope (NTH_AGENT_0_4_4 item 1): the farm's address — nginx
// in front of the farm's API — answering in the farm's place, while the farm restarts or does not answer in time. The farm was
// not reached. It took nothing and refused nothing, so WhatTheFarmSaid does not read it, and every reader says what it says
// when the farm cannot be reached. The CallStatus beside it is the one that status always had (CallTransport, and CallBusy
// for a 503), so the backoff and the tunnel's evidence do not change.
type GatewayError struct {
	// Status is the HTTP status the farm's address answered with.
	Status int
}

func (e *GatewayError) Error() string {
	return fmt.Sprintf("the farm's address answered %d, not the farm", e.Status)
}

// ReadAnswerBody is at most refusalBodyLimit bytes of an answer the farm did not take a call with, kept so they can be read
// twice: for the farm's sentence (readRefusal, RefusalReason) and for whether they are the farm's envelope at all (GatewayOr).
// It never fails: what could not be read is not there.
func ReadAnswerBody(body io.Reader) []byte {
	if body == nil {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(body, refusalBodyLimit))
	return raw
}

// GatewayOr is a *GatewayError when status is 502, 503 or 504 and body is not the farm's envelope, and answered — the farm's
// own answer — otherwise. Every other status is the farm's answer whatever its body (nginx's 413 included, as before).
//
// THE FARM'S ENVELOPE is the farm's ApiResponse (its error envelope), which every refusal of the farm's is
// written in: a JSON object whose `success` is a boolean, a member ASP.NET always writes. A body cut at refusalBodyLimit does
// not parse, so a 502, 503 or 504 longer than that reads as the gateway's; the farm's refusals are a few hundred bytes.
func GatewayOr(status int, body []byte, answered error) error {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		var wrapped struct {
			Success *bool `json:"success"`
		}
		if json.Unmarshal(body, &wrapped) != nil || wrapped.Success == nil {
			return &GatewayError{Status: status}
		}
	}
	return answered
}

// WhatTheFarmSaid is the status and the sentence of an answer the farm did not take a call with, from either carrier —
// ok false when err is no answer at all (a transport failure, a timeout).
func WhatTheFarmSaid(err error) (status int, reason string, ok bool) {
	var refused *RefusedError
	if errors.As(err, &refused) {
		return http.StatusUnauthorized, refused.Reason, true
	}
	var answered *AnswerError
	if errors.As(err, &answered) {
		return answered.Status, answered.Reason, true
	}
	return 0, "", false
}

// RefusalReason is readRefusal for a refused answer read outside post() — the drive tunnel's refused upgrade, whose body
// the WebSocket library keeps a kilobyte of. The same bound, the same door, the token never printed.
func RefusalReason(body io.Reader, token string) string {
	if body == nil {
		return ""
	}
	return readRefusal(body, token)
}

// readRefusal reads at most refusalBodyLimit bytes of a refused call's body and returns the farm's message, made safe
// for a terminal — or "" when there is none. It never fails: a refusal with an unreadable body is still a refusal.
func readRefusal(body io.Reader, token string) string {
	raw, err := io.ReadAll(io.LimitReader(body, refusalBodyLimit))
	if err != nil && len(raw) == 0 {
		return ""
	}
	var wrapped envelope[json.RawMessage]
	if json.Unmarshal(raw, &wrapped) != nil || wrapped.Message == nil {
		return ""
	}
	return SafeForTerminal(*wrapped.Message, token)
}

// escapeSequence matches a terminal escape as a WHOLE unit, so its parameters do not survive as visible debris:
// CSI (`ESC [ … final`), OSC (`ESC ] … BEL` or `ESC ] … ESC \`, or unterminated to the end), the same two introduced by
// their single-character C1 forms (U+009B, U+009D), and any other two-byte `ESC x`.
var escapeSequence = regexp.MustCompile(
	`\x1b\[[0-?]*[ -/]*[@-~]` +
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?` +
		`|\x{9b}[0-?]*[ -/]*[@-~]` +
		`|\x{9d}[^\x07\x1b]*(?:\x07|\x1b\\)?` +
		`|\x1b[ -/]*[0-~]`,
)

// SafeForTerminal makes text from a server fit to print on a person's terminal: invalid UTF-8 dropped, escape
// sequences removed whole, line breaks and tabs folded to one space, every other control character (Cc: C0, DEL, C1)
// and every FORMAT character (Cf) removed, the agent's own token redacted wherever it appears, and the result cut to
// reasonLimit characters.
//
// ⚠️ ALL OF Cf, NOT A HAND-LISTED PART OF IT. The first version removed Cc and a table of bidirectional overrides —
// every one of which is itself Cf — and let the rest of Cf through: zero-width space, ZWNJ, ZWJ, word joiner, BOM,
// soft hyphen, the invisible tag characters. A server echoing the token with a zero-width space inside it then
// defeated the redaction (the search no longer matched) while the terminal drew what looked like the whole token.
//
// The token is redacted AFTER the stripping, so a server cannot slip it past by splitting it with a character this
// function removes. It CAN still split it with an invisible character outside Cc and Cf — see the note in refusal_test.go.
func SafeForTerminal(text, token string) string {
	safe := OneLineForTerminal(text)
	if token != "" {
		safe = strings.ReplaceAll(safe, token, "«token»")
	}
	if utf8.RuneCountInString(safe) > reasonLimit {
		runes := []rune(safe)
		safe = strings.TrimSpace(string(runes[:reasonLimit-1])) + "…"
	}
	return safe
}

// OneLineForTerminal is a value from outside this agent (a farm's error, an id it named) made fit to stand inside one of
// its lines: SafeForTerminal's filter, with every line break folded to a space, and nothing redacted or cut.
func OneLineForTerminal(text string) string {
	return strings.Join(strings.Fields(stripControls(text, false)), " ")
}

// TerminalWriter is w with ForTerminal applied to every write: what the narrators write through. fmt's Fprint family
// writes each call in one Write, so a sequence cannot be split across two. nil stays nil.
func TerminalWriter(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	if _, already := w.(terminalWriter); already {
		return w
	}
	return terminalWriter{w}
}

type terminalWriter struct{ w io.Writer }

func (t terminalWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(t.w, ForTerminal(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ForTerminal is a whole line of the agent's own narration made fit for a person's terminal, whatever values were put
// into it (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST): the same filter as SafeForTerminal — invalid UTF-8, escape
// sequences, control and format characters out — except that the line breaks the agent wrote stay, and nothing is cut.
// It is the last step of every narrator (connect's, the tunnel's, the capture controller's), so a value nobody thought to
// filter at its source still cannot move the cursor, retitle the window or rewrite a line. A value that could carry a
// line break of its own (a session id, an error) is folded at its source with SafeForTerminal as well.
func ForTerminal(line string) string { return stripControls(line, true) }

// stripControls is the one filter both doors share. keepLines keeps '\n' as it is; otherwise white-space controls become
// a space. Every other control character (Cc: C0, DEL, C1) and every format character (Cf) is removed.
func stripControls(text string, keepLines bool) string {
	text = strings.ToValidUTF8(text, "")
	text = escapeSequence.ReplaceAllString(text, "")
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' && keepLines:
			b.WriteRune(r)
		case r == '\n' || r == '\r' || r == '\t' || r == '\v' || r == '\f':
			b.WriteRune(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
