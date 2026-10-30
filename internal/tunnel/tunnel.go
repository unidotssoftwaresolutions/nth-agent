// Package tunnel is the drive path: an outbound WebSocket to the farm's /v1/nodes/{id}/tunnel that
// replays request frames against the LOCAL Appium and returns the responses — a PORT of the farm's rack node's
// RelayTunnelClient (the reference implementation), not an invention. Nothing listens; no port opens.
//
// AN_AGENT_THAT_SHOWS_ITS_PHONE moved it to the node's CURRENT shape (A_TUNNEL_THAT_KNOWS_IT_DIED): requests are
// answered by one worker, in order, OFF the read loop, so the loop always reads and a ping is always answered; this end
// pings the farm and gives up on a silence (liveness.go), says it reads at all times so the farm pings it too, writes
// large frames as fragments through a send queue that never makes the library wait on the network (sendqueue.go), and
// tells the heartbeat what it knows about itself (state.go). And it carries the live view (stream.go).
package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// The reference's constants, ported with their reasons:
const (
	// ReconnectFloor/Cap — RelayTunnelClient reconnects from ~1s doubling to ~30s, plus 0–750ms jitter.
	ReconnectFloor = time.Second
	ReconnectCap   = 30 * time.Second
	// ReconnectCapAtTheLimit — after the farm refused the upgrade with a 429 (its tunnel limit), the backoff doubles to a
	// minute instead, as the farm asks (NodesController: "1 s doubling to a minute"), and no heartbeat cuts it short.
	ReconnectCapAtTheLimit = time.Minute
	// AppiumForwardTimeout — ≥ the driver's 180s command timeout + margin, exactly the reference's 200s:
	// a forward that gives up BEFORE the driver's own command timeout would turn a slow-but-succeeding
	// Appium command into a 502 the driver never asked for. THE CHAIN, outermost hop: consumer (the runner,
	// 180s) < farm relay (RelayPublishOptions.RequestTimeoutSeconds, 190s) < this (200s) — each hop keeps
	// headroom over the one in front of it so the hop that owns a deadline is the one that reports it
	// (A_MARGIN_IS_A_PROPERTY_OF_THE_CHAIN). The farm's number moved into the gap; this one did not chase it.
	AppiumForwardTimeout = 200 * time.Second
	// (A `KeepAlive = 30 * time.Second` stood here, "detect dead tunnels; keep NAT warm". Nothing ever read it, and
	// coder/websocket has no keep-alive of its own, so this tunnel had none. liveness.go is what replaced it.)
)

// WHAT ONE FARM CAN MAKE THIS AGENT HOLD (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST). Each bound lets through everything today's
// The farm sends; what passes it is refused in words, never dropped silently.
const (
	// MaxMessageBytes is RelayProtocol.MaxMessageBytes, 64 MiB: the largest message EITHER end of a tunnel reads. The
	// farm refuses a consumer's body that would not fit in one (RelayEndpoint, BodyFitsInAFrame), so every request frame it
	// sends is at most this; one byte more ends this tunnel, unread, and it is dialled again.
	MaxMessageBytes int64 = 64 << 20
	// MaxAnswerBytes is the largest Appium or hub answer relayed: 50,282,496 bytes, the largest body whose frame —
	// base64, plus the contract's 64 KiB for id, status and headers — still fits in MaxMessageBytes (BodyFitsInAFrame). A
	// larger answer would end the farm's side of the tunnel, every other lease's requests on it included; it is answered
	// with a plain 502 instead, as the reference node does.
	MaxAnswerBytes int64 = 50_282_496
	// MaxQueuedRequests and MaxQueuedBytes bound the request queue. Today's farm has at most one request in flight
	// per lease and a node at most NodeReportLimits.MaxDevices (200) leasable devices, so 512 is over twice what it can have
	// waiting; 128 MiB is two of its largest messages. A frame past either is answered at once with a 503 in words.
	MaxQueuedRequests       = 512
	MaxQueuedBytes    int64 = 2 * MaxMessageBytes
	// maxRelayedContentType keeps the answer's one relayed header inside the contract's 64 KiB allowance.
	maxRelayedContentType = 1024
)

// Frame shapes — the wire contract verbatim (JSON over websocket TEXT messages, camelCase — the
// farm's RelayProtocol uses JsonSerializerDefaults.Web). A frame with no "kind" is a request; kinds
// name the stream/capture families.
type requestFrame struct {
	Kind       string            `json:"kind,omitempty"`
	ID         string            `json:"id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 *string           `json:"bodyBase64"`
}

type responseFrame struct {
	ID         string            `json:"id"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 *string           `json:"bodyBase64"`
}

// Config wires the tunnel. Every timing is a parameter so tests drive it without sleeping.
type Config struct {
	FarmURL   string // https://… — flipped to wss:// exactly like the reference flips its backend URL
	NodeID    string
	Token     string // rides X-Agent-Token on the upgrade, like every other farm call
	AppiumURL string // the LOCAL Appium every request frame is replayed against — and nowhere else

	// BrowserStack, when the customer supplied both halves on THIS machine, redirects the replay to their hub
	// instead of the local Appium and merges the credentials into the session-creation body. Zero value ⇒ the
	// agent behaves exactly as it did before this existed: same target, same bytes, same everything.
	BrowserStack browserstack.Credentials
	// BrowserStackHub overrides the hub, and it is a TEST SEAM rather than a customer surface: no flag reaches
	// it, no environment variable reaches it, and empty means browserstack.HubURL. It exists so the stub-hub
	// test drives the SAME code path the live one does — a merge proved against a different path is a merge
	// nobody proved.
	BrowserStackHub string
	// BrowserStackApp is the bs://… id (or custom_id) of the build a cloud session runs. BrowserStack cannot
	// start a session without one, and the hub's refusal names platforms and browsers rather than builds — so
	// the agent refuses first, where the sentence can name the real thing.
	BrowserStackApp string

	Forward        *http.Client // nil ⇒ a client with AppiumForwardTimeout
	ReconnectFloor time.Duration
	ReconnectCap   time.Duration
	// ReconnectCapAtTheLimit overrides the cap after a 429, and it is a TEST SEAM: no flag or variable reaches it, and zero
	// means ReconnectCapAtTheLimit.
	ReconnectCapAtTheLimit time.Duration
	// ReleaseMargin overrides what is added to a session's own declared silence budget before this agent ends
	// it, and it is a TEST SEAM rather than a customer surface — exactly like BrowserStackHub above: no flag
	// reaches it, no environment variable reaches it, and zero means ReleaseMarginDefault. It exists so the
	// seals drive the SAME code path a real session does, at a duration a test can wait for.
	ReleaseMargin time.Duration
	Sleep         func(ctx context.Context, d time.Duration)
	Out           io.Writer // narration: STATE CHANGES only (connected / dropped / unsupported family)

	// State is what this tunnel tells the heartbeat about itself — see State. nil = nothing is recorded and the
	// reconnect waits out its backoff with Sleep, which is what every tunnel did before it existed.
	State *State
	// Mirrors is which phones' mirrors this tunnel may stream, kept by the connect loop. nil = none: every live view
	// is refused, in words.
	Mirrors *liveview.Offered
	// Phones is which phones this agent offers right now, kept by the connect loop. A session for a phone on this
	// machine reaches Appium only when it names one of them, and only with capabilities on the allow-list
	// (internal/allowed). nil = none: every local session is refused, in words.
	Phones *allowed.Phones
	// Capture is this machine's network capture (capture.Controller): capture-start and capture-stop go to it, the farm's
	// handback question is answered by it, and a capture pull is served from it. nil = this tunnel carries none — every
	// capture frame is refused in words, and the handback path is forwarded like any other, as before capture existed.
	Capture CaptureControl
	Builds  BuildShelf // where a build is fetched to, counted and handed to Appium from (builds.go); zero = no folder: every build is refused
	// TEST SEAMS, each zero = the node's number (liveness.go, stream.go). No flag and no environment variable reaches
	// any of them; they exist so the seals drive the same code at durations a test can wait for.
	PingInterval      time.Duration
	AnswerWithin      time.Duration
	WriteBound        time.Duration
	MaxStreams        int
	StreamIdle        time.Duration
	PumpHighWater     int
	ResponseHighWater int
	// The bounds on what the farm can make this agent hold (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST), each zero = the
	// number named beside its constant: one inbound message, one forwarded answer, and the request queue.
	MaxMessageBytes   int64
	MaxAnswerBytes    int64
	MaxQueuedRequests int
	MaxQueuedBytes    int64
	// StreamClient opens a mirror; nil = the loopback-only client. TLSClientConfig is the upgrade's TLS; nil = the
	// system's. Both are seams for the same reason as the timings above.
	StreamClient    *http.Client
	TLSClientConfig *tls.Config
	// LocalOwner names the account whose program answered a connection to this machine's Appium or a phone's mirror; nil =
	// the kernel's answer (internal/localconn). A TEST SEAM: no flag or variable reaches it.
	LocalOwner localconn.Owner
}

