package tunnel_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// stubFarmTunnel upgrades /v1/nodes/{id}/tunnel and lets the test speak frames to the agent.
type stubFarmTunnel struct {
	t        *testing.T
	conns    chan *websocket.Conn
	token    atomic.Value
	upgrades atomic.Int32
}

func newStubFarmTunnel(t *testing.T) (*stubFarmTunnel, *httptest.Server) {
	s := &stubFarmTunnel{t: t, conns: make(chan *websocket.Conn, 4)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tunnel") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s.token.Store(r.Header.Get("X-Agent-Token"))
		s.upgrades.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s.conns <- conn
		// keep the handler alive; the test drives the socket
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return s, server
}

func (s *stubFarmTunnel) waitConn(t *testing.T) *websocket.Conn {
	select {
	case c := <-s.conns:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the agent never dialed the tunnel")
		return nil
	}
}

func sendFrame(t *testing.T, conn *websocket.Conn, frame any) {
	payload, _ := json.Marshal(frame)
	if err := conn.Write(context.Background(), websocket.MessageText, payload); err != nil {
		t.Fatalf("sending frame: %v", err)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading frame: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("frame was not JSON: %v", err)
	}
	return decoded
}

func startTunnel(t *testing.T, farmURL, appiumURL string) *tunnel.Tunnel {
	var out strings.Builder
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: farmURL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Token: "sk-agent-TUNNELSECRET", AppiumURL: appiumURL,
		ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out: &out,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go drive.Run(ctx)
	return drive
}

// ── THE ROUND-TRIP SEAL: what the stub Appium received, and what the stub farm got back — verbatim ─────────

func TestARequestFrameRoundTripsExactly(t *testing.T) {
	var received atomic.Value // what Appium saw
	appiumStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.Store(map[string]string{
			"method": r.Method, "path": r.URL.RequestURI(),
			"contentType": r.Header.Get("Content-Type"), "body": string(body),
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(`{"value":{"sessionId":"abc123"}}`))
	}))
	defer appiumStub.Close()

	stub, server := newStubFarmTunnel(t)
	startTunnel(t, server.URL, appiumStub.URL)
	conn := stub.waitConn(t)

	// The first request of a real Appium session, as the relay would send it: POST /session with the
	// W3C capabilities body — base64 on the wire, byte-exact on arrival.
	sessionBody := `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:udid":"00008030-HEALTHY"}}}`
	encoded := base64.StdEncoding.EncodeToString([]byte(sessionBody))
	sendFrame(t, conn, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "method": "POST", "path": "/session",
		"headers":    map[string]string{"Content-Type": "application/json; charset=utf-8"},
		"bodyBase64": encoded,
	})

	response := readFrame(t, conn)
	saw := received.Load().(map[string]string)
	if saw["method"] != "POST" || saw["path"] != "/session" {
		t.Fatalf("Appium must receive the frame's method and path, got %v", saw)
	}
	if saw["body"] != sessionBody {
		t.Fatalf("the body must arrive byte-exact, got %q", saw["body"])
	}
	if saw["contentType"] != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type must cross (the one header the reference forwards), got %q", saw["contentType"])
	}

	if response["id"] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("the response must correlate by id, got %v", response["id"])
	}
	if response["status"] != float64(200) {
		t.Fatalf("Appium's status must come back, got %v", response["status"])
	}
	decoded, _ := base64.StdEncoding.DecodeString(response["bodyBase64"].(string))
	if string(decoded) != `{"value":{"sessionId":"abc123"}}` {
		t.Fatalf("Appium's body must come back byte-exact, got %q", decoded)
	}
	if stub.token.Load().(string) != "sk-agent-TUNNELSECRET" {
		t.Fatal("the upgrade must carry X-Agent-Token")
	}
}

// ── THE FORWARD-TARGET SEAL: the frame goes to the CONFIGURED Appium and nowhere else ───────────────────────

func TestTheForwardTargetIsTheConfiguredAppium(t *testing.T) {
	var configuredHits, otherHits atomic.Int32
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configuredHits.Add(1)
		w.WriteHeader(200)
	}))
	defer configured.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		w.WriteHeader(200)
	}))
	defer other.Close()
	_ = other

	stub, server := newStubFarmTunnel(t)
	startTunnel(t, server.URL, configured.URL)
	conn := stub.waitConn(t)

	sendFrame(t, conn, map[string]any{"id": "22222222-2222-3333-4444-555555555555", "method": "GET", "path": "/status", "headers": map[string]string{}})
	readFrame(t, conn)

	if configuredHits.Load() != 1 || otherHits.Load() != 0 {
		t.Fatalf("the frame must reach the configured Appium only: configured=%d other=%d", configuredHits.Load(), otherHits.Load())
	}
}

// ── THE HONEST-REFUSAL SEAL: other frame families are refused in the contract's own words ───────────────────

func TestAStreamFrameGetsAnHonestRefusalNeverASilentDrop(t *testing.T) {
	appiumStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer appiumStub.Close()
	stub, server := newStubFarmTunnel(t)
	startTunnel(t, server.URL, appiumStub.URL)
	conn := stub.waitConn(t)

	sendFrame(t, conn, map[string]any{"kind": "stream-start", "id": "33333333-2222-3333-4444-555555555555",
		"method": "GET", "path": "/stream", "headers": map[string]string{}})

	end := readFrame(t, conn)
	if end["kind"] != "stream-end" || end["id"] != "33333333-2222-3333-4444-555555555555" {
		t.Fatalf("a stream-start must be answered with a stream-end for its id, got %v", end)
	}
	// Was: the refusal had to say "does not carry" — every stream was refused because none was carried. Reversed by
	// AN_AGENT_THAT_SHOWS_ITS_PHONE: the live view is carried now, and what is still refused is anything that is not a
	// phone's mirror. The rule this seal holds is unchanged — a refusal in the contract's own words, never a silence.
	if !strings.Contains(end["error"].(string), "phone's mirror and nothing else") {
		t.Fatalf("the refusal must say what this agent streams instead, got %v", end["error"])
	}
}

// ── THE CONNECTED FLAG: true while serving, false the moment the socket drops (injection 5's target) ────────

func TestConnectedTracksTheSocket(t *testing.T) {
	appiumStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer appiumStub.Close()
	stub, server := newStubFarmTunnel(t)
	drive := startTunnel(t, server.URL, appiumStub.URL)
	conn := stub.waitConn(t)

	waitFor(t, "connected", func() bool { return drive.Connected() })
	conn.Close(websocket.StatusNormalClosure, "server side goes away")
	waitFor(t, "disconnected", func() bool { return !drive.Connected() })
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never became %s", what)
}
