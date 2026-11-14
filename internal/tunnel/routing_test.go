package tunnel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A_PROMISE_THE_TUNNEL_DOES_NOT_KEEP — the README said "a machine can do both" and the code served one path.
//
// forward chose its endpoint from BrowserStack.Configured(), a fact about the NODE: with credentials, every frame
// went to the hub, and a session for a phone on the desk reached Merge, was found to name a serial, and was
// refused with ErrNoDeviceSpec. Fail-closed by ACCIDENT of a refusal; the local path disabled by configuring
// credentials. The tunnel now routes PER SESSION, from the device the lease named, and these seals hold both the
// property that was protected by accident and the promise the README makes.

const localCreate = `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"00008140-0000C0FFEE0000A1"}]}}`
const cloudCreate = `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"browserstack:iPhone 15|17"}]}}`

// mixedTunnel is a node with BOTH a local Appium and a BrowserStack account — the shape the README promises.
func mixedTunnel(t *testing.T, farmURL, appiumURL, hubURL string, creds browserstack.Credentials) (*tunnel.Tunnel, *syncWriter) {
	t.Helper()
	var out syncWriter
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: farmURL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Token: "sk-agent-TUNNELSECRET", AppiumURL: appiumURL,
		BrowserStack: creds, BrowserStackHub: hubURL, BrowserStackApp: testApp,
		ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond,
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

func frame(id, method, path string) map[string]any {
	return map[string]any{"id": id, "method": method, "path": path, "headers": map[string]string{}}
}

// twoEndpoints stands up a hub and a local Appium that answer session-creates with DIFFERENT ids, so the routing
// table cannot be fooled by two endpoints handing out the same name.
func twoEndpoints(t *testing.T) (hub, appium *recorder, hubURL, appiumURL string) {
	t.Helper()
	hub = &recorder{reply: `{"value":{"sessionId":"hub-session-1","capabilities":{}}}`}
	hubServer := httptest.NewServer(hub.handler())
	t.Cleanup(hubServer.Close)
	appium = &recorder{reply: `{"value":{"sessionId":"local-session-1","capabilities":{}}}`}
	appiumServer := httptest.NewServer(appium.handler())
	t.Cleanup(appiumServer.Close)
	return hub, appium, hubServer.URL, appiumServer.URL
}

var creds = browserstack.Credentials{User: "acme-user", Key: testKey}

// ── INJECTION 1: a session for a LOCAL device must never be sent to the vendor's hub ─────────────────────────

func TestALocalSessionOnAMixedNodeGoesToTheLocalAppiumAndNeverTheHub(t *testing.T) {
	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", localCreate))
	response := readFrame(t, conn)

	if got := int(response["status"].(float64)); got != http.StatusOK {
		t.Fatalf("a local session on a mixed node must be CREATED, not refused — got %d (%s)", got, out.String())
	}
	if got := len(hub.seen()); got != 0 {
		t.Fatalf("a session for a phone on the desk reached the vendor's hub (%d requests) — the property this file exists for", got)
	}
	seen := appium.seen()
	if len(seen) != 1 || seen[0].Body != localCreate {
		t.Fatalf("the local Appium must receive the local session byte-identical: %+v", seen)
	}
	if strings.Contains(out.String(), "refused") {
		t.Fatalf("nothing was refused on the path the README promises:\n%s", out.String())
	}
}

func TestACloudSessionOnTheSameMixedNodeGoesToTheHubWithCredentials(t *testing.T) {
	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudCreate))
	readFrame(t, conn)

	if got := len(appium.seen()); got != 0 {
		t.Fatalf("a cloud session reached the local Appium (%d requests)", got)
	}
	seen := hub.seen()
	if len(seen) != 1 || !strings.Contains(seen[0].Body, `"bstack:options"`) || strings.Contains(seen[0].Body, "browserstack:iPhone") {
		t.Fatalf("the hub must receive the merged session — credentials in, the spec out of appium:udid: %+v", seen)
	}
}

// ── EVERY FRAME AFTER THE CREATE FOLLOWS ITS SESSION ────────────────────────────────────────────────────────