// Tunnel runs the connect-serve-reconnect loop and answers "is the drive path up right now".
// destination is where a frame goes — decided PER SESSION, never per node. See route.
type destination int

const (
	toLocal destination = iota + 1
	toHub
)

type Tunnel struct {
	cfg Config
	// local carries every frame for this machine's Appium: loopback only, and only to a program this account runs
	// (internal/localconn). cfg.Forward carries the hub's, which is a host on the internet and is not held to that rule.
	local *http.Client
	// sessions is the routing table: session id → *sessionEntry, which is where that session was created, how
	// long it is allowed to go quiet, and when it was last driven. Every later frame for it goes to the same
	// place. Filled from the create response's own sessionId, emptied on DELETE /session/{id} — and emptied by
	// the release when nobody sends that DELETE at all (see release.go, which is the whole reason the value is
	// no longer a bare destination).
	// A session this tunnel did not create — after a restart, every session it had — has no entry and is
	// refused rather than routed by a fact about the node.
	sessions        sync.Map
	connected       atomic.Bool
	saidDown        atomic.Bool // said "dropped"/"not connected yet" for the CURRENT outage (reset on connect)
	saidWoken       atomic.Bool // said "the farm answered a heartbeat — dialling now" for the CURRENT outage (reset on connect)
	everConnected   atomic.Bool // THREE STATES need two bits: never connected / established / reconnected
	saidNoHar       atomic.Bool
	saidRefused     string // the refused upgrade last said this outage, as status and sentence (sayRefused); Run's goroutine only
	saidBadPull     atomic.Bool
	saidNotAMirror  atomic.Bool
	saidStreaming   atomic.Bool
	saidNoCapture   atomic.Bool
	saidNoBudget    atomic.Bool // said "this session declared no silence budget" (see release.go's arm)
	saidQueueFull   atomic.Bool
	saidUnrooted    atomic.Bool
	saidSessionless atomic.Bool
	saidRedirect    atomic.Bool
	saidTooLarge    atomic.Bool
	shelf           shelfState
}

