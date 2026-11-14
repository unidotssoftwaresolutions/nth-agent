package tunnel

import (
	"net"
	"testing"
	"time"
)

// ANOTHER_ACCOUNT_AND_LIMITS. The send queue copied every write into memory and never refused one: a farm that read
// nothing, or a trickle, while it provoked the agent's answers made the agent hold whatever it was made to queue. Past
// its ceiling the queue now closes the connection, and says that was why.
func TestTheSendQueueClosesTheTunnelPastItsCeiling(t *testing.T) {
	agentSide, farmSide := net.Pipe() // the far end reads nothing
	defer farmSide.Close()
	q := newQueuedConn(agentSide, time.Minute)
	defer q.Close()
	q.limitTo(64 << 10)
	chunk := make([]byte, 16<<10)
	var err error
	for i := 0; i < 8 && err == nil; i++ {
		_, err = q.Write(chunk)
	}
	if err == nil {
		t.Fatalf("128 KiB were queued for a far end that reads nothing, past a ceiling of 64 KiB, and every write was taken (%d queued)", q.Queued())
	}
	if !q.Overfull() || q.Open() {
		t.Fatalf("passing the ceiling closes the connection and says so: overfull %v, open %v", q.Overfull(), q.Open())
	}
}
