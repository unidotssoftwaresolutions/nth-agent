package tunnel_test

import (
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — what the tunnel reaches on this machine belongs to this account.
//
// another account that holds the --appium port while the customer's Appium is down was sent the workspace's runs
// and wrote every answer. another account that holds a phone's mirror port was shown to the workspace as that
// phone's screen. The capture proxy already refused other accounts' connections; the tunnel now asks the same kernel
// about the programs it connects to. LocalOwner is the seam: a test cannot open a listener as another account.

// anotherAccount is the kernel's answer for a listener another account on this machine holds.
func anotherAccount(net.Conn) (int, bool) { return 1002, true }

// nobodyKnows is the kernel's answer on a system that cannot be asked.
func nobodyKnows(net.Conn) (int, bool) { return 0, false }

func localTunnel(t *testing.T, farmURL, appiumURL, hubURL string, owner func(net.Conn) (int, bool)) *syncWriter {
	t.Helper()
	var out syncWriter
	liveTunnel(t, farmURL, &out, func(c *tunnel.Config) {
		c.AppiumURL = appiumURL
		c.LocalOwner = owner
		if hubURL != "" {
			c.BrowserStack, c.BrowserStackHub, c.BrowserStackApp = creds, hubURL, testApp
		}
	})
	return &out
}

func TestASessionIsNeverSentToAnAppiumAnotherAccountRuns(t *testing.T) {
	for _, c := range []struct {
		name  string
		owner func(net.Conn) (int, bool)
		says  string
	}{
		{"another account's listener", anotherAccount, "runs as another account on this machine (uid 1002), so the agent will not use it as your Appium"},
		{"a listener whose account cannot be found", nobodyKnows, "could not tell which account runs the program on 127.0.0.1:"},
	} {
		t.Run(c.name, func(t *testing.T) {
			impostor := &recorder{}
			server := httptest.NewServer(impostor.handler())
			t.Cleanup(server.Close)
			farm := newLiveFarm(t)
			localTunnel(t, farm.server.URL, server.URL, "", c.owner)
			conn := farm.next(t)
			reader := readFarmSide(conn)

			send(t, conn, sessionFrame("f1", localCreate))
			response := reader.await(t, 5*time.Second, "the create's answer", func(f map[string]any) bool { return f["id"] == "f1" })
			if response["status"] != float64(http.StatusBadGateway) {
				t.Fatalf("a create for an Appium another account runs must be refused, got %v", response["status"])
			}
			if got := decodeBody(t, response); !strings.Contains(got, c.says) {
				t.Fatalf("the refusal must say why in plain words (%q):\n%s", c.says, got)
			}
			if seen := impostor.seen(); len(seen) != 0 {
				t.Fatalf("the workspace's session reached a program another account runs: %+v", seen)
			}
		})
	}
}

func TestThisAccountsOwnAppiumStillGetsTheSessionByteIdentical(t *testing.T) {
	appium := &recorder{}
	server := httptest.NewServer(appium.handler())
	t.Cleanup(server.Close)
	farm := newLiveFarm(t)
	localTunnel(t, farm.server.URL, server.URL, "", nil) // nil = the real kernel, and the test's own listener is this account's
	conn := farm.next(t)
	reader := readFarmSide(conn)

	send(t, conn, sessionFrame("f1", localCreate))
	response := reader.await(t, 5*time.Second, "the create's answer", func(f map[string]any) bool { return f["id"] == "f1" })
	if response["status"] != float64(http.StatusOK) {
		t.Fatalf("this account's own Appium must still get the session, got %v: %s", response["status"], decodeBody(t, response))
	}
	if seen := appium.seen(); len(seen) != 1 || seen[0].Body != localCreate {
		t.Fatalf("the local Appium must receive the local session byte-identical: %+v", seen)
	}
}

// THE HUB IS NOT ON THIS MACHINE, and the owner rule must not reach it: BrowserStack's hub is a host on the internet.
func TestTheCloudHubIsNotHeldToTheLocalRule(t *testing.T) {
	hub, appium, hubURL, appiumURL := twoEndpoints(t)
	farm := newLiveFarm(t)
	localTunnel(t, farm.server.URL, appiumURL, hubURL, anotherAccount)
	conn := farm.next(t)
	reader := readFarmSide(conn)

	send(t, conn, sessionFrame("f1", cloudCreate))
	reader.await(t, 5*time.Second, "the create's answer", func(f map[string]any) bool { return f["id"] == "f1" })
	if len(hub.seen()) != 1 || len(appium.seen()) != 0 {
		t.Fatalf("a cloud create goes to the hub whatever the local rule says: hub %d, appium %d", len(hub.seen()), len(appium.seen()))
	}
}

func TestAMirrorAnotherAccountServesIsNeverShownAsThePhone(t *testing.T) {
	mirror := newFakeMirror(t, []byte("--BoundaryString\r\nContent-Type: image/jpeg\r\n\r\nFAKE\r\n"), time.Millisecond, 4096, false)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
		c.Mirrors = offering(mirror.port)
		c.LocalOwner = anotherAccount
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "33333333-0000-0000-0000-0000000000aa"
	send(t, conn, streamStart(id, mirror.url()))
	for {
		f := reader.await(t, 5*time.Second, "the stream's end", func(f map[string]any) bool { return f["id"] == id })
		if f["kind"] == "stream-chunk" {
			t.Fatalf("a mirror another account serves reached the farm as the phone's screen: %v", f)
		}
		if f["kind"] == "stream-end" {
			if msg, _ := f["error"].(string); !strings.Contains(msg, "runs as another account on this machine (uid 1002), so the agent will not use it as a phone's live view") {
				t.Fatalf("the end must say why in plain words: %v", f)
			}
			break
		}
	}
	if mirror.wrote.Load() != 0 {
		t.Fatalf("the impostor's screen server was read (%d bytes)", mirror.wrote.Load())
	}
}

func decodeBody(t *testing.T, frame map[string]any) string {
	t.Helper()
	encoded, _ := frame["bodyBase64"].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("an answer whose body is not base64: %v", frame)
	}
	return string(decoded)
}

