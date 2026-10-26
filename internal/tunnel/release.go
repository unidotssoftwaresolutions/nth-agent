package tunnel

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// A SESSION NOBODY ENDS — the agent notices the silence, and the agent ends it.
//
// MEASURED, 2026-09-03, field test item 4, with the vendor's own session log as the record. A cloud iOS
// discovery was running; the worker container was SIGKILLed mid-run; `DELETE` appears ZERO times in the whole
// session log. Nothing on our side ever released it. The vendor ended it 121.6 s after our last answered
// command and labelled the teardown `[BROWSERSTACK_IDLE_TIMEOUT] … (default: 90 secs)`.
//
// ⚠️ THREE NUMBERS, NONE OF WHICH CONFIRMS ANOTHER, and the one that fits is not ours:
//
//   - `bstack:options.idleTimeout: 60` — what THIS PACKAGE sends. It did not fire at 60 s.
//   - 90 — the vendor's documented default, quoted back at us in their own message for a session that carried
//     60, which is the vendor saying our value was not the one in force. Their App Automate documentation
//     describes this capability only as something you INCREASE ("you can increase the timeout to a maximum of
//     300 seconds"); a value below the default is not a documented request. It did not fire at 90 s either.
//   - `appium:newCommandTimeout: 120` — what the PLATFORM puts in the session body. 121.6 s is within 1.6 s of
//     it, and no other number in that capability block is within a minute.
//
// Appium's own documented behaviour for that capability is to end the session — it "specifies how long Appium
// will wait for a new command from the client before assuming the client quit and ending the session". So the
// reading the measurement supports is that Appium's newCommandTimeout retired this session and BrowserStack
// recorded the resulting teardown under its own idle label. That is what `StuckRunReaper` already says on the
// platform side ("the Appium session has no handle to quit from here — Appium's newCommandTimeout retires it");
// it is NOT what this package's own comment said, and that comment is corrected where it lives.
//
// ⚠️ AND THE MECHANISM BELOW DOES NOT DEPEND ON WINNING THAT ARGUMENT. Whatever retires the session on the far
// side, it retires it AFTER a bill has been run up, and only ever by accident of a timeout nobody here controls.
// The agent is the only party that can end it on purpose, and this is that.
//
// WHY THE AGENT AND NOT THE PLATFORM. The worker cannot: BrowserStack credentials are resolved on the customer's
// machine and never reach the platform, which is a deliberate, documented property. And the platform does not
// even know the id — `StuckRunReaper` reaps on the LEASE id it read from the run row, and says in the same
// breath that the Appium session has no handle. The session id is written down in exactly two places once the
// creating process is gone: the vendor's own session record, and `Tunnel.sessions` here.

// ReleaseMarginDefault is the margin added to a session's OWN declared silence budget before this agent will
// conclude that nobody is driving it.
//
// ⚠️ IT IS NOT A THIRD NUMBER. Two numbers already disagreed with each other and with the measurement, and a
// bound invented here would have made three. Both terms of this bound are numbers that already exist and are
// already argued:
//
//   - the session's own `appium:newCommandTimeout`, read out of the create body this agent already parses. It
//     is the silence budget the SESSION declared to Appium, so a headless run (120 s) and an interactive
//     authoring session with a human pausing between gestures (600 s) each get their own, and this agent never
//     has an opinion about which is right;
//   - plus [AppiumForwardTimeout], which is this file's outermost hop in the margin chain (consumer 180 s <
//     farm relay 190 s < this 200 s) and is by construction the longest this agent could still be waiting on a
//     single frame. Past it, no frame it forwarded can still be outstanding.
//
// ⚠️ THE RISK THIS BOUND EXISTS TO ARGUE: A BOUND THAT IS TOO SHORT KILLS A HEALTHY SESSION. The gap between two
// frames is not idle time, it is the worker thinking — measured 2026-09-03, a mobile discovery step spends about
// 11 s in the model and 10 s over twelve page-source reads, so gaps of tens of seconds are ordinary traffic. The
// longest legitimate gap is not a number this agent gets to guess: the session itself declared it, because
// `newCommandTimeout` is precisely the client telling Appium "silence longer than this means I am gone". This
// agent therefore tolerates that whole budget AND the forward margin on top of it, and acts only after the
// far side's own retirement should already have happened. It is the second line of defence, never the first —
// the same relationship `LeaseHeartbeat` and `QueueLease` negotiate on the platform side by clamping one to the
// other, and the same chain rule `AppiumForwardTimeout` states three hops up.
const ReleaseMarginDefault = AppiumForwardTimeout

