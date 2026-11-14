package tunnel_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// THE KEY THIS SUITE HUNTS FOR. Distinctive on purpose: every assertion that the credential did not escape
// looks for THIS string, so a leak is found by searching rather than by remembering which call might print.
const testKey = "bskey-ZZTOPSECRETZZ"

// A build id in the shape an upload returns. NOT A SECRET.
const testApp = "bs://f00dfeedf00dfeedf00dfeedf00dfeedf00dfeed"

// A LOCAL lease: the identifier in appium:udid is a serial. Used for the paths that must stay untouched.
const realSessionBody = `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"LOCAL-SERIAL-1"}]}}`

// A CLOUD lease: the identifier is the spec the customer configured and the farm leased. This is how the agent
// learns WHICH device to ask BrowserStack for — the channel already existed, and nothing new rides the wire.
const cloudSessionBody = `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"browserstack:Google Pixel 9|15.0"}]}}`

// seenRequest is what a stub endpoint received, recorded verbatim.
type seenRequest struct {
	Method, Path, ContentType, Body string
}

type recorder struct {
	mu       sync.Mutex
	requests []seenRequest
	status   int
	reply    string
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, seenRequest{
			Method: req.Method, Path: req.URL.RequestURI(),
			ContentType: req.Header.Get("Content-Type"), Body: string(body),
		})
		status, reply := r.status, r.reply
		r.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		if reply == "" {
			reply = `{"value":{"sessionId":"stub-session","capabilities":{}}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}
}

func (r *recorder) seen() []seenRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seenRequest(nil), r.requests...)
}

// cloudTunnel starts an agent whose hub is a stub. THE HUB IS THE ONLY THING REDIRECTED — the merge, the
// session recognition and the narration are the production ones, so this proves the code the live run uses.
func cloudTunnel(t *testing.T, farmURL, appiumURL, hubURL string, creds browserstack.Credentials) *strings.Builder {
	return cloudTunnelWithApp(t, farmURL, appiumURL, hubURL, creds, testApp)
}

func cloudTunnelWithApp(t *testing.T, farmURL, appiumURL, hubURL string, creds browserstack.Credentials, app string) *strings.Builder {
	t.Helper()
	out := &strings.Builder{}
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: farmURL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Token: "sk-agent-TUNNELSECRET", AppiumURL: appiumURL,
		BrowserStack: creds, BrowserStackHub: hubURL, BrowserStackApp: app,
		ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out: out,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go drive.Run(ctx)
	return out
}

func sessionFrame(id, body string) map[string]any {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	return map[string]any{
		"id": id, "method": "POST", "path": "/session",
		"headers": map[string]string{"Content-Type": "application/json"}, "bodyBase64": &encoded,
	}
}

// ── the one thing this slice proves ────────────────────────────────────────────────────────────────────────

// A REAL SESSION GOES TO THE HUB, AUTHENTICATED, AND THE PLATFORM NEVER HELD THE KEY. The farm frame carries a
// method, a path, a body and a content type — nothing else can cross — and what reaches BrowserStack carries
// the customer's credentials that were read on this machine.
func TestASessionGoesToTheHubCarryingCredentialsThePlatformNeverSent(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	appium := &recorder{}
	appiumServer := httptest.NewServer(appium.handler())
	defer appiumServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnel(t, farmServer.URL, appiumServer.URL, hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	response := readFrame(t, conn)

	if got := int(response["status"].(float64)); got != http.StatusOK {
		t.Fatalf("the hub's own status must come back verbatim, got %d", got)
	}

	seen := hub.seen()
	if len(seen) != 1 {
		t.Fatalf("the hub must have received exactly the session, got %d requests", len(seen))
	}
	if seen[0].Method != "POST" || !strings.HasSuffix(seen[0].Path, "/session") {
		t.Fatalf("wrong request reached the hub: %s %s", seen[0].Method, seen[0].Path)
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(seen[0].Body), &root); err != nil {
		t.Fatalf("what arrived was not JSON: %v", err)
	}
	entry := root["capabilities"].(map[string]any)["firstMatch"].([]any)[0].(map[string]any)
	options, ok := entry["bstack:options"].(map[string]any)
	if !ok || options["userName"] != "acme-user" || options["accessKey"] != testKey {
		t.Fatalf("the session arrived without the customer's credentials: %v", entry)
	}
	if _, present := entry["appium:udid"]; present {
		t.Fatal("the LOCAL serial travelled to a third party")
	}

	// AND THE LOCAL APPIUM WAS NEVER TOUCHED — a cloud session that also woke the local endpoint would be two
	// sessions, one of them silent.
	if got := len(appium.seen()); got != 0 {
		t.Fatalf("the local Appium received %d requests; with credentials the replay goes to the hub instead", got)
	}
	if strings.Contains(out.String(), testKey) {
		t.Fatal("the key reached the agent's own narration")
	}
}

// WITHOUT CREDENTIALS NOTHING CHANGES, and this is the seal for the half of the product that already worked.
// A plain local-Appium agent must behave exactly as it did before this slice existed.
func TestWithoutCredentialsTheLocalAppiumStillGetsTheBytesUnchanged(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	appium := &recorder{}
	appiumServer := httptest.NewServer(appium.handler())
	defer appiumServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	// The hub is CONFIGURED and still must not be used: absence of credentials is what decides, not absence of
	// a hub, so a future default hub cannot quietly switch a local agent to the cloud.
	cloudTunnel(t, farmServer.URL, appiumServer.URL, hubServer.URL, browserstack.Credentials{})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", realSessionBody))
	readFrame(t, conn)

	if got := len(hub.seen()); got != 0 {
		t.Fatalf("BrowserStack heard about a session on an agent with no BrowserStack account (%d requests)", got)
	}
	seen := appium.seen()
	if len(seen) != 1 {
		t.Fatalf("the local Appium must have received the session, got %d", len(seen))
	}
	if seen[0].Body != realSessionBody {
		t.Fatalf("the body must arrive BYTE-IDENTICAL on the path that predates this slice.\n want %s\n got  %s", realSessionBody, seen[0].Body)
	}
}

// ONE REQUEST IS REWRITTEN AND NO OTHER. Every in-session command is a POST too, and a merge that fired on all
// of them would corrupt element clicks and executes with a capability block.
func TestOnlySessionCreationIsRewrittenAndEveryOtherFrameIsBytes(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	// THE SESSION MUST EXIST FIRST. Since A_PROMISE_THE_TUNNEL_DOES_NOT_KEEP a frame follows the session it names
	// to wherever that session was created, and a session this tunnel never created is refused — so the command
	// below is preceded by the create that gives `stub-session` (the recorder's default id) a destination.
	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	readFrame(t, conn)
	const commandBody = `{"id":"element-42","text":"hello"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(commandBody))
	sendFrame(t, conn, map[string]any{
		"id": "f2", "method": "POST", "path": "/session/stub-session/element/42/value",
		"headers": map[string]string{"Content-Type": "application/json"}, "bodyBase64": &encoded,
	})
	readFrame(t, conn)

	seen := hub.seen()
	if len(seen) != 2 {
		t.Fatalf("expected the create and one forwarded command, got %d", len(seen))
	}
	if seen[1].Body != commandBody {
		t.Fatalf("an in-session command must cross as BYTES.\n want %s\n got  %s", commandBody, seen[1].Body)
	}
	if strings.Contains(seen[1].Body, testKey) {
		t.Fatal("the credential was merged into a command body — the key now travels on every request instead of one")
	}
}

