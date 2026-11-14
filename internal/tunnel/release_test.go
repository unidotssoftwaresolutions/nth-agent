package tunnel_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A_SESSION_NOBODY_ENDS — the seals for the mechanism that ends one.
//
// MEASURED, 2026-09-03: a cloud session whose worker was SIGKILLed mid-run carried `DELETE` ZERO times in the
// vendor's entire session log. Nothing on our side ever released it; the far side tore it down 121.6 s later
// and billed for the wait. The agent is the only party that both sees the silence and holds the session id —
// the platform's own reaper says so in its code ("the Appium session has no handle to quit from here").

// ── fixtures ────────────────────────────────────────────────────────────────────────────────────────────────

// A create body that DECLARES its silence budget, which is the only number this agent's bound is built from.
// 0.05 s keeps the seals fast; the shape is the platform's own (`capabilities.firstMatch[0]`, measured).
func createDeclaring(udid string, newCommandTimeout float64) string {
	body := map[string]any{"capabilities": map[string]any{"firstMatch": []any{map[string]any{
		"platformName": "iOS", "appium:automationName": "XCUITest",
		"appium:udid": udid, "appium:newCommandTimeout": newCommandTimeout,
	}}}}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

const (
	cloudUdid = "browserstack:iPhone 15|17"
	localUdid = "00008140-0000C0FFEE0000A1"
)

// releasingTunnel is a mixed node whose release margin is a duration a test can wait for. THE MARGIN IS THE ONLY
// THING SHORTENED — the budget still comes from the session's own body, the bound is still budget+margin, and
// the code path is the one a real session takes.
func releasingTunnel(t *testing.T, farmURL, appiumURL, hubURL string, margin time.Duration) (*tunnel.Tunnel, *syncWriter) {
	t.Helper()
	var out syncWriter
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: farmURL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Token: "sk-agent-TUNNELSECRET", AppiumURL: appiumURL,
		BrowserStack: creds, BrowserStackHub: hubURL, BrowserStackApp: testApp,
		ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond,
		ReleaseMargin: margin,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out: &out,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go drive.Run(ctx)
	return drive, &out
}

// deletesOf is every session-ending request one endpoint received.
func deletesOf(rec *recorder) []seenRequest {
	var out []seenRequest
	for _, request := range rec.seen() {
		if strings.EqualFold(request.Method, http.MethodDelete) {
			out = append(out, request)
		}
	}
	return out
}

// waitForDeletes polls until an endpoint has received `want` session-ending requests, or the deadline passes.
func waitForDeletes(rec *recorder, want int, within time.Duration) []seenRequest {
	deadline := time.Now().Add(within)
	for {
		got := deletesOf(rec)
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitForSaid polls the agent's own output until it contains `want`, or the deadline passes, and returns what was
// said by then.
//
// THE HUB COUNTING A DELETE IS NOT THE AGENT HAVING SPOKEN. release sends the DELETE, waits for the hub's answer,
// and only then says what it did, so output read the moment the hub has counted the request can predate the
// sentence. Reading it there failed TestTheReleaseNamesWhatItEndedAndWhyOnTheAgentsOwnOutput about one run in five
// under -race on an otherwise idle machine. A test that reads the agent's words waits for the words.
func waitForSaid(out *syncWriter, want string, within time.Duration) string {
	deadline := time.Now().Add(within)
	for {
		said := out.String()
		if strings.Contains(said, want) || time.Now().After(deadline) {
			return said
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ── INJECTION 1: a session that goes silent must be ENDED, not left running ─────────────────────────────────

func TestASessionThatGoesSilentIsEndedRatherThanLeftRunning(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	drive, _ := releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 30*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.05)))
	readFrame(t, conn)

	deletes := waitForDeletes(hub, 1, 3*time.Second)
	// ⚠️ THE MESSAGE MUST NOT NAME A CAUSE THAT IS NOT THE ONE THAT HAPPENED. An earlier version of this seal said
	// "NOBODY ENDED IT" for every failure, and injection 4 — which sends the release TWICE — printed it under two
	// DELETEs. A failure message is a claim; this one now reads the count before making it.
	if len(deletes) == 0 {
		t.Fatal("session hub-session-1 went silent for longer than the 80ms it was allowed and NOBODY ENDED IT — " +
			"a session nobody ends holds a device and bills for it until a timeout on somebody else's machine " +
			"gets around to it")
	}
	if len(deletes) != 1 || !strings.Contains(deletes[0].Path, "hub-session-1") {
		t.Fatalf("session hub-session-1 must be ended exactly once, by name; the hub received %d session-ending "+
			"requests: %+v", len(deletes), deletes)
	}
	if got := drive.RoutedSessions(); got != 0 {
		t.Fatalf("the session was ended but the table still holds %d entry — an entry for a dead session is a leak "+
			"with the wrong answer in it, and the next frame naming it would be routed to a session that is gone", got)
	}
}

// ── INJECTION 2 AND 3: THE FAILURE THIS MECHANISM CAN CAUSE, AND IT MUST BE INSPECTABLE ─────────────────────
//
// A gap between two frames is the worker THINKING, not an abandoned session — measured 2026-09-03, a mobile
// discovery step spends ~11 s in the model and ~10 s over twelve page-source reads. A bound below the longest
// legitimate gap does not save money, it kills a healthy run, and this is that failure written down.

func TestAHealthySessionIsNeverEndedWhileItIsStillBeingDriven(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 40*time.Millisecond)
	conn := farm.waitConn(t)

	// A session allowed 60ms of silence, driven with 25ms gaps for well over ten bounds' worth of wall clock.
	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.06)))
	readFrame(t, conn)
	for step := 0; step < 20; step++ {
		time.Sleep(25 * time.Millisecond)
		sendFrame(t, conn, frame("s", "GET", "/wd/hub/session/hub-session-1/source"))
		readFrame(t, conn)
	}

	if killed := deletesOf(hub); len(killed) != 0 {
		t.Fatalf("A HEALTHY SESSION WAS KILLED. hub-session-1 answered a frame every 25ms for half a second and this "+
			"agent ended it anyway: %+v — the gap between two frames is the worker thinking, and a bound below the "+
			"longest legitimate gap turns this mechanism from a saving into an outage", killed)
	}

	// …and the same session, once it really does go quiet, is ended. Without this half the seal above would be
	// satisfied by a mechanism that never fires at all.
	switch got := waitForDeletes(hub, 1, 3*time.Second); {
	case len(got) == 0:
		t.Fatal("the session was driven, then abandoned, and was never ended — without this half the seal above " +
			"would be satisfied by a mechanism that never fires at all")
	case len(got) != 1:
		t.Fatalf("the abandoned session was ended %d times: %+v", len(got), got)
	}
}