func TestFramesAfterCreateFollowTheSessionTheyName(t *testing.T) {
	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	drive, _ := mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", localCreate))
	readFrame(t, conn)
	sendFrame(t, conn, sessionFrame("c2", cloudCreate))
	readFrame(t, conn)
	if got := drive.RoutedSessions(); got != 2 {
		t.Fatalf("two sessions were created, %d are known", got)
	}

	// The farm prefixes the hub base; the id is what matters, and a query string must not confuse the parse.
	sendFrame(t, conn, frame("f1", "GET", "/wd/hub/session/local-session-1/source?format=xml"))
	readFrame(t, conn)
	sendFrame(t, conn, frame("f2", "POST", "/session/hub-session-1/element"))
	readFrame(t, conn)

	local, cloud := appium.seen(), hub.seen()
	if len(local) != 2 || !strings.Contains(local[1].Path, "local-session-1") {
		t.Fatalf("the local session's frame must reach the local Appium: %+v", local)
	}
	if len(cloud) != 2 || !strings.Contains(cloud[1].Path, "hub-session-1") {
		t.Fatalf("the cloud session's frame must reach the hub: %+v", cloud)
	}
}

// ── INJECTION 2: the tidy-minded fallback — an unknown session routed by the node's flags ──────────────────

func TestAnUnknownSessionIsRefusedAndNeverRoutedByAFactAboutTheNode(t *testing.T) {
	// The shape a person produces while tidying: "no entry? fall back to the endpoint this node was configured
	// for". That is the rule this slice removed. After an agent restart every session it had is unknown, and a
	// frame for one must go NOWHERE — least of all to a stranger's hub because credentials happen to be set.
	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, frame("f1", "GET", "/session/nobody-created-this/source"))
	response := readFrame(t, conn)

	if got := int(response["status"].(float64)); got != http.StatusBadGateway {
		t.Fatalf("an unknown session is a refusal (502 in forward's own shape), got %d", got)
	}
	if len(hub.seen())+len(appium.seen()) != 0 {
		t.Fatalf("a frame for a session this agent never created was sent somewhere: hub=%d local=%d", len(hub.seen()), len(appium.seen()))
	}
	if !strings.Contains(out.String(), "never created session nobody-created-this") {
		t.Fatalf("the refusal must name the session and the reason:\n%s", out.String())
	}
}

func TestASessionIsForgottenWhenTheClientDeletesIt(t *testing.T) {
	_, appium, hubURL, appiumURL := twoEndpoints(t)
	_ = appium
	farm, farmServer := newStubFarmTunnel(t)
	drive, _ := mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("c1", localCreate))
	readFrame(t, conn)
	if drive.RoutedSessions() != 1 {
		t.Fatalf("one session known after create, got %d", drive.RoutedSessions())
	}
	sendFrame(t, conn, frame("d1", "DELETE", "/session/local-session-1"))
	readFrame(t, conn)
	if drive.RoutedSessions() != 0 {
		t.Fatalf("a deleted session must be forgotten — an entry for a dead session is a leak with the wrong answer in it; %d still known", drive.RoutedSessions())
	}
	// …and a frame for it afterwards is an unknown session, refused.
	sendFrame(t, conn, frame("f1", "GET", "/session/local-session-1/source"))
	response := readFrame(t, conn)
	if got := int(response["status"].(float64)); got != http.StatusBadGateway {
		t.Fatalf("a frame for a deleted session must be refused, got %d", got)
	}
}

// ── THE TWO SHAPES THAT ARE NOT MIXED KEEP EXACTLY WHAT THEY HAD ────────────────────────────────────────────

func TestACloudOnlyNodeRefusesALocalSessionWithASentenceAboutTheMissingAppium(t *testing.T) {
	// Shape 2 of Item 0 — credentials and cloud rows, no --appium. A local serial reaching it names a device this
	// node cannot drive; it is refused, and the sentence says which flag is missing rather than which vendor key.
	hub, _, hubURL, _ := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := mixedTunnel(t, farmServer.URL, "", hubURL, creds)
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", localCreate))
	response := readFrame(t, conn)
	if got := int(response["status"].(float64)); got != http.StatusBadGateway {
		t.Fatalf("refused, got %d", got)
	}
	if got := len(hub.seen()); got != 0 {
		t.Fatalf("a local session reached the hub from a cloud-only node (%d requests)", got)
	}
	if !strings.Contains(out.String(), "started without --appium") {
		t.Fatalf("the refusal must name the missing flag:\n%s", out.String())
	}
}

