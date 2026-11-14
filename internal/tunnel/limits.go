package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// ── WHAT THIS AGENT HOLDS AT ONCE, ACROSS EVERY TUNNEL IT HAS HAD (ANOTHER_ACCOUNT_AND_LIMITS) ───────────────────────
//
// The request queue bounds what WAITS on one tunnel (tunnel.go). What it did not bound was everything else the farm end
// of a tunnel can start: the handback questions, which never entered the queue; the answers to frames the queue
// refused, each held with its whole frame until the farm read again; the bytes waiting to be written to the farm; the
// answers read from Appium and the hub on their way out; and the forwards and creates still running from a tunnel that
// has since ended, which the next tunnel's queue does not count. Each now has a limit of its own, kept by the Tunnel —
// so it holds across reconnects — and each is chosen so that a run today's farm sends never meets it. A limit met is
// said once on this terminal, and, where the frame has an answer, answered in words.

const (
	// MaxFrameIDBytes is the longest frame id this agent echoes. The farm names every request and stream with a GUID
	// (RelayEndpoint, RelayStreamEndpoint: Guid.NewGuid(), 36 characters), and the agent writes the id back in its
	// answer; a frame whose id is longer than this is skipped unanswered, so an id is never the farm's way to make the
	// agent hold or send more than it asked.
	MaxFrameIDBytes = 128
	// MaxPendingRefusals is how many answers to refused frames may wait for room to be sent, across every tunnel. A
	// refusal waits only while the farm reads nothing (the send queue at ResponseHighWater) and the request queue is
	// full (MaxQueuedRequests waiting), which today's farm, with at most one request in flight per leased phone, never
	// reaches; past this one a refused frame is not answered, and the farm's own deadline fails it.
	MaxPendingRefusals = MaxQueuedRequests
	// MaxHandbacks is how many of the farm's handback questions are answered at once. The farm asks them one at a time
	// (CaptureCoordinator, awaiting each) and only about a phone that was captured; this machine captures at most
	// capture.MaxConcurrent (4) phones at once. Twice that leaves room for a farm's retry of a question still being
	// answered. A question past it is answered at once with a 503 in words, and the farm asks again (PhoneStateNode).
	MaxHandbacks = 8
	// MaxHandbackQuestionBytes is the largest handback question read: the farm's is {"udid":…,"leaseId":…}, a serial of
	// at most 128 characters (capture.IsAdbSerial) and a GUID, under 250 bytes as base64.
	MaxHandbackQuestionBytes = 4096
	// MaxForwards is how many requests may be on their way to Appium or the hub at once, across every tunnel this agent
	// has had: a forward outlives its tunnel (serveLane). Today's farm has at most one request in flight per leased
	// phone, and a node at most NodeReportLimits.MaxDevices (200) phones; a reconnect can leave the old tunnel's still
	// running beside the new one's, so 400. 512 is over that. A request past it is answered at once with a 503 in
	// words.
	MaxForwards = 512
	// MaxAnswerBytesInFlight is how many bytes of answers read from Appium and the hub may be held at once, from the
	// first byte read until the answer is in the send queue, across every tunnel. Five answers of the largest size a
	// tunnel message carries (MaxAnswerBytes), or 1,600 of the load harness's 160 KB screenshots (loadtest/fakephone,
	// DefaultScreenshotBytes) at the same moment; a run's answers are read in milliseconds on loopback and queued at
	// once while the farm reads. An answer that would pass it is not relayed: a 502 in words.
	MaxAnswerBytesInFlight int64 = 256 << 20
	// MaxSendQueueBytes is the most this agent keeps waiting to be written to the farm. Answers queue only below
	// ResponseHighWater (8 MiB), each held under MaxAnswerBytesInFlight until then, so answers alone stay under 8 MiB +
	// 4/3 x 256 MiB (base64) = 350 MiB; a live view waits for PumpHighWater. What else queues is the farm's own doing:
	// an end for every stream it starts and a pong for every ping, which never wait. A farm that keeps sending while it
	// reads so little that more than this waits ends the tunnel, said on this terminal, and it is dialled again.
	MaxSendQueueBytes int64 = 512 << 20
	// CreateDeadline is the farm's RelayPublishOptions.RequestTimeoutSeconds: once a request is that old, the farm has
	// failed it and removed it (InMemoryTunnelRegistry), and nobody waits for its answer. A session create that waited
	// longer than this on this machine (behind another create) is not sent to Appium: a session it started would belong
	// to no lease, and hold its phone.
	CreateDeadline = 190 * time.Second
)

// agentLimits is the state those limits keep, one for the Tunnel and every tunnel it dials.
type agentLimits struct {
	once        sync.Once
	createTurn  chan struct{} // one session create on its way to Appium or the hub at a time, on this machine
	handbacks   chan struct{}
	forwards    atomic.Int64
	answerBytes atomic.Int64
	refusals    atomic.Int64

	saidLongID, saidRefusals, saidHandbacks, saidForwards, saidAnswers, saidStale atomic.Bool
}