func TestTheSilenceBoundIsTheSessionsOwnBudgetAndNotANumberThisAgentChose(t *testing.T) {
	source := readSource(t, "release.go")
	for _, required := range []string{
		"e.budget + margin",
		"newCommandTimeoutCapability = \"appium:newCommandTimeout\"",
		"ReleaseMarginDefault = AppiumForwardTimeout",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("the bound must be the session's OWN declared budget plus an EXISTING margin, and %q is gone "+
				"from release.go — two numbers already disagreed with each other and with the measurement, and a "+
				"bound invented here would make three", required)
		}
	}
	// The two budgets the platform actually sends must produce two different bounds — a headless run may be
	// silent for 120s, an interactive authoring session with a human pausing for 600s, and this agent has no
	// opinion about which is right.
	headless := browserstack.CapabilitySets([]byte(createDeclaring(cloudUdid, 120)))
	interactive := browserstack.CapabilitySets([]byte(createDeclaring(cloudUdid, 600)))
	if len(headless) != 1 || len(interactive) != 1 ||
		headless[0]["appium:newCommandTimeout"] == interactive[0]["appium:newCommandTimeout"] {
		t.Fatalf("the two budgets the platform sends must be readable and different: %v vs %v", headless, interactive)
	}
}

// ── INJECTION 4: ONE RELEASE PER SESSION, AND THE DESIGN IS WHY ─────────────────────────────────────────────