func New(cfg Config) *Tunnel {
	if cfg.Forward == nil {
		cfg.Forward = &http.Client{Timeout: AppiumForwardTimeout}
	}
	// THE HUB'S REDIRECT IS NOT FOLLOWED, whoever built the client: a redirect is an address nobody checked, and a session
	// create carries the customer's access key in its body, which Go carries along a 307 or 308. forward answers it in words.
	forward := *cfg.Forward
	forward.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.Forward = &forward
	// EVERYTHING THIS TUNNEL SAYS goes through the agent's terminal filter: a farm's words in it cannot drive the customer's terminal.
	cfg.Out = farm.TerminalWriter(cfg.Out)
	if cfg.ReconnectFloor <= 0 {
		cfg.ReconnectFloor = ReconnectFloor
	}
	if cfg.ReconnectCap <= 0 {
		cfg.ReconnectCap = ReconnectCap
	}
	if cfg.ReconnectCapAtTheLimit <= 0 {
		cfg.ReconnectCapAtTheLimit = ReconnectCapAtTheLimit
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	if cfg.StreamClient == nil {
		cfg.StreamClient = loopbackStreamClient(cfg.LocalOwner)
	}
	return (&Tunnel{cfg: cfg, local: localconn.Client("your Appium", AppiumForwardTimeout, cfg.LocalOwner)}).openBuilds()
}

// Connected is the drive-path truth the report builder consults: A DEVICE IS OFFERED ONLY WHILE THIS
// IS TRUE — a registered device whose tunnel is down would be the phantom again (published Available,
// nobody can drive it). Flipped false the moment serve returns, not when a retry gives up.
func (t *Tunnel) Connected() bool { return t.connected.Load() }

// RoutedSessions is how many sessions this tunnel currently knows a destination for — for the seals that prove
// a deleted session is forgotten and an unknown one was never guessed at.
func (t *Tunnel) RoutedSessions() int {
	n := 0
	t.sessions.Range(func(_, _ any) bool { n++; return true })
	return n
}

// Run dials, serves, and reconnects with backoff until ctx ends. Narrates state changes once.
//
// THE TRANSPORT RULE APPLIES HERE TOO, at this path's own point of use: the tunnel upgrade carries
// X-Agent-Token and does NOT ride Client.post(), so it validates the farm URL itself — a ws:// (plain)
// upgrade to a non-loopback host would hand the token to the network exactly like plain http. (The
// token census caught this dial as an unclassified token read before this check existed.)
// EverConnected reports whether this tunnel has been established at least once — the fact that separates "not connected yet" from "dropped" for the reader.
func (t *Tunnel) EverConnected() bool { return t.everConnected.Load() }

func (t *Tunnel) Run(ctx context.Context) {
	defer t.removeAllBuilds() // the agent is stopping: no build it fetched outlives it (builds.go)
	if err := farm.ValidateBaseURL(t.cfg.FarmURL); err != nil {
		fmt.Fprintf(t.cfg.Out, "%v — the drive tunnel will not dial\n", farm.OneLineForTerminal(err.Error()))
		return
	}
	url := tunnelURL(t.cfg.FarmURL, t.cfg.NodeID)
	backoff := t.cfg.ReconnectFloor
	atTheLimit := false // the last attempt was an upgrade the farm refused with a 429
	for ctx.Err() == nil {
		// Whether the farm has answered a heartbeat since the tunnel went down: a failure of THIS attempt is then
		// evidence against the tunnel, not against a farm that is simply away (State).
		var attempt Attempt
		if t.cfg.State != nil {
			attempt = t.cfg.State.AttemptBegins()
		}
		established, err := t.connectAndServe(ctx, url, func() { backoff, atTheLimit = t.cfg.ReconnectFloor, false })
		t.connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		if !established && t.cfg.State != nil {
			t.cfg.State.AttemptFailed("could not connect: "+farm.OneLineForTerminal(err.Error()), attempt)
		}
		status, reason, refused := farm.WhatTheFarmSaid(err)
		refused = refused && !established
		// ANY OTHER OUTCOME AFTER THE LIMIT PUTS TODAY'S RECONNECT BACK, from its floor; the next 429 doubles on from there.
		wasAtTheLimit := atTheLimit
		atTheLimit = refused && status == http.StatusTooManyRequests
		if wasAtTheLimit && !atTheLimit {
			backoff = t.cfg.ReconnectFloor
		}
		if refused {
			// THE FARM ANSWERED THE UPGRADE AND REFUSED IT (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO): its status and its sentence,
			// not the library's "expected handshake response status code 101".
			t.saidDown.Store(true)
			t.sayRefused(status, reason)
		} else if t.saidDown.CompareAndSwap(false, true) {
			// A SOCKET THAT NEVER EXISTED CANNOT DROP. The first dial's failure is "not connected yet"; only
			// a socket that was established can be reported as dropped — and only one can come "back".
			if t.everConnected.Load() {
				fmt.Fprintf(t.cfg.Out, "the drive tunnel dropped (%v) — reconnecting with backoff; devices stay unoffered until it is back\n", farm.OneLineForTerminal(err.Error()))
			} else {
				fmt.Fprintf(t.cfg.Out, "the drive tunnel is not connected yet (%v) — retrying with backoff; devices are offered once it connects\n", farm.OneLineForTerminal(err.Error()))
			}
		}
		wait := backoff + time.Duration(rand.Int63n(int64(750*time.Millisecond)))
		backoff *= 2
		if limit := t.reconnectCap(atTheLimit); backoff > limit {
			backoff = limit
		}
		if t.cfg.State == nil || atTheLimit {
			// AT THE FARM'S TUNNEL LIMIT (NTH_AGENT_0_4_2) the wait is the tunnel's own, 1 s doubling to a minute: a heartbeat
			// the farm takes says it is there, which the 429 said already, and says nothing about a place coming free. Woken
			// by every one, the eleventh machine dialled about once per heartbeat, forever, and the farm logged each.
			t.cfg.Sleep(ctx, wait)
			continue
		}
		// CUT SHORT WHEN THE FARM ANSWERS a heartbeat meanwhile: a farm that is back gets its tunnel back at once,
		// not at the end of a backoff that grew while it was away (RelayTunnelClient's wait, ported).
		if t.cfg.State.WaitToReconnect(ctx, wait) {
			if t.saidWoken.CompareAndSwap(false, true) {
				t.say("the farm answered a heartbeat — dialling the drive tunnel now, without waiting out the backoff\n")
			}
			backoff = t.cfg.ReconnectFloor
		}
	}
}

// reconnectCap is how far the backoff doubles: a minute at the farm's tunnel limit, today's cap otherwise.
func (t *Tunnel) reconnectCap(atTheLimit bool) time.Duration {
	if atTheLimit {
		return t.cfg.ReconnectCapAtTheLimit
	}
	return t.cfg.ReconnectCap
}

// sleep waits d, or until ctx ends: the reconnect's wait when nobody gave the tunnel a Sleep.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// connectAndServe dials one tunnel and serves it until it ends. established says whether the upgrade succeeded — a
// failed dial is evidence about reaching the farm, an ended tunnel is not — and the error says how it ended.
func (t *Tunnel) connectAndServe(ctx context.Context, url string, onConnected func()) (bool, error) {
	header := http.Header{}
	header.Set("X-Agent-Token", t.cfg.Token)
	// THIS END READS AT ALL TIMES, so the farm may ping it with an answer due (TunnelAccept): see liveness.go.
	header.Set(AnswersPingsHeader, AnswersPingsValue)
	// WHEN THE FARM LAST SAID ANYTHING — a pong, or any bytes of any frame — as MONOTONIC time since this dial began: a
	// wall clock stepped by NTP, or a laptop's sleep, must not make a living farm look dead or a dead one alive.
	started := time.Now()
	var heard atomic.Int64
	hear := func() { heard.Store(int64(time.Since(started))) }
	var queue atomic.Pointer[queuedConn]
	client := dialClient(t.writeBound(), t.cfg.TLSClientConfig, func(q *queuedConn) { queue.Store(q) })
	conn, answer, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader:     header,
		HTTPClient:     client,
		OnPongReceived: func(context.Context, []byte) { hear() },
	})
	// A REFUSED UPGRADE LEAVES ITS CONNECTION IDLE in this dial's own transport, which nothing will use again — with a
	// send-queue goroutine attached. The upgraded connection is not in that pool; everything that is goes now.
	client.CloseIdleConnections()
	if err != nil {
		// NOT THE UPGRADE: the farm saying no, read through the one door (farm.RefusalReason), or its address (farm.GatewayOr).
		if answer != nil && answer.StatusCode != http.StatusSwitchingProtocols {
			raw := farm.ReadAnswerBody(answer.Body) // the library keeps the first kilobyte of the refused answer's body
			return false, farm.GatewayOr(answer.StatusCode, raw, &farm.AnswerError{Status: answer.StatusCode, Reason: farm.RefusalReason(bytes.NewReader(raw), t.cfg.Token)})
		}
		return false, err
	}
	// THE RELAY CONTRACT'S CAP, NOT NONE: Appium's payloads exceed the library's 32 KiB default, and no farm message
	// exceeds MaxMessageBytes. One that does ends this tunnel without being dispatched, and the tunnel is dialled again.
	conn.SetReadLimit(t.maxMessageBytes())
	hear()

	life, end := context.WithCancelCause(ctx)
	l := &link{t: t, conn: conn, queue: queue.Load(), hear: hear, heard: &heard, started: started, life: life, agent: ctx,
		requests: newRequestQueue(t.maxQueuedRequests(), t.maxQueuedBytes()), streams: map[string]context.CancelCauseFunc{}}

	onConnected()
	t.connected.Store(true)
	t.saidDown.Store(false)
	t.saidWoken.Store(false)
	t.saidRefused = ""
	if t.cfg.State != nil {
		t.cfg.State.Established()
	}
	// THE THREE SENTENCES, in the only orders they can appear: "not connected yet" (0..n times, once per
	// unconnected stretch) → "established" (exactly once) → ("dropped" → "is back")*.
	if t.everConnected.CompareAndSwap(false, true) {
		fmt.Fprintf(t.cfg.Out, "drive tunnel established — %s\n", t.destination())
	} else {
		fmt.Fprintf(t.cfg.Out, "the drive tunnel is back\n")
	}

	go l.keepAlive()
	go l.serveRequests()
	readErr := l.read()
	why := l.howItEnded(readErr, ctx)
	// THE TUNNEL ENDED: the worker, every pump and every wait for room end with it (a fresh tunnel starts clean), and
	// the socket is closed without a handshake — on a dead peer a close handshake is five seconds of waiting for nobody.
	end(errTunnelEnded)
	_ = conn.CloseNow()
	if t.cfg.State != nil {
		t.cfg.State.Lost(why)
	}
	return true, errors.New(why)
}

