package tunnel_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST — what one farm frame can make this agent do, when the far end of the tunnel is
// not the farm we run: a compromised farm, a TLS-inspecting middlebox, a --farm the customer was talked into. Today's
// The farm sends none of the frames refused here; TestEveryFrameTodaysFarmSendsStillPasses holds that it is not refused.

func answerTo(t *testing.T, reader *farmReader, id string) map[string]any {
	t.Helper()
	return reader.await(t, 5*time.Second, "the answer to "+id, func(f map[string]any) bool { return f["id"] == id && f["kind"] == nil })
}

func statusOf(frame map[string]any) int {
	status, _ := frame["status"].(float64)
	return int(status)
}

func answerBody(t *testing.T, frame map[string]any) string {
	t.Helper()
	if frame["bodyBase64"] == nil {
		return ""
	}
	return decodeBody(t, frame)
}

func requestWithBody(id, method, path, body string) map[string]any {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	return map[string]any{"id": id, "method": method, "path": path,
		"headers": map[string]string{"Content-Type": "application/json"}, "bodyBase64": encoded}
}

// ── a frame's path cannot move the request to another address ─────────────────────────────────────────────

func TestAFramesPathCannotMoveTheRequestToAnotherAddress(t *testing.T) {
	configured := &recorder{}
	configuredServer := httptest.NewServer(configured.handler())
	t.Cleanup(configuredServer.Close)
	// ANOTHER PROGRAM ON THIS MACHINE, run by this account: what the local client's owner and loopback checks let through.
	other := &recorder{reply: `{"value":{"sessionId":"other-session","capabilities":{}}}`}
	otherServer := httptest.NewServer(other.handler())
	t.Cleanup(otherServer.Close)
	otherHost := strings.TrimPrefix(otherServer.URL, "http://")

	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = configuredServer.URL })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	for i, c := range []struct {
		name  string
		frame map[string]any
	}{
		{"a session create whose path names another address", requestWithBody("", "POST", "@"+otherHost+"/session", localCreate)},
		{"a status read whose path names another address", frame("", "GET", "@"+otherHost+"/status")},
		{"a path with no leading slash", frame("", "GET", "status")},
	} {
		t.Run(c.name, func(t *testing.T) {
			id := fmt.Sprintf("14141414-0000-0000-0000-%012d", i)
			c.frame["id"] = id
			send(t, conn, c.frame)
			answer := answerTo(t, reader, id)
			if statusOf(answer) != http.StatusBadGateway || !strings.Contains(answerBody(t, answer), `does not start with "/"`) {
				t.Fatalf("the frame must be refused in words, got %d %q", statusOf(answer), answerBody(t, answer))
			}
		})
	}
	if seen := other.seen(); len(seen) != 0 {
		t.Fatalf("a frame's path reached another address on this machine: %+v", seen)
	}
	if seen := configured.seen(); len(seen) != 0 {
		t.Fatalf("a refused frame reached the configured Appium: %+v", seen)
	}
	if !strings.Contains(out.String(), `does not start with "/"`) {
		t.Fatalf("the console must say what was refused:\n%s", out.String())
	}

	// The same machine's own status read still goes where it always went.
	send(t, conn, frame("14141414-0000-0000-0000-999999999999", "GET", "/status"))
	if answer := answerTo(t, reader, "14141414-0000-0000-0000-999999999999"); statusOf(answer) != http.StatusOK || len(configured.seen()) != 1 {
		t.Fatalf("GET /status must still reach the configured Appium: %v, %+v", answer, configured.seen())
	}
}

// ── a frame that names no session reaches only the status routes ─────────────────────────────────────────────