func (t *Tunnel) lim() *agentLimits {
	t.limits.once.Do(func() {
		t.limits.createTurn = make(chan struct{}, 1)
		t.limits.handbacks = make(chan struct{}, t.maxHandbacks())
	})
	return &t.limits
}

// The test seams, each falling back to the number above.
func (t *Tunnel) createDeadline() time.Duration {
	return orDuration(t.cfg.CreateDeadline, CreateDeadline)
}
func (t *Tunnel) maxForwards() int        { return orInt(t.cfg.MaxForwards, MaxForwards) }
func (t *Tunnel) maxHandbacks() int       { return orInt(t.cfg.MaxHandbacks, MaxHandbacks) }
func (t *Tunnel) maxPendingRefusals() int { return orInt(t.cfg.MaxPendingRefusals, MaxPendingRefusals) }
func (t *Tunnel) maxSendQueueBytes() int64 {
	return orInt64(t.cfg.MaxSendQueueBytes, MaxSendQueueBytes)
}
func (t *Tunnel) maxAnswerBytesInFlight() int64 {
	return orInt64(t.cfg.MaxAnswerBytesInFlight, MaxAnswerBytesInFlight)
}

// idTooLong says whether a frame's id is longer than any the farm mints, and says so once. Such a frame is skipped: its
// answer would echo the id.
func (l *link) idTooLong(id string) bool {
	if len(id) <= MaxFrameIDBytes {
		return false
	}
	if l.t.lim().saidLongID.CompareAndSwap(false, true) {
		l.t.say(fmt.Sprintf("the farm sent a frame whose id is %d bytes long, longer than the %d this agent echoes (the farm "+
			"names each with a GUID) — it was skipped unanswered, and every one like it is\n", len(id), MaxFrameIDBytes))
	}
	return true
}

// refuse answers a frame in words, off the read loop, when the send queue has room — holding the id and the sentence,
// never the frame. At most maxPendingRefusals wait at once; past that the frame is not answered, and the farm's own
// deadline fails it.
func (l *link) refuse(id string, status int, sentence string) {
	lim := l.t.lim()
	if lim.refusals.Add(1) > int64(l.t.maxPendingRefusals()) {
		lim.refusals.Add(-1)
		if lim.saidRefusals.CompareAndSwap(false, true) {
			l.t.say(fmt.Sprintf("the farm keeps sending requests while it reads none of this agent's answers — %d refusals are "+
				"already waiting to be sent, its most, so each one past that goes unanswered and the farm's own deadline fails it\n",
				l.t.maxPendingRefusals()))
		}
		return
	}
	answer := answerInWords(id, status, sentence)
	go func() {
		defer lim.refusals.Add(-1)
		if l.queue.WaitBelow(l.life, l.t.responseHighWater()) == nil {
			_ = l.send(answer)
		}
	}()
}

// handback answers the farm's handback question off the read loop, at most maxHandbacks at once across every tunnel; a
// question past that is answered at once, in words, and asked again by the farm.
func (l *link) handback(frame requestFrame) {
	lim := l.t.lim()
	// A QUESTION IS A PHONE AND A LEASE, about a hundred bytes; one larger than MaxHandbackQuestionBytes is answered as
	// a question without them, and its frame is not held while a slot is.
	if frame.BodyBase64 != nil && len(*frame.BodyBase64) > MaxHandbackQuestionBytes {
		l.refuse(frame.ID, http.StatusBadRequest, "A capture handback was asked for without a phone and the lease that captured on it.")
		return
	}
	select {
	case lim.handbacks <- struct{}{}:
	default:
		if lim.saidHandbacks.CompareAndSwap(false, true) {
			l.t.say(fmt.Sprintf("the farm asked more capture handback questions at once than the %d this agent answers at once "+
				"— each one past that was answered with a 503, and the farm asks it again\n", l.t.maxHandbacks()))
		}
		l.refuse(frame.ID, http.StatusServiceUnavailable, fmt.Sprintf("Relay forward error: this agent is already answering %d "+
			"capture handback questions, its most, so this one was not asked; ask it again", l.t.maxHandbacks()))
		return
	}
	go func() {
		defer func() { <-lim.handbacks }()
		l.answerHandback(frame)
	}()
}