// ── item 3: the three sentences, on the agent's own output ─────────────────────────────────────────────────

func TestTheHubsRefusalIsNamedForWhatItIs(t *testing.T) {
	hub := &recorder{status: http.StatusUnauthorized, reply: `{"value":{"error":"unknown error"}}`}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	response := readFrame(t, conn)

	// THE RESPONSE IS NOT REWRITTEN. The driver on the platform side must see what the hub said, or its own
	// error handling is reading a sentence we invented.
	if got := int(response["status"].(float64)); got != http.StatusUnauthorized {
		t.Fatalf("the hub's status must cross verbatim, got %d", got)
	}
	if !strings.Contains(out.String(), "refused the credentials") {
		t.Fatalf("a rejected key must be named as one; got %q", out.String())
	}
	if strings.Contains(out.String(), testKey) {
		t.Fatal("the refusal carried the key")
	}
}

func TestAHubWeCannotReachIsNamedAsOurSideRatherThanTheirs(t *testing.T) {
	farm, farmServer := newStubFarmTunnel(t)
	// A port nothing listens on: the connection fails before any account is touched.
	out := cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", "http://127.0.0.1:1",
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	response := readFrame(t, conn)

	if got := int(response["status"].(float64)); got != http.StatusBadGateway {
		t.Fatalf("an unreachable target is a 502 exactly as it always was, got %d", got)
	}
	if !strings.Contains(out.String(), "could not reach BrowserStack") {
		t.Fatalf("the sentence must say nothing was attempted on the account; got %q", out.String())
	}
	if strings.Contains(out.String(), testKey) {
		t.Fatal("the unreachable sentence carried the key")
	}
}

func TestABusyAccountIsNotReportedAsABadKey(t *testing.T) {
	hub := &recorder{status: http.StatusInternalServerError,
		reply: `{"value":{"message":"All parallel tests are currently in use"}}`}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	readFrame(t, conn)

	said := out.String()
	if !strings.Contains(said, "could not give us the device") {
		t.Fatalf("a busy plan must be named as one; got %q", said)
	}
	if strings.Contains(said, "refused the credentials") {
		t.Fatal("a busy account reported as a bad key sends the customer to reset a key that works — the exact defect three sentences exist to prevent")
	}
}

// ── refusing beats forwarding, end to end ──────────────────────────────────────────────────────────────────

