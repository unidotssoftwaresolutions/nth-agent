package connect_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// ── item 3: the producer seal + the probe-not-run seal ──────────────────────────────────────────────────────

// THE PRODUCER SEAL: --skip ios produces NotAsked — the deliberate decision — never CouldNotAsk (a
// failure) and never silence. And THE PROBE-NOT-RUN SEAL: the skipped platform's seam is never
// touched — not asked means not asked, not "asked and discarded".
func TestSkipProducesNotAskedAndNeverTouchesTheSeam(t *testing.T) {
	skip, err := connect.ParseSkip([]string{"ios"})
	if err != nil {
		t.Fatal(err)
	}
	var listerCalls, runnerCalls atomic.Int32
	probes := connect.ProbeSet(skip,
		func(context.Context, ...string) (string, error) {
			runnerCalls.Add(1)
			return "List of devices attached\n\n", nil
		},
		func(context.Context) ([]ios.Device, error) {
			listerCalls.Add(1)
			return nil, nil
		}, time.Second, false)

	outcomes := probes(context.Background())

	iosOutcome := outcomes[1]
	if !iosOutcome.IsNotAsked() {
		t.Fatalf("--skip ios must produce NOT-ASKED (a decision), got %s", iosOutcome.KindString())
	}
	if got := listerCalls.Load(); got != 0 {
		t.Fatalf("a skipped platform's seam must never run, got %d call(s)", got)
	}
	// …and the OTHER platform is untouched by the switch: still asked, still answered.
	if !outcomes[0].IsAnswered() || runnerCalls.Load() != 1 {
		t.Fatalf("android must still be asked exactly once, got %s / %d calls", outcomes[0].KindString(), runnerCalls.Load())
	}
}

func TestParseSkipNamesPlatformsAsValues(t *testing.T) {
	skip, err := connect.ParseSkip([]string{"android,ios"})
	if err != nil || !skip[inventory.Android] || !skip[inventory.IOS] {
		t.Fatalf("comma-separated values must parse, got %v / %v", skip, err)
	}
	if _, err := connect.ParseSkip([]string{"windows"}); err == nil || !strings.Contains(err.Error(), "android, ios") {
		t.Fatalf("an unknown platform is refused naming the valid values, got %v", err)
	}
}

// ── item 3: the narration offers the switch, once ───────────────────────────────────────────────────────────

func TestASustainedFailureOffersTheSkipSwitchExactlyOnce(t *testing.T) {
	// AMENDED (AN_AGENT_THAT_PASSES_ON_WINDOWS_ROUND_TWO): this read the real clock. The four ticks run back to back, and
	// on windows-latest the clock read the same instant at each: a failure 0 old, never 1 ns, and the switch was rightly
	// never offered (got 0). The window is the subject, both ways, on the loop's own clock (Config.Now): a clock that
	// moves past the window offers the switch exactly once; a clock that does not move never offers it, however many
	// ticks fail.
	for _, c := range []struct {
		name   string
		step   time.Duration // how far the clock moves at each read
		offers int
	}{
		{"a failure the clock has carried past the window is offered the switch exactly once", time.Minute, 1},
		{"a failure the clock has not aged is never offered the switch", 0, 0},
	} {
		stub, server := newStubFarm(t)
		var out strings.Builder
		cfg := config(server, &out, 4)
		cfg.Probes = func(ctx context.Context) []inventory.Outcome {
			return []inventory.Outcome{
				inventory.Answered(inventory.Android, nil, nil),
				inventory.CouldNotAsk(inventory.IOS, "usbmuxd is not running", "start it"),
			}
		}
		cfg.HintAfter = time.Nanosecond // the window is a parameter; "sustained" = any tick after the first
		var mu sync.Mutex
		clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
		cfg.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			clock = clock.Add(c.step)
			return clock
		}

		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("run: %v", err)
		}
		// Instrument guard: every tick ran and said the failure, so a switch never offered is the window's answer, not
		// a loop that stopped short.
		if beats := stub.heartbeats.Load(); beats != 4 || !strings.Contains(out.String(), "iOS: could not check") {
			t.Fatalf("%s: instrument guard: 4 ticks that each find iOS failing, got %d heartbeats in %q", c.name, beats, out.String())
		}
		if got := strings.Count(out.String(), "--skip ios"); got != c.offers {
			t.Fatalf("%s: want %d, got %d in %q", c.name, c.offers, got, out.String())
		}
		// AMENDED (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): the platform is written iOS; the flag's value stays `ios`.
		if c.offers == 1 && !strings.Contains(out.String(), "will never drive iOS devices") {
			t.Fatalf("the offer explains when to use it, got %q", out.String())
		}
	}
}

// ── item 0.2: one home, and the budgets live in it ──────────────────────────────────────────────────────────

// THE PER-PROBE BUDGET SEAL, ported to the one home (its predecessor drove main's withDeadline, which
// is retired): a slow first probe must not spend the second probe's allowance.
func TestASlowFirstProbeDoesNotShortenTheSecondsBudget(t *testing.T) {
	const budget = 200 * time.Millisecond
	const firstProbeSpends = 120 * time.Millisecond

	// AMENDED (AN_AGENT_THAT_PASSES_ON_WINDOWS): the second probe is the iOS lister, and on Windows the iOS probe is never
	// run (TestOnWindowsTheIOSProbeNeverRuns) — so on windows-latest it was never called, and its remaining budget read as
	// the zero it was declared with: "0s of a 200ms budget". The budget is this seal's subject, not the system, so both
	// probes run on a system that asks both; and a probe never called is said as that, never read as a budget.
	var secondRemaining time.Duration
	secondRan := false
	probes := connect.ProbeSetFor("linux", nil,
		func(ctx context.Context, _ ...string) (string, error) {
			time.Sleep(firstProbeSpends) // a slow adb
			return "List of devices attached\n\n", nil
		},
		func(ctx context.Context) ([]ios.Device, error) {
			secondRan = true
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("a probe must run under a deadline at all")
			}
			secondRemaining = time.Until(deadline)
			return nil, nil
		}, budget, false)

	probes(context.Background())
	if !secondRan {
		t.Fatal("instrument guard: the second probe was never run, so there is no budget of its to read")
	}

	// Shared budget ⇒ the second sees roughly budget−firstProbeSpends (80ms). Its own ⇒ ~200ms.
	if secondRemaining <= (budget-firstProbeSpends)+(firstProbeSpends/2) {
		t.Fatalf("the second probe got %v of a %v budget — the first probe's slowness was charged to it", secondRemaining, budget)
	}
}

// THE ONE-HOME PIN: package main WIRES the probe list (naming connect.ProbeSet) and never builds its
// own — a probe literal in main is the two-homes drift this item removed, whatever it is named.
func TestTheProbeListHasOneHome(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "cmd", "nth-agent", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "connect.ProbeSet(") {
		t.Fatal("main must wire the probes through connect.ProbeSet — the one home")
	}
	for _, forbidden := range []string{"android.Probe{", "ios.Probe{"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("main constructs %s… — the probe list has ONE home (internal/connect/probes.go)", forbidden)
		}
	}
}