func TestASessionlessFrameReachesOnlyTheStatusRoutes(t *testing.T) {
	appium := &recorder{reply: `{"value":{"ready":true,"message":"ok"}}`}
	appiumServer := httptest.NewServer(appium.handler())
	t.Cleanup(appiumServer.Close)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = appiumServer.URL })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	allowed := []struct{ method, path string }{
		{"GET", "/status"}, {"GET", "/wd/hub/status"}, {"GET", "/status?probe=1"},
	}
	refused := []map[string]any{
		frame("", "POST", "/status"),
		frame("", "DELETE", "/status"),
		frame("", "GET", "/sessions"),
		frame("", "GET", "/appium/sessions"),
		requestWithBody("", "POST", "/appium/plugins/reporting/export", `{"all":true}`),
		frame("", "GET", "/Session/another-lease-session/url"),
		requestWithBody("", "POST", "/SESSION", localCreate),
		requestWithBody("", "POST", "/gurufarm-node/resign", `{"artifactUrl":"https://example.invalid/a.ipa","udid":"x"}`),
		frame("", "GET", "/"),
	}
	for i, a := range allowed {
		id := fmt.Sprintf("15151515-0000-0000-0000-%012d", i)
		send(t, conn, frame(id, a.method, a.path))
		if answer := answerTo(t, reader, id); statusOf(answer) != http.StatusOK {
			t.Fatalf("%s %s must reach Appium, got %v", a.method, a.path, answer)
		}
	}
	for i, f := range refused {
		id := fmt.Sprintf("15151515-1111-0000-0000-%012d", i)
		f["id"] = id
		send(t, conn, f)
		answer := answerTo(t, reader, id)
		if statusOf(answer) != http.StatusNotFound || !strings.Contains(answerBody(t, answer), "only GET /status") {
			t.Fatalf("%v %v must be refused in words, got %d %q", f["method"], f["path"], statusOf(answer), answerBody(t, answer))
		}
	}
	seen := appium.seen()
	if len(seen) != len(allowed) {
		t.Fatalf("Appium must see only the status reads, saw %+v", seen)
	}
	for i, a := range allowed {
		if seen[i].Method != a.method || seen[i].Path != a.path {
			t.Fatalf("Appium saw %+v, want %s %s", seen[i], a.method, a.path)
		}
	}
	if strings.Count(out.String(), "only GET /status") != 1 {
		t.Fatalf("the console says it once:\n%s", out.String())
	}
}

// ── a live view opens exactly the loopback address and port this agent reserved ─────────────────────────────

type recordingMirror struct {
	server *httptest.Server
	port   uint16
	mu     sync.Mutex
	seen   []string // "<request URI> auth=<Authorization>"
	left   chan struct{}
}

func newRecordingMirror(t *testing.T) *recordingMirror {
	m := &recordingMirror{left: make(chan struct{}, 16)}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.seen = append(m.seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		m.mu.Unlock()
		defer func() { m.left <- struct{}{} }()
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=--BoundaryString")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("--BoundaryString\r\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(m.server.Close)
	port, _ := strconv.Atoi(m.server.URL[strings.LastIndex(m.server.URL, ":")+1:])
	m.port = uint16(port)
	return m
}

func (m *recordingMirror) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seen...)
}

func TestALiveViewOpensExactlyTheAddressItReserved(t *testing.T) {
	mirror := newRecordingMirror(t)
	p := fmt.Sprint(mirror.port)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offering(mirror.port) })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	for i, path := range []string{
		"http://127.0.0.1:" + p + "/x?y=1",
		"http://127.0.0.1:" + p + "/?y=1",
		"http://u:p@127.0.0.1:" + p + "/",
		"https://127.0.0.1:" + p + "/",
		"http://127.0.0.2:" + p + "/",
		"http://127.0.0.1:" + p + "/#top",
		"http://127.0.0.1:" + p + "//",
	} {
		id := fmt.Sprintf("16161616-0000-0000-0000-%012d", i)
		send(t, conn, streamStart(id, path))
		end := reader.await(t, 5*time.Second, "the refusal of "+path, ofStream(id, "stream-end"))
		if !strings.Contains(fmt.Sprint(end["error"]), "only at http://127.0.0.1:"+p+"/") {
			t.Fatalf("%s must be refused in words, got %v", path, end["error"])
		}
	}
	if seen := mirror.requests(); len(seen) != 0 {
		t.Fatalf("a refused stream reached the mirror port: %v", seen)
	}

	// THE SPELLINGS TODAY'S FARM HANDS OUT (LiveViewRules.IsTheMirror, compared ignoring case): each opens 127.0.0.1:P, at /.
	for i, path := range []string{
		"http://127.0.0.1:" + p,
		"http://127.0.0.1:" + p + "/",
		"http://localhost:" + p + "/",
		"http://[::1]:" + p,
		"HTTP://LOCALHOST:" + p + "/",
	} {
		id := fmt.Sprintf("16161616-1111-0000-0000-%012d", i)
		send(t, conn, streamStart(id, path))
		reader.await(t, 5*time.Second, "the mirror's first chunk for "+path, ofStream(id, "stream-chunk"))
		send(t, conn, map[string]any{"kind": "stream-cancel", "id": id})
		select {
		case <-mirror.left:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the cancelled stream's viewer never left", path)
		}
	}
	for _, seen := range mirror.requests() {
		if seen != "GET / auth=" {
			t.Fatalf("the mirror must be asked for / and nothing else, saw %q", seen)
		}
	}
	if n := len(mirror.requests()); n != 5 {
		t.Fatalf("each farm spelling must open the mirror once, opened %d", n)
	}
}

