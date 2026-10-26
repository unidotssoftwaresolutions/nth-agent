package connect_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// WHAT_WE_LEAVE_RUNNING_ON_YOUR_PHONE item 5.2 — BOTH DIRECTIONS, because a cleanup that only ever fires is not a
// cleanup, it is a kill; and one that never fires is the banner we started with.

const udid = "00008140-0000C0FFEE0000A1"

func at(minutes int) time.Time { return time.Date(2026, 8, 24, 22, minutes, 0, 0, time.UTC) }

func TestTheIdleStopDoesNotFireWhileSomethingIsUsingIt(t *testing.T) {
	tracker := &connect.WdaIdleTracker{After: 5 * time.Minute}

	// An hour of ticks with a session attached the whole time: never stopped, not once.
	for m := 0; m <= 60; m++ {
		if got := tracker.Decide(udid, wda.RunnerInUse, at(m)); got != connect.WdaLeaveAlone {
			t.Fatalf("minute %d: a phone with a session attached must be left alone, got %v", m, got)
		}
	}

	// …and a session that ENDS starts the window from the end, not from whenever the phone was last quiet.
	if got := tracker.Decide(udid, wda.RunnerIdle, at(61)); got != connect.WdaLeaveAlone {
		t.Fatalf("the first idle tick starts the window; it does not end it, got %v", got)
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(65)); got != connect.WdaLeaveAlone {
		t.Fatalf("four minutes of quiet is not five, got %v", got)
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(66)); got != connect.WdaStop {
		t.Fatalf("five minutes of quiet after the session ended must stop it, got %v", got)
	}
}

func TestTheIdleStopFiresWhenNothingIsUsingIt(t *testing.T) {
	tracker := &connect.WdaIdleTracker{After: 5 * time.Minute}

	if got := tracker.Decide(udid, wda.RunnerIdle, at(0)); got != connect.WdaLeaveAlone {
		t.Fatalf("the first sighting starts the clock, got %v", got)
	}
	for m := 1; m < 5; m++ {
		if got := tracker.Decide(udid, wda.RunnerIdle, at(m)); got != connect.WdaLeaveAlone {
			t.Fatalf("minute %d is inside the window, got %v", m, got)
		}
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(5)); got != connect.WdaStop {
		t.Fatalf("the window elapsed and nothing had used it — this must stop, got %v", got)
	}
}

// AN UNANSWERED QUESTION IS NOT AN IDLE PHONE. A flaky cable must never accumulate into "nobody has used this for
// five minutes": that is a sentence about the phone and the probe was about the cable.
func TestAnUnknownProbeNeitherStartsNorAdvancesTheWindow(t *testing.T) {
	tracker := &connect.WdaIdleTracker{After: 5 * time.Minute}

	for m := 0; m <= 30; m++ {
		if got := tracker.Decide(udid, wda.RunnerUnknown, at(m)); got != connect.WdaLeaveAlone {
			t.Fatalf("minute %d: an unknown probe must stop nothing, got %v", m, got)
		}
	}
	// Half an hour of unknowns, then the phone answers idle: the window starts NOW, not half an hour ago.
	tracker.Decide(udid, wda.RunnerIdle, at(31))
	if got := tracker.Decide(udid, wda.RunnerIdle, at(35)); got != connect.WdaLeaveAlone {
		t.Fatalf("the unknowns must not have counted toward the window, got %v", got)
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(36)); got != connect.WdaStop {
		t.Fatalf("five minutes of ANSWERED quiet must stop it, got %v", got)
	}
}

func TestNothingListeningIsNothingToStopAndForgetsTheWindow(t *testing.T) {
	tracker := &connect.WdaIdleTracker{After: 5 * time.Minute}

	tracker.Decide(udid, wda.RunnerIdle, at(0))
	if got := tracker.Decide(udid, wda.RunnerNotListening, at(1)); got != connect.WdaLeaveAlone {
		t.Fatalf("a port nobody answers has nothing to stop, got %v", got)
	}
	// The clock is forgotten, so a runner that comes back gets a fresh window rather than inheriting an old one.
	tracker.Decide(udid, wda.RunnerIdle, at(2))
	if got := tracker.Decide(udid, wda.RunnerIdle, at(6)); got != connect.WdaLeaveAlone {
		t.Fatalf("the window must have restarted at minute 2, got %v", got)
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(7)); got != connect.WdaStop {
		t.Fatalf("…and elapsed at minute 7, got %v", got)
	}
}

func TestKeepWarmNeverStopsAnything(t *testing.T) {
	tracker := &connect.WdaIdleTracker{After: time.Second, KeepWarm: true}
	for m := 0; m <= 60; m++ {
		if got := tracker.Decide(udid, wda.RunnerIdle, at(m)); got != connect.WdaLeaveAlone {
			t.Fatalf("minute %d: --keep-wda-warm keeps it, got %v", m, got)
		}
	}
}

