package connect_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── NTH_AGENT_0_4_2 item 1: A TUNNEL AT THE FARM'S TUNNEL LIMIT WAITS ITS OWN BACKOFF ─────────────────────────────────
//
// The eleventh machine of a workspace has its tunnel refused with a 429 while its heartbeats are taken. Each taken
// heartbeat woke the tunnel's reconnect, so it dialled about once per heartbeat, forever, and the farm logged a warning for
// each. The farm asks for its own backoff, 1 s doubling to a minute (NodesController, TunnelAdmission). These seals run the
// real loop and the real tunnel and state against a farm that takes every heartbeat.

// runWithATunnel runs the loop and the tunnel together, as main does, and stops both when the test ends.
func runWithATunnel(t *testing.T, f *limitFarm, out *syncBuilder, interval time.Duration, change func(*tunnel.Config)) *tunnel.Tunnel {
	t.Helper()
	state := tunnel.NewState()
	tc := tunnel.Config{FarmURL: f.server.URL, NodeID: limitNode, Token: testToken, AppiumURL: "http://127.0.0.1:1",
		State: state, Out: out}
	change(&tc)
	drive := tunnel.New(tc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	var once sync.Once
	cfg := limitConfig(f, out, &waits{}, interval, interval, interval, 0)
	cfg.Sleep = func(ctx context.Context, d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	cfg.Driver = &connect.Driver{AppiumURL: "http://127.0.0.1:1", Probe: func(context.Context) error { return nil },
		TunnelUp: drive.Connected, TunnelEverUp: drive.EverConnected, Evidence: state}
	cfg.OnRegistered = func() { once.Do(func() { go func() { drive.Run(ctx); done <- struct{}{} }() }) }
	go func() { _ = connect.Run(ctx, cfg); done <- struct{}{} }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
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

// HEARTBEATS AT 200 DO NOT HURRY A TUNNEL AT THE LIMIT, and once a place frees, a later try connects.
func TestATunnelAtTheLimitIsNotHurriedByHeartbeatsTheFarmTakes(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.tunnel.Store(f.said(http.StatusTooManyRequests, said.TooManyTunnels))
	var out syncBuilder
	floor := 20 * time.Millisecond
	drive := runWithATunnel(t, f, &out, 20*time.Millisecond, func(c *tunnel.Config) {
		c.ReconnectFloor, c.ReconnectCap = floor, 2*floor
	})

	eventually(t, 5*time.Second, "the first refused upgrade", func() bool { return f.upgrades.Load() >= 1 })
	upgrades, heartbeats := f.upgrades.Load(), f.heartbeats.Load()
	window := 2 * time.Second
	time.Sleep(window)
	dialled, beat := f.upgrades.Load()-upgrades, f.heartbeats.Load()-heartbeats

	// A backoff from the floor, doubling, allows this many dials in the window, before its jitter; the jitter only
	// makes it fewer. Today's tunnel dialled once per taken heartbeat: one every 20 ms here.
	most, spent := int32(1), time.Duration(0)
	for d := floor; spent+d <= window; d *= 2 {
		spent += d
		most++
	}
	if beat < 20 {
		t.Fatalf("instrument guard: %d heartbeats taken in %v — the loop was not beating beside the tunnel", beat, window)
	}
	if dialled > most {
		t.Fatalf("at the farm's tunnel limit the tunnel dialled %d times in %v beside %d taken heartbeats, past what its own "+
			"backoff allows (%d): a taken heartbeat cut its wait short\n%s", dialled, window, beat, most, out.String())
	}
	if strings.Contains(out.String(), "dialling the drive tunnel now") {
		t.Fatalf("a taken heartbeat woke a tunnel the farm refused at its limit:\n%s", out.String())
	}

	// ANOTHER MACHINE STOPS: this one connects on a later try, as the farm's sentence says.
	f.tunnel.Store(accepted)
	eventually(t, 6*time.Second, "the tunnel connected once a place was free", drive.Connected)
	t.Logf("%v at the limit: %d dials beside %d taken heartbeats (its backoff allows %d)", window, dialled, beat, most)
}

// A FARM THAT COMES BACK STILL WAKES THE TUNNEL: every case but the limit keeps today's reconnect. The farm is away (its
// gateway answers every call 503), then back; the first heartbeat it takes dials the tunnel at once, not at the end of a
// three-second backoff.
func TestAFarmThatComesBackStillWakesTheTunnel(t *testing.T) {
	f := newLimitFarm(t)
	away := &answer{status: http.StatusServiceUnavailable}
	f.heartbeat.Store(away)
	f.tunnel.Store(away)
	var out syncBuilder
	floor := 3 * time.Second
	drive := runWithATunnel(t, f, &out, 20*time.Millisecond, func(c *tunnel.Config) {
		c.ReconnectFloor, c.ReconnectCap = floor, floor
	})

	eventually(t, 5*time.Second, "the first upgrade, while the farm is away", func() bool { return f.upgrades.Load() >= 1 })
	beats := f.heartbeats.Load()
	eventually(t, 2*time.Second, "heartbeats unanswered after it", func() bool { return f.heartbeats.Load() >= beats+3 })

	back := time.Now()
	f.heartbeat.Store(accepted)
	f.tunnel.Store(accepted)
	eventually(t, 5*time.Second, "the tunnel up once the farm is back", drive.Connected)
	if took := time.Since(back); took > floor/2 {
		t.Fatalf("the farm came back and the tunnel took %v to connect: a taken heartbeat no longer wakes its %v backoff\n%s",
			took.Round(time.Millisecond), floor, out.String())
	}
	if !strings.Contains(out.String(), "the farm answered a heartbeat — dialling the drive tunnel now") {
		t.Fatalf("the wake was not said:\n%s", out.String())
	}
}