// sayRefused says a refused upgrade ONCE: when it starts, and again only when the farm's status or sentence changes — the
// eleventh machine of a workspace is refused at every dial for as long as ten others keep theirs. "Not connected yet" on a
// first start; after a drop, its own line (the drop was said already).
func (t *Tunnel) sayRefused(status int, reason string) {
	key := fmt.Sprint(status, " ", reason)
	if key == t.saidRefused {
		return
	}
	t.saidRefused = key
	switch first := !t.everConnected.Load(); {
	case first && reason != "":
		fmt.Fprintf(t.cfg.Out, "the drive tunnel is not connected yet: the farm refused it (%d). It says: “%s” — retrying with backoff; devices are offered once it connects\n", status, reason)
	case first:
		fmt.Fprintf(t.cfg.Out, "the drive tunnel is not connected yet: the farm refused it (%d) and gave no reason this agent could read — retrying with backoff; devices are offered once it connects\n", status)
	case reason != "":
		fmt.Fprintf(t.cfg.Out, "the farm refused the drive tunnel (%d). It says: “%s” — retrying with backoff; devices stay unoffered until it connects\n", status, reason)
	default:
		fmt.Fprintf(t.cfg.Out, "the farm refused the drive tunnel (%d) and gave no reason this agent could read — retrying with backoff; devices stay unoffered until it connects\n", status)
	}
}

// link is ONE ESTABLISHED TUNNEL CONNECTION — everything that lives exactly as long as it does.
type link struct {
	t     *Tunnel
	conn  *websocket.Conn
	queue *queuedConn
	// heard is when the farm last said anything, in monotonic nanoseconds since started; hear records it now.
	heard   *atomic.Int64
	hear    func()
	started time.Time
	// life is cancelled (cause errTunnelEnded) the moment this connection ends; agent is the process's own context,
	// which is what a forward runs under — see serveRequests.
	life  context.Context
	agent context.Context
	dead  atomic.Bool // the keep-alive found it dead

	sendMu sync.Mutex // one message at a time — the reference's send lock

	requests  *requestQueue
	streamsMu sync.Mutex
	streams   map[string]context.CancelCauseFunc
}

// read is THE READ LOOP, and it never waits on anything but the socket: a ping is answered only while a read is
// pending (measured), so a request goes to the worker, a stream to its pump, and the loop reads again.
//
// ARRIVING DATA IS HEARING FROM THE FARM — the `alive` frame's whole point — and it is heard AS IT ARRIVES, piece by
// piece, not once a whole message has: a large request coming in slowly is the farm talking all the way through
// (the review's finding, 2026-10-03, where a farm still sending one long message was being timed as silent).
func (l *link) read() error {
	buffer := make([]byte, 32*1024)
	for {
		typ, reader, err := l.conn.Reader(l.life)
		if err != nil {
			return err
		}
		l.hear()
		var message bytes.Buffer
		for {
			n, err := reader.Read(buffer)
			if n > 0 {
				message.Write(buffer[:n])
				l.hear()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
		if typ != websocket.MessageText {
			continue
		}
		l.dispatch(message.Bytes())
	}
}

func (l *link) dispatch(data []byte) {
	var head struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return // a malformed frame is skipped, exactly like the reference
	}
	switch head.Kind {
	case "":
		// The request/response family. ONE WORKER, IN ORDER, OFF THIS LOOP — the node's arrangement — because an
		// Appium command can take minutes and the farm now pings this end with a 30 s answer bound.
		var frame requestFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			return
		}
		// THE FARM'S HANDBACK QUESTION is this machine's to answer, never Appium's — off the worker, as the node answers it.
		if l.t.cfg.Capture != nil && isCaptureHandback(frame.Path) {
			go l.answerHandback(frame)
			return
		}
		if refusal := l.requests.push(frame, len(data)); refusal != "" {
			// ANSWERED AT ONCE, OFF THIS LOOP: the farm hears why rather than waiting out its own bound.
			if l.t.saidQueueFull.CompareAndSwap(false, true) {
				l.t.say("the farm sent more requests than this agent keeps waiting (" + refusal + ") — each one past that is answered at once with a 503, and nothing of it is sent on\n")
			}
			go func() {
				if l.queue.WaitBelow(l.life, l.t.responseHighWater()) == nil {
					_ = l.send(answerInWords(frame.ID, http.StatusServiceUnavailable, "Relay forward error: this agent already has "+refusal+", so this request was not queued; nothing of it was sent on"))
				}
			}()
		}
	case "stream-start":
		l.startStream(data)
	case "stream-cancel":
		l.cancelStream(head.ID)
	case "capture-start", "capture-stop":
		// The network-capture family: no reply frame (the farm sends and moves on, as to the node); the controller
		// decides — see capture.go.
		l.captureControl(head.Kind, data)
	case "alive":
		// The farm's RelayAlive, every 10 s. There is nothing to do with it: its ARRIVAL is what matters, and read
		// already recorded it. It comes the way a live view never fills, so a ping waiting behind a mirror's chunks
		// never makes a living farm look dead.
	default:
		// Kinds this end never receives (stream-chunk/end) — ignored, like the reference.
	}
}