// A STOP THAT DID NOT TAKE IS RETRIED A WINDOW LATER, NOT EVERY TICK. A cleanup that hammers is a cleanup nobody
// leaves switched on.
func TestAStopRestartsTheWindowRatherThanRepeatingEveryTick(t *testing.T) {
	tracker := &connect.WdaIdleTracker{After: 5 * time.Minute}
	tracker.Decide(udid, wda.RunnerIdle, at(0))
	if got := tracker.Decide(udid, wda.RunnerIdle, at(5)); got != connect.WdaStop {
		t.Fatalf("setup: expected the first stop, got %v", got)
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(6)); got != connect.WdaLeaveAlone {
		t.Fatalf("the tick after a stop must not stop again, got %v", got)
	}
	if got := tracker.Decide(udid, wda.RunnerIdle, at(10)); got != connect.WdaStop {
		t.Fatalf("…and a full window later it tries again, got %v", got)
	}
}

// item 5.3 — THE STATEMENT NAMES THE BEHAVIOUR THAT IS ACTUALLY CONFIGURED, or it is decoration.
func TestTheStartupStatementDiffersWithAndWithoutTheWarmFlag(t *testing.T) {
	def := connect.WdaStartupStatement(false, 5*time.Minute)
	warm := connect.WdaStartupStatement(true, 5*time.Minute)

	if def == warm {
		t.Fatal("two configurations, one sentence — the statement is decoration")
	}
	for _, must := range []string{"stops it", "5 minutes", "--keep-wda-warm", "Automation Running"} {
		if !strings.Contains(def, must) {
			t.Fatalf("the default statement must say %q, got %q", must, def)
		}
	}
	for _, must := range []string{"left running", "--keep-wda-warm", "Automation Running"} {
		if !strings.Contains(warm, must) {
			t.Fatalf("the warm statement must say %q, got %q", must, warm)
		}
	}
	if strings.Contains(warm, "stops it once nothing has used it for") {
		t.Fatal("the warm statement must not promise a stop that will not happen")
	}
	// …and the WINDOW it names is the one configured, not a hard-coded five.
	if !strings.Contains(connect.WdaStartupStatement(false, 30*time.Second), "30 seconds") {
		t.Fatalf("the statement must name the configured window, got %q", connect.WdaStartupStatement(false, 30*time.Second))
	}
}

// ── A_PHONE_WE_WERE_TOLD_TO_LEAVE_ALONE — what the machine SPENDS, counted ────────────────────────────────────
//
// Every other seal here covers what the tracker DECIDES. These two cover what it costs the phone, because the
// first version decided correctly and asked anyway: a usbmuxd forward and an HTTP request to somebody's device
// every ten seconds, for the life of the session, to obtain an answer discarded on the next line.

// countingProbes runs the real loop for a few ticks and reports which devices were actually asked.
func countingProbes(t *testing.T, keepWarm bool, ticks int) ([]string, string) {
	t.Helper()
	stub, server := newStubFarm(t)
	_ = stub
	var out strings.Builder
	cfg := config(server, &out, ticks)
	cfg.Probes = driverOutcomes // one offerable iPhone (00008030-HEALTHY) and one Android
	cfg.KeepWdaWarm = keepWarm

	var asked []string
	cfg.WdaProbe = func(_ context.Context, udid string, _ uint16) wda.RunnerStatus {
		asked = append(asked, udid)
		return wda.RunnerStatus{State: wda.RunnerNotListening}
	}
	cfg.WdaStop = func(context.Context, string, uint16) error { return nil }

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	return asked, out.String()
}

func TestAPhoneWeWereToldToLeaveAloneIsNotAskedAnything(t *testing.T) {
	asked, transcript := countingProbes(t, true, 3)

	if len(asked) != 0 {
		t.Fatalf("--keep-wda-warm is an instruction about the PHONE, not only about the outcome: %d probe(s) still went to %v", len(asked), asked)
	}
	// …AND THE PERSON IS STILL TOLD. The saving is on their device, never on what they hear from us: a silent
	// agent that leaves WebDriverAgent running is the defect the parent slice removed.
	if !strings.Contains(transcript, "--keep-wda-warm") || !strings.Contains(transcript, "Automation Running") {
		t.Fatalf("the warm statement must still be said: %q", transcript)
	}
}

func TestWithoutTheFlagEveryOfferableIphoneIsAskedExactlyOncePerTick(t *testing.T) {
	const ticks = 3
	asked, _ := countingProbes(t, false, ticks)

	// A PROBE THAT NEVER RUNS IS NOT A SAVING, IT IS A BROKEN AGENT — so this counts up as well as down.
	if len(asked) != ticks {
		t.Fatalf("one offerable iPhone over %d ticks is %d probes, got %d (%v)", ticks, ticks, len(asked), asked)
	}
	for _, udid := range asked {
		if udid != "00008030-HEALTHY" {
			t.Fatalf("only an OFFERABLE iPhone is asked — never the Android, never the unusable one: %v", asked)
		}
	}
}
