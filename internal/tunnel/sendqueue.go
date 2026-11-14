package tunnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// queuedConn IS THE TUNNEL'S NETWORK CONNECTION WITH A SEND QUEUE IN FRONT OF IT. Everything coder/websocket writes —
// frames, fragments, pongs, pings, close frames, and under TLS the records that carry them — lands in memory at once,
// and one goroutine drains it to the network in order. Reads pass straight through.
//
// WHY IT EXISTS (measured; see liveness.go): coder/websocket gives a pong five seconds to take its write lock and fails
// the READ when it cannot, so any write that sits on a stalled network for five seconds — one large frame over a slow
// uplink, or a fragment while the farm holds its loop for a slow viewer — took the agent's own tunnel down. Below the
// library a write is a copy, the lock is held for that long and no longer, and a pong is queued behind whatever is
// already queued — which is also exactly where TCP would have put it. Nothing reaches the farm sooner than before;
// nothing in this process waits on the network while holding a lock anything else needs.
//
// WHAT BOUNDS IT. Memory: a live view waits for the queue to fall below PumpHighWater before it queues another chunk,
// and Appium's answers wait for ResponseHighWater — both outside every lock (waitBelow). Time: one write to the network
// that has not finished after writeBound aborts the connection (the node's WriteBound — the far end has taken no bytes
// for that long), which ends the tunnel and reconnects it.
type queuedConn struct {
	net.Conn
	writeBound time.Duration

	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every change to the queue
	chunks  [][]byte
	queued  int  // bytes not yet written to the network, the one being written included
	closed  bool // nothing more is accepted or written
	stuck   bool // a write passed writeBound and the connection was aborted for it
	once    sync.Once
}

func newQueuedConn(raw net.Conn, writeBound time.Duration) *queuedConn {
	if writeBound <= 0 {
		writeBound = WriteBound
	}
	q := &queuedConn{Conn: raw, writeBound: writeBound, changed: make(chan struct{})}
	go q.drain()
	return q
}

func (q *queuedConn) notifyLocked() {
	close(q.changed)
	q.changed = make(chan struct{})
}

// Write queues a copy of p and returns at once — the whole point. It fails only on a closed connection.
func (q *queuedConn) Write(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0, net.ErrClosed
	}
	q.chunks = append(q.chunks, append([]byte(nil), p...))
	q.queued += len(p)
	q.notifyLocked()
	return len(p), nil
}

func (q *queuedConn) drain() {
	for {
		q.mu.Lock()
		for len(q.chunks) == 0 && !q.closed {
			changed := q.changed
			q.mu.Unlock()
			<-changed
			q.mu.Lock()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		next := q.chunks[0]
		q.chunks[0] = nil // the slot would keep a written chunk reachable until the slice grew again
		q.chunks = q.chunks[1:]
		q.mu.Unlock()

		// THE BOUND IS ON THE NETWORK, where a write can actually wait. A timer, not a deadline: net/http may set the
		// connection's deadlines itself during the handshake, and this must not be undone by — or undo — that.
		bound := time.AfterFunc(q.writeBound, func() {
			q.mu.Lock()
			q.stuck = true
			q.mu.Unlock()
			_ = q.Close()
		})
		_, err := q.Conn.Write(next)
		bound.Stop()

		q.mu.Lock()
		q.queued -= len(next)
		q.notifyLocked()
		q.mu.Unlock()
		if err != nil {
			_ = q.Close()
			return
		}
	}
}

// Queued is how many bytes are waiting to reach the network.
func (q *queuedConn) Queued() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queued
}

// WaitBelow returns once the queue holds fewer than level bytes, the connection has closed, or ctx ends — and it
// holds no lock while it waits, which is what lets a slow viewer wait without holding anybody else up.
func (q *queuedConn) WaitBelow(ctx context.Context, level int) error {
	if q == nil {
		return ctx.Err()
	}
	for {
		q.mu.Lock()
		if q.queued < level || q.closed {
			q.mu.Unlock()
			return nil
		}
		changed := q.changed
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// Open reports whether the connection still takes writes: WaitBelow also returns once it has closed, with whatever was
// queued dropped, so a sender that must know its bytes left (a capture's pull, before its file is deleted) asks this too.
func (q *queuedConn) Open() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.closed && !q.stuck
}

// Stuck reports whether the connection was aborted because a write to the network passed the bound.
func (q *queuedConn) Stuck() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stuck
}

// Close drops whatever is still queued and closes the network connection — once.
func (q *queuedConn) Close() error {
	var err error
	q.once.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.chunks = nil
		q.notifyLocked()
		q.mu.Unlock()
		err = q.Conn.Close()
	})
	return err
}

// dialClient is the HTTP client the tunnel's upgrade is made with: Go's default transport — the same TLS and dial
// timeouts the default client gave this dial before, and the farm's own proxy rule (farm.Proxy) — with every connection
// it makes wrapped in a send queue, handed to made so the tunnel can wait on it. One addition: a farm that takes the
// connection and never answers the upgrade is given up on after thirty seconds, as a dead one is; before this, that dial
// waited forever.
func dialClient(writeBound time.Duration, tlsConfig *tls.Config, made func(*queuedConn)) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		queue := newQueuedConn(raw, writeBound)
		made(queue)
		return queue, nil
	}
	transport.ResponseHeaderTimeout = 30 * time.Second
	// THE FARM'S PROXY RULE (farm.Proxy): wss follows this machine's proxy settings; a plain ws upgrade, which
	// ValidateBaseURL allows only to loopback, never goes through a proxy.
	transport.Proxy = farm.Proxy
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}
	// THE UPGRADE FOLLOWS NO REDIRECT, because it carries X-Agent-Token and Go copies a custom header to wherever a
	// redirect points — any host, any scheme, plain http included (it strips only Authorization and cookies). The farm's
	// tunnel never redirects; a 3xx is answered as the refusal it is. This was true of the dial before this slice too
	// (the default client followed); the review found it, 2026-10-03, and farm.Client already refuses the same way.
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
