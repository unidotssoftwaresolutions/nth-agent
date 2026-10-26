package tunnel_test

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// ── A HUB ANSWER NEVER CARRIES THE CUSTOMER'S KEY TO THE FARM ──────────────────────────────────────────
//
// The session create is the one request into which the agent merges the customer's access key, and the hub's answer to
// it — and to every later frame of a cloud session — went back to the farm byte for byte. "The key never leaves this
// machine and is never sent to us" then rested on BrowserStack never echoing it: in value.capabilities.bstack:options, in
// an echo of the requested capabilities, or in an error quoting the request. Nobody here has seen what a successful
// create answers, so the agent no longer relies on it: whatever a hub answer holds, the key is replaced before the
// answer is relayed, in every spelling the answer could carry it in.

// keySpellings are the ways a hub answer could hold the key: as written, JSON-escaped, percent-encoded, base64 (alone,
// in the user:key of a basic credential, and in the middle of a longer base64 run) and hex.
func keySpellings() map[string]string {
	escaped := ""
	for _, r := range testKey {
		escaped += fmt.Sprintf(`\u%04x`, r)
	}
	percent := ""
	for i := 0; i < len(testKey); i++ {
		percent += fmt.Sprintf("%%%02X", testKey[i])
	}
	return map[string]string{
		"as written":            testKey,
		"JSON-escaped":          escaped,
		"percent-encoded":       percent,
		"base64":                base64.StdEncoding.EncodeToString([]byte(testKey)),
		"base64url, unpadded":   base64.RawURLEncoding.EncodeToString([]byte(testKey)),
		"base64 user:key":       base64.StdEncoding.EncodeToString([]byte("acme-user:" + testKey)),
		"base64, mid-run":       base64.StdEncoding.EncodeToString([]byte("Authorization header for acme: " + testKey + " end")),
		"hex":                   hex.EncodeToString([]byte(testKey)),
		"hex, in capitals":      strings.ToUpper(hex.EncodeToString([]byte(testKey))),
		"JSON-escaped solidus":  strings.ReplaceAll(`"`+testKey+`"`, "-", `-`),
		"inside a longer value": "prefix-" + testKey + "-suffix",
	}
}

// answerWithout decodes a relayed answer and fails if any spelling of the key is in it.
func answerWithout(t *testing.T, response map[string]any) string {
	t.Helper()
	encoded, _ := response["bodyBase64"].(string)
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("the relayed answer is not base64: %v", err)
	}
	for name, spelling := range keySpellings() {
		inner := strings.Trim(spelling, `"`)
		if name == "inside a longer value" {
			inner = testKey
		}
		if strings.Contains(string(body), inner) {
			t.Errorf("the key reached the farm (%s) in %q", name, body)
		}
	}
	for _, mid := range base64Middles(testKey) {
		if strings.Contains(string(body), mid) {
			t.Errorf("the key reached the farm inside a base64 run (%s) in %q", mid, body)
		}
	}
	return string(body)
}

// base64Middles is the part of the key's base64 that is the same wherever in a longer run the key starts.
func base64Middles(key string) []string {
	var out []string
	for offset := 0; offset < 3; offset++ {
		run := base64.StdEncoding.EncodeToString(append(make([]byte, offset), key...))
		start := 0
		if offset > 0 {
			start = 4
		}
		end := len(run)
		if (offset+len(key))%3 != 0 {
			end -= 4
		}
		if end-start >= 8 {
			out = append(out, run[start:end])
		}
	}
	return out
}

func echoingCreate(spelling string) string {
	return `{"value":{"sessionId":"stub-session","capabilities":{"platformName":"Android",` +
		`"bstack:options":{"userName":"acme-user","accessKey":"` + spelling + `","sessionName":"keepNAME"}}}}`
}

