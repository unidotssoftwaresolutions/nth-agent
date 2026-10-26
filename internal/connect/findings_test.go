package connect_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// THE_CHECK_THAT_ALREADY_KNEW — the loop's half: findings ride the reports the farm sees, refreshed on the
// loop's own cadence, and an agent with no findings provider sends a wire byte-compatible with yesterday's.

func TestFindingsRideTheHeartbeat(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.IosTeamID, cfg.IosWdaBundleID = "TEAM7QZ4X2", "com.example.WebDriverAgentRunner" // the offer gate
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723",
		Probe:     func(ctx context.Context) error { return nil },
		TunnelUp:  func() bool { return true },
	}
	stamp := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	cfg.DeviceFindings = func(ctx context.Context, outcomes []inventory.Outcome) map[string][]farm.WireFinding {
		return map[string][]farm.WireFinding{
			"00008030-HEALTHY": {{Check: "IosWdaProject", State: "missing", CheckedAtUtc: stamp}},
		}
	}

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	body := string(stub.lastHeartbeat.Load().([]byte))
	for _, want := range []string{`"findings":[{`, `"check":"IosWdaProject"`, `"state":"missing"`, `"checkedAtUtc":"2026-08-25T12:00:00Z"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the heartbeat must carry the finding WITH its stamp — %q missing from %s", want, body)
		}
	}
}

func TestAnAgentWithNoFindingsProviderSendsYesterdaysWire(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.IosTeamID, cfg.IosWdaBundleID = "TEAM7QZ4X2", "com.example.WebDriverAgentRunner"
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723",
		Probe:     func(ctx context.Context) error { return nil },
		TunnelUp:  func() bool { return true },
	}

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	body := string(stub.lastHeartbeat.Load().([]byte))
	if strings.Contains(body, `"findings"`) {
		t.Fatalf("no provider ⇒ no field at all — absent is 'the machine said nothing', got %s", body)
	}
}

func TestTheFindingsCacheRefreshesOnItsOwnCadenceNotTheHeartbeats(t *testing.T) {
	// THE FRESHNESS RULE, measured: the checks shell out (xcodebuild takes seconds), so the provider must NOT run
	// per 10s heartbeat. A long max-age computes once for registration and once for the loop, then reuses; a tiny
	// max-age recomputes every tick. Both directions, one test.
	stub, server := newStubFarm(t)
	_ = stub
	var calls atomic.Int32
	provider := func(ctx context.Context, outcomes []inventory.Outcome) map[string][]farm.WireFinding {
		calls.Add(1)
		return map[string][]farm.WireFinding{"00008030-HEALTHY": {{Check: "IosWdaProject", State: "satisfied", CheckedAtUtc: time.Now().UTC()}}}
	}

	var out strings.Builder
	cfg := config(server, &out, 3)
	cfg.IosTeamID, cfg.IosWdaBundleID = "TEAM7QZ4X2", "com.example.WebDriverAgentRunner"
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723",
		Probe:     func(ctx context.Context) error { return nil },
		TunnelUp:  func() bool { return true },
	}
	cfg.DeviceFindings = provider
	cfg.FindingsMaxAge = time.Hour
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("a fresh cache is REUSED across heartbeats: want 2 computations (registration + first tick), got %d", got)
	}

	calls.Store(0)
	stub2, server2 := newStubFarm(t)
	_ = stub2
	var out2 strings.Builder
	cfg2 := config(server2, &out2, 3)
	cfg2.IosTeamID, cfg2.IosWdaBundleID = "TEAM7QZ4X2", "com.example.WebDriverAgentRunner"
	cfg2.Driver = cfg.Driver
	cfg2.DeviceFindings = provider
	cfg2.FindingsMaxAge = time.Nanosecond
	if err := connect.Run(context.Background(), cfg2); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("an aged cache recomputes: want 4 (registration + every tick), got %d", got)
	}
}
