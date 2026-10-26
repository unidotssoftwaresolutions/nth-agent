package connect_test

import (
	"context"
	"encoding/json"
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
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO ─────────────────────────────────────────────────────────────────────
//
// Since A_NODE_THAT_CANNOT_WEAR_THE_FARM_DOWN the farm refuses a workspace's machine with a plain sentence when it goes
// past a limit: 429 for too many machines, devices or tunnels, 503 for a heartbeat too soon or still being applied. The
// agent read a refusal's body only on a 401, so a person at a limit read "cannot reach the farm (the farm answered 429)"
// — about a farm that had answered — and never which limit, or what to do. These seals drive the real loop against a
// farm that answers with the farm's own sentences, read from the farm's tree (internal/farmtest).

const limitNode = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// answer is one way the fake farm answers a call: a status and a body ("" sends none).
type answer struct {
	status int
	body   string
}

var accepted = &answer{status: http.StatusOK}

// limitFarm answers register, heartbeat and the tunnel's upgrade as the test sets each, and counts every call.
type limitFarm struct {
	server     *httptest.Server
	register   atomic.Pointer[answer]
	heartbeat  atomic.Pointer[answer]
	tunnel     atomic.Pointer[answer] // accepted = upgrade
	registers  atomic.Int32
	heartbeats atomic.Int32
	upgrades   atomic.Int32 // upgrade attempts, accepted or refused
	mu         sync.Mutex
	beats      []string
}

func newLimitFarm(t *testing.T) *limitFarm {
	f := &limitFarm{}
	f.register.Store(accepted)
	f.heartbeat.Store(accepted)
	f.tunnel.Store(accepted)
	reply := func(w http.ResponseWriter, a *answer, data string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.status)
		if a.status == http.StatusOK {
			_, _ = io.WriteString(w, `{"success":true,"data":`+data+`}`)
			return
		}
		_, _ = io.WriteString(w, a.body)
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			f.registers.Add(1)
			reply(w, f.register.Load(), `{"nodeId":"`+limitNode+`"}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			f.heartbeats.Add(1)
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.beats = append(f.beats, string(body))
			f.mu.Unlock()
			reply(w, f.heartbeat.Load(), `{"provisionTasks":[],"commands":[]}`)
		case strings.HasSuffix(r.URL.Path, "/tunnel"):
			f.upgrades.Add(1)
			if a := f.tunnel.Load(); a != accepted {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(a.status)
				_, _ = io.WriteString(w, a.body)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
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

func (f *limitFarm) said(status int, sentence string) *answer {
	return &answer{status: status, body: farmtest.Envelope(sentence)}
}

// lastBeatsSay reports whether the last n heartbeats all declared this reachability.
func (f *limitFarm) lastBeatsSay(n int, reachability string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.beats) < n {
		return false
	}
	for _, body := range f.beats[len(f.beats)-n:] {
		var beat map[string]any
		if json.Unmarshal([]byte(body), &beat) != nil || beat["reachability"] != reachability {
			return false
		}
	}
	return true
}

// waits records every wait the loop asks for, and waits for none of them.
type waits struct {
	mu sync.Mutex
	d  []time.Duration
}

func (w *waits) sleep(_ context.Context, d time.Duration) {
	w.mu.Lock()
	w.d = append(w.d, d)
	w.mu.Unlock()
}

func (w *waits) all() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.d...)
}

// limitConfig is the loop at the given timings, against f, with no phones (so nothing is narrated but the farm's answers).
func limitConfig(f *limitFarm, out io.Writer, w *waits, interval, base, ceiling time.Duration, maxTicks int) connect.Config {
	return connect.Config{
		Client:   &farm.Client{BaseURL: f.server.URL, Token: testToken, HTTP: f.server.Client()},
		NodeID:   limitNode,
		HostName: "omers-macbook",
		Probes:   func(context.Context) []inventory.Outcome { return nil },
		Interval: interval, BackoffBase: base, BackoffCeiling: ceiling,
		MaxTicks: maxTicks,
		Sleep:    w.sleep,
		Out:      out,
	}
}

// afterRegistration is every line said after "registered as node", which is where the farm's answers are narrated.
func afterRegistration(out string) []string {
	_, rest, _ := strings.Cut(out, "registered as node "+limitNode+"\n")
	var lines []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// quoted is how a line carries the farm's sentence: in quotation marks, whole.
func quoted(sentence string) string { return "“" + sentence + "”" }

// ── item 2: A 429 IS THE FARM ANSWERING, NOT THE FARM GONE ──────────────────────────────────────────────────────

// At registration: a new machine past the workspace's machines. The person reads which limit and what to do, in the
// farm's words; the agent keeps asking, backing off as it does for a network failure, and never stops.
func TestA429AtRegistrationSaysTheFarmsSentenceAndKeepsAsking(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.register.Store(f.said(http.StatusTooManyRequests, said.TooManyMachines))
	var out strings.Builder
	w := &waits{}
	cfg := limitConfig(f, &out, w, time.Second, time.Millisecond, 8*time.Millisecond, 1)
	cfg.Sleep = func(ctx context.Context, d time.Duration) {
		w.sleep(ctx, d)
		if f.registers.Load() == 6 {
			f.register.Store(accepted) // the person freed a place
		}
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("a machine at its workspace's limit stopped the agent: %v\n%s", err, out.String())
	}
	transcript := out.String()
	if strings.Count(transcript, quoted(said.TooManyMachines)) != 1 || !strings.Contains(transcript, "(429)") {
		t.Fatalf("the farm said why it did not add this machine, and the person should read it once, with its status:\n%s", transcript)
	}
	if strings.Contains(transcript, "cannot reach the farm") {
		t.Fatalf("the farm answered, and the person was told it could not be reached:\n%s", transcript)
	}
	if got := f.registers.Load(); got != 7 {
		t.Fatalf("the agent asks again until the farm adds the machine: %d registrations", got)
	}
	backs := w.all()
	if len(backs) != 7 || backs[6] != time.Second {
		t.Fatalf("one backoff per refused registration, then the tick after the first heartbeat, got %v", backs)
	}
	backs = backs[:6]
	for i, d := range backs {
		floor := min(time.Millisecond<<i, 8*time.Millisecond)
		if d < floor || d > floor*3/2 {
			t.Fatalf("wait %d was %v: a 429 backs off like a network failure, doubling from the base to the ceiling (%v…%v): %v",
				i+1, d, floor, floor*3/2, backs)
		}
	}
}

// At heartbeat: a report past one machine's devices. The node is not re-registered and the agent does not stop; the
// tunnel's state hears that the farm answered; and no heartbeat is sent sooner than the tick, because a heartbeat the
// farm counted (a 429 is counted) followed a second later meets the farm's own pace and is answered 503.
func TestA429AtHeartbeatIsTheFarmAnsweringNotTheFarmGone(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.heartbeat.Store(f.said(http.StatusTooManyRequests, said.TooManyDevices))
	var out strings.Builder
	w := &waits{}
	interval := 10 * time.Millisecond
	cfg := limitConfig(f, &out, w, interval, time.Millisecond, 40*time.Millisecond, 12)
	evidence := &recordedEvidence{}
	cfg.Driver = &connect.Driver{Probe: func(context.Context) error { return nil }, TunnelUp: func() bool { return false },
		Evidence: evidence}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("a machine past its device limit stopped the agent: %v", err)
	}
	transcript := out.String()
	if strings.Count(transcript, quoted(said.TooManyDevices)) != 1 || strings.Contains(transcript, "cannot reach the farm") {
		t.Fatalf("the farm said why it applied nothing, once, and it was reached:\n%s", transcript)
	}
	if f.registers.Load() != 1 || f.heartbeats.Load() != 12 {
		t.Fatalf("a 429 is never answered by re-registering: %d registrations, %d heartbeats", f.registers.Load(), f.heartbeats.Load())
	}
	evidence.mu.Lock()
	silent, answered := evidence.silent, evidence.answered+evidence.turnedAway
	evidence.mu.Unlock()
	if silent != 0 || answered != 13 {
		t.Fatalf("each 429 is the farm answering, for the tunnel's state: %d answered (registration and 12 heartbeats), %d told it did not", answered, silent)
	}
	backs := w.all()
	reached := false
	for i, d := range backs {
		if d < interval {
			t.Fatalf("wait %d after a 429 heartbeat was %v, sooner than the tick (%v): %v", i+1, d, interval, backs)
		}
		if d > 60*time.Millisecond {
			t.Fatalf("wait %d was %v, past the ceiling and its jitter: %v", i+1, d, backs)
		}
		reached = reached || d >= 40*time.Millisecond
	}
	if !reached {
		t.Fatalf("the waits never grew to the ceiling: %v", backs)
	}
}

// ── item 7: SAID ONCE, NOT EVERY TICK — a day at a limit ────────────────────────────────────────────────────────

// A day of waits, at the production timings, with nothing actually waited for: the loop's own clock.
func aDayAt(t *testing.T, f *limitFarm) (lines []string, heartbeats int32) {
	t.Helper()
	var out strings.Builder
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var day time.Duration
	cfg := limitConfig(f, &out, &waits{}, connect.DefaultInterval, time.Second, time.Minute, 0)
	cfg.Sleep = func(_ context.Context, d time.Duration) {
		if day += d; day >= 24*time.Hour {
			cancel()
		}
	}
	done := make(chan error, 1)
	go func() { done <- connect.Run(ctx, cfg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a day at a limit stopped the agent: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("a day of the loop's own clock did not end in a minute of the test's")
	}
	return afterRegistration(out.String()), f.heartbeats.Load()
}

func TestADayAtALimitIsAHandfulOfLines(t *testing.T) {
	said := farmtest.Read(t)

	f := newLimitFarm(t)
	f.heartbeat.Store(f.said(http.StatusTooManyRequests, said.TooManyDevices))
	lines, beats := aDayAt(t, f)
	if len(lines) > 3 || !strings.Contains(strings.Join(lines, "\n"), quoted(said.TooManyDevices)) {
		t.Fatalf("a day at the device limit: %d lines, and the farm's sentence must be one of them:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	// Backing off to a minute, with up to half again as jitter: at most one heartbeat a minute once grown, never more
	// than one a tick before.
	if beats > 24*60+30 || beats < 24*40 {
		t.Fatalf("a day at the device limit made %d heartbeats: about one a minute is the farm's ask", beats)
	}
	t.Logf("a day at a 429: %d heartbeats, %d line(s)", beats, len(lines))

	f = newLimitFarm(t)
	f.heartbeat.Store(f.said(http.StatusServiceUnavailable, said.HeartbeatInFlight))
	lines, beats = aDayAt(t, f)
	if len(lines) > 3 || !strings.Contains(strings.Join(lines, "\n"), quoted(said.HeartbeatInFlight)) {
		t.Fatalf("a day of 503s: %d lines, and the farm's sentence must be one of them:\n%s", len(lines), strings.Join(lines[:min(len(lines), 8)], "\n"))
	}
	// A 503 waits for the next tick, as the farm asks: one heartbeat a tick.
	if beats < 8600 || beats > 8641 {
		t.Fatalf("a day of 503s made %d heartbeats: one a tick (8,640) is what the farm asks", beats)
	}
	t.Logf("a day at a 503: %d heartbeats, %d line(s)", beats, len(lines))
}

// A refusal is said when it starts and again only when the farm's sentence changes; the farm taking the calls again is
// said once, when it ends.
func TestARefusalIsSaidAgainOnlyWhenTheFarmsSentenceChanges(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	script := []*answer{}
	for range 4 {
		script = append(script, f.said(http.StatusTooManyRequests, said.TooManyDevices))
	}
	for range 4 {
		script = append(script, f.said(http.StatusServiceUnavailable, said.RegistryBusy))
	}
	for range 4 {
		script = append(script, f.said(http.StatusTooManyRequests, said.TooManyDevices))
	}
	for range 4 {
		script = append(script, accepted)
	}
	var out strings.Builder
	w := &waits{}
	cfg := limitConfig(f, &out, w, time.Millisecond, time.Millisecond, 2*time.Millisecond, len(script))
	f.heartbeat.Store(script[0])
	cfg.Sleep = func(ctx context.Context, d time.Duration) {
		w.sleep(ctx, d)
		if n := int(f.heartbeats.Load()); n < len(script) {
			f.heartbeat.Store(script[n])
		}
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := afterRegistration(out.String())
	if len(lines) != 4 {
		t.Fatalf("three refusals, each said once when it started, and one line when the farm took the calls — got %d:\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	for i, sentence := range []string{said.TooManyDevices, said.RegistryBusy, said.TooManyDevices} {
		if !strings.Contains(lines[i], quoted(sentence)) {
			t.Fatalf("line %d should carry the farm's sentence %q:\n%s", i+1, sentence, strings.Join(lines, "\n"))
		}
	}
	if strings.Contains(lines[3], "“") || strings.Contains(lines[3], "cannot reach") {
		t.Fatalf("the last line says the farm takes the calls, in the agent's own words: %q", lines[3])
	}
}

// ── item 3: A 503 SAYS WHAT THE FARM SAID ───────────────────────────────────────────────────────────────────────

func TestA503SaysTheFarmsSentenceOrTodaysLine(t *testing.T) {
	said := farmtest.Read(t)
	for name, c := range map[string]struct {
		body, want string
	}{
		"the farm's sentence": {farmtest.Envelope(said.HeartbeatTooSoon(1.2)), quoted(said.HeartbeatTooSoon(1.2))},
		// (A gateway's 503 page is not the farm's answer since NTH_AGENT_0_4_4: a_gateways_answer_is_not_the_farms_test.go.)
		"the farm's envelope, no sentence": {farmtest.EnvelopeSayingNothing, "the farm is busy; nothing was applied this tick — waiting for the next one"},
	} {
		f := newLimitFarm(t)
		f.heartbeat.Store(&answer{status: http.StatusServiceUnavailable, body: c.body})
		var out strings.Builder
		w := &waits{}
		cfg := limitConfig(f, &out, w, 10*time.Millisecond, time.Millisecond, 40*time.Millisecond, 6)
		evidence := &recordedEvidence{}
		cfg.Driver = &connect.Driver{Probe: func(context.Context) error { return nil }, TunnelUp: func() bool { return false },
			Evidence: evidence}
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("%s: run: %v", name, err)
		}
		if strings.Count(out.String(), c.want) != 1 || strings.Contains(out.String(), "Service Temporarily") {
			t.Fatalf("%s: six 503s, said once as %q:\n%s", name, c.want, out.String())
		}
		for _, d := range w.all() {
			if d != 10*time.Millisecond {
				t.Fatalf("%s: a 503 waits for the next tick, no more and no less: %v", name, w.all())
			}
		}
		evidence.mu.Lock()
		silent := evidence.silent
		evidence.mu.Unlock()
		if silent != 6 {
			t.Fatalf("%s: a 503 tells the tunnel's state nothing about the tunnel (a gateway gives one too): %d of 6", name, silent)
		}
	}
}

// ── item 4: ANY OTHER ANSWER NAMES ITS STATUS, AND THE FARM WAS REACHED ─────────────────────────────────────────

func TestAnotherAnswerNamesItsStatusAndNeverSaysTheFarmCannotBeReached(t *testing.T) {
	for name, c := range map[string]struct {
		at    string
		reply answer
		want  string
	}{
		"nginx's 413 at heartbeat": {"heartbeat", answer{http.StatusRequestEntityTooLarge,
			"<html>\r\n<head><title>413 Request Entity Too Large</title></head>\r\n<body>nginx</body></html>"},
			"(413) and gave no reason this agent could read"},
		"the farm's 400 at registration": {"register", answer{http.StatusBadRequest, farmtest.Envelope("A registration body is required.")},
			"(400). It says: " + quoted("A registration body is required.")},
		// (A gateway's 502 page is the farm not reached since NTH_AGENT_0_4_4: a_gateways_answer_is_not_the_farms_test.go.)
		"the farm's own 502 at heartbeat": {"heartbeat", answer{http.StatusBadGateway, farmtest.EnvelopeSayingNothing}, "(502) and gave no reason this agent could read"},
	} {
		f := newLimitFarm(t)
		var out strings.Builder
		w := &waits{}
		cfg := limitConfig(f, &out, w, time.Second, time.Millisecond, 8*time.Millisecond, 5)
		refused := 5
		if c.at == "register" {
			refused = 3
			f.register.Store(&c.reply)
			cfg.Sleep = func(ctx context.Context, d time.Duration) {
				w.sleep(ctx, d)
				if f.registers.Load() == 3 {
					f.register.Store(accepted)
				}
			}
		} else {
			f.heartbeat.Store(&c.reply)
		}
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("%s: run: %v", name, err)
		}
		transcript := out.String()
		if strings.Count(transcript, c.want) != 1 || strings.Contains(transcript, "cannot reach the farm") ||
			strings.Contains(transcript, "nginx") {
			t.Fatalf("%s: the line names the status and what the farm said, once, and the farm was reached:\n%s", name, transcript)
		}
		backs := w.all()
		if len(backs) < refused {
			t.Fatalf("%s: one wait per refused call: %v", name, backs)
		}
		for i, d := range backs[:refused] {
			if d >= time.Second {
				t.Fatalf("%s: wait %d was the tick (%v) — another answer keeps today's backoff: %v", name, i+1, d, w.all())
			}
		}
	}
}

// ── items 2 and 5 together: the real tunnel, its real state, and a farm at its limits ──────────────────────────

// The heartbeat at the device limit and the tunnel at the tunnel limit. The farm answers every heartbeat, so the tunnel's
// failed upgrades are evidence against the tunnel, and the farm is told it is down. And a 429 does not wake the tunnel's
// reconnect: it dials on its own backoff, no faster than it did when a 429 was a heartbeat with no answer.
func TestALimitAtTheHeartbeatIsAnAnswerAndDoesNotHurryTheTunnel(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.heartbeat.Store(f.said(http.StatusTooManyRequests, said.TooManyDevices))
	f.tunnel.Store(f.said(http.StatusTooManyRequests, said.TooManyTunnels))
	var out syncBuilder
	floor := 200 * time.Millisecond
	state := tunnel.NewState()
	drive := tunnel.New(tunnel.Config{FarmURL: f.server.URL, NodeID: limitNode, Token: testToken, AppiumURL: "http://127.0.0.1:1",
		ReconnectFloor: floor, ReconnectCap: floor, State: state, Out: &out})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	var once sync.Once
	cfg := limitConfig(f, &out, &waits{}, 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond, 0)
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

	started := time.Now()
	// Five heartbeats in a row say it: one could say it on a race today, before the first 429 voided the evidence.
	eventually(t, 5*time.Second, "five heartbeats in a row that say the tunnel is down", func() bool {
		return f.lastBeatsSay(5, farm.ReachabilityTunnelDown)
	})
	time.Sleep(time.Second)
	window := time.Since(started)
	// Its own backoff alone allows one upgrade per floor, plus the first and the jitter's slack. A wake on every 429
	// heartbeat (one every 20 ms here) would be ten times that.
	if most := int32(window/floor) + 2; f.upgrades.Load() > most {
		t.Fatalf("the tunnel dialled %d times in %v at a %v backoff (at most %d) — a 429 heartbeat hurried its reconnect",
			f.upgrades.Load(), window.Round(time.Millisecond), floor, most)
	}
	if strings.Contains(out.String(), "dialling the drive tunnel now") {
		t.Fatalf("a 429 woke the tunnel's reconnect:\n%s", out.String())
	}
}