func TestOnlyOneReleaseIsEverSentForOneSessionAndTheTableIsWhy(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 20*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.02)))
	readFrame(t, conn)

	// Long past many bounds' worth of firing opportunities.
	waitForDeletes(hub, 1, 2*time.Second)
	time.Sleep(300 * time.Millisecond)
	if got := deletesOf(hub); len(got) != 1 {
		t.Fatalf("one session must be ended exactly once; the hub received %d session-ending requests: %+v", len(got), got)
	}

	// AND WHERE THE DESIGN MAKES IT IMPOSSIBLE, shown at that point: removing the entry and winning the right to
	// send are the same atomic act, so a second firing finds nothing and sends nothing.
	source := readSource(t, "release.go")
	if !strings.Contains(source, "if !t.sessions.CompareAndDelete(id, value) {") {
		t.Fatal("the release must be gated by taking the table entry atomically — a flag read separately from the " +
			"send is a double DELETE waiting for two timers to overlap")
	}
	if strings.Index(source, "CompareAndDelete") > strings.Index(source, "t.release(id, entry") {
		t.Fatal("the entry must be taken BEFORE the release is sent, or the gate is behind the thing it gates")
	}
}

func TestTheClientsOwnTeardownEndsTheSessionAndTheAgentDoesNotSendASecond(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	drive, _ := releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 20*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.05)))
	readFrame(t, conn)
	sendFrame(t, conn, frame("d1", "DELETE", "/wd/hub/session/hub-session-1"))
	readFrame(t, conn)

	if got := drive.RoutedSessions(); got != 0 {
		t.Fatalf("the client ended its own session and the table still holds %d entry", got)
	}
	time.Sleep(300 * time.Millisecond) // many bounds past the point the release would have fired
	if got := deletesOf(hub); len(got) != 1 {
		t.Fatalf("the NORMAL teardown path must be unchanged and must not gain a second DELETE from this "+
			"mechanism — the hub saw %d: %+v", len(got), got)
	}
}

// ── INJECTION 5: a local session's release must be routed by the TABLE, never by a fact about the node ──────

func TestALocalSessionsReleaseGoesToTheLocalAppiumAndNeverTheHub(t *testing.T) {
	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 30*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(localUdid, 0.05)))
	readFrame(t, conn)

	local := waitForDeletes(appium, 1, 3*time.Second)
	// THE SHARPER HALF FIRST, because it is the property: where the teardown WENT, not merely that one arrived.
	if strayed := deletesOf(hub); len(strayed) != 0 {
		t.Fatalf("A LOCAL SESSION'S TEARDOWN WAS SENT TO A STRANGER'S HUB: %+v — the destination must come from "+
			"the routing table that recorded where the session was created, never from which flags this agent "+
			"was started with, which is the rule that sent a local session to the hub in the first place", strayed)
	}
	if len(local) == 0 {
		t.Fatal("a phone on this machine held open by a dead worker must be released too — one mechanism serves " +
			"both destinations, and the local Appium received no session-ending request at all")
	}
	if len(local) != 1 || !strings.Contains(local[0].Path, "local-session-1") {
		t.Fatalf("local-session-1 must be ended exactly once, by name; the local Appium received %d "+
			"session-ending requests: %+v", len(local), local)
	}
}

// ── ITEM 5: the properties this slice must not weaken ───────────────────────────────────────────────────────

func TestTheReleaseCarriesNoCredentialAnywhere(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 30*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.05)))
	readFrame(t, conn)
	deletes := waitForDeletes(hub, 1, 3*time.Second)
	if len(deletes) == 0 {
		t.Fatal("no release was sent, so there is nothing to inspect for a credential")
	}
	if strings.Contains(deletes[0].Body, testKey) || strings.Contains(deletes[0].Path, testKey) {
		t.Fatalf("THE ACCESS KEY RODE THE RELEASE. Credentials belong in a session CREATE's capabilities and "+
			"nowhere else; every later frame is addressed by the session id alone: %+v", deletes[0])
	}
	// Read after the release has been SAID, not after the hub counted it: an absence checked before the sentence
	// exists proves nothing about the sentence.
	said := waitForSaid(out, "this agent ended it", 3*time.Second)
	if !strings.Contains(said, "this agent ended it") {
		t.Fatalf("the release was never said on the agent's own output, so a key missing from it proves nothing:\n%s", said)
	}
	if strings.Contains(said, testKey) {
		t.Fatalf("the access key reached the agent's own output:\n%s", said)
	}
}