func TestALocalOnlyNodeRefusesACloudSessionItHasNoAccountFor(t *testing.T) {
	// Shape 1 with a stale cloud lease: no credentials, a body naming a spec. Never to the local Appium.
	_, appium, _, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	_, out := mixedTunnel(t, farmServer.URL, appiumURL, "", browserstack.Credentials{})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudCreate))
	readFrame(t, conn)
	if got := len(appium.seen()); got != 0 {
		t.Fatalf("a cloud session reached a local Appium that has never heard of it (%d requests)", got)
	}
	if !strings.Contains(out.String(), "no BrowserStack account") {
		t.Fatalf("the refusal must say why:\n%s", out.String())
	}
}

// ── INJECTIONS 3 AND 4: THE README'S SENTENCE AND THE STARTUP LINE ──────────────────────────────────────────

// readmePromise is the paragraph a customer might rely on, read from the file itself.
func readmePromise(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading the README: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "**A machine can do both.**")
	if start < 0 {
		t.Fatal("the README no longer promises that a machine can do both — if that is a decision, this seal and the routing table are the things to remove with it")
	}
	end := strings.Index(text[start:], "\n\n")
	if end < 0 {
		end = len(text) - start
	}
	return text[start : start+end]
}

func TestTheReadmesPromiseIsTheOneTheTunnelKeeps(t *testing.T) {
	// A SENTENCE CANNOT BE SEALED BY ITSELF. What can be sealed is the mechanism it describes: this test holds the
	// README's paragraph AND drives the tunnel it describes in the same breath, so a code change that serves one
	// path again fails here while quoting the promise it broke. (Injection 3: leave the sentence, serve one path.)
	promise := readmePromise(t)
	for _, claim := range []string{"local Appium", "BrowserStack's hub", "the device the lease named", "never sent to anybody's hub"} {
		if !strings.Contains(promise, claim) {
			t.Fatalf("the README's promise must say %q — it is the rule the tunnel applies:\n%s", claim, promise)
		}
	}

	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm, farmServer := newStubFarmTunnel(t)
	mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	conn := farm.waitConn(t)
	sendFrame(t, conn, sessionFrame("c1", localCreate))
	readFrame(t, conn)
	sendFrame(t, conn, sessionFrame("c2", cloudCreate))
	readFrame(t, conn)
	if len(appium.seen()) != 1 || len(hub.seen()) != 1 {
		t.Fatalf("the README promises \"%s\" — and the tunnel served local=%d hub=%d",
			strings.SplitN(promise, "\n", 2)[0], len(appium.seen()), len(hub.seen()))
	}
}

func TestTheStartupLineAndTheReadmeDescribeTheSameRouting(t *testing.T) {
	// Item 2. destination() documents the routing out loud at startup; the README documents it in prose. Both
	// name the same two endpoints and the same per-session rule, or one of them is lying. (Injection 4.)
	_, _, hubURL, appiumURL := twoEndpoints(t)
	_, farmServer := newStubFarmTunnel(t)
	_, out := mixedTunnel(t, farmServer.URL, appiumURL, hubURL, creds)
	waitFor(t, "drive tunnel established", func() bool { return strings.Contains(out.String(), "drive tunnel established") })

	var line string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "drive tunnel established") {
			line = l
		}
	}
	promise := readmePromise(t)
	for _, noun := range []string{"local Appium", "BrowserStack's hub", "per session"} {
		if !strings.Contains(line, noun) {
			t.Fatalf("the startup line for a mixed node must name %q, as the README does:\n%s", noun, line)
		}
	}
	if !strings.Contains(promise, "routed to where its device is") {
		t.Fatalf("the README must describe the per-session routing the startup line announces:\n%s", promise)
	}
}