// serveRequests is THE WORKER: the farm's requests, one at a time, in arrival order, until the tunnel ends.
//
// ⚠️ A FORWARD RUNS UNDER THE AGENT'S CONTEXT, NOT THE TUNNEL'S — a deliberate difference from the node, which cancels
// it with the connection. A forward in flight when the tunnel drops is most often a session being created; cancelled,
// Appium may still create it and nothing here would know its id. Let run to its end, the routing table remembers it
// and the release (release.go) ends it if nobody comes back for it. Only its answer is dropped — there is no tunnel
// to carry it — and the requests still queued behind it are dropped with the tunnel, as the farm already failed them.
func (l *link) serveRequests() {
	for {
		frame, ok := l.requests.pop(l.life)
		if !ok {
			return
		}
		response := l.t.forward(l.agent, frame)
		if l.life.Err() != nil {
			return
		}
		// Appium's answers wait only for a full queue — never behind a slow viewer's chunks, which wait for far less.
		if err := l.queue.WaitBelow(l.life, l.t.responseHighWater()); err != nil {
			return
		}
		_ = l.send(response)
	}
}

// send writes one frame — as FragmentBytes fragments when it is larger — one message at a time, under the
// connection's life and nothing else: a caller's or a stream's cancellation never reaches a write, because cancelling
// a write closes the whole connection (measured).
func (l *link) send(frame any) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	if len(payload) <= FragmentBytes {
		return l.conn.Write(l.life, websocket.MessageText, payload)
	}
	writer, err := l.conn.Writer(l.life, websocket.MessageText)
	if err != nil {
		return err
	}
	for offset := 0; offset < len(payload); offset += FragmentBytes {
		if _, err := writer.Write(payload[offset:min(offset+FragmentBytes, len(payload))]); err != nil {
			return err
		}
	}
	return writer.Close()
}

// keepAlive pings the farm every PingInterval and calls the tunnel dead when nothing at all — no frame, no pong — has
// come back within AnswerWithin of a ping. Dead means aborted at once: the socket is closed without a handshake, the
// read loop ends, and Run reconnects.
//
// A ping is sent only when the last one was answered (or anything else was heard since), so one silence is timed from
// the ping that met it. A ping that cannot even be sent is not an unanswered one; only the silence decides.
func (l *link) keepAlive() {
	interval, within := l.t.pingInterval(), l.t.answerWithin()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var outstanding int64 // when the ping being waited on was sent, on heard's monotonic scale; 0 = none
	for {
		select {
		case <-l.life.Done():
			return
		case <-ticker.C:
		}
		now := int64(time.Since(l.started))
		if outstanding != 0 && l.heard.Load() <= outstanding {
			if time.Duration(now-outstanding) >= within {
				l.dead.Store(true)
				_ = l.conn.CloseNow()
				return
			}
			continue
		}
		outstanding = now
		go func() {
			ctx, cancel := context.WithTimeout(l.life, within)
			defer cancel()
			_ = l.conn.Ping(ctx) // its answer is recorded by OnPongReceived; this only carries the ping
		}()
	}
}

// howItEnded is this end's sentence for how the tunnel ended — what the narration and State say.
func (l *link) howItEnded(err error, agent context.Context) string {
	switch {
	case l.dead.Load():
		return fmt.Sprintf("found dead: the farm did not answer this agent's ping within %s", l.t.answerWithin())
	case l.queue.Stuck():
		return fmt.Sprintf("a frame could not be written to the farm within %s", l.t.writeBound())
	case agent.Err() != nil:
		return "this agent is stopping"
	case websocket.CloseStatus(err) != -1:
		return fmt.Sprintf("the farm closed it (%v)", websocket.CloseStatus(err))
	default:
		return fmt.Sprintf("the connection failed: %v", err)
	}
}

// requestQueue is the worker's FIFO. A push never makes the read loop wait: past its bounds (MaxQueuedRequests waiting, or
// MaxQueuedBytes of their messages) it refuses at once, and says which bound it met.
type requestQueue struct {
	mu       sync.Mutex
	items    []queuedRequest
	bytes    int64
	maxItems int
	maxBytes int64
	ready    chan struct{}
}

type queuedRequest struct {
	frame requestFrame
	size  int64
}

func newRequestQueue(maxItems int, maxBytes int64) *requestQueue {
	return &requestQueue{maxItems: maxItems, maxBytes: maxBytes, ready: make(chan struct{})}
}

func (q *requestQueue) push(frame requestFrame, size int) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case len(q.items) >= q.maxItems:
		return fmt.Sprintf("%d requests waiting, its most", len(q.items))
	case q.bytes+int64(size) > q.maxBytes:
		return fmt.Sprintf("%d bytes of requests waiting, and this one would take it past its %d", q.bytes, q.maxBytes)
	}
	q.items = append(q.items, queuedRequest{frame: frame, size: int64(size)})
	q.bytes += int64(size)
	close(q.ready)
	q.ready = make(chan struct{})
	return ""
}

func (q *requestQueue) pop(ctx context.Context) (requestFrame, bool) {
	for {
		// A TUNNEL THAT HAS ENDED HANDS OUT NOTHING, queued or not: the farm failed every request still waiting when
		// it ended, and a retry of one would run it twice (the review's finding, 2026-10-03).
		if ctx.Err() != nil {
			return requestFrame{}, false
		}
		q.mu.Lock()
		if len(q.items) > 0 {
			item := q.items[0]
			q.items[0] = queuedRequest{}
			q.items = q.items[1:]
			q.bytes -= item.size
			q.mu.Unlock()
			return item.frame, true
		}
		ready := q.ready
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return requestFrame{}, false
		case <-ready:
		}
	}
}

// The test seams, each falling back to the node's number.
func (t *Tunnel) pingInterval() time.Duration { return orDuration(t.cfg.PingInterval, PingInterval) }
func (t *Tunnel) answerWithin() time.Duration { return orDuration(t.cfg.AnswerWithin, AnswerWithin) }
func (t *Tunnel) writeBound() time.Duration   { return orDuration(t.cfg.WriteBound, WriteBound) }
func (t *Tunnel) streamIdle() time.Duration   { return orDuration(t.cfg.StreamIdle, StreamIdle) }
func (t *Tunnel) maxStreams() int             { return orInt(t.cfg.MaxStreams, MaxConcurrentStreams) }
func (t *Tunnel) pumpHighWater() int          { return orInt(t.cfg.PumpHighWater, PumpHighWater) }
func (t *Tunnel) responseHighWater() int      { return orInt(t.cfg.ResponseHighWater, ResponseHighWater) }
func (t *Tunnel) streamClient() *http.Client  { return t.cfg.StreamClient }
func (t *Tunnel) maxMessageBytes() int64      { return orInt64(t.cfg.MaxMessageBytes, MaxMessageBytes) }
func (t *Tunnel) maxAnswerBytes() int64       { return orInt64(t.cfg.MaxAnswerBytes, MaxAnswerBytes) }
func (t *Tunnel) maxQueuedRequests() int      { return orInt(t.cfg.MaxQueuedRequests, MaxQueuedRequests) }
func (t *Tunnel) maxQueuedBytes() int64       { return orInt64(t.cfg.MaxQueuedBytes, MaxQueuedBytes) }

func orDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func orInt64(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func orInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

// destination says where a request frame goes once the tunnel is up — READ FROM THE SAME RULE forward applies
// below, so the sentence cannot describe a route the frames do not take.
//
// THE SENTENCE THIS REPLACES was printed unconditionally: "requests for this node's devices now reach the local
// Appium". A cloud-only node passes no --appium at all — the README says so in bold — and a customer reading that
// line concluded they had to install Appium for a device they will never touch. The rule in forward is one
// branch on one fact, and the sentence is now that same branch on that same fact: with credentials configured
// every frame goes to BrowserStack's hub, and the line says so; without them it says what it always said.
func (t *Tunnel) destination() string {
	local, hub := t.cfg.AppiumURL != "", t.cfg.BrowserStack.Configured()
	switch {
	case local && hub:
		// A MIXED NODE, and the sentence is the rule route applies: per session, by the device the lease named.
		return "requests for this node's devices are routed per session — a phone on this machine's to the local Appium, a cloud device's to BrowserStack's hub with your credentials added on the way"
	case hub:
		return "requests for this node's devices now reach BrowserStack's hub, with your credentials added on the way"
	default:
		return "requests for this node's devices now reach the local Appium"
	}
}

// route decides where ONE frame goes, and it is the decision this file used to make from a fact about the node.
//
// THE DEFECT. forward chose its endpoint from `BrowserStack.Configured()` — with credentials, EVERY frame went to
// the hub, including a session for a phone on the desk, which then reached Merge, was found to name a serial and
// not a spec, and was refused with ErrNoDeviceSpec. It failed closed, by accident of a refusal: configuring
// credentials disabled the local path entirely, the opposite of what the README promised ("a machine can do
// both"). Nothing but the absence of --appium stopped a mixed node from publishing a local row it could not serve.
//
// THE RULE NOW. A session-create carries the device the lease named (`appium:udid` — a spec for a cloud row, a
// serial for a local one; the channel MergeSpecFrom already reads), so it decides its own destination and is
// remembered by the session id its response carries. Every later frame names its session in the path and follows
// it. A session this tunnel never created has no destination and is REFUSED — never routed by which flags the
// agent was started with, because that is the rule that sent a local session to a stranger's hub.
//
// ⚠️ THE PROPERTY, ON PURPOSE: a session for a local device goes to the local Appium or nowhere. It cannot reach
// the hub, because the hub branch is entered only for a body naming a cloud spec — and Merge's own refusal of a
// serial stays behind it as the second lock.
func (t *Tunnel) route(frame requestFrame, decoded []byte) (destination, string) {
	if browserstack.IsSessionCreate(frame.Method, frame.Path) {
		if _, isCloud := browserstack.MergeSpecFrom(decoded); isCloud {
			if !t.cfg.BrowserStack.Configured() {
				return 0, "the lease named a cloud device, and this agent has no BrowserStack account to reach it with — the session was refused rather than sent to a local Appium that has never heard of it"
			}
			return toHub, ""
		}
		if t.cfg.AppiumURL == "" {
			return 0, "the lease named a device on this machine, and this agent was started without --appium — the session was refused rather than sent to BrowserStack, which has never heard of it"
		}
		return toLocal, ""
	}
	if id := sessionIDInPath(frame.Path); id != "" {
		if value, ok := t.sessions.Load(id); ok {
			// A FRAME IS ACTIVITY, and this is the only place that has to know it: every frame naming a session
			// passes through here, so the release's clock is fed by the routing decision itself rather than by a
			// second walk somebody could forget to add a case to.
			entry := value.(*sessionEntry)
			entry.touch(time.Now())
			return entry.dest, ""
		}
		// The id is the farm's, so it is folded to one line and cut before it stands in a sentence.
		return 0, "this agent routes each session to where it created it, and it never created session " + farm.SafeForTerminal(id, "") + " — after a restart every session it had is gone, and a frame for one has no destination"
	}
	// A frame that names no session and creates none — `GET /status`, the only one forward lets this far (isStatusRead):
	// the node's own endpoint, which is the one behaviour this file always had for such a frame — nothing of a customer's
	// crosses in it.
	if t.cfg.AppiumURL != "" {
		return toLocal, ""
	}
	if t.cfg.BrowserStack.Configured() {
		return toHub, ""
	}
	return 0, "this agent has neither a local Appium nor a BrowserStack account to send this frame to"
}

// sessionIDInPath reads the session a frame is for out of its path — `/session/{id}` or `/session/{id}/…`, with
// whatever the farm prefixed (`/wd/hub/…`) and any query string ignored. Empty for a path naming no session,
// including `POST /session`, which CREATES one.
func sessionIDInPath(path string) string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	const marker = "/session/"
	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}
	rest := path[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// endpointFor is WHERE A DESTINATION LIVES, in one place, because two paths now need it: forward, which
// replays a frame, and release, which ends a session nobody ended. Two copies of this switch would be two
// chances for a local session's teardown to be addressed to a stranger's hub.
func (t *Tunnel) endpointFor(dest destination) string {
	if dest == toHub {
		if t.cfg.BrowserStackHub != "" {
			return t.cfg.BrowserStackHub
		}
		return browserstack.HubURL
	}
	return t.cfg.AppiumURL
}

// clientFor is HOW A DESTINATION IS REACHED, beside where it lives: the hub over the ordinary client, this machine's Appium
// over the owner-checked one (A_MACHINE_THAT_STAYS_THE_CUSTOMERS). forward and release both ask here.
func (t *Tunnel) clientFor(dest destination) *http.Client {
	if dest == toHub {
		return t.cfg.Forward
	}
	return t.local
}

// forward replays one request frame against the LOCAL Appium — the reference's ForwardToAppiumAsync,
// behaviour for behaviour: target = AppiumURL + path; only Content-Type crosses into the request; the
// response carries status, Content-Type and the body; any failure is a 502 text/plain naming itself.
//
// EVERY ANSWER TO A FRAME SENT TO THE HUB has the customer's access key replaced before it is relayed (
// browserstack.ScrubAnswer) — the create's, every later frame's, a sessionless frame's on a node without --appium, and
// this agent's own refusals on that path — so "the key is never sent to us" does not rest on what BrowserStack echoes.
func (t *Tunnel) forward(ctx context.Context, frame requestFrame) (answer responseFrame) {
	// A FRAME'S PATH IS A PATH. The target is the configured endpoint with the path appended, and a path that does
	// not start with "/" is read as part of the address: "@host/…" after an Appium URL with no path of its own moves the
	// request, its method and its body to another program. Today's farm always prepends the "/" (RelayEndpoint).
	if !strings.HasPrefix(frame.Path, "/") {
		if t.saidUnrooted.CompareAndSwap(false, true) {
			t.say(`the farm sent a request whose path does not start with "/" — it could name another address than the one this agent sends to, so it was refused, and every one like it is` + "\n")
		}
		return errorResponse(frame.ID, `Relay forward error: this request's path does not start with "/", so it could name another address than the one this agent sends to; it was refused`)
	}
	// A FRAME THAT NAMES NO SESSION READS THE STATUS, AND NOTHING ELSE. Any live lease on this machine can send one,
	// and whatever session-less routes the customer's Appium, its drivers and its plugins serve would be that lease's to
	// call. The farm and every consumer of it send GET /status; a session create and a session's own frames go on below.
	if !browserstack.IsSessionCreate(frame.Method, frame.Path) && sessionIDInPath(frame.Path) == "" && !isStatusRead(frame.Method, frame.Path) {
		if t.saidSessionless.CompareAndSwap(false, true) {
			t.say("the farm sent a request that names no Appium session and is not a status read — this agent passes only GET /status and GET /wd/hub/status without a session, so it was refused, and every one like it is; nothing on this machine was asked\n")
		}
		return answerInWords(frame.ID, http.StatusNotFound, "Relay forward error: this agent passes only GET /status and GET /wd/hub/status without an Appium session, and this request names none, so it was not sent on")
	}
	var decoded []byte
	if frame.BodyBase64 != nil && *frame.BodyBase64 != "" {
		var err error
		decoded, err = base64.StdEncoding.DecodeString(*frame.BodyBase64)
		if err != nil {
			return errorResponse(frame.ID, "Relay forward error: "+err.Error())
		}
	}
	// WHERE THIS FRAME GOES — per session, never per node. See route.
	dest, refusal := t.route(frame, decoded)
	if refusal != "" {
		t.say(refusal + "\n")
		return errorResponse(frame.ID, "Relay forward error: "+refusal)
	}
	cloud := dest == toHub
	if cloud {
		defer func() { answer = t.withoutTheKey(answer) }()
	}
	endpoint := t.endpointFor(dest)
	target := strings.TrimRight(endpoint, "/") + frame.Path
	// …AND THE ADDRESS BUILT IS THE ONE CONFIGURED: same scheme, same host and port, no user name.
	if !sameAddress(endpoint, target) {
		if t.saidUnrooted.CompareAndSwap(false, true) {
			t.say("the farm sent a request whose path would take it to another address than the one this agent sends to, so it was refused, and every one like it is\n")
		}
		return errorResponse(frame.ID, "Relay forward error: this request's path would take it to another address than the one this agent sends to; it was refused")
	}
	create := browserstack.IsSessionCreate(frame.Method, frame.Path)
	// THE CUSTOMER'S APPLE ACCOUNT IS THEIRS TO CHANGE, at this machine (appleaccount.go).
	if create && !cloud && changesTheAppleAccount(decoded) {
		t.say(refusedAppleAccountChange() + "\n")
		return errorResponse(frame.ID, "Relay forward error: "+refusedAppleAccountChange())
	}
	// …AND THE REST OF WHAT A SESSION MAY ASK OF THIS MACHINE IS ON ONE LIST (internal/allowed): a create for a phone here
	// passes only with capabilities on it, a phone this agent offers and a build from the farm's own host; inside a
	// session, what runs a program or touches files on this computer is refused. The hub is not this machine.
	var handed []string // the builds this frame fetched (builds.go)
	if !cloud {
		refusal := ""
		if create {
			refusal = allowed.Create(decoded, t.cfg.FarmURL, t.cfg.Phones)
		} else {
			refusal = allowed.InSession(frame.Method, frame.Path, decoded, t.cfg.FarmURL)
		}
		if refusal != "" {
			t.say(refusal + "\n")
			return errorResponse(frame.ID, "Relay forward error: "+refusal)
		}
		// …AND APPIUM INSTALLS ONLY A BUILD THIS AGENT HAS FETCHED, MEASURED AND CHECKED, handed to it as the agent's own file
		// (builds.go): the farm's address never reaches Appium, which would fetch it, keep it and perhaps unpack it itself. A
		// file no session holds once this frame is answered (a create that started none) is removed then.
		handed, refusal = t.handOverBuilds(ctx, create, frame.Method, frame.Path, &decoded)
		if refusal != "" {
			t.say(refusal + "\n")
			return errorResponse(frame.ID, "Relay forward error: "+refusal)
		}
		defer t.forgetBuilds(handed)
	}

	var body io.Reader
	if len(decoded) > 0 {
		// SESSION CREATION ONLY. Every other frame stays a byte relay — the agent understands exactly one
		// request and moves the rest without reading them, which is what keeps this a relay rather than a
		// second Appium client.
		if cloud && create {
			// WHICH DEVICE WAS LEASED, read from the body the platform built. The farm puts the leased row's
			// identifier in `appium:udid`; for a cloud row that is a spec. Nothing is guessed and nothing is
			// hard-coded any more — a session whose lease named no cloud device is refused below.
			spec, _ := browserstack.MergeSpecFrom(decoded)
			merged, err := browserstack.Merge(decoded, t.cfg.BrowserStack, t.cfg.BrowserStackApp, spec)
			if err != nil {
				// The refusal names the SHAPE, never the body and never the key. Forwarding an unmergeable
				// body would send the session up unauthenticated and come back as "bad credentials", which is
				// the one failure a customer would misread as their own mistake.
				// TWO REFUSALS, TWO SENTENCES. A body we cannot merge into and a session with no build are
				// different mistakes with different remedies, and one message covering both would be the defect
				// this whole path exists to remove.
				if errors.Is(err, browserstack.ErrNoApp) {
					t.say(browserstack.MissingAppMessage + "\n")
				} else if errors.Is(err, browserstack.ErrNoDeviceSpec) {
					// THREE REFUSALS, THREE SENTENCES. A body we cannot merge into, a session with no build and a
					// session naming no cloud device are different mistakes with different remedies.
					t.say("the lease named no BrowserStack device, so this session was refused rather than sent " +
						"with a device nobody chose\n")
				} else {
					t.say("the farm asked to start a session in a shape this agent does not recognise — it was refused rather than sent to BrowserStack without credentials\n")
				}
				return errorResponse(frame.ID, "Relay forward error: "+err.Error())
			}
			decoded = merged
		}
		body = bytes.NewReader(decoded)
	}
	request, err := http.NewRequestWithContext(ctx, frame.Method, target, body)
	if err != nil {
		return errorResponse(frame.ID, "Relay forward error: "+err.Error())
	}
	if contentType, ok := frame.Headers["Content-Type"]; ok && body != nil {
		request.Header.Set("Content-Type", contentType)
	}

	response, err := t.clientFor(dest).Do(request)
	if err != nil {
		if cloud {
			// ITEM 3, FIRST OF THREE: we never reached the hub, so nothing was attempted on the account.
			t.say(browserstack.FailureUnreachable.Describe() + "\n")
		}
		return errorResponse(frame.ID, "Relay forward error: "+err.Error())
	}
	defer response.Body.Close()
	if cloud && isRedirect(response.StatusCode) {
		if t.saidRedirect.CompareAndSwap(false, true) {
			t.say(fmt.Sprintf("BrowserStack's hub answered with a redirect (HTTP %d), which this agent does not follow — a redirect is an address nobody checked, and a session start carries your access key; the run was answered with a 502\n", response.StatusCode))
		}
		return errorResponse(frame.ID, fmt.Sprintf("Relay forward error: BrowserStack's hub answered with a redirect (HTTP %d), which this agent does not follow, so nothing was sent on", response.StatusCode))
	}
	// THE ANSWER IS READ UP TO WHAT ONE TUNNEL MESSAGE CARRIES, never past it: a bounded read never learns the full
	// size, so the sentence names the bound the answer passed.
	limit := t.maxAnswerBytes()
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return errorResponse(frame.ID, "Relay forward error: "+err.Error())
	}
	if int64(len(payload)) > limit {
		if strings.EqualFold(frame.Method, http.MethodDelete) {
			if id := sessionIDInPath(frame.Path); id != "" {
				t.forget(id)
			}
		}
		if t.saidTooLarge.CompareAndSwap(false, true) {
			t.say(fmt.Sprintf("an answer from %s was larger than the %d bytes one tunnel message carries, so it was not relayed — the run was told so with a 502, and the tunnel stays up for every other phone\n", place(dest), limit))
		}
		return errorResponse(frame.ID, fmt.Sprintf("Relay forward error: the answer was larger than the %d bytes one tunnel message carries, so it was not relayed", limit))
	}

	// THE TABLE IS WRITTEN FROM THE RESPONSE'S OWN SESSION ID, in whichever dialect the endpoint speaks, and only
	// for a session that really started. From here on every frame naming this id follows this destination.
	if create && response.StatusCode < 300 {
		if id := browserstack.SessionIDOf(payload); id != "" {
			// `decoded` is the body that was actually SENT — merged, for a cloud create — so the silence budget
			// the entry remembers is the one the far side was actually asked for.
			t.remember(id, dest, decoded)
			t.holdBuilds(id, handed)
		}
	}
	// …and forgotten when the session is deleted, whatever the endpoint answered: a DELETE is the client saying
	// it is done with the id, and an entry kept for a dead session is a leak with the wrong answer in it.
	if strings.EqualFold(frame.Method, http.MethodDelete) {
		if id := sessionIDInPath(frame.Path); id != "" {
			t.forget(id)
		}
	}

	// ITEM 3, THE OTHER TWO. The hub's response goes back VERBATIM but for the key — rewriting a WebDriver response
	// would break the driver's own error handling and is the fabricated-answer shape this programme keeps deleting, so
	// the one change made to it is the key replaced where it stands (withoutTheKey), status and shape kept. What the
	// agent adds is a sentence on its OWN output, where a person with a credential problem actually looks.
	if cloud && create {
		if failure := browserstack.ClassifyResponse(response.StatusCode, payload); failure != "" {
			t.say(failure.Describe() + "\n")
		}
	}

	headers := map[string]string{}
	if contentType := response.Header.Get("Content-Type"); contentType != "" && len(contentType) <= maxRelayedContentType {
		headers["Content-Type"] = contentType
	}
	var encoded *string
	if len(payload) > 0 {
		s := base64.StdEncoding.EncodeToString(payload)
		encoded = &s
	}
	return responseFrame{ID: frame.ID, Status: response.StatusCode, Headers: headers, BodyBase64: encoded}
}