func TestTheReleaseNamesWhatItEndedAndWhyOnTheAgentsOwnOutput(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 30*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.05)))
	readFrame(t, conn)
	waitForDeletes(hub, 1, 3*time.Second)

	said := waitForSaid(out, "this agent ended it", 3*time.Second)
	for _, required := range []string{"hub-session-1", "this agent ended it", "BrowserStack account"} {
		if !strings.Contains(said, required) {
			t.Fatalf("the sentence a person reads must name WHAT was ended and WHY — %q is missing from:\n%s",
				required, said)
		}
	}
}

func TestNoWebDriverResponseIsFabricatedForARelease(t *testing.T) {
	hub, _, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 30*time.Millisecond)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", createDeclaring(cloudUdid, 0.05)))
	readFrame(t, conn)
	waitForDeletes(hub, 1, 3*time.Second)

	// The farm asked for nothing here and must be told nothing. A frame arriving now would be an answer to a
	// request nobody made — the fabricated-response shape this programme keeps deleting.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, data, err := conn.Read(ctx); err == nil {
		t.Fatalf("the agent sent the farm a frame for a release nobody asked for: %s", data)
	} else if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("the tunnel died instead of staying quiet: %v", err)
	}
}

// ── THE TWO SESSIONS THIS AGENT REFUSES TO END, AND THE REFUSAL IS THE ARGUMENT ─────────────────────────────

func TestASessionThatDeclaredNoSilenceBudgetIsNotEndedOnABoundThisAgentMadeUp(t *testing.T) {
	for _, shape := range []struct {
		name string
		body string
	}{
		// Absent: Appium's own default (60 s) is SHORTER than any bound derived from it, so the far side retires
		// such a session before this mechanism could add anything.
		{"declares nothing", cloudCreate},
		// Zero: the client explicitly asked Appium for no retirement at all. Ending it anyway would be this
		// agent overruling the only party that stated an intention.
		{"declares zero", createDeclaring(cloudUdid, 0)},
	} {
		t.Run(shape.name, func(t *testing.T) {
			hub, _, hubURL, appiumURL := twoEndpoints(t)
			farm, farmServer := newStubFarmTunnel(t)
			_, out := releasingTunnel(t, farmServer.URL, appiumURL, hubURL, 20*time.Millisecond)
			conn := farm.waitConn(t)

			sendFrame(t, conn, sessionFrame("c1", shape.body))
			readFrame(t, conn)
			time.Sleep(300 * time.Millisecond)

			if killed := deletesOf(hub); len(killed) != 0 {
				t.Fatalf("a session that %s was ended on a bound this agent invented: %+v", shape.name, killed)
			}
			if !strings.Contains(out.String(), "will not end it on a bound it made up") {
				t.Fatalf("the refusal must be said once, on the agent's own output:\n%s", out.String())
			}
		})
	}
}

// ── THE CORRECTED BELIEF, SEALED WHERE IT WAS WRITTEN ───────────────────────────────────────────────────────

func TestTheIdleTimeoutIsNoLongerClaimedToBeWhatEndsAnAbandonedSession(t *testing.T) {
	source := readSourceIn(t, filepath.Join("..", "browserstack"), "browserstack.go")
	if strings.Contains(source, "IdleTimeoutSeconds ends an abandoned session instead of billing for it") {
		t.Fatal("MEASURED FALSE 2026-09-03 and still written down: the session was torn down 121.6s after our last " +
			"command, not at the 60s this constant asks for, and the vendor's own message quoted their 90s default " +
			"back at a session that carried 60. A comment that is wrong is a defect whether or not code moves.")
	}
	if !strings.Contains(source, "IT IS NOT WHAT ENDS AN ABANDONED SESSION") {
		t.Fatal("the corrected claim must stand where the false one did, not only in a report nobody reads next")
	}
	if browserstack.IdleTimeoutSeconds <= 0 || browserstack.IdleTimeoutSeconds > browserstack.IdleTimeoutMaxSeconds {
		t.Fatalf("idleTimeout is %d — the one property of this number that is checkable is that it stays inside "+
			"the vendor's documented range (1..%d); its EFFECT was measured not to occur",
			browserstack.IdleTimeoutSeconds, browserstack.IdleTimeoutMaxSeconds)
	}
}

// ── source helpers ──────────────────────────────────────────────────────────────────────────────────────────

func readSource(t *testing.T, name string) string { return readSourceIn(t, ".", name) }

func readSourceIn(t *testing.T, dir, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

var _ = websocket.MessageText // the frame helpers above live in this package's other files
