package tunnel_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// WHAT_THE_FIRST_TRY_SHOWED, decision A6, once a session has started: the phone's set says whether the session the driver
// talks to is on the WebDriverAgent this machine declared (internal/connect, wdaidentity.go). When it is not, the agent
// ends the session at the local Appium, keeps nothing of it, and answers the farm's create in words, not with Appium's 200.
func TestASessionOnAWebDriverAgentThisMachineDidNotDeclareIsEndedAndTheFarmToldWhy(t *testing.T) {
	const why = "iPhone 00008140-0000C0FFEE0000A1's session is not on the WebDriverAgent this machine declared"
	_, appium, _, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	phones := allowed.Offering(fixturePhones...)
	phones.OfferAsking(fixturePhones, nil, func(_ context.Context, udid string) string {
		if udid == localUdid {
			return why
		}
		return ""
	})
	var out syncWriter
	drive := tunnel.New(tunnel.Config{
		Phones: phones, FarmURL: farmServer.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-TUNNELSECRET",
		AppiumURL: appiumURL, ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond, Out: &out,
		Sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go drive.Run(ctx)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(localUdid, 60)))
	answer := readFrame(t, conn)
	if status, _ := answer["status"].(float64); status < 400 {
		raw, _ := json.Marshal(answer)
		t.Fatalf("the farm must not be answered with Appium's 200 for a session on another WebDriverAgent: %s", raw)
	}
	encoded, _ := answer["bodyBase64"].(string)
	body, _ := base64.StdEncoding.DecodeString(encoded)
	if !strings.Contains(string(body), why) {
		t.Fatalf("the farm's answer must say why: %q", body)
	}
	deletes := waitForDeletes(appium, 1, 3*time.Second)
	if len(deletes) != 1 || !strings.Contains(deletes[0].Path, "/session/local-session-1") {
		t.Fatalf("the session must be ended at the local Appium, once, by its id: %+v", deletes)
	}
	if drive.RoutedSessions() != 0 {
		t.Fatalf("nothing of the ended session is kept: %d routed", drive.RoutedSessions())
	}
	if said := waitForSaid(&out, "this machine declared", 3*time.Second); !strings.Contains(said, why) {
		t.Fatalf("the terminal must say why:\n%s", said)
	}
}

// …and a session the set has nothing against starts as before.
func TestASessionOnTheDeclaredWebDriverAgentStartsAsBefore(t *testing.T) {
	_, appium, _, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	phones := allowed.Offering(fixturePhones...)
	phones.OfferAsking(fixturePhones, nil, func(context.Context, string) string { return "" })
	drive := tunnel.New(tunnel.Config{
		Phones: phones, FarmURL: farmServer.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-TUNNELSECRET",
		AppiumURL: appiumURL, ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond, Out: &syncWriter{},
		Sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go drive.Run(ctx)
	conn := farm.waitConn(t)
	sendFrame(t, conn, sessionFrame("c1", createDeclaring(localUdid, 60)))
	if status, _ := readFrame(t, conn)["status"].(float64); status != 200 {
		t.Fatalf("the create is answered with Appium's own 200, got %v", status)
	}
	if len(deletesOf(appium)) != 0 || drive.RoutedSessions() != 1 {
		t.Fatalf("the session is kept and not ended: %d deletes, %d routed", len(deletesOf(appium)), drive.RoutedSessions())
	}
}