// withoutTheKey is an answer to a hub-bound frame with the customer's access key replaced, in its body and its
// Content-Type, wherever and however it appears there (browserstack.ScrubAnswer). Status and shape are kept.
func (t *Tunnel) withoutTheKey(answer responseFrame) responseFrame {
	if contentType, ok := answer.Headers["Content-Type"]; ok {
		answer.Headers["Content-Type"] = browserstack.ScrubHeader(contentType, t.cfg.BrowserStack)
	}
	if answer.BodyBase64 == nil {
		return answer
	}
	relayed, err := base64.StdEncoding.DecodeString(*answer.BodyBase64)
	if err != nil {
		return errorResponse(answer.ID, "Relay forward error: the hub's answer could not be checked for the access key, so it was not relayed")
	}
	scrubbed := browserstack.ScrubAnswer(relayed, t.cfg.BrowserStack)
	if !bytes.Equal(scrubbed, relayed) {
		encoded := base64.StdEncoding.EncodeToString(scrubbed)
		answer.BodyBase64 = &encoded
	}
	return answer
}

// say narrates to the agent's own output. NOTHING IT IS EVER GIVEN CONTAINS A CREDENTIAL — the three
// BrowserStack sentences are constants on Failure and the refusal names a shape, so there is no format verb here
// that a key could be threaded through. Nil-safe: Out is optional everywhere else in this file too.
func (t *Tunnel) say(message string) {
	if t.cfg.Out == nil {
		return
	}
	fmt.Fprint(t.cfg.Out, message)
}