// newCommandTimeoutCapability is the client's own declaration of how long silence may last on this session.
const newCommandTimeoutCapability = "appium:newCommandTimeout"

// sessionEntry is what the routing table holds for one live session: where it goes, how long it may be silent,
// and when it was last driven. It replaced a bare destination, and the destination is still the ONLY thing the
// routing decision reads — a release must be sent where the session was created, and this is the record that
// says where that was.
type sessionEntry struct {
	dest destination

	// budget is the silence this session ITSELF declared, never a number this agent chose. Zero means it
	// declared none (or declared 0, which Appium documents as "disable the timeout") — see arm for why neither
	// is armed.
	budget time.Duration

	mu       sync.Mutex
	lastSeen time.Time
	timer    *time.Timer
}

// touch records that this session was driven. EVERY frame naming it counts, which is the whole reason the agent
// can do this and nothing else can: it sees them all.
func (e *sessionEntry) touch(now time.Time) {
	e.mu.Lock()
	e.lastSeen = now
	e.mu.Unlock()
}

// remember writes the routing table entry for a session that really started, and arms its release.
func (t *Tunnel) remember(id string, dest destination, createBody []byte) {
	entry := &sessionEntry{dest: dest, budget: declaredSilenceBudget(createBody), lastSeen: time.Now()}
	t.sessions.Store(id, entry)
	t.arm(id, entry)
}

// forget drops a session the client ended itself. The entry is the gate the release fires through, so dropping
// it here is also what makes a double release impossible — see releaseIfSilent.
func (t *Tunnel) forget(id string) {
	if value, ok := t.sessions.LoadAndDelete(id); ok {
		entry := value.(*sessionEntry)
		entry.mu.Lock()
		if entry.timer != nil {
			entry.timer.Stop()
		}
		entry.mu.Unlock()
	}
}

// silenceBound is the whole rule, in one line: the session's own budget plus the forward margin.
func (t *Tunnel) silenceBound(e *sessionEntry) time.Duration {
	margin := t.cfg.ReleaseMargin
	if margin <= 0 {
		margin = ReleaseMarginDefault
	}
	return e.budget + margin
}

// arm starts the clock for one session.
//
// ⚠️ A SESSION THAT DECLARED NO BUDGET IS NOT ARMED, and that is a decision rather than an omission. Appium's
// documented default when the capability is absent is 60 s — SHORTER than any bound this agent could derive
// from it, so the far side retires such a session before this mechanism could add anything. And a session that
// declared `0` has explicitly asked Appium for no retirement at all ("setting to 0 will disable the new command
// timeout"); ending it anyway would be this agent overruling the only party that stated an intention, which is
// exactly the healthy-session kill this bound is written to avoid. In both cases the honest answer is to leave
// it alone and say so once.
func (t *Tunnel) arm(id string, e *sessionEntry) {
	if e.budget <= 0 {
		if t.saidNoBudget.CompareAndSwap(false, true) {
			t.say("a session started without telling Appium how long it may go quiet — this agent will not end it " +
				"on a bound it made up, so an abandoned one is retired by Appium's own default instead\n")
		}
		return
	}
	e.mu.Lock()
	e.timer = time.AfterFunc(t.silenceBound(e), func() { t.releaseIfSilent(id) })
	e.mu.Unlock()
}

// releaseIfSilent is the decision, taken when the clock fires.
//
// ⚠️ IT RE-ARMS RATHER THAN RELEASING WHENEVER THE SESSION HAS BEEN DRIVEN SINCE. The timer says "the bound has
// elapsed since I was set", which is not the same claim as "this session has been silent for the bound" — a
// frame that arrived in between is exactly the healthy traffic this must not kill. lastSeen is the fact; the
// timer is only what wakes us up to read it.
func (t *Tunnel) releaseIfSilent(id string) {
	value, ok := t.sessions.Load(id)
	if !ok {
		return // the client ended it first, or a previous release already did
	}
	entry := value.(*sessionEntry)
	bound := t.silenceBound(entry)

	entry.mu.Lock()
	silent := time.Since(entry.lastSeen)
	if silent < bound {
		entry.timer = time.AfterFunc(bound-silent, func() { t.releaseIfSilent(id) })
		entry.mu.Unlock()
		return
	}
	entry.mu.Unlock()

	// ⚠️ ONE RELEASE PER SESSION, AND THE TABLE IS THE GATE. Whoever removes the entry is the one who sends the
	// DELETE; everybody else — a second timer, a re-armed one, the client's own teardown — finds nothing and
	// sends nothing. A double DELETE is not prevented by a flag that could be read stale, it is impossible
	// because removing the entry and winning the right to send are the same atomic act.
	if !t.sessions.CompareAndDelete(id, value) {
		return
	}
	t.release(id, entry, silent, bound)
}

