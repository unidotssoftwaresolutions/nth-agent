package tunnel

import "time"

// ── HOW THIS END FINDS OUT ITS TUNNEL IS DEAD, AND KEEPS THE FARM ABLE TO TELL ─────────────────────────────────
//
// The node's numbers (the farm's RelayLiveness, A_TUNNEL_THAT_KNOWS_IT_DIED), ported. Before this the
// agent's tunnel had NO keep-alive at all: a `KeepAlive = 30 * time.Second` constant sat here and nothing read it, and
// coder/websocket has none of its own (measured: a Read on a silent connection is still waiting after any wait you
// care to give it). A tunnel whose far end went silent was held as live until the process ended.
//
// WHAT coder/websocket v1.8.15 DOES, MEASURED ON LOOPBACK 2026-10-03, and where it differs from .NET in a way that
// matters (the harness is kept beside the slice's report, AN_AGENT_THAT_SHOWS_ITS_PHONE_HARNESS.go.txt):
//
//   - Pings are answered only by a pending Read, exactly like .NET: a peer that is not reading did not answer a ping
//     within 2 s, and answered in under a millisecond once it read. So the read loop never waits on Appium.
//   - There is no keep-alive timer. Conn.Ping sends one ping and waits for its pong; the interval, the answer bound and
//     the verdict are this file's (keepAlive in tunnel.go). Arriving data counts as hearing from the farm, as it does
//     for .NET's keep-alive, which is what the farm's `alive` frame relies on.
//   - Cancelling a write's context closes the WHOLE connection (measured: the next write fails "use of closed network
//     connection") — .NET's "cancelling a send aborts the socket". No caller's or stream's context reaches a write.
//   - ⚠️ THE DIFFERENCE: a pong (and a ping) gets FIVE SECONDS to take the write lock, and a pong that cannot fails the
//     READ. .NET's pong simply waits its turn. Measured: one 2 MB frame over a 100 KB/s uplink, the farm pinging every
//     second — the agent's own reader died 5 s in with "failed to handle control frame opPing: failed to write control
//     frame opPong: failed to acquire lock: context deadline exceeded". The same bytes in 32 KB fragments survived.
//     And fragments are not enough on their own: a farm that stops reading for 10 s (its own bound for one slow
//     viewer, InMemoryTunnelRegistry.ChunkDeliveryBound) left a 32 KB fragment write blocked on TCP, and the reader
//     died at 6 s, twice in two runs. So the bytes go to a send queue BELOW the library (sendqueue.go): the library's
//     writes land in memory at once and never hold that lock behind the network, and a live view waits for the queue
//     to drain instead. With the queue the same 10 s stall passed twice, every one of 14 pings answered.

// PingInterval — how often this end pings the farm (RelayLiveness.PingInterval).
const PingInterval = 15 * time.Second

// AnswerWithin — how long this end waits to hear from the farm after a ping before it calls the tunnel dead: twice the
// interval, so one late answer on a congested link is not a dead tunnel (RelayLiveness.AnswerWithin).
const AnswerWithin = 30 * time.Second

// FoundDeadWithin — the longest a silent tunnel is held as live: the last word arrived just before the far end went
// silent, the next ping goes out an interval later, and its answer is given up on after AnswerWithin. 45 s.
const FoundDeadWithin = PingInterval + AnswerWithin

// FragmentBytes — the largest piece of one frame written in one WebSocket frame (RelayProtocol.FragmentBytes). Page
// sources and screenshots come back as one frame of megabytes.
const FragmentBytes = 32 * 1024

// WriteBound — the longest the send queue may wait on one write to the network before it aborts the connection: the
// far end has taken no bytes for that long (RelayLiveness.WriteBound, below the farm's 190 s relay timeout).
const WriteBound = 120 * time.Second

// AnswersPingsHeader / AnswersPingsValue — the farm's RelayAnswersPings header and RelayLiveness.AnswersPingsValue.
// Sent on the upgrade, they tell the farm this end reads at all times, so the farm pings it with a 30 s answer bound
// too (TunnelAccept). Sent ONLY because the read loop no longer waits on anything: a farm pinging an agent that
// forwarded on its read loop would close a healthy tunnel in the middle of any Appium command longer than 30 s.
const (
	AnswersPingsHeader = "X-Relay-Answers-Pings"
	AnswersPingsValue  = "1"
)

// The send queue's two levels. A live view waits until the queue holds less than PumpHighWater before it queues its
// next chunk, so a viewer slower than the phone holds a quarter of a megabyte here, not the tunnel; Appium's answers
// wait only for ResponseHighWater, so a mirror never stands between a run and its answers. Control frames — pongs,
// pings, a stream's end — never wait.
const (
	PumpHighWater     = 256 * 1024
	ResponseHighWater = 8 * 1024 * 1024
)
