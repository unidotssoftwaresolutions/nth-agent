package tunnel_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// THE_HEADER_THAT_REACHES_APPIUM — a value typed into the app is kept out of Appium's own log on a customer's phone too.
//
// The platform marks each request that sets a value (a saved login, a person's recorded text, the model's text) with
// X-Appium-Is-Sensitive, and Appium then writes that request's body as **SECURE** in its log. The farm carries the header
// in the frame's headers; this agent forwarded Content-Type alone, so every saved login typed on a customer's phone was
// written in clear in the log of the Appium on the customer's own computer.
//
// Every frame here goes through the real tunnel: a fake farm's WebSocket, the agent's own Run loop, and a fake Appium (and
// a fake hub) that keep every header they are sent.

const typedValue = "S3cret-typed-by-a-person"

// seenWithHeaders is one request a fake endpoint was sent, every header with it.
type seenWithHeaders struct {
	Method, Path, Body, Host string
	Header                   http.Header
}

// headerKeeper is a fake Appium (or hub): it keeps every request, answers a create with its own session id, and anything
// else with a null value, or with the error errorFor names.
type headerKeeper struct {
	mu       sync.Mutex
	seen     []seenWithHeaders
	session  string
	errorFor func(path string) string
}

func (k *headerKeeper) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		k.mu.Lock()
		k.seen = append(k.seen, seenWithHeaders{Method: r.Method, Path: r.URL.Path, Body: string(body), Host: r.Host, Header: r.Header.Clone()})
		fail := k.errorFor
		k.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail != nil {
			if reply := fail(r.URL.Path); reply != "" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(reply))
				return
			}
		}
		if r.Method == http.MethodPost && r.URL.Path == "/session" {
			_, _ = w.Write([]byte(`{"value":{"sessionId":"` + k.session + `","capabilities":{}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":null}`))
	}
}

func (k *headerKeeper) last(t *testing.T, path string) seenWithHeaders {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	for i := len(k.seen) - 1; i >= 0; i-- {
		if k.seen[i].Path == path {
			return k.seen[i]
		}
	}
	t.Fatalf("no request for %s reached this endpoint: %+v", path, k.seen)
	return seenWithHeaders{}
}

func (k *headerKeeper) setErrorFor(f func(string) string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.errorFor = f
}