// forwardWithinTheLimits is one lane's forward under the agent's limits. answer false: nothing is sent back (the farm
// has already failed the request, or the tunnel ended while it waited). done releases what the answer holds; the lane
// calls it once the answer is queued, or dropped.
func (l *link) forwardWithinTheLimits(item queuedRequest) (response responseFrame, answer bool, done func()) {
	lim, frame := l.t.lim(), item.frame
	if browserstack.IsSessionCreate(frame.Method, frame.Path) {
		// ONE CREATE AT A TIME ON THIS MACHINE, whichever tunnel it came on: the create lane is one per tunnel, and a
		// create from a tunnel that has ended still runs (serveLane).
		select {
		case lim.createTurn <- struct{}{}:
		case <-l.life.Done():
			return responseFrame{}, false, func() {}
		}
		defer func() { <-lim.createTurn }()
		if waited := time.Since(time.Unix(0, item.arrived)); waited > l.t.createDeadline() {
			if lim.saidStale.CompareAndSwap(false, true) {
				l.t.say(fmt.Sprintf("a session create waited %s on this machine behind another, longer than the farm's %s — the "+
					"farm had already given up on it, so it was not sent on, and no session was started for it; every one like it is "+
					"left the same way\n", waited.Round(time.Second), l.t.createDeadline()))
			}
			return responseFrame{}, false, func() {}
		}
	}
	if lim.forwards.Add(1) > int64(l.t.maxForwards()) {
		lim.forwards.Add(-1)
		if lim.saidForwards.CompareAndSwap(false, true) {
			l.t.say(fmt.Sprintf("the farm sent more requests at once than the %d this agent has on their way to Appium and the "+
				"hub at a time — each one past that is answered at once with a 503, and nothing of it is sent on\n", l.t.maxForwards()))
		}
		return answerInWords(frame.ID, http.StatusServiceUnavailable, fmt.Sprintf("Relay forward error: this agent already has %d "+
			"requests on their way to Appium and the hub, its most, so this one was not sent on", l.t.maxForwards())), true, func() {}
	}
	charge := &answerCharge{lim: lim, max: l.t.maxAnswerBytesInFlight(), say: l.t.say}
	response = l.t.forward(context.WithValue(l.agent, answerChargeKey{}, charge), frame)
	lim.forwards.Add(-1)
	return response, true, charge.release
}

// answerCharge is what one answer holds of maxAnswerBytesInFlight.
type answerCharge struct {
	lim  *agentLimits
	max  int64
	say  func(string)
	held atomic.Int64
}

type answerChargeKey struct{}

// errAnswersHeld is an answer not read because the answers already held would pass maxAnswerBytesInFlight.
var errAnswersHeld = errors.New("this agent is already holding as many bytes of answers on their way to the farm as it holds at once")

func (c *answerCharge) release() { c.lim.answerBytes.Add(-c.held.Swap(0)) }

// charged reads r, counting what it reads against the answers held at once (the charge forward's context carries).
func charged(ctx context.Context, r io.Reader) io.Reader {
	charge, _ := ctx.Value(answerChargeKey{}).(*answerCharge)
	if charge == nil {
		return r
	}
	return chargedReader{r, charge}
}

type chargedReader struct {
	r      io.Reader
	charge *answerCharge
}

func (c chargedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.charge.held.Add(int64(n))
		if c.charge.lim.answerBytes.Add(int64(n)) > c.charge.max {
			if c.charge.lim.saidAnswers.CompareAndSwap(false, true) {
				c.charge.say(fmt.Sprintf("answers on their way to the farm already hold %d bytes here, this agent's most — each "+
					"answer past that is not relayed and is answered with a 502 instead, and the tunnel stays up\n", c.charge.max))
			}
			return n, fmt.Errorf("%w (%d bytes), so this answer was not relayed — the request itself did reach Appium or the hub", errAnswersHeld, c.charge.max)
		}
	}
	return n, err
}

// limitTo sets the most that may wait in the send queue (MaxSendQueueBytes).
func (q *queuedConn) limitTo(ceiling int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ceiling = ceiling
}

// Overfull reports whether the connection was closed because more than its ceiling waited to be written.
func (q *queuedConn) Overfull() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.overfull
}

// overfullSentence is how a tunnel the send queue's ceiling ended is said (howItEnded).
func overfullSentence(ceiling int64) string {
	return fmt.Sprintf("the farm took so little of what this agent sent it that more than %d bytes waited to be sent, this "+
		"agent's most, so it ended the tunnel", ceiling)
}

// scrubbedTooLarge answers a hub answer that the key scrub made longer than one tunnel message carries: the mask is
// longer than a short user name it replaces.
func (t *Tunnel) scrubbedTooLarge(id string, size int) responseFrame {
	if t.saidTooLarge.CompareAndSwap(false, true) {
		t.say(fmt.Sprintf("an answer from BrowserStack's hub, with your access key replaced in it, was larger than the %d bytes "+
			"one tunnel message carries, so it was not relayed — the run was told so with a 502, and the tunnel stays up for "+
			"every other phone\n", t.maxAnswerBytes()))
	}
	return errorResponse(id, fmt.Sprintf("Relay forward error: the hub's answer, with your access key replaced in it, was "+
		"%d bytes, larger than the %d one tunnel message carries, so it was not relayed", size, t.maxAnswerBytes()))
}

// tooLargeToSend is a frame larger than one tunnel message, which the farm would end the tunnel for reading: never
// written. An answer is answered in words instead, so its request is not left to the farm's deadline.
func (l *link) tooLargeToSend(frame any, size int) error {
	err := fmt.Errorf("a frame of %d bytes is larger than the %d one tunnel message carries, so it was not sent", size, l.t.maxMessageBytes())
	if answer, ok := frame.(responseFrame); ok {
		l.t.say(fmt.Sprintf("an answer of %d bytes is larger than the %d one tunnel message carries, so it was not sent — the "+
			"run was told so with a 502\n", size, l.t.maxMessageBytes()))
		_ = l.send(errorResponse(answer.ID, "Relay forward error: "+err.Error()))
	}
	return err
}
