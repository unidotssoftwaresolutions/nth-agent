package connect_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── A GATEWAY'S ANSWER IS NOT THE FARM'S (NTH_AGENT_0_4_4 item 1) ───────────────────────────────────────────────
//
// The operator saw it on 2026-10-24: the farm's API restarted, nginx in front of it answered 502 with its own page, and the
// agent said "the farm did not take this machine's heartbeat (502)", "the farm refused the drive tunnel (502)", then "the
// farm took this machine's heartbeat". Nothing refused anything; the farm was not there behind its address. These seals
// drive the real loop and the real tunnel against a fake that answers as nginx does in front of an absent farm, then as
// the farm.

// doorFarm answers register, heartbeat and the tunnel's upgrade as the test sets each (nil takes the call), and records
// every register and heartbeat answer in order.
type doorFarm struct {
	server    *httptest.Server
	register  atomic.Pointer[answer]
	heartbeat atomic.Pointer[answer]
	tunnel    atomic.Pointer[answer]
	conn      atomic.Pointer[websocket.Conn]
	upgrades  atomic.Int32
	mu        sync.Mutex
	answered  []int // the status of each register and heartbeat, in order
}

func newDoorFarm(t *testing.T) *doorFarm {
	f := &doorFarm{}
	reply := func(w http.ResponseWriter, a *answer, data string) {
		status := http.StatusOK
		if a != nil {
			status = a.status
		}
		f.mu.Lock()
		f.answered = append(f.answered, status)
		f.mu.Unlock()
		if a == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"success":true,"data":`+data+`}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			reply(w, f.register.Load(), `{"nodeId":"`+limitNode+`"}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			_, _ = io.Copy(io.Discard, r.Body)
			reply(w, f.heartbeat.Load(), `{"provisionTasks":[],"commands":[]}`)
		case strings.HasSuffix(r.URL.Path, "/tunnel"):
			f.upgrades.Add(1)
			if a := f.tunnel.Load(); a != nil {
				w.WriteHeader(a.status)
				_, _ = io.WriteString(w, a.body)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			f.conn.Store(conn)
			for {
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

func (f *doorFarm) calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.answered...)
}

// told is the tunnel's real state, with each word the loop tells it recorded in order.
type told struct {
	*tunnel.State
	mu    sync.Mutex
	words []string
}

func (e *told) record(word string) { e.mu.Lock(); e.words = append(e.words, word); e.mu.Unlock() }
func (e *told) FarmAnswered()      { e.record("answered"); e.State.FarmAnswered() }
func (e *told) FarmTurnedAway()    { e.record("turned away"); e.State.FarmTurnedAway() }
func (e *told) FarmDidNotAnswer()  { e.record("did not answer"); e.State.FarmDidNotAnswer() }
func (e *told) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.words...)
}

func gateway(status int) *answer {
	return &answer{status: status, body: farmtest.GatewayPage(status)}
}

func TestAGatewayInFrontOfAnAbsentFarmIsTheFarmNotReached(t *testing.T) {
	said := farmtest.Read(t)
	f := newDoorFarm(t)
	var out syncBuilder
	state := &told{State: tunnel.NewState()}
	drive := tunnel.New(tunnel.Config{FarmURL: f.server.URL, NodeID: limitNode, Token: testToken, AppiumURL: "http://127.0.0.1:1",
		ReconnectFloor: 20 * time.Millisecond, ReconnectCap: 40 * time.Millisecond, ReconnectCapAtTheLimit: 40 * time.Millisecond,
		State: state.State, Out: &out})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	var once sync.Once
	cfg := connect.Config{
		Client:   &farm.Client{BaseURL: f.server.URL, Token: testToken, HTTP: f.server.Client()},
		NodeID:   limitNode,
		HostName: "omers-macbook",
		Probes:   func(context.Context) []inventory.Outcome { return nil },
		Interval: 20 * time.Millisecond, BackoffBase: 10 * time.Millisecond, BackoffCeiling: 40 * time.Millisecond,
		Out: &out,
	}
	cfg.Sleep = func(ctx context.Context, d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	cfg.Driver = &connect.Driver{AppiumURL: "http://127.0.0.1:1", Probe: func(context.Context) error { return nil },
		TunnelUp: drive.Connected, TunnelEverUp: drive.EverConnected, Evidence: state}
	cfg.OnRegistered = func() { once.Do(func() { go func() { drive.Run(ctx); done <- struct{}{} }() }) }
	go func() { connect.Run(ctx, cfg); done <- struct{}{} }()
	defer func() {
		cancel()
		for range 2 {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the agent did not stop")
				return
			}
		}
	}()
	within := func(what string, condition func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !condition(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("never: %s — the agent said:\n%s", what, out.String())
			}
		}
	}
	saying := func(what string, n int) {
		t.Helper()
		within(what, func() bool { return strings.Count(out.String(), what) >= n })
	}
	beats := func(n int) {
		t.Helper()
		from := len(f.calls())
		within("more heartbeats", func() bool { return len(f.calls()) >= from+n+1 })
	}
	saying("drive tunnel established", 1)
	up := out.String()

	// THE FARM RESTARTS: its socket goes, and nginx answers everything in its place — 502, then 504, then 503.
	f.heartbeat.Store(gateway(http.StatusBadGateway))
	f.tunnel.Store(gateway(http.StatusBadGateway))
	_ = f.conn.Load().CloseNow()
	beats(3)
	upgrades := f.upgrades.Load()
	within("upgrades nginx answered", func() bool { return f.upgrades.Load() >= upgrades+2 })
	f.heartbeat.Store(gateway(http.StatusGatewayTimeout))
	f.tunnel.Store(gateway(http.StatusGatewayTimeout))
	beats(3)
	f.heartbeat.Store(gateway(http.StatusServiceUnavailable))
	f.tunnel.Store(gateway(http.StatusServiceUnavailable))
	beats(3)
	// …AND IT IS BACK.
	f.heartbeat.Store(nil)
	f.tunnel.Store(nil)
	saying("connection to the farm restored", 1)
	saying("the drive tunnel is back", 1)
	outage := strings.TrimPrefix(out.String(), up)
	lines := strings.Split(strings.TrimSpace(outage), "\n")
	want := []string{
		"cannot reach the farm (the farm's address answered 502, not the farm) — retrying with backoff",
		"connection to the farm restored",
	}
	var loop []string
	for _, line := range lines {
		if strings.Contains(line, "farm") && !strings.Contains(line, "drive tunnel") {
			loop = append(loop, line)
		}
	}
	if strings.Join(loop, "\n") != strings.Join(want, "\n") {
		t.Fatalf("a restart behind nginx: the farm was not reached, said once, then back — the loop said:\n%s\nwant:\n%s",
			strings.Join(loop, "\n"), strings.Join(want, "\n"))
	}
	for _, never := range []string{"refused", "did not take", "the farm took", "busy", "nginx", "Bad Gateway"} {
		if strings.Contains(outage, never) {
			t.Fatalf("a restart behind nginx said %q:\n%s", never, outage)
		}
	}
	if strings.Count(outage, "the drive tunnel dropped") != 1 {
		t.Fatalf("the tunnel says its drop, once:\n%s", outage)
	}

	// WHAT THE TUNNEL'S STATE HEARD: every heartbeat nginx answered is the farm not answering, as before.
	heard := state.all()
	for i, status := range f.calls() {
		if i >= len(heard) {
			break
		}
		wantWord := "answered"
		if status != http.StatusOK {
			wantWord = "did not answer"
		}
		if heard[i] != wantWord {
			t.Fatalf("call %d was answered %d, and the tunnel's state heard %q (want %q): %v", i+1, status, heard[i], wantWord, heard)
		}
	}

	// THE FARM'S OWN ANSWERS ARE ITS OWN: a 503 with its sentence, a 429 with its sentence, at the heartbeat and the tunnel.
	back := out.String()
	f.heartbeat.Store(f2(http.StatusServiceUnavailable, said.HeartbeatInFlight))
	beats(2)
	f.heartbeat.Store(f2(http.StatusTooManyRequests, said.TooManyDevices))
	beats(2)
	f.tunnel.Store(f2(http.StatusTooManyRequests, said.TooManyTunnels))
	_ = f.conn.Load().CloseNow()
	saying("the farm refused the drive tunnel (429)", 1)
	f.heartbeat.Store(nil)
	f.tunnel.Store(nil)
	saying("the drive tunnel is back", 2)
	saying("the farm took this machine's heartbeat", 1)
	farms := strings.TrimPrefix(out.String(), back)
	for _, line := range []string{
		"the farm did not apply this heartbeat (503). It says: " + quoted(said.HeartbeatInFlight) + " — waiting for the next tick",
		"the farm did not take this machine's heartbeat (429). It says: " + quoted(said.TooManyDevices) + " — retrying with backoff",
		"the farm refused the drive tunnel (429). It says: " + quoted(said.TooManyTunnels),
		"the farm took this machine's heartbeat",
	} {
		if strings.Count(farms, line) != 1 {
			t.Fatalf("the farm's own answer keeps its words, once:\n  %s\nin:\n%s", line, farms)
		}
	}
	if strings.Contains(farms, "cannot reach the farm") || strings.Contains(farms, "connection to the farm restored") {
		t.Fatalf("the farm answered, and the person was told it could not be reached:\n%s", farms)
	}
}

// f2 is the farm's own answer: status, in its envelope, with its sentence.
func f2(status int, sentence string) *answer {
	return &answer{status: status, body: farmtest.Envelope(sentence)}
}

// ── HOW SOON IT CALLS AGAIN, AND WHAT THE TUNNEL'S STATE HEARS: as before ──────────────────────────────────────

// A gateway's 502 and 504 back off as a network failure does; its 503 waits for the tick, as a 503 always did. Each is
// "the farm did not answer" to the tunnel's state. Said once, at registration and at heartbeat, and then "connection to the
// farm restored".
func TestAGatewaysAnswerKeepsTodaysWaitsAndIsSaidOnce(t *testing.T) {
	for _, c := range []struct {
		at     string
		status int
		tail   string
	}{
		{"register", http.StatusBadGateway, "retrying with backoff"},
		{"register", http.StatusGatewayTimeout, "retrying with backoff"},
		{"register", http.StatusServiceUnavailable, "retrying next tick"},
		{"heartbeat", http.StatusBadGateway, "retrying with backoff"},
		{"heartbeat", http.StatusGatewayTimeout, "retrying with backoff"},
		{"heartbeat", http.StatusServiceUnavailable, "retrying next tick"},
	} {
		name := c.at + " " + http.StatusText(c.status)
		f := newLimitFarm(t)
		var out strings.Builder
		w := &waits{}
		interval := time.Second
		cfg := limitConfig(f, &out, w, interval, time.Millisecond, 8*time.Millisecond, 1)
		evidence := &recordedEvidence{}
		cfg.Driver = &connect.Driver{Probe: func(context.Context) error { return nil }, TunnelUp: func() bool { return true }, Evidence: evidence}
		page := &answer{status: c.status, body: farmtest.GatewayPage(c.status)}
		counter := &f.heartbeats
		if c.at == "register" {
			f.register.Store(page)
			counter = &f.registers
			cfg.MaxTicks = 1
		} else {
			f.heartbeat.Store(page)
			cfg.MaxTicks = 6
		}
		cfg.Sleep = func(ctx context.Context, d time.Duration) {
			w.sleep(ctx, d)
			if counter.Load() == 5 {
				f.register.Store(accepted)
				f.heartbeat.Store(accepted)
			}
		}
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("%s: run: %v", name, err)
		}
		transcript := out.String()
		line := "cannot reach the farm (the farm's address answered " + strconv.Itoa(c.status) + ", not the farm) — " + c.tail + "\n"
		if strings.Count(transcript, line) != 1 || strings.Count(transcript, "connection to the farm restored\n") != 1 ||
			strings.Index(transcript, line) > strings.Index(transcript, "connection to the farm restored") {
			t.Fatalf("%s: five answers from the farm's address, said once, then that the farm is back:\n%s", name, transcript)
		}
		for _, never := range []string{"refused", "did not take", "did not apply", "the farm took", "busy"} {
			if strings.Contains(transcript, never) {
				t.Fatalf("%s: said %q:\n%s", name, never, transcript)
			}
		}
		backs := w.all()
		if len(backs) < 5 {
			t.Fatalf("%s: one wait per call nginx answered: %v", name, backs)
		}
		for i, d := range backs[:5] {
			if c.status == http.StatusServiceUnavailable {
				if d != interval {
					t.Fatalf("%s: a 503 waits for the tick, as it did: %v", name, backs)
				}
				continue
			}
			floor := min(time.Millisecond<<i, 8*time.Millisecond)
			if d < floor || d > floor*3/2 {
				t.Fatalf("%s: wait %d was %v — a gateway's %d backs off as a network failure does (%v…%v): %v", name, i+1, d, c.status, floor, floor*3/2, backs)
			}
		}
		evidence.mu.Lock()
		silent, answered := evidence.silent, evidence.answered+evidence.turnedAway
		evidence.mu.Unlock()
		if silent != 5 || answered < 1 {
			t.Fatalf("%s: each answer from the farm's address is the farm not answering, for the tunnel's state: %d of 5 (%d answered)", name, silent, answered)
		}
	}
}