// a session-create from a lease must not change the customer's Apple team or devices. The one capability that
// can is appium:allowProvisioningDeviceRegistration — WebDriverAgent's build then runs xcodebuild with
// -allowProvisioningUpdates -allowProvisioningDeviceRegistration, which registers the phone with the team and may create
// profiles on it. Only the person at this machine says yes to that, in npx nthcase-agent setup-ios.
func TestASessionThatWouldChangeTheCustomersAppleAccountIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"alwaysMatch":         `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:udid":"00008140-0000C0FFEE0000A1","appium:allowProvisioningDeviceRegistration":true}}}`,
		"firstMatch":          `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"00008140-0000C0FFEE0000A1"},{"appium:allowProvisioningDeviceRegistration":true}]}}`,
		"appium:options":      `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:options":{"udid":"00008140-0000C0FFEE0000A1","allowProvisioningDeviceRegistration":true}}}}`,
		"legacy desired":      `{"desiredCapabilities":{"platformName":"iOS","udid":"00008140-0000C0FFEE0000A1","allowProvisioningDeviceRegistration":true}}`,
		"another spelling":    `{"capabilities":{"alwaysMatch":{"appium:udid":"00008140-0000C0FFEE0000A1","APPIUM:AllowProvisioningDeviceRegistration":"true"}}}`,
		"escaped in the JSON": `{"capabilities":{"alwaysMatch":{"appium:udid":"00008140-0000C0FFEE0000A1","appium:allowProvisioningDeviceRegistration":true}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			appium := &recorder{}
			server := httptest.NewServer(appium.handler())
			t.Cleanup(server.Close)
			farm := newLiveFarm(t)
			out := localTunnel(t, farm.server.URL, server.URL, "", nil)
			conn := farm.next(t)
			reader := readFarmSide(conn)

			send(t, conn, sessionFrame("f1", body))
			response := reader.await(t, 5*time.Second, "the create's answer", func(f map[string]any) bool { return f["id"] == "f1" })
			if seen := appium.seen(); len(seen) != 0 {
				t.Fatalf("a create that registers the phone with the customer's Apple team reached Appium: %+v", seen)
			}
			if response["status"] == float64(http.StatusOK) {
				t.Fatalf("the create must be refused: %v", response)
			}
			if got := decodeBody(t, response); !strings.Contains(got, "only the person at this machine may do that") ||
				!strings.Contains(got, "npx nthcase-agent@latest setup-ios") {
				t.Fatalf("the refusal says why and where the yes is given:\n%s", got)
			}
			if !strings.Contains(out.String(), "allowProvisioningDeviceRegistration") {
				t.Fatalf("the console says what was refused:\n%s", out.String())
			}
		})
	}
}

// …and the capabilities a platform iOS run always sends — the team, the signing identity, the WebDriverAgent bundle id —
// choose how to sign and change nothing at Apple; they still go through byte-identical. So does an explicit false.
func TestTheSigningCapabilitiesARunSendsStillGoThrough(t *testing.T) {
	body := `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"00008140-0000C0FFEE0000A1",` +
		`"appium:xcodeOrgId":"CERT8KD3M1","appium:xcodeSigningId":"Apple Development","appium:updatedWDABundleId":"com.example.WebDriverAgentRunner",` +
		`"appium:allowProvisioningDeviceRegistration":false}]}}`
	appium := &recorder{}
	server := httptest.NewServer(appium.handler())
	t.Cleanup(server.Close)
	farm := newLiveFarm(t)
	localTunnel(t, farm.server.URL, server.URL, "", nil)
	conn := farm.next(t)
	reader := readFarmSide(conn)
	send(t, conn, sessionFrame("f1", body))
	response := reader.await(t, 5*time.Second, "the create's answer", func(f map[string]any) bool { return f["id"] == "f1" })
	if response["status"] != float64(http.StatusOK) {
		t.Fatalf("an ordinary iOS create goes through: %v %s", response["status"], decodeBody(t, response))
	}
	if seen := appium.seen(); len(seen) != 1 || seen[0].Body != body {
		t.Fatalf("byte-identical: %+v", seen)
	}
}