func errorResponse(id, message string) responseFrame {
	return answerInWords(id, http.StatusBadGateway, message)
}

// answerInWords is this agent's own answer to a frame: the status, and the sentence as text/plain.
func answerInWords(id string, status int, message string) responseFrame {
	encoded := base64.StdEncoding.EncodeToString([]byte(message))
	return responseFrame{ID: id, Status: status,
		Headers: map[string]string{"Content-Type": "text/plain"}, BodyBase64: &encoded}
}

// isStatusRead is the one request that names no session and is passed on: GET /status, or /wd/hub/status, whatever its
// query — what the farm, the runner and the agent's own probe send (internal/appium).
func isStatusRead(method, path string) bool {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return method == http.MethodGet && (path == "/status" || path == "/wd/hub/status")
}

// sameAddress: target, parsed, is at endpoint's scheme, host and port, with endpoint's user name (none, unless the
// customer's own --appium carries one) and no other.
func sameAddress(endpoint, target string) bool {
	want, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	got, err := url.Parse(target)
	if err != nil {
		return true // not a URL at all: http.NewRequest refuses it, in its own words
	}
	return got.Scheme == want.Scheme && strings.EqualFold(got.Host, want.Host) && got.User.String() == want.User.String()
}

// isRedirect is a status Go's client would follow.
func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// tunnelURL flips https→wss / http→ws on the farm URL, exactly the reference's BuildTunnelUrl.
func tunnelURL(farmURL, nodeID string) string {
	url := strings.TrimRight(farmURL, "/")
	switch {
	case strings.HasPrefix(url, "https://"):
		url = "wss://" + strings.TrimPrefix(url, "https://")
	case strings.HasPrefix(url, "http://"):
		url = "ws://" + strings.TrimPrefix(url, "http://")
	}
	return url + "/v1/nodes/" + nodeID + "/tunnel"
}