// ── the hub's redirect is not followed ─────────────────────────────────────────────────────────────────────────────

func TestTheHubsRedirectIsNotFollowed(t *testing.T) {
	elsewhere := &recorder{}
	elsewhereServer := httptest.NewServer(elsewhere.handler())
	t.Cleanup(elsewhereServer.Close)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		http.Redirect(w, r, elsewhereServer.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hub.Close)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
		c.AppiumURL = ""
		c.BrowserStack, c.BrowserStackHub, c.BrowserStackApp = creds, hub.URL+"/wd/hub", testApp
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)

	send(t, conn, sessionFrame("17171717-0000-0000-0000-000000000001", cloudCreate))
	send(t, conn, frame("17171717-0000-0000-0000-000000000002", "GET", "/status"))
	for _, id := range []string{"17171717-0000-0000-0000-000000000001", "17171717-0000-0000-0000-000000000002"} {
		answer := answerTo(t, reader, id)
		if statusOf(answer) != http.StatusBadGateway || !strings.Contains(answerBody(t, answer), "redirect") {
			t.Fatalf("a redirect from the hub must be answered in words, got %d %q", statusOf(answer), answerBody(t, answer))
		}
	}
	if seen := elsewhere.seen(); len(seen) != 0 {
		t.Fatalf("the hub's redirect was followed — the create, with the customer's key in it, went elsewhere: %+v", seen)
	}
	if !strings.Contains(out.String(), "redirect") {
		t.Fatalf("the console must say so:\n%s", out.String())
	}
}

// ── an answer too large for one tunnel message is refused in words, and the tunnel stays ──────────────────────

func TestAnAnswerTooLargeForOneMessageIsRefusedInWordsAndTheTunnelStays(t *testing.T) {
	const limit = 4096
	release := make(chan struct{})
	appium := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/session":
			_, _ = w.Write([]byte(`{"value":{"sessionId":"s1","capabilities":{}}}`))
		case strings.HasSuffix(r.URL.Path, "/big"):
			_, _ = w.Write([]byte(strings.Repeat("b", limit+1)))
			w.(http.Flusher).Flush()
			<-release // and it holds the answer open: the agent must not wait for its end to answer
		case strings.HasSuffix(r.URL.Path, "/exact"):
			_, _ = w.Write([]byte(strings.Repeat("e", limit)))
		default:
			_, _ = w.Write([]byte(`{"value":{"ready":true}}`))
		}
	}))
	t.Cleanup(appium.Close)
	t.Cleanup(func() { close(release) }) // runs first: the held answer ends, then the server closes
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = appium.URL; c.MaxAnswerBytes = limit })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	send(t, conn, sessionFrame("24242424-0000-0000-0000-000000000000", localCreate))
	answerTo(t, reader, "24242424-0000-0000-0000-000000000000")

	send(t, conn, frame("24242424-0000-0000-0000-000000000001", "GET", "/session/s1/big"))
	big := answerTo(t, reader, "24242424-0000-0000-0000-000000000001")
	if statusOf(big) != http.StatusBadGateway || !strings.Contains(answerBody(t, big), "larger than the 4096 bytes") {
		t.Fatalf("an answer past the bound must be a plain 502 naming it, got %d %q", statusOf(big), answerBody(t, big))
	}
	send(t, conn, frame("24242424-0000-0000-0000-000000000002", "GET", "/session/s1/exact"))
	exact := answerTo(t, reader, "24242424-0000-0000-0000-000000000002")
	if statusOf(exact) != http.StatusOK || answerBody(t, exact) != strings.Repeat("e", limit) {
		t.Fatalf("an answer of exactly the bound is relayed whole, got %d (%d bytes)", statusOf(exact), len(answerBody(t, exact)))
	}
	send(t, conn, frame("24242424-0000-0000-0000-000000000003", "GET", "/status"))
	if statusOf(answerTo(t, reader, "24242424-0000-0000-0000-000000000003")) != http.StatusOK || farm.upgrades.Load() != 1 {
		t.Fatalf("the tunnel must stay up for every other lease (upgrades %d)", farm.upgrades.Load())
	}
	if !strings.Contains(out.String(), "larger than the 4096 bytes") {
		t.Fatalf("the console must say so:\n%s", out.String())
	}
}

