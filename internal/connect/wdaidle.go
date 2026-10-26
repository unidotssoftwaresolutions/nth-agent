package connect

import (
	"context"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// ── PUTTING SOMEBODY'S PHONE BACK THE WAY WE FOUND IT ────────────────────────────────────────────────────────
//
// Two rules pull against each other here on purpose, and the decision below is where they meet:
//
//	WE DO NOT SILENTLY LEAVE SOMETHING RUNNING on hardware we do not own. The standing rule of this programme
//	has always been read as "do not install"; it reads the same way about "do not leave behind".
//	WE DO NOT SILENTLY KILL SOMETHING EITHER. A stop that surprises a person mid-suite is the same failure
//	wearing the opposite sign — which is why RunnerInUse and RunnerUnknown both mean LEAVE IT ALONE.

// DefaultWdaIdleAfter is how long WebDriverAgent must be idle before this agent stops it.
//
// FIVE MINUTES, AND HERE IS THE ARGUMENT, because a number nobody argued for is a number nobody can change.
// It is THIRTY HEARTBEATS (DefaultInterval is 10s), so no single missed or slow probe can move it, and the clock
// only advances on ticks where the phone actually ANSWERED idle. What it costs when it fires is one cold
// WebDriverAgent start on the next run: the operator measured 6702 ms cold against 1693 ms warm, so about five
// seconds. What it buys is that a phone somebody lent us stops wearing an Automation Running banner five minutes
// after the last thing that used it, instead of until they notice. A person running suites back-to-back never
// reaches five minutes of quiet; a person who has finished always does. Anyone who would rather pay the banner
// than the five seconds passes --keep-wda-warm.
const DefaultWdaIdleAfter = 5 * time.Minute

// WdaIdleDecision is what one tick decided about one device's WebDriverAgent.
type WdaIdleDecision int

const (
	// WdaLeaveAlone: do nothing. The default answer, and the answer to every question that was not established.
	WdaLeaveAlone WdaIdleDecision = iota
	// WdaStop: it has been idle for the whole window — ask it to end itself.
	WdaStop
)

// WdaIdleTracker decides, per device, whether the WebDriverAgent left running there should be stopped.
//
// THE CLOCK ONLY ADVANCES ON AN ANSWER. An unknown probe does not extend the idle window and does not start one:
// a flaky cable must never accumulate into "nobody has used this for five minutes", because that is a sentence
// about the phone and the probe was about the cable.
type WdaIdleTracker struct {
	// After is the idle window; zero means DefaultWdaIdleAfter.
	After time.Duration
	// KeepWarm is --keep-wda-warm: never stop anything, and say so at startup rather than at the moment nothing
	// happens.
	KeepWarm bool

	idleSince map[string]time.Time
}

// Watches reports whether this tracker will act on anything it learns — and it is asked BEFORE the phone is, which
// is the whole reason it exists as its own question.
//
// `Decide` also knows the answer, and knowing it there was not enough: the probe happened first, so a customer who
// passed --keep-wda-warm — who told us in words to leave their phone alone — still had a usbmuxd forward opened to
// their device and an HTTP request made to it every ten seconds, for the life of the session, to obtain an answer
// discarded on the next line. **A decision function that must be consulted BEFORE the expensive thing is a
// different shape from one consulted after**, so it is two questions over one field rather than one question asked
// too late. "Leave it alone" is an instruction about the phone, not only about the outcome.
func (t *WdaIdleTracker) Watches() bool { return !t.KeepWarm }

// Decide answers for one device, from one probe, at one moment. Pure: the clock is a parameter and the only state
// is when this device was first seen idle. It re-checks Watches so it is right on its own — one field, two
// readers, and neither of them a second copy of the fact.
func (t *WdaIdleTracker) Decide(udid string, state wda.RunnerState, now time.Time) WdaIdleDecision {
	if !t.Watches() {
		return WdaLeaveAlone
	}
	if t.idleSince == nil {
		t.idleSince = map[string]time.Time{}
	}

	switch state {
	case wda.RunnerInUse:
		// Something is driving it right now. Forget any idle clock: the window measures quiet, and this is not it.
		delete(t.idleSince, udid)
		return WdaLeaveAlone
	case wda.RunnerNotListening:
		// Nothing to stop. Also the state a successful stop leaves behind, which is why it forgets too.
		delete(t.idleSince, udid)
		return WdaLeaveAlone
	case wda.RunnerIdle:
		since, seen := t.idleSince[udid]
		if !seen {
			t.idleSince[udid] = now
			return WdaLeaveAlone
		}
		if now.Sub(since) < t.after() {
			return WdaLeaveAlone
		}
		// STOP — and restart the clock rather than forgetting it, so a shutdown that did not take is retried a
		// window later instead of every tick. A cleanup that hammers is a cleanup nobody leaves switched on.
		t.idleSince[udid] = now
		return WdaStop
	default: // wda.RunnerUnknown
		return WdaLeaveAlone
	}
}

func (t *WdaIdleTracker) after() time.Duration {
	if t.After > 0 {
		return t.After
	}
	return DefaultWdaIdleAfter
}

// WdaStartupStatement is what the agent says ONCE, at startup, about what it will leave running and when it will
// not. A customer must never learn from their phone's status bar what our software is doing on their device —
// and a stop nobody was told about is the same surprise as a banner nobody was told about.
func WdaStartupStatement(keepWarm bool, after time.Duration) string {
	if keepWarm {
		return "iOS: WebDriverAgent is left running between runs (--keep-wda-warm). Your phone will keep showing " +
			"\"Automation Running\" until you stop the agent or unplug it — that is the trade: the next run starts in " +
			"about a fifth of the time. Drop the flag and it is stopped automatically once nothing has used it for a while."
	}
	if after <= 0 {
		after = DefaultWdaIdleAfter
	}
	return "iOS: a run leaves WebDriverAgent running on your phone — that is what makes the next run start in about a " +
		"fifth of the time, and it is why the phone shows \"Automation Running\". This agent stops it once nothing has " +
		"used it for " + humaniseIdle(after) + ", and never while a session is attached. Pass --keep-wda-warm to keep it up."
}

// humaniseIdle renders the window the way a person would say it, so the startup line reads as a sentence.
func humaniseIdle(d time.Duration) string {
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		minutes := int(d / time.Minute)
		if minutes == 1 {
			return "a minute"
		}
		return itoa(minutes) + " minutes"
	case d >= time.Second && d%time.Second == 0:
		seconds := int(d / time.Second)
		if seconds == 1 {
			return "a second"
		}
		return itoa(seconds) + " seconds"
	default:
		return d.String()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// tidyWda is the per-tick half: for every iPhone this machine SEES, ask its WebDriverAgent about itself and act on
// the answer. It touches nothing else — the report is built from the probe outcomes exactly as before, and this
// runs beside it rather than inside it.
//
// It is skipped whole when the build cannot ask (WdaProbe nil), which is what keeps a machine with no way to reach
// a phone byte-for-byte what it was.
func tidyWda(ctx context.Context, cfg Config, narrator *narrator, tracker *WdaIdleTracker, outcomes []inventory.Outcome) {
	if cfg.WdaProbe == nil {
		return
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}

	for _, outcome := range outcomes {
		if outcome.Platform() != inventory.IOS || !outcome.IsAnswered() {
			continue
		}
		for _, device := range outcome.Offerable() {
			// SAID ONCE, THE FIRST TIME AN iPHONE IS SEEN — a person hears what we leave running from us, before
			// they read it off their own status bar. Not per tick: a line every ten seconds is a line nobody reads.
			narrator.wdaPolicy(WdaStartupStatement(cfg.KeepWdaWarm, tracker.after()))

			// THE PHONE IS NOT ASKED WHEN WE WERE TOLD TO LEAVE IT ALONE. The statement above still goes out —
			// a person hears what we are doing either way — but their battery, their USB bus and their device pay
			// nothing for a decision that was made before the question.
			if !tracker.Watches() {
				continue
			}

			// THE PORT IS THE ONE THIS MACHINE DECLARED, not one derived a second time: the registration and this
			// probe must be talking about the same number, and the only way to be sure is for there to be one.
			port := wda.PortFor(device.ID)
			if cfg.WdaPortFor != nil {
				port = cfg.WdaPortFor(ctx, device.ID)
			}
			status := cfg.WdaProbe(ctx, device.ID, port)
			if tracker.Decide(device.ID, status.State, now()) != WdaStop || cfg.WdaStop == nil {
				continue
			}
			if err := cfg.WdaStop(ctx, device.ID, port); err != nil {
				narrator.say("could not stop WebDriverAgent on %s (%v) — it is still running there", device.ID, err)
				continue
			}
			narrator.say("nothing has used WebDriverAgent on %s for %s, so it was asked to stop — your phone should "+
				"drop the \"Automation Running\" banner. The next run there starts cold (about five seconds more).",
				device.ID, humaniseIdle(tracker.after()))
		}
	}
}
