package tunnel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// NTH_AGENT_0_4_2 item 1 — A TUNNEL AT THE FARM'S TUNNEL LIMIT WAITS ITS OWN BACKOFF, at production timing. Every wait the
// tunnel takes at the limit goes through its Sleep, which this seal records instead of waiting out, while a heartbeat the
// farm takes is said to the tunnel's state every 2 ms. The other waits are real, and short.

// limitWait is one wait the tunnel took at the limit, after which dial.
type limitWait struct {
	after int32
	d     time.Duration
}

func TestAtTheTunnelLimitTheWaitDoublesToAMinuteAndNoHeartbeatCutsItShort(t *testing.T) {
	said := farmtest.Read(t)
	// The farm: dials 1–9 at the limit; 10 a 503 (any other failure); 11 the limit again; 12 taken, and closed at once;
	// 13 on, the limit.
	var dials atomic.Int32
	var mu sync.Mutex
	dialledAt := map[int32]time.Time{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := dials.Add(1)
		mu.Lock()
		dialledAt[n] = time.Now()
		mu.Unlock()
		switch {
		case n == 10:
			time.Sleep(20 * time.Millisecond) // a heartbeat already on its way lands before the answer does
			w.WriteHeader(http.StatusServiceUnavailable)
		case n == 12:
			if conn, err := websocket.Accept(w, r, nil); err == nil {
				_ = conn.CloseNow()
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, farmtest.Envelope(said.TooManyTunnels))
		}
	}))
	t.Cleanup(server.Close)

	state := tunnel.NewState()
	var waitsMu sync.Mutex
	var waits []limitWait
	var out syncWriter
	liveTunnel(t, server.URL, &out, func(cfg *tunnel.Config) {
		cfg.ReconnectFloor, cfg.ReconnectCap = 0, 0 // production: 1 s doubling to 30 s
		cfg.State = state
		cfg.Sleep = func(_ context.Context, d time.Duration) {
			waitsMu.Lock()
			waits = append(waits, limitWait{after: dials.Load(), d: d})
			waitsMu.Unlock()
			time.Sleep(time.Millisecond)
		}
	})
	// HEARTBEATS THE FARM TAKES, every 2 ms — except while dial 10's wait runs, which is the one this seal times for real.
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				if dials.Load() != 10 {
					state.FarmAnswered()
				}
			}
		}
	}()
	waitFor(t, "thirteen dials", func() bool { return dials.Load() >= 13 })
	// The wait after the thirteenth, when there is one: a tunnel whose waits are all cut short takes none of its own.
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		waitsMu.Lock()
		seen := len(waits) > 0 && waits[len(waits)-1].after >= 13
		waitsMu.Unlock()
		if seen {
			break
		}
	}
	waitsMu.Lock()
	took := map[int32]time.Duration{}
	for _, w := range waits {
		if _, twice := took[w.after]; twice {
			t.Fatalf("two waits after dial %d: %v", w.after, waits)
		}
		took[w.after] = w.d
	}
	waitsMu.Unlock()
	const jitter = 750 * time.Millisecond
	within := func(dial int32, base time.Duration) {
		t.Helper()
		d, ok := took[dial]
		if !ok {
			t.Fatalf("the wait after dial %d (refused at the limit) was not the tunnel's own: a taken heartbeat cut it short, "+
				"or it was never taken. Waits at the limit: %v", dial, took)
		}
		if d < base || d >= base+jitter {
			t.Fatalf("the wait after dial %d was %v; at the limit it is %v plus up to %v of jitter. Waits: %v", dial, d, base, jitter, took)
		}
	}

	// 1 S DOUBLING TO A MINUTE, and no longer: a place that frees is taken on a try at most a minute later.
	base := time.Second
	for dial := int32(1); dial <= 9; dial++ {
		within(dial, base)
		if base *= 2; base > time.Minute {
			base = time.Minute
		}
	}
	// ANY OTHER FAILURE PUTS TODAY'S RECONNECT BACK: dial 10's wait is today's, from the floor (and woken by a taken
	// heartbeat, so it is not a wait at the limit at all).
	if _, ok := took[10]; ok {
		t.Fatalf("a 503 was waited out as if at the limit: %v", took)
	}
	mu.Lock()
	gap := dialledAt[11].Sub(dialledAt[10])
	mu.Unlock()
	if gap < time.Second || gap > time.Second+jitter+500*time.Millisecond {
		t.Fatalf("after a 503 the tunnel dialled again %v later; today's reconnect starts again from its 1 s floor", gap.Round(time.Millisecond))
	}
	within(11, 2*time.Second) // today's next step after it
	// AND SO DOES A TUNNEL THAT CONNECTED: dial 13, the first refusal after it, waits the floor again.
	within(13, time.Second)
	t.Logf("waits at the limit, by the dial before each: %v; after the 503: %v", took, gap.Round(time.Millisecond))
}