func TestTheHubsAnswerNeverCarriesTheKey(t *testing.T) {
	for name, spelling := range keySpellings() {
		t.Run("a create answer, "+name, func(t *testing.T) {
			hub := &recorder{reply: echoingCreate(strings.Trim(spelling, `"`))}
			hubServer := httptest.NewServer(hub.handler())
			defer hubServer.Close()
			farm, farmServer := newStubFarmTunnel(t)
			drive, _ := mixedTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL, creds)
			conn := farm.waitConn(t)

			sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
			response := readFrame(t, conn)
			body := answerWithout(t, response)
			if int(response["status"].(float64)) != http.StatusOK || !strings.Contains(body, `"sessionId":"stub-session"`) ||
				!strings.Contains(body, "keepNAME") {
				t.Fatalf("the status, the session id and the rest of the answer stay: %v %s", response["status"], body)
			}
			if drive.RoutedSessions() != 1 {
				t.Fatalf("the session is still routed from its own id")
			}

			// EVERY LATER FRAME OF THE SESSION, TOO.
			sendFrame(t, conn, frame("f2", "GET", "/session/stub-session"))
			answerWithout(t, readFrame(t, conn))
		})
	}

	t.Run("an error that quotes the key", func(t *testing.T) {
		hub := &recorder{status: http.StatusBadRequest,
			reply: `{"value":{"error":"invalid argument","message":"bad capability: accessKey=` + testKey + ` user acme-user:` + testKey + `"}}`}
		hubServer := httptest.NewServer(hub.handler())
		defer hubServer.Close()
		farm, farmServer := newStubFarmTunnel(t)
		drive, _ := mixedTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL, creds)
		conn := farm.waitConn(t)

		sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
		response := readFrame(t, conn)
		body := answerWithout(t, response)
		if int(response["status"].(float64)) != http.StatusBadRequest || !strings.Contains(body, "invalid argument") {
			t.Fatalf("the hub's own status and error stay, so the driver's error handling is unchanged: %v %s", response["status"], body)
		}
		if drive.RoutedSessions() != 0 {
			t.Fatal("a failed create routes nothing")
		}
	})

	t.Run("a sessionless frame on a node without --appium", func(t *testing.T) {
		hub := &recorder{reply: `{"value":{"ready":true,"message":"echo ` + testKey + `"}}`}
		hubServer := httptest.NewServer(hub.handler())
		defer hubServer.Close()
		farm, farmServer := newStubFarmTunnel(t)
		cloudTunnel(t, farmServer.URL, "", hubServer.URL, creds)
		conn := farm.waitConn(t)

		sendFrame(t, conn, frame("f4", "GET", "/status"))
		answerWithout(t, readFrame(t, conn))
	})

	t.Run("an answer without the key crosses byte for byte", func(t *testing.T) {
		const reply = "{\n  \"value\" : {\"sessionId\":\"stub-session\", \"capabilities\":{\"note\":\"caf\\u00e9 \\/ <ok>\"}}\n}"
		hub := &recorder{reply: reply}
		hubServer := httptest.NewServer(hub.handler())
		defer hubServer.Close()
		farm, farmServer := newStubFarmTunnel(t)
		mixedTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL, creds)
		conn := farm.waitConn(t)

		sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
		if body := answerWithout(t, readFrame(t, conn)); body != reply {
			t.Fatalf("an answer that holds no key is relayed as the hub wrote it:\n got %q\nwant %q", body, reply)
		}
	})
}

// WHAT THE LEASE HOLDER SEES IN THE KEY'S PLACE is one fixed placeholder — the capture's own mask — and the user name, where
// it stands as the account's name beside the key, is replaced too: together they are the credential.
func TestTheLeaseHolderSeesThePlaceholderWhereTheKeyWas(t *testing.T) {
	hub := &recorder{reply: echoingCreate(testKey)}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()
	farm, farmServer := newStubFarmTunnel(t)
	mixedTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	body := answerWithout(t, readFrame(t, conn))
	want := `"bstack:options":{"userName":"` + browserstack.KeyMask + `","accessKey":"` + browserstack.KeyMask + `","sessionName":"keepNAME"}`
	if !strings.Contains(body, want) {
		t.Fatalf("the lease holder sees the placeholder where the key and the account's name were:\n got %s\nwant …%s…", body, want)
	}
}