func TestAnUnmergeableSessionNeverReachesTheHub(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", `{"desiredCapabilities":{"platformName":"Android"}}`))
	response := readFrame(t, conn)

	if got := int(response["status"].(float64)); got != http.StatusBadGateway {
		t.Fatalf("the refusal is a 502 in the shape forward() already uses, got %d", got)
	}
	if got := len(hub.seen()); got != 0 {
		t.Fatal("an unauthenticated session reached BrowserStack — it would come back as a credential error and send the customer to check the one thing that is not wrong")
	}
	// SINCE A_PROMISE_THE_TUNNEL_DOES_NOT_KEEP the property holds BY CONSTRUCTION rather than by a parse failure:
	// a body that names no cloud device is never routed to the hub at all — it is a local session (here, against a
	// dead local port, hence the 502), so the hub sees nothing and no credential was ever near it. The "shape this
	// agent does not recognise" sentence is now reachable only for a body that names a cloud device and still
	// cannot be merged, which the same parser makes very nearly impossible; it stays as the second lock.
	if strings.Contains(out.String(), testKey) {
		t.Fatalf("a session that never went near the hub must not narrate the key: %q", out.String())
	}
}

// ── the credential never leaves, asserted over everything this suite produced ──────────────────────────────

// A BELT-AND-BRACES SWEEP over the narration of every path above. The per-test checks are the sharp ones; this
// is the one that catches a NEW narration line somebody adds later without thinking about it.
func TestNoPathInThisSuiteEverNarratesTheKey(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	var narrations []string
	for _, body := range []string{realSessionBody, `{"desiredCapabilities":{}}`, `not json at all`} {
		farm, farmServer := newStubFarmTunnel(t)
		out := cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
			browserstack.Credentials{User: "acme-user", Key: testKey})
		conn := farm.waitConn(t)
		sendFrame(t, conn, sessionFrame("f1", body))
		readFrame(t, conn)
		narrations = append(narrations, out.String())
	}
	for i, said := range narrations {
		if strings.Contains(said, testKey) {
			t.Fatalf("narration %d leaked the key: %q", i, said)
		}
	}
}

// ── the build reaches the hub, and a session without one never leaves the machine ──────────────────────────

func TestTheBuildTravelsWithTheSession(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	cloudTunnel(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	readFrame(t, conn)

	seen := hub.seen()
	if len(seen) != 1 {
		t.Fatalf("expected the session, got %d requests", len(seen))
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(seen[0].Body), &root); err != nil {
		t.Fatalf("what arrived was not JSON: %v", err)
	}
	entry := root["capabilities"].(map[string]any)["firstMatch"].([]any)[0].(map[string]any)
	if entry[browserstack.AppCapability] != testApp {
		t.Fatalf("the session arrived with no build — App Automate cannot start one, and its refusal names "+
			"platforms and browsers rather than builds: %v", entry)
	}
}

// ITEM 2, END TO END: with credentials and no build, nothing leaves the machine and the agent says what is
// missing. The hub's own message for this failure sends a reader to check two things that are fine.
func TestASessionWithNoBuildNeverReachesTheHub(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnelWithApp(t, farmServer.URL, "http://127.0.0.1:1", hubServer.URL,
		browserstack.Credentials{User: "acme-user", Key: testKey}, "")
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", cloudSessionBody))
	response := readFrame(t, conn)

	if got := len(hub.seen()); got != 0 {
		t.Fatal("a session with no build reached BrowserStack — it comes back as \"Platform and Browser not " +
			"valid.\", which names neither the missing build nor the remedy")
	}
	if got := int(response["status"].(float64)); got != http.StatusBadGateway {
		t.Fatalf("the refusal is a 502 in the shape forward() already uses, got %d", got)
	}
	said := out.String()
	for _, must := range []string{"--browserstack-app", "upload"} {
		if !strings.Contains(said, must) {
			t.Fatalf("the narration must name %q so the reader knows the next command to type; got %q", must, said)
		}
	}
	if strings.Contains(said, testKey) {
		t.Fatal("the missing-build refusal carried the key")
	}
}

// AND IT MUST NOT FIRE WITHOUT BROWSERSTACK. An agent driving a local Appium needs no build; a refusal here
// would break every customer who never asked for the cloud.
func TestAnAgentWithNoBrowserStackIsUnaffectedByTheBuildRule(t *testing.T) {
	appium := &recorder{}
	appiumServer := httptest.NewServer(appium.handler())
	defer appiumServer.Close()

	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnelWithApp(t, farmServer.URL, appiumServer.URL, "", browserstack.Credentials{}, "")
	conn := farm.waitConn(t)

	sendFrame(t, conn, sessionFrame("f1", realSessionBody))
	readFrame(t, conn)

	seen := appium.seen()
	if len(seen) != 1 || seen[0].Body != realSessionBody {
		t.Fatalf("the local path must be byte-identical and untouched by a rule about BrowserStack builds: %+v", seen)
	}
	if strings.Contains(out.String(), "browserstack-app") {
		t.Fatalf("an agent with no BrowserStack account was told it needs a BrowserStack build: %q", out.String())
	}
}
