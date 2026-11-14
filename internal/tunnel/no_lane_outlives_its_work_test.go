package tunnel_test

import (
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// NO LANE OUTLIVES ITS WORK, ITS SESSION OR ITS TUNNEL: a lane has a goroutine only while it has a request, so a deleted
// session leaves none, and a tunnel that ends leaves none once its forwards in flight have ended.
func TestNoLaneOutlivesItsSessionOrItsTunnel(t *testing.T) {
	phones, drive, sendOne, got := sideBySide(t)
	go func() {
		for range phones.entered {
		}
	}()
	sendOne(frame("57575757-0000-0000-0000-000000000001", "GET", "/session/sess-A/source?n=1"))
	sendOne(frame("57575757-0000-0000-0000-000000000002", "DELETE", "/session/sess-A"))
	sendOne(frame("57575757-0000-0000-0000-000000000003", "GET", "/status"))
	for _, id := range []string{"57575757-0000-0000-0000-000000000001", "57575757-0000-0000-0000-000000000002", "57575757-0000-0000-0000-000000000003"} {
		if _, ok := got.within(id, 5*time.Second); !ok {
			t.Fatalf("%s was never answered", id)
		}
	}
	waitFor(t, "no lane left once every request was answered", func() bool { return drive.RequestLanes() == 0 })
	if drive.RoutedSessions() != 0 {
		t.Fatalf("the deleted session is still routed")
	}
}

// …AND A FORWARD IN FLIGHT WHEN ITS TUNNEL ENDS STILL RUNS TO ITS END, under the agent's context (serveLane): a session
// being created then is remembered, so the release can end it if nobody comes back for it. What waited behind it is never
// handed out.
func TestATunnelThatEndsLeavesNoLaneBehindItsForwardInFlight(t *testing.T) {
	phones, appiumURL := newTwoPhones(t)
	farm := newLiveFarm(t)
	var out syncWriter
	drive := liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = appiumURL })
	conn := farm.next(t)
	phones.hold("create A")
	send(t, conn, requestWithBody("58585858-0000-0000-0000-00000000000a", "POST", "/session", createA))
	if gate := <-phones.entered; gate != "create A" {
		t.Fatalf("phone A's create should have reached Appium, not %q", gate)
	}
	send(t, conn, requestWithBody("58585858-0000-0000-0000-00000000000b", "POST", "/session", createB))
	time.Sleep(50 * time.Millisecond)
	conn.CloseNow() // the tunnel ends with one create in flight and one waiting behind it
	second := farm.next(t)
	defer second.CloseNow()
	phones.release("create A")
	waitFor(t, "no lane left once the forward in flight ended", func() bool { return drive.RequestLanes() == 0 })
	if drive.RoutedSessions() != 1 {
		t.Fatalf("the create in flight when its tunnel ended must run to its end and be remembered; %d session(s) routed", drive.RoutedSessions())
	}
	phones.mu.Lock()
	creates := strings.Join(phones.creates, "")
	phones.mu.Unlock()
	if creates != "A" {
		t.Fatalf("the create still waiting when its tunnel ended was handed out anyway: Appium saw creates %q", creates)
	}
}
