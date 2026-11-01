package tunnel

import (
	"context"
	"strings"
	"testing"
)

// A TUNNEL THAT HAS ENDED HANDS OUT NOTHING. The farm fails every request still queued when its tunnel ends, and a
// request the worker took after that would run against Appium anyway — twice, if the caller retried. The window is a
// send that fails as the tunnel goes, so it is sealed here, on the queue itself. (The review's finding, 2026-10-03.)
//
// AMENDED: the queue hands out per lane (next) since one machine's phones stopped waiting for each other; two requests of
// one session share its lane, and the rule is unchanged.
func TestARequestQueuedWhenTheTunnelEndedIsNeverHandedOut(t *testing.T) {
	queue := newRequestQueue(MaxQueuedRequests, MaxQueuedBytes)
	first := requestFrame{ID: "11111111-0000-0000-0000-000000000001", Method: "GET", Path: "/session/s1/source"}
	key, start, _ := queue.push(first, 0)
	queue.push(requestFrame{ID: "11111111-0000-0000-0000-000000000002", Method: "GET", Path: "/session/s1/url"}, 0)
	if !start {
		t.Fatal("the first request of a lane starts its worker")
	}
	life, end := context.WithCancel(context.Background())
	if frame, ok := queue.next(life, key); !ok || frame.ID != first.ID {
		t.Fatalf("a live tunnel's queue hands out in order: %v %v", frame.ID, ok)
	}
	end()
	if frame, ok := queue.next(life, key); ok {
		t.Fatalf("the tunnel ended and the queue still handed out %s, which the farm had already failed", frame.ID)
	}
	if queue.open() != 0 || queue.waiting != 0 || queue.bytes != 0 {
		t.Fatalf("an ended tunnel's lane must be gone and count nothing: %d lane(s), %d waiting, %d bytes", queue.open(), queue.waiting, queue.bytes)
	}
}

// THE QUEUE'S TWO BOUNDS ARE COUNTED ACROSS EVERY LANE: requests spread over many sessions are no way past them.
func TestTheQueuesBoundsAreCountedAcrossEveryLane(t *testing.T) {
	queue := newRequestQueue(3, 1000)
	paths := []string{"/session/a/source", "/session/b/source", "/status"}
	for _, path := range paths {
		if _, _, refusal := queue.push(requestFrame{Method: "GET", Path: path}, 10); refusal != "" {
			t.Fatalf("%s must be queued: %s", path, refusal)
		}
	}
	if _, _, refusal := queue.push(requestFrame{Method: "GET", Path: "/session/c/source"}, 10); !strings.Contains(refusal, "3 requests waiting") {
		t.Fatalf("a fourth request, in a fourth lane, is past the bound of three: %q", refusal)
	}
	bytes := newRequestQueue(100, 1000)
	bytes.push(requestFrame{Method: "GET", Path: "/session/a/source"}, 600)
	if _, _, refusal := bytes.push(requestFrame{Method: "POST", Path: "/session"}, 600); !strings.Contains(refusal, "bytes of requests waiting") {
		t.Fatalf("a create's 600 bytes beside a session's 600 pass the 1000 bytes counted across lanes: %q", refusal)
	}
}

// WHICH LANE: a session's frames by its id, every create in one lane, and the rest — status reads — in a third.
func TestEachRequestWaitsInItsOwnLane(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         laneKey
	}{
		{"POST", "/session", laneKey{kind: createLane}},
		{"POST", "/wd/hub/session", laneKey{kind: createLane}},
		{"GET", "/session/abc/source", laneKey{kind: sessionLane, session: "abc"}},
		{"DELETE", "/wd/hub/session/abc", laneKey{kind: sessionLane, session: "abc"}},
		{"GET", "/session/abc/element/e1/text?x=1", laneKey{kind: sessionLane, session: "abc"}},
		{"GET", "/status", laneKey{kind: sessionlessLane}},
		{"GET", "/wd/hub/status", laneKey{kind: sessionlessLane}},
		{"GET", "/sessions", laneKey{kind: sessionlessLane}},
	} {
		if got := laneOf(requestFrame{Method: c.method, Path: c.path}); got != c.want {
			t.Errorf("%s %s waits in %+v, not %+v", c.method, c.path, got, c.want)
		}
	}
}

// A LANE WITH NOTHING WAITING IS REMOVED by the worker that finds it empty, and the next request makes it again with a new
// worker — so nothing stays behind a session that ended.
func TestALaneIsRemovedWhenItsWorkIsDoneAndMadeAgainForTheNext(t *testing.T) {
	queue := newRequestQueue(MaxQueuedRequests, MaxQueuedBytes)
	life := context.Background()
	key, start, _ := queue.push(requestFrame{ID: "1", Method: "DELETE", Path: "/session/gone"}, 5)
	if !start {
		t.Fatal("a new lane starts a worker")
	}
	if _, ok := queue.next(life, key); !ok {
		t.Fatal("the DELETE is handed out")
	}
	if _, ok := queue.next(life, key); ok || queue.open() != 0 {
		t.Fatalf("an empty lane must be removed and its worker told to end (%d open)", queue.open())
	}
	if _, start, _ := queue.push(requestFrame{ID: "2", Method: "GET", Path: "/session/gone/source"}, 5); !start {
		t.Fatal("a request after the lane ended starts a new worker; none is left waiting without one")
	}
}
