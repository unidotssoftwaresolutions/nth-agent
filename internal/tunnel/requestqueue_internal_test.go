package tunnel

import (
	"context"
	"testing"
)

// A TUNNEL THAT HAS ENDED HANDS OUT NOTHING. The farm fails every request still queued when its tunnel ends, and a
// request the worker took after that would run against Appium anyway — twice, if the caller retried. The window is a
// send that fails as the tunnel goes, so it is sealed here, on the queue itself. (The review's finding, 2026-10-03.)
func TestARequestQueuedWhenTheTunnelEndedIsNeverHandedOut(t *testing.T) {
	queue := newRequestQueue(MaxQueuedRequests, MaxQueuedBytes)
	queue.push(requestFrame{ID: "11111111-0000-0000-0000-000000000001"}, 0)
	queue.push(requestFrame{ID: "11111111-0000-0000-0000-000000000002"}, 0)
	life, end := context.WithCancel(context.Background())
	if frame, ok := queue.pop(life); !ok || frame.ID != "11111111-0000-0000-0000-000000000001" {
		t.Fatalf("a live tunnel's queue hands out in order: %v %v", frame.ID, ok)
	}
	end()
	if frame, ok := queue.pop(life); ok {
		t.Fatalf("the tunnel ended and the queue still handed out %s, which the farm had already failed", frame.ID)
	}
}