// valueFrame is an element's value, as a WebDriver client sends it, with the headers the farm put in the frame — and,
// besides them, what a farm never sends and this agent must never pass on.
func valueFrame(id, session, sensitive string) map[string]any {
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"text":"` + typedValue + `"}`))
	headers := map[string]string{
		"Content-Type":   "application/json",
		"Authorization":  "Bearer never-forwarded",
		"X-Consumer-Key": "never-forwarded",
		"Host":           "farm.example.test",
	}
	if sensitive != "" {
		headers["X-Appium-Is-Sensitive"] = sensitive
	}
	return map[string]any{
		"id": id, "method": "POST", "path": "/session/" + session + "/element/e-1/value",
		"headers": headers, "bodyBase64": &encoded,
	}
}

// names is a request's header names, sorted, canonical.
func names(h http.Header) []string {
	out := make([]string, 0, len(h))
	for name := range h {
		out = append(out, http.CanonicalHeaderKey(name))
	}
	sort.Strings(out)
	return out
}

// startHeaderRig is a node with both a local Appium and a hub account, its tunnel up to a fake farm; ask sends one frame
// and reads its answer.
func startHeaderRig(t *testing.T) (*headerKeeper, *headerKeeper, *syncWriter, func(map[string]any) map[string]any) {
	t.Helper()
	appium := &headerKeeper{session: "local-session-1"}
	appiumServer := httptest.NewServer(appium.handler())
	t.Cleanup(appiumServer.Close)
	hub := &headerKeeper{session: "hub-session-1"}
	hubServer := httptest.NewServer(hub.handler())
	t.Cleanup(hubServer.Close)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := mixedTunnel(t, farmServer.URL, appiumServer.URL, hubServer.URL, creds)
	conn := farm.waitConn(t)
	ask := func(frame map[string]any) map[string]any {
		sendFrame(t, conn, frame)
		return readFrame(t, conn)
	}
	return appium, hub, out, ask
}

func status(answer map[string]any) int { return int(answer["status"].(float64)) }

// The headers a value request reached Appium with before this round: Go's own client's, and the body's.
var headersAppiumSawBefore = []string{"Accept-Encoding", "Content-Length", "Content-Type", "User-Agent"}

func withSensitive(before []string) []string {
	out := append(append([]string(nil), before...), "X-Appium-Is-Sensitive")
	sort.Strings(out)
	return out
}

// RULE 1 — red at HEAD. A frame that carries the header reaches the customer's Appium with it, exactly as the frame
// carries it, and nothing else of the frame's crosses: not Authorization, not X-Consumer-Key, not a Host.
func TestAValueFrameThatCarriesTheHeaderReachesTheLocalAppiumWithItExactlyAsItCame(t *testing.T) {
	for _, value := range []string{"true", "1", "yes", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			appium, _, out, ask := startHeaderRig(t)
			if got := status(ask(sessionFrame("c1", localCreate))); got != http.StatusOK {
				t.Fatalf("the local session was not created: %d\n%s", got, out.String())
			}
			if got := status(ask(valueFrame("v1", "local-session-1", value))); got != http.StatusOK {
				t.Fatalf("the value frame was answered %d\n%s", got, out.String())
			}
			seen := appium.last(t, "/session/local-session-1/element/e-1/value")
			if got := seen.Header.Values("X-Appium-Is-Sensitive"); len(got) != 1 || got[0] != value {
				t.Fatalf("the header did not reach Appium as the frame carried it (%q): got %q; headers were %v", value, got, names(seen.Header))
			}
			if got, want := strings.Join(names(seen.Header), ","), strings.Join(withSensitive(headersAppiumSawBefore), ","); got != want {
				t.Fatalf("Appium was sent other headers than before plus the one: got %s, want %s", got, want)
			}
			theFramesOwnHeadersNeverCrossed(t, seen)
		})
	}
}

// RULE 1, THE HUB — red at HEAD. A session on a BrowserStack phone runs on BrowserStack's Appium: its value frames carry the
// header there too, and nothing else of the frame's.
func TestAValueFrameForAHubSessionCarriesTheHeaderToTheHub(t *testing.T) {
	_, hub, out, ask := startHeaderRig(t)
	if got := status(ask(sessionFrame("c1", cloudCreate))); got != http.StatusOK {
		t.Fatalf("the hub session was not created: %d\n%s", got, out.String())
	}
	if got := status(ask(valueFrame("v1", "hub-session-1", "true"))); got != http.StatusOK {
		t.Fatalf("the value frame was answered %d\n%s", got, out.String())
	}
	seen := hub.last(t, "/session/hub-session-1/element/e-1/value")
	if got := seen.Header.Get("X-Appium-Is-Sensitive"); got != "true" {
		t.Fatalf("the header did not reach the hub: headers were %v", names(seen.Header))
	}
	theFramesOwnHeadersNeverCrossed(t, seen)
}

// CONTROL — green at HEAD and now. A frame without the header is forwarded exactly as before: the same headers, the same
// body, nothing of the frame's own.
func TestAValueFrameWithoutTheHeaderReachesAppiumAsItDidBefore(t *testing.T) {
	appium, _, _, ask := startHeaderRig(t)
	ask(sessionFrame("c1", localCreate))
	if got := status(ask(valueFrame("v1", "local-session-1", ""))); got != http.StatusOK {
		t.Fatalf("the value frame was answered %d", got)
	}
	seen := appium.last(t, "/session/local-session-1/element/e-1/value")
	if got, want := strings.Join(names(seen.Header), ","), strings.Join(headersAppiumSawBefore, ","); got != want {
		t.Fatalf("a request without the header must reach Appium as before: got %s, want %s", got, want)
	}
	if seen.Header.Get("Content-Type") != "application/json" || !strings.Contains(seen.Body, typedValue) {
		t.Fatalf("Content-Type and the body reach Appium unchanged: %+v", seen)
	}
	theFramesOwnHeadersNeverCrossed(t, seen)
	if got := strings.Join(names(appium.last(t, "/session").Header), ","); got != strings.Join(headersAppiumSawBefore, ",") {
		t.Fatalf("a session's create reaches Appium as before: %s", got)
	}
}

// AN AGENT ONE VERSION BACK, AND ONE AHEAD — green at HEAD and now. The frame keeps its shape (the header is one more entry
// in the map it always carried), so a frame that carries it is answered by an agent of either version.
func TestAFrameThatCarriesTheHeaderIsAnsweredByAnAgentOfEitherVersion(t *testing.T) {
	appium, _, _, ask := startHeaderRig(t)
	ask(sessionFrame("c1", localCreate))
	if got := status(ask(valueFrame("v1", "local-session-1", "true"))); got != http.StatusOK {
		t.Fatalf("a frame with the header was answered %d", got)
	}
	if !strings.Contains(appium.last(t, "/session/local-session-1/element/e-1/value").Body, typedValue) {
		t.Fatal("the value request did not reach Appium")
	}
}

// ITEM 2, A SEAL — green at HEAD and now. This agent writes no typed value: not a request's body, not Appium's error that
// quotes it (the UiAutomator2 server's "Cannot set the element to '…'"). The answer goes back to the farm as before.
func TestTheAgentWritesNoTypedValueNorAnErrorThatQuotesIt(t *testing.T) {
	for _, sensitive := range []string{"true", ""} {
		t.Run("sensitive="+sensitive, func(t *testing.T) {
			appium, _, out, ask := startHeaderRig(t)
			ask(sessionFrame("c1", localCreate))
			ask(valueFrame("v1", "local-session-1", sensitive))
			appium.setErrorFor(func(path string) string {
				if strings.HasSuffix(path, "/value") {
					return `{"value":{"error":"invalid element state","message":"Cannot set the element to '` + typedValue + `'"}}`
				}
				return ""
			})
			answer := ask(valueFrame("v2", "local-session-1", sensitive))
			if status(answer) != http.StatusInternalServerError {
				t.Fatalf("Appium's error is the farm's answer, as before: %v", answer)
			}
			body, _ := base64.StdEncoding.DecodeString(answer["bodyBase64"].(string))
			if !strings.Contains(string(body), typedValue) {
				t.Fatalf("the answer goes back to the farm verbatim: %s", body)
			}
			if strings.Contains(out.String(), typedValue) {
				t.Fatalf("the agent wrote a typed value:\n%s", out.String())
			}
		})
	}
}

func theFramesOwnHeadersNeverCrossed(t *testing.T, seen seenWithHeaders) {
	t.Helper()
	for _, never := range []string{"Authorization", "X-Consumer-Key"} {
		if seen.Header.Get(never) != "" {
			t.Fatalf("%s crossed into the request to Appium", never)
		}
	}
	if seen.Host == "farm.example.test" || !strings.HasPrefix(seen.Host, "127.0.0.1:") {
		t.Fatalf("the request reached Appium with another Host than its own address: %q", seen.Host)
	}
}