// The bound's number, from the relay contract: the largest answer whose frame — base64 and the contract's 64 KiB for
// everything else — still fits in RelayProtocol.MaxMessageBytes.
func TestTheAnswerBoundIsTheLargestBodyThatFitsInOneMessage(t *testing.T) {
	fits := func(n int64) bool { return (n+2)/3*4+64*1024 <= tunnel.MaxMessageBytes }
	if tunnel.MaxMessageBytes != 64*1024*1024 {
		t.Fatalf("one message is RelayProtocol.MaxMessageBytes, 64 MiB; got %d", tunnel.MaxMessageBytes)
	}
	if tunnel.MaxAnswerBytes != 50_282_496 || !fits(tunnel.MaxAnswerBytes) || fits(tunnel.MaxAnswerBytes+1) {
		t.Fatalf("the answer bound must be the largest body that fits: %d", tunnel.MaxAnswerBytes)
	}
}

// ── one inbound message is read up to the relay contract's cap, and the request queue is bounded ─────────────

// streamMessage writes one text message of exactly size bytes — a request frame for GET /status padded in a header — in
// pieces, so the farm side never holds it. Writes after the agent ends the connection fail, and are not this test's.
func streamMessage(conn *websocket.Conn, id string, size int64) {
	prefix := `{"id":"` + id + `","method":"GET","path":"/status","headers":{"X-Pad":"`
	const suffix = `"}}`
	writer, err := conn.Writer(context.Background(), websocket.MessageText)
	if err != nil {
		return
	}
	defer writer.Close()
	if _, err := writer.Write([]byte(prefix)); err != nil {
		return
	}
	piece := []byte(strings.Repeat("a", 32*1024))
	for left := size - int64(len(prefix)) - int64(len(suffix)); left > 0; {
		n := min(left, int64(len(piece)))
		if _, err := writer.Write(piece[:n]); err != nil {
			return
		}
		left -= n
	}
	_, _ = writer.Write([]byte(suffix))
}

func TestOneFarmMessagePastTheRelayCapEndsTheTunnelUnread(t *testing.T) {
	appium := &recorder{reply: `{"value":{"ready":true}}`}
	appiumServer := httptest.NewServer(appium.handler())
	t.Cleanup(appiumServer.Close)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = appiumServer.URL })
	first := farm.next(t)
	reader := readFarmSide(first)

	// EXACTLY THE CAP: the largest message today's farm can send is read and answered.
	streamMessage(first, "25252525-0000-0000-0000-000000000001", tunnel.MaxMessageBytes)
	if statusOf(answerTo(t, reader, "25252525-0000-0000-0000-000000000001")) != http.StatusOK {
		t.Fatal("a message of exactly 64 MiB must be read and answered")
	}
	// ONE BYTE OVER: the tunnel ends, the message is never dispatched, and the agent dials a fresh tunnel.
	go streamMessage(first, "25252525-0000-0000-0000-000000000002", tunnel.MaxMessageBytes+1)
	select {
	case second := <-farm.conns:
		second.CloseNow()
	case <-time.After(15 * time.Second):
		t.Fatal("a message past 64 MiB must end the tunnel, and the agent dial again")
	}
	if n := len(appium.seen()); n != 1 {
		t.Fatalf("the over-cap frame must never be dispatched; Appium saw %d request(s)", n)
	}
}

