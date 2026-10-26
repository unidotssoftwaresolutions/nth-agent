package connect_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A_TUNNEL_THAT_KNOWS_IT_DIED's rule 4, on the agent, end to end: the REAL connect loop, the REAL drive tunnel and its
// State, and one farm that answers the heartbeat and the tunnel upgrade from the same switches. What is asserted is
// what the farm READ — every heartbeat body — because that is what takes a machine's devices off offer.

type wholeFarm struct {
	server   *httptest.Server
	up       atomic.Bool // heartbeats answered AND the tunnel accepted — one switch, flipped at once, like a farm
	tunnelOK atomic.Bool // the tunnel upgrade accepted (when up)
	mu       sync.Mutex
	beats    []string
	conn     atomic.Pointer[websocket.Conn]
	upgrades atomic.Int32
}

func newWholeFarm(t *testing.T) *wholeFarm {
	f := &wholeFarm{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			w.Write([]byte(`{"success":true,"data":{"nodeId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","apiKey":""}}`))
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.beats = append(f.beats, string(body))
			f.mu.Unlock()
			if !f.up.Load() {
				w.WriteHeader(http.StatusServiceUnavailable) // a gateway in front of a farm that is away
				return
			}
			w.Write([]byte(`{"success":true,"data":{"provisionTasks":[],"commands":[]}}`))
		case strings.HasSuffix(r.URL.Path, "/tunnel"):
			if !f.up.Load() || !f.tunnelOK.Load() {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			f.upgrades.Add(1)
			f.conn.Store(conn)
			for { // read, so the agent's pings are answered
				if _, _, err := conn.Read(context.Background()); err != nil {
					return
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *wholeFarm) heartbeats() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.beats...)
}

// runAgent starts the connect loop with a real tunnel dialled from OnRegistered, exactly as main wires them.
func runAgent(t *testing.T, f *wholeFarm, out *syncBuilder, floor time.Duration) *tunnel.Tunnel {
	t.Helper()
	state := tunnel.NewState()
	drive := tunnel.New(tunnel.Config{
		FarmURL: f.server.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: testToken,
		AppiumURL: "http://127.0.0.1:4723", ReconnectFloor: floor, ReconnectCap: floor,
		State: state, Out: out,
	})
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	done := make(chan struct{}, 2)
	cfg := connect.Config{
		Client:   &farm.Client{BaseURL: f.server.URL, Token: testToken, HTTP: f.server.Client()},
		NodeID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		HostName: "omers-macbook",
		Probes: func(context.Context) []inventory.Outcome {
			return []inventory.Outcome{inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT30ABCDE"}}, nil)}
		},
		Interval: 30 * time.Millisecond, BackoffBase: 30 * time.Millisecond, BackoffCeiling: 30 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		},
		Out: out,
		Driver: &connect.Driver{
			AppiumURL: "http://127.0.0.1:4723", Probe: func(context.Context) error { return nil },
			TunnelUp: drive.Connected, TunnelEverUp: drive.EverConnected, Evidence: state,
		},
		OnRegistered: func() { once.Do(func() { go func() { drive.Run(ctx); done <- struct{}{} }() }) },
	}
	go func() { connect.Run(ctx, cfg); done <- struct{}{} }()
	t.Cleanup(func() {
		cancel()
		for i := 0; i < 2; i++ {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the agent did not stop")
				return
			}
		}
	})
	return drive
}

type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncBuilder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuilder) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func eventually(t *testing.T, bound time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

func TestATunnelThatCannotBeOpenedWhileTheFarmAnswersIsDeclaredDownAndUpAgain(t *testing.T) {
	f := newWholeFarm(t)
	f.up.Store(true) // the farm answers every heartbeat; the tunnel upgrade is refused
	var out syncBuilder
	drive := runAgent(t, f, &out, 20*time.Millisecond)

	eventually(t, 5*time.Second, "a heartbeat saying TunnelDown", func() bool {
		beats := f.heartbeats()
		return len(beats) > 0 && strings.Contains(beats[len(beats)-1], `"reachability":"TunnelDown"`)
	})
	if strings.Count(out.String(), "could not be opened although the farm answers") != 1 {
		t.Fatalf("the declaration is said once:\n%s", out.String())
	}

	// The tunnel can be opened again: the agent connects, and the farm is told it is up.
	f.tunnelOK.Store(true)
	eventually(t, 5*time.Second, "connected", drive.Connected)
	eventually(t, 5*time.Second, "a heartbeat saying TunnelOnly again", func() bool {
		beats := f.heartbeats()
		return strings.Contains(beats[len(beats)-1], `"reachability":"TunnelOnly"`)
	})
	eventually(t, 5*time.Second, "the up sentence", func() bool { return strings.Contains(out.String(), "the drive tunnel is up") })
}

func TestAFarmThatWasAwayNeverHearsDownAndItsAnswerWakesTheReconnect(t *testing.T) {
	f := newWholeFarm(t)
	f.up.Store(true)
	f.tunnelOK.Store(true)
	var out syncBuilder
	// A reconnect backoff of a MINUTE: only the farm's answer can bring the tunnel back inside this test's bounds.
	drive := runAgent(t, f, &out, time.Minute)
	eventually(t, 5*time.Second, "connected", drive.Connected)

	// THE FARM RESTARTS: its socket goes, its heartbeats get a gateway's 503, the upgrade is refused — for far longer
	// than the node's old five-second clock and many heartbeats.
	f.up.Store(false)
	f.conn.Load().CloseNow()
	time.Sleep(1500 * time.Millisecond)
	f.up.Store(true) // …and it is back, all of it at once
	startedBack := time.Now()

	eventually(t, 5*time.Second, "the tunnel back", func() bool { return drive.Connected() && f.upgrades.Load() == 2 })
	if took := time.Since(startedBack); took > 3*time.Second {
		t.Fatalf("the farm answered and the tunnel waited %s — its answer must wake the reconnect, not a minute's backoff", took)
	}
	time.Sleep(200 * time.Millisecond)
	for i, beat := range f.heartbeats() {
		if strings.Contains(beat, "TunnelDown") {
			t.Fatalf("heartbeat %d said TunnelDown for a farm that was simply away — every lease would end for a deploy:\n%s", i, beat)
		}
	}
	if !strings.Contains(out.String(), "the farm answered a heartbeat — dialling the drive tunnel now") {
		t.Fatalf("the woken reconnect is said:\n%s", out.String())
	}
}
