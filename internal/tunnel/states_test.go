package tunnel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── THE THREE-STATES SEAL: never connected yet → established → dropped → back, in that order only ────────────

// A farm that answers the tunnel upgrade 404 until `accepting` is set (a node it has not seen yet), then
// accepts; the test closes accepted sockets server-side to produce a real drop.
type gatedFarm struct {
	accepting atomic.Bool
	conns     chan *websocket.Conn
}

func newGatedFarm(t *testing.T) (*gatedFarm, *httptest.Server) {
	g := &gatedFarm{conns: make(chan *websocket.Conn, 4)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.accepting.Load() {
			w.WriteHeader(http.StatusNotFound) // the farm has never seen this node
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		g.conns <- conn
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return g, server
}

// syncWriter lets the test read narration while the tunnel goroutine writes it.
type syncWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestTheThreeTunnelSentencesAppearInTheOnlyOrderTheyCan(t *testing.T) {
	farm, server := newGatedFarm(t)
	var out syncWriter
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: server.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-T", AppiumURL: "http://127.0.0.1:1",
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

	// 1) never connected: the farm 404s. The sentence is "not connected yet" — NEVER "dropped", never "back".
	waitFor(t, "not connected yet", func() bool { return strings.Contains(out.String(), "not connected yet") })
	if strings.Contains(out.String(), "dropped") || strings.Contains(out.String(), "is back") {
		t.Fatalf("a socket that never existed cannot drop or come back:\n%s", out.String())
	}
	if drive.EverConnected() {
		t.Fatal("EverConnected must be false before the first establishment")
	}

	// 2) the farm now knows the node: the first connection is "established" — exactly once, and not "back".
	farm.accepting.Store(true)
	first := waitConnFrom(t, farm.conns)
	waitFor(t, "drive tunnel established", func() bool { return strings.Contains(out.String(), "drive tunnel established") })
	if strings.Contains(out.String(), "is back") {
		t.Fatalf("the first connection is established, not back:\n%s", out.String())
	}
	if !drive.EverConnected() {
		t.Fatal("EverConnected must be true after the first establishment")
	}

	// 3) a real drop: the farm closes the socket → "dropped"; the reconnect → "is back".
	first.Close(websocket.StatusGoingAway, "farm restart")
	waitFor(t, "the drive tunnel dropped", func() bool { return strings.Contains(out.String(), "the drive tunnel dropped") })
	waitConnFrom(t, farm.conns)
	waitFor(t, "the drive tunnel is back", func() bool { return strings.Contains(out.String(), "the drive tunnel is back") })

	// The order, read back as a transcript.
	transcript := out.String()
	idx := func(s string) int { return strings.Index(transcript, s) }
	if !(idx("not connected yet") < idx("drive tunnel established") && idx("drive tunnel established") < idx("the drive tunnel dropped") && idx("the drive tunnel dropped") < idx("the drive tunnel is back")) {
		t.Fatalf("the three sentences must appear in their only possible order:\n%s", transcript)
	}
	if strings.Count(transcript, "drive tunnel established") != 1 {
		t.Fatalf("established is said exactly once:\n%s", transcript)
	}
}

func waitConnFrom(t *testing.T, conns chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case c := <-conns:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the agent never dialed the tunnel")
		return nil
	}
}