func TestTheRequestQueueIsBoundedAndAFrameOverItIsAnsweredAtOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(*tunnel.Config)
		pad    int
	}{
		{"by count", func(c *tunnel.Config) { c.MaxQueuedRequests = 2 }, 0},
		{"by bytes", func(c *tunnel.Config) { c.MaxQueuedBytes = 2500 }, 1000},
	} {
		t.Run(c.name, func(t *testing.T) {
			entered, hold := make(chan struct{}, 8), make(chan struct{})
			appium := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("n") == "0" {
					entered <- struct{}{}
					<-hold
				}
				_, _ = w.Write([]byte(`{"value":{"ready":true}}`))
			}))
			t.Cleanup(appium.Close)
			t.Cleanup(func() { close(hold) })
			farm := newLiveFarm(t)
			var out syncWriter
			liveTunnel(t, farm.server.URL, &out, func(cfg *tunnel.Config) { cfg.AppiumURL = appium.URL; c.change(cfg) })
			conn := farm.next(t)
			reader := readFarmSide(conn)
			padded := func(id string, n int) map[string]any {
				f := frame(id, "GET", "/status?n="+strconv.Itoa(n))
				f["headers"] = map[string]string{"X-Pad": strings.Repeat("p", c.pad)}
				return f
			}

			send(t, conn, padded("26262626-0000-0000-0000-000000000000", 0))
			<-entered // the worker holds the first; the next two wait in the queue
			send(t, conn, padded("26262626-0000-0000-0000-000000000001", 1))
			send(t, conn, padded("26262626-0000-0000-0000-000000000002", 2))
			send(t, conn, padded("26262626-0000-0000-0000-000000000003", 3))
			over := answerTo(t, reader, "26262626-0000-0000-0000-000000000003")
			if statusOf(over) != http.StatusServiceUnavailable || !strings.Contains(answerBody(t, over), "waiting") {
				t.Fatalf("a frame over the queue's bound is answered at once, in words; got %d %q", statusOf(over), answerBody(t, over))
			}
			hold <- struct{}{}
			for i := 0; i < 3; i++ {
				id := fmt.Sprintf("26262626-0000-0000-0000-%012d", i)
				if statusOf(answerTo(t, reader, id)) != http.StatusOK {
					t.Fatalf("queued frame %s must still be answered", id)
				}
			}
			if !strings.Contains(out.String(), "waiting") {
				t.Fatalf("the console must say so:\n%s", out.String())
			}
		})
	}
}

// ── the farm's words reach the console without terminal controls ───────────────────────────────────────────

func TestAFarmsWordsReachTheConsoleWithoutTerminalControls(t *testing.T) {
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, nil)
	conn := farm.next(t)
	reader := readFarmSide(conn)
	send(t, conn, frame("23232323-0000-0000-0000-000000000001", "GET", "/session/\x1b]0;owned\x07\x1b[2J\u009b31mforged‮/url"))
	answerTo(t, reader, "23232323-0000-0000-0000-000000000001")
	waitFor(t, "the refusal said", func() bool { return strings.Contains(out.String(), "never created session") })
	if said := out.String(); strings.ContainsAny(said, "\x1b\x07\u009b‮") {
		t.Fatalf("a terminal control from the farm reached the console: %q", said)
	}
}

// ── TODAY'S FARM: every frame shape the farm sends an agent still passes ──────────────────────────────────────────────

type recordedCapture struct {
	mu    sync.Mutex
	calls []string
}

func (c *recordedCapture) record(call string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *recordedCapture) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func (c *recordedCapture) Start(_ context.Context, lease, serial string) {
	c.record("start " + lease + " " + serial)
}
func (c *recordedCapture) Stop(_ context.Context, lease string) { c.record("stop " + lease) }
func (c *recordedCapture) HandBackForFarm(_ context.Context, udid, lease string) (bool, string) {
	c.record("handback " + udid + " " + lease)
	return true, ""
}
func (c *recordedCapture) Pull(lease string) ([]byte, string) {
	c.record("pull " + lease)
	return []byte(`{"log":{"entries":[]}}`), ""
}
func (c *recordedCapture) Pulled(lease string) { c.record("pulled " + lease) }