// release sends the DELETE and says what it did.
//
// THE DESTINATION COMES FROM THE TABLE, never from a fact about the node — the same rule route applies, for the
// same reason: a session for a phone on this machine must not have its teardown sent to a stranger's hub.
//
// NO NEW CREDENTIAL HANDLING. Credentials ride in the capabilities of a session CREATE and nowhere else (see
// browserstack.HubURL); every later frame for a session, this one included, is addressed by the session id
// alone. This is the same request the client's own teardown makes, over the same http.Client, to the same
// endpoint — the agent sends it a little earlier than nobody.
//
// AND NOTHING IS FABRICATED. No frame is answered here: the farm did not ask for this and gets no reply for it.
// The agent's own stdout is where a person reads what it did, which is the shape this programme keeps choosing
// over a made-up WebDriver response.
func (t *Tunnel) release(id string, e *sessionEntry, silent, bound time.Duration) {
	endpoint := t.endpointFor(e.dest)
	if endpoint == "" {
		return
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/session/"+id, nil)
	if err != nil {
		return
	}
	response, err := t.clientFor(e.dest).Do(request)
	if err != nil {
		// SAY WHAT HAPPENED, NOT WHAT WAS WANTED. Claiming to have ended a session we could not reach would be
		// the same false comfort the corrected idleTimeout claim was.
		t.say("session " + id + " went quiet for " + round(silent) + " and this agent could not reach " +
			place(e.dest) + " to end it (" + err.Error() + ") — it will be retired by its own timeout instead\n")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		t.say("session " + id + " went quiet for " + round(silent) + " and " + place(e.dest) +
			" refused to end it (HTTP " + strconv.Itoa(response.StatusCode) + ") — it will be retired by its own timeout instead\n")
		return
	}
	t.say("session " + id + " sent nothing for " + round(silent) + ", longer than the " + round(bound) +
		" it is allowed to go quiet for, so this agent ended it — " + freed(e.dest) + "\n")
}

// place names the far end for a sentence about reaching it.
func place(dest destination) string {
	if dest == toHub {
		return "BrowserStack's hub"
	}
	return "the local Appium"
}

// freed names WHAT WAS FREED, and the two answers differ because the costs differ: a metered slot on somebody's
// account, or a phone on the desk that nothing else could lease. One mechanism, two honest sentences.
func freed(dest destination) string {
	if dest == toHub {
		return "a cloud device on your BrowserStack account is no longer being billed for a run nobody is watching"
	}
	return "the phone on this machine is free for the next lease"
}

// round keeps a duration readable in a sentence a person reads — whole seconds, which is the resolution every
// number in this decision has.
func round(d time.Duration) string { return d.Round(time.Second).String() }

// declaredSilenceBudget reads `appium:newCommandTimeout` out of the session's own create body — the SAME
// capability sets browserstack.Merge writes into, navigated by the same function, so this cannot read a shape
// the merge does not. Zero when the session declared nothing, declared something unreadable, or declared 0.
func declaredSilenceBudget(createBody []byte) time.Duration {
	for _, set := range browserstack.CapabilitySets(createBody) {
		value, ok := set[newCommandTimeoutCapability]
		if !ok {
			continue
		}
		seconds, ok := asSeconds(value)
		if !ok || seconds <= 0 {
			continue
		}
		return time.Duration(seconds * float64(time.Second))
	}
	return 0
}

// asSeconds accepts the two shapes a capability number arrives in — a JSON number, and the string some clients
// send instead. Anything else is not a number and is treated as an absent declaration.
func asSeconds(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		seconds, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return seconds, err == nil
	default:
		return 0, false
	}
}
