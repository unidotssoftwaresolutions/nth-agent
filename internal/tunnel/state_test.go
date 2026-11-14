package tunnel_test

import (
	"context"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── RULE 3 · "DOWN" IS SAID ON EVIDENCE, NEVER BY THE CLOCK ─────────────────────────────────────────────────────────
//
// The node's RelayTunnelState sequences, driven against the port. There is no clock anywhere in these: the rule must
// not need one, and a version that read one (the node's first draft, "down after five seconds") fails the first.

func down(s *tunnel.State) bool {
	said, _ := s.DeclareDown()
	return said
}

func TestAFarmThatWasAwayLongerThanAnyBoundNeverHearsDown(t *testing.T) {
	// A farm restart: the tunnel drops AND the heartbeats fail, for as long as the restart takes; every reconnect in
	// between fails too. Then the farm answers, the reconnect it wakes succeeds — and no report ever said "down".
	s := tunnel.NewState()
	s.Established()
	s.Lost("the farm closed it (StatusGoingAway)")
	for i := 0; i < 50; i++ { // as many failed rounds as you like: the count is not evidence either
		if down(s) {
			t.Fatalf("round %d of a farm restart said down — every lease on every node would end for a deploy", i)
		}
		s.FarmDidNotAnswer()
		begun := s.AttemptBegins()
		s.AttemptFailed("could not connect: connection refused", begun)
	}
	s.FarmAnswered() // the farm is back…
	if down(s) {
		t.Fatal("the farm's first answer after a restart said down before the reconnect it woke had even run")
	}
	begun := s.AttemptBegins()
	if !begun.AfterTheFarmAnswered() {
		t.Fatal("an attempt begun after the farm answered must know it")
	}
	s.Established() // …and the reconnect it woke succeeded
	if down(s) {
		t.Fatal("a tunnel that is up is never down")
	}
}

func TestAReconnectBegunAfterTheFarmAnsweredThatFailsIsDown(t *testing.T) {
	s := tunnel.NewState()
	s.Established()
	s.Lost("found dead: the farm did not answer this agent's ping within 30s")
	s.FarmAnswered()
	s.AttemptFailed("could not connect: failed to WebSocket dial: expected handshake response status code 101 but got 403", s.AttemptBegins())
	said, why := s.DeclareDown()
	if !said || why == "" {
		t.Fatalf("the farm answers and the tunnel still cannot be opened: that is the evidence (said=%v why=%q)", said, why)
	}

	// An attempt that began BEFORE the farm answered is not evidence, even if it fails after.
	s = tunnel.NewState()
	s.Established()
	s.Lost("x")
	begunBefore := s.AttemptBegins()
	s.FarmAnswered()
	s.AttemptFailed("could not connect", begunBefore)
	if down(s) {
		t.Fatal("an attempt that began before the farm answered is about a farm that may have been away")
	}
}

func TestDownOnceHeardStaysSaidUntilTheTunnelIsBack(t *testing.T) {
	s := tunnel.NewState()
	s.Lost("x")
	s.FarmAnswered()
	s.AttemptFailed("could not connect", s.AttemptBegins())
	if !down(s) {
		t.Fatal("setup: the evidence should say down")
	}
	s.FarmAnswered() // the heartbeat that carried "down" was answered: the farm has HEARD it
	s.FarmDidNotAnswer()
	if !down(s) {
		t.Fatal("a failed heartbeat after the farm heard down turned it back into tunnel-only — every device offered for a tick")
	}
	s.Established()
	if down(s) {
		t.Fatal("a tunnel that is back is not down")
	}
}

func TestEvidenceTheFarmHasNotHeardIsClearedByAFailedHeartbeat(t *testing.T) {
	s := tunnel.NewState()
	s.Lost("x")
	s.FarmAnswered()
	s.AttemptFailed("could not connect", s.AttemptBegins())
	if !down(s) {
		t.Fatal("setup: the evidence should say down")
	}
	s.FarmDidNotAnswer() // the heartbeat carrying it got no answer: the farm may be the one away after all
	if down(s) {
		t.Fatal("evidence the farm never heard must not survive a heartbeat that failed")
	}
}

func TestTheFarmsAnswerWakesTheReconnect(t *testing.T) {
	s := tunnel.NewState()
	s.Lost("x")
	woke := make(chan bool, 1)
	go func() { woke <- s.WaitToReconnect(context.Background(), time.Minute) }()
	time.Sleep(50 * time.Millisecond)
	s.FarmAnswered()
	select {
	case cut := <-woke:
		if !cut {
			t.Fatal("the wait ended but does not say it was cut short")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the farm answered and the reconnect kept waiting out its backoff")
	}

	// While the tunnel is up there is nothing to wake.
	s.Established()
	go func() { time.Sleep(20 * time.Millisecond); s.FarmAnswered() }()
	start := time.Now()
	if s.WaitToReconnect(context.Background(), 100*time.Millisecond) || time.Since(start) < 90*time.Millisecond {
		t.Fatal("an answer while the tunnel is up woke nothing, so the wait runs its course")
	}
}

func TestAHeartbeatThatFailsWhileAnAttemptRunsVoidsItsFailure(t *testing.T) {
	// The rule's own words — "no heartbeat failing in between" — over the sequence the review found: the farm answers,
	// an attempt begins, a heartbeat FAILS, the farm answers again, the attempt fails. A rolling deploy whose heartbeats
	// flap during one slow attempt looks exactly like this, and must not end every lease on the machine. (the farm's rack node's
	// RelayTunnelState, which this ports, takes this sequence as evidence.)
	s := tunnel.NewState()
	s.Established()
	s.Lost("x")
	s.FarmAnswered()
	attempt := s.AttemptBegins()
	s.FarmDidNotAnswer()
	s.FarmAnswered()
	s.AttemptFailed("could not connect: refused", attempt)
	if down(s) {
		t.Fatal("an attempt that a failed heartbeat interrupted is not evidence against the tunnel")
	}
	// The next attempt, begun after that answer and failing with no heartbeat failing meanwhile, is.
	s.AttemptFailed("could not connect: refused", s.AttemptBegins())
	if !down(s) {
		t.Fatal("an attempt begun after the farm answered, with no heartbeat failing since, is evidence")
	}
}