func TestEveryFrameTodaysFarmSendsStillPasses(t *testing.T) {
	appium := &recorder{reply: `{"value":{"sessionId":"s1","capabilities":{}}}`}
	appiumServer := httptest.NewServer(appium.handler())
	t.Cleanup(appiumServer.Close)
	mirror := newRecordingMirror(t)
	p := fmt.Sprint(mirror.port)
	capture := &recordedCapture{}
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
		c.AppiumURL = appiumServer.URL
		c.Mirrors = offering(mirror.port)
		c.Capture = capture
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)
	raw := func(json string) {
		t.Helper()
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(json)); err != nil {
			t.Fatal(err)
		}
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	const lease = "6f1c2b0e-3d4a-4b5c-8d6e-7f8091a2b3c4"

	// As the farm serialises them (RelayProtocol.Json: camelCase, declaration order, the computed kind last).
	raw(`{"kind":"alive"}`)
	requests := []struct{ id, json string }{
		// RelayEndpoint: "/" + path + query, Content-Type only, base64 body or null.
		{"0a000000-0000-4000-8000-000000000001", `{"id":"0a000000-0000-4000-8000-000000000001","method":"POST","path":"/session","headers":{"Content-Type":"application/json; charset=utf-8"},"bodyBase64":"` + b64(localCreate) + `"}`},
		{"0a000000-0000-4000-8000-000000000002", `{"id":"0a000000-0000-4000-8000-000000000002","method":"GET","path":"/session/s1/source","headers":{},"bodyBase64":null}`},
		{"0a000000-0000-4000-8000-000000000003", `{"id":"0a000000-0000-4000-8000-000000000003","method":"POST","path":"/session/s1/element","headers":{"Content-Type":"application/json"},"bodyBase64":"` + b64(`{"using":"id","value":"login"}`) + `"}`},
		{"0a000000-0000-4000-8000-000000000004", `{"id":"0a000000-0000-4000-8000-000000000004","method":"GET","path":"/status","headers":{},"bodyBase64":null}`},
		{"0a000000-0000-4000-8000-000000000005", `{"id":"0a000000-0000-4000-8000-000000000005","method":"GET","path":"/wd/hub/status?x=1","headers":{},"bodyBase64":null}`},
		{"0a000000-0000-4000-8000-000000000006", `{"id":"0a000000-0000-4000-8000-000000000006","method":"DELETE","path":"/session/s1","headers":{},"bodyBase64":null}`},
		// TunnelPhoneStateNode: the farm's handback question, with its mark.
		{"0a000000-0000-4000-8000-000000000007", `{"id":"0a000000-0000-4000-8000-000000000007","method":"POST","path":"/gurufarm-node/android/capture-handback","headers":{"Content-Type":"application/json","X-GuruFarm-Asks":"phone-state"},"bodyBase64":"` + b64(`{"udid":"R5CT30ABCDE","leaseId":"`+lease+`"}`) + `"}`},
	}
	for _, r := range requests {
		raw(r.json)
		if answer := answerTo(t, reader, r.id); statusOf(answer) != http.StatusOK {
			t.Fatalf("today's farm frame %s was not passed: %d %q", r.json, statusOf(answer), answerBody(t, answer))
		}
	}
	if n := len(appium.seen()); n != 6 {
		t.Fatalf("every Appium frame must reach Appium, saw %+v", appium.seen())
	}
	// CaptureCoordinator: RelayCaptureStart and the legacy RelayCaptureStop.
	raw(`{"leaseId":"` + lease + `","serial":"R5CT30ABCDE","kind":"capture-start"}`)
	raw(`{"leaseId":"` + lease + `","serial":"R5CT30ABCDE","kind":"capture-stop"}`)
	waitFor(t, "the capture frames handed over", func() bool { return len(capture.seen()) == 3 })
	// RelayStreamEndpoint: a capture pull (with its lease) and a mirror in each spelling LiveViewRules hands out.
	raw(`{"id":"0b000000-0000-4000-8000-000000000001","method":"GET","path":"gfhar:` + lease + `","headers":{},"leaseId":"` + lease + `","kind":"stream-start"}`)
	if end := reader.await(t, 5*time.Second, "the pull's end", ofStream("0b000000-0000-4000-8000-000000000001", "stream-end")); end["status"] != float64(200) {
		t.Fatalf("today's pull must be served: %v", end)
	}
	for i, path := range []string{"http://127.0.0.1:" + p, "http://localhost:" + p + "/", "http://[::1]:" + p} {
		id := fmt.Sprintf("0c000000-0000-4000-8000-%012d", i)
		raw(`{"id":"` + id + `","method":"GET","path":"` + path + `","headers":{},"kind":"stream-start"}`)
		reader.await(t, 5*time.Second, "the mirror for "+path, ofStream(id, "stream-chunk"))
		raw(`{"id":"` + id + `","kind":"stream-cancel"}`)
		select {
		case <-mirror.left:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the cancelled stream's viewer never left", path)
		}
	}
	if farm.upgrades.Load() != 1 {
		t.Fatalf("today's frames must never end the tunnel (upgrades %d)", farm.upgrades.Load())
	}
	for _, refusal := range []string{"refused", "will not open", "does not start", "only GET /status", "larger than", "waiting"} {
		if strings.Contains(out.String(), refusal) {
			t.Fatalf("today's farm met a refusal (%q):\n%s", refusal, out.String())
		}
	}
}
