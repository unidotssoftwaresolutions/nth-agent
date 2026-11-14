package tunnel

import (
	"context"
	"sync"
	"time"
)

// State is WHAT THIS AGENT KNOWS ABOUT ITS OWN TUNNEL, for the heartbeat to say — the farm's rack node's RelayTunnelState
// (A_TUNNEL_THAT_KNOWS_IT_DIED), ported rule for rule.
//
// WHAT THE HEARTBEAT SAID BEFORE: "TunnelOnly", from the existence of a driver alone — the same word whether the tunnel
// was up, reconnecting, or gone for half an hour. The heartbeat is a separate HTTPS request, the one channel that still
// works when the tunnel does not, so it is where "down" has to be said; the farm answers NodeReachability.TunnelDown by
// taking this node's devices off offer, ending their leases (DeviceTransitionReason.TunnelWentDown) and telling a lease
// in flight "its node says its tunnel to the farm is down" instead of "between reconnects".
//
// ⚠️ WHEN "DOWN" MAY BE SAID, AND WHY NEVER BY THE CLOCK. A farm restart of fifteen seconds drops the tunnel AND the
// heartbeats; by the time it is back the reconnect backoff has grown, so the first heartbeat that lands would say
// "down for twenty seconds" — and every lease on every node would end for an ordinary deploy (the review's blocker on
// the node's first draft). So it asks for EVIDENCE: the farm answered a heartbeat WHILE the tunnel was down (it is
// reachable), that answer woke the reconnect at once (WaitToReconnect), and an attempt begun after it still failed,
// with no heartbeat failing in between. A farm that was simply away gets its tunnel back within moments of its first
// answer, and never hears "down".
//
// ⚠️ ONCE THE FARM HAS HEARD "DOWN", IT STAYS SAID until the tunnel is back: a heartbeat failing afterwards (a gateway's
// blip) must not turn the next one back into "tunnel-only", which would offer every device again for a tick and take
// it off the tick after. Evidence the farm has NOT heard yet is still cleared by a failed heartbeat.
//
// The tunnel writes its side (Established, Lost, AttemptBegins/AttemptFailed); the connect loop writes the farm's side
// (FarmAnswered, FarmDidNotAnswer) and asks DeclareDown for every registration and heartbeat it builds.
type State struct {
	mu                      sync.Mutex
	established             bool
	why                     string
	farmAnsweredWhileDown   bool
	failedWhileFarmAnswered bool
	declaredDown            bool // the last declaration handed out said TunnelDown
	downHeard               bool // …and the farm answered the request that carried it
	farmAnswered            chan struct{}
	// silences counts the heartbeats the farm did not answer. An attempt remembers the count it began at, so a
	// heartbeat that failed WHILE it ran voids its failure as evidence — the rule says "no heartbeat failing in
	// between", and a flag that the next answer sets back could not say it (the review's finding, 2026-10-03; the
	// node's RelayTunnelState has the same hole: answer, attempt begins, heartbeat fails, answer, attempt fails).
	silences uint64
}

// Attempt is what AttemptBegins hands back for AttemptFailed: whether the farm had answered while the tunnel was down
// when the attempt began, and how many unanswered heartbeats there had been by then.
type Attempt struct {
	afterAnswer bool
	silences    uint64
}

// AfterTheFarmAnswered reports whether the attempt began after the farm answered while the tunnel was down.
func (a Attempt) AfterTheFarmAnswered() bool { return a.afterAnswer }

// NewState starts down: a process that has not established a tunnel yet has none.
func NewState() *State {
	return &State{farmAnswered: make(chan struct{})}
}

// Established: a tunnel is up, and whatever evidence there was is spent.
func (s *State) Established() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.established = true
	s.why = ""
	s.farmAnsweredWhileDown = false
	s.failedWhileFarmAnswered = false
	s.declaredDown = false
	s.downHeard = false
}

// Lost: the established tunnel ended, and why, in the tunnel's words.
func (s *State) Lost(why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.established = false
	s.why = why
}

// AttemptBegins is called as a connect attempt begins; the caller hands what it returns back to AttemptFailed.
func (s *State) AttemptBegins() Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Attempt{afterAnswer: s.farmAnsweredWhileDown, silences: s.silences}
}

// AttemptFailed: a connect attempt failed. It is evidence against the TUNNEL only when it began after the farm answered
// while the tunnel was down, and no heartbeat has failed since it began.
func (s *State) AttemptFailed(why string, attempt Attempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.why = why
	if attempt.afterAnswer && attempt.silences == s.silences && s.farmAnsweredWhileDown {
		s.failedWhileFarmAnswered = true
	}
}

// FarmAnswered: the farm answered a registration or heartbeat (a 404 "unknown node" included — an answer is an
// answer). While the tunnel is down this also wakes the reconnect at once: the farm is there, so the tunnel gets its
// next attempt now rather than at the end of a backoff that grew while it was away.
func (s *State) FarmAnswered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.established {
		return
	}
	s.farmAnsweredWhileDown = true
	if s.declaredDown {
		s.downHeard = true
	}
	close(s.farmAnswered)
	s.farmAnswered = make(chan struct{})
}

// FarmTurnedAway: the farm answered a registration or heartbeat with a 429 — it is there, and this machine is past one of
// its limits (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO). It is an answer, so it is FarmAnswered's evidence; but it does NOT
// wake the reconnect. The heartbeat that met it backs off, and a wake at every one would dial the tunnel faster than a 429
// did before it counted, when it was no answer at all and the tunnel waited out its own backoff. Nor has the farm heard a
// "down" the request carried: a 429 applies nothing.
func (s *State) FarmTurnedAway() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.established {
		return
	}
	s.farmAnsweredWhileDown = true
}

// FarmDidNotAnswer: a registration or heartbeat got no answer that says the farm can be reached — a transport failure,
// or a "busy" that a gateway in front of an absent farm also gives. Nothing learned about the tunnel until it answers
// again is evidence against it — unless the farm has already heard "down", which stays said.
func (s *State) FarmDidNotAnswer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.silences++
	s.farmAnsweredWhileDown = false
	if !s.downHeard {
		s.failedWhileFarmAnswered = false
		s.declaredDown = false
	}
}

// WaitToReconnect waits out the reconnect backoff — or less, when the farm answers meanwhile. True when cut short.
func (s *State) WaitToReconnect(ctx context.Context, backoff time.Duration) bool {
	s.mu.Lock()
	answered := s.farmAnswered
	s.mu.Unlock()
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	case <-answered:
		return true
	}
}

// DeclareDown is this registration's or heartbeat's word: true once a reconnect begun after the farm answered has
// failed (see the type's remarks for why that is evidence and the clock is not), or once the farm has heard it.
// Why is the tunnel's own account of the outage, for the one line the agent says when it starts declaring it. There
// is deliberately no duration here: how long it has been down is exactly the fact this rule refuses to decide on.
func (s *State) DeclareDown() (down bool, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.declaredDown = !s.established && (s.downHeard || s.failedWhileFarmAnswered)
	return s.declaredDown, s.why
}
