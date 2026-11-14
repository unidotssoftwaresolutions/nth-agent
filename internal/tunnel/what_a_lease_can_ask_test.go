package tunnel_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// WHAT A LEASE CAN ASK (WHAT_A_LEASE_CAN_ASK). A lease holder reaches this machine only through the farm, and a farm can be
// wrong or not be the farm at all; so what a request may make this agent do on the customer's computer is held here, not
// left to the farm. Each seal below names one thing a request could ask for that reached the customer's Appium, the hub
// or the capture before this round, and says what happens now. Every refusal is a sentence on the farm's answer and on
// the customer's terminal, and nothing refused reaches Appium.

// ── INSIDE A SESSION ────────────────────────────────────────────────────────────────────────────────────────────────

// A recording of an iPhone's screen that XCTest makes is uploaded FROM THIS COMPUTER when its stop names a remotePath, as
// every sibling recording command the agent already refuses is; and the app-strings commands read a strings file at a path
// on this computer. Both are refused by name, whoever asks.
func TestWhatReadsOrUploadsFromThisComputerInsideASessionIsRefused(t *testing.T) {
	const s = "/session/local-session-1"
	for name, c := range map[string]struct{ path, body string }{
		"an XCTest recording, uploaded from here": {s + "/execute/sync", `{"script":"mobile: stopXCTestScreenRecording","args":[{"remotePath":"http://192.168.1.9/x","method":"PUT"}]}`},
		"an XCTest recording, in other letters":   {s + "/execute/sync", `{"script":"MOBILE: stopxctestscreenrecording","args":[{"remotePath":"http://192.168.1.9/x"}]}`},
		"an app's strings, by mobile: command":    {s + "/execute/sync", `{"script":"mobile: getAppStrings","args":[{"stringFile":"/Users/ada/Library/Preferences/x.plist"}]}`},
		"an app's strings, by its route":          {s + "/appium/app/strings", `{"language":"en","stringFile":"../../../../etc/hosts"}`},
		"an app's strings, its route in capitals": {s + "/Appium/App/Strings", `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			reader, send, appium, out, _ := startedSession(t)
			send(inSession("f1", "POST", c.path, c.body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "this computer", "nothing was sent to Appium")
		})
	}
}

// The emulator commands Appium sends through `adb emu` write their text to the emulator's console on this computer as one
// line; a line break inside that text would start a second console command of the sender's choosing. Their text is held to
// one line, and a run's own values (the runner's shake, sensorSet with "x:y:z") still pass byte for byte.
func TestAnEmulatorConsoleCommandCarriesNoSecondLine(t *testing.T) {
	const s = "/session/local-session-1"
	for name, body := range map[string]string{
		"sensorSet, a line break in its value":  `{"script":"mobile: sensorSet","args":[{"sensorType":"acceleration","value":"0:9.8:0\nredir add tcp:5000:22"}]}`,
		"sendSms, a carriage return":            `{"script":"mobile: sendSms","args":[{"phoneNumber":"5551234","message":"hi\r\nscreenrecord start /tmp/x.webm"}]}`,
		"gsmCall, a line break in the number":   `{"script":"mobile: gsmCall","args":[{"phoneNumber":"555\nkill","action":"call"}]}`,
		"sensorSet, another control character":  `{"script":"mobile: sensorSet","args":[{"sensorType":"acceleration","value":"0:9.8:0\u0000"}]}`,
		"sensorSet, in other letters":           `{"script":"mobile:SENSORSET","args":[{"sensorType":"light","value":"1\nkill"}]}`,
		"fingerprint, a line break in its id":   `{"script":"mobile: fingerprint","args":[{"fingerprintId":"1\navd stop"}]}`,
		"networkSpeed, a line break in a value": `{"script":"mobile: networkSpeed","args":[{"speed":"full\nkill"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			reader, send, appium, out, _ := startedSession(t)
			send(inSession("f1", "POST", s+"/execute/sync", body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "emulator's console", "nothing was sent to Appium")
		})
	}

	reader, send, appium, out, _ := startedSession(t)
	for i, value := range []string{"100:0:0", "-100:0:0", "0:9.81:0"} {
		id := fmt.Sprintf("shake-%d", i)
		body := `{"script":"mobile: sensorSet","args":[{"sensorType":"acceleration","value":"` + value + `"}]}`
		send(inSession(id, "POST", s+"/execute/sync", body))
		if r := answerOf(t, reader, id); r["status"] != float64(http.StatusOK) {
			t.Fatalf("the runner's shake (%s) must still pass: %v %s\n%s", value, r["status"], decodeBody(t, r), out.String())
		}
		if seen := appium.seen(); len(seen) != i+1 || seen[i].Body != body {
			t.Fatalf("the runner's shake must reach Appium byte for byte: %+v", seen)
		}
	}
}

// A character Go does not count as white space and JavaScript does (U+FEFF) hid a refused command from the agent while the
// driver, which trims the script as JavaScript does, still ran it. The name is read once, the way the driver reads it, and
// a command's name holds letters and digits only.
func TestACommandHiddenByAnInvisibleCharacterIsRefused(t *testing.T) {
	const s = "/session/local-session-1"
	for name, script := range map[string]string{
		"after the prefix":          "mobile:\ufeffstopMediaProjectionRecording",
		"before the prefix":         "\ufeffmobile: shell",
		"after the name":            "mobile: shell\ufeff",
		"a zero-width space inside": "mobile: sh\u200bell",
		"a build gate, hidden":      "mobile:\ufeffinstallApp",
	} {
		t.Run(name, func(t *testing.T) {
			reader, send, appium, out, _ := startedSession(t)
			body, _ := json.Marshal(map[string]any{"script": script, "args": []any{map[string]any{"remotePath": "http://192.168.1.9/", "app": "/Users/ada/app.apk", "command": "id"}}})
			send(inSession("f1", "POST", s+"/execute/sync", string(body)))
			says := []string{"nothing was sent to Appium"}
			if name == "a zero-width space inside" {
				says = append(says, `"sh\u200bell"`) // the name as it came, its invisible character written out
			}
			refused(t, name, answerOf(t, reader, "f1"), appium, out, says...)
		})
	}
}

// The agent read a body as Go's decoder does — a member whose name matches in any letters, the last one winning — while
// Appium reads the exact name. A body that names a member twice, or in two spellings that differ only in letters, is
// refused: the agent cannot tell which of them Appium reads.
func TestAMemberNamedTwiceOrInOtherLettersIsRefused(t *testing.T) {
	const s = "/session/local-session-1"
	for name, c := range map[string]struct{ path, body string }{
		"a script, and a decoy in capitals":   {s + "/execute/sync", `{"script":"mobile: stopScreenRecording","args":[{"remotePath":"http://192.168.1.9/"}],"SCRIPT":"mobile: getDeviceTime"}`},
		"a script, and a decoy with a long s": {s + "/execute/sync", `{"script":"mobile: stopScreenRecording","args":[{"remotePath":"http://192.168.1.9/"}],"\u017fcript":"mobile: getDeviceTime"}`},
		"a script named twice":                {s + "/execute/sync", `{"script":"mobile: shell","args":[{"command":"id"}],"script":"mobile: getDeviceTime"}`},
		"args, and a decoy in capitals":       {s + "/execute/sync", `{"script":"mobile: installApp","args":[{"app":"/Users/ada/a.apk"}],"ARGS":[]}`},
		"a log's type, and a decoy":           {s + "/se/log", `{"type":"server","TYPE":"logcat"}`},
		"a log's type named twice":            {s + "/log", `{"type":"server","type":"logcat"}`},
		"an install's path, and a decoy":      {s + "/appium/device/install_app", `{"appPath":"/Users/ada/a.apk","APPPATH":"/Users/ada/b.apk"}`},
		"a member twice inside args":          {s + "/execute/sync", `{"script":"mobile: clickGesture","args":[{"x":1,"x":2}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			reader, send, appium, out, _ := startedSession(t)
			send(inSession("f1", "POST", c.path, c.body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "nothing was sent to Appium")
		})
	}
}

// ── A SESSION'S CREATE ─────────────────────────────────────────────────────────────────────────────────────────────

// language, locale, appPackage and appActivity were any text; a driver that puts one into a shell command on the phone
// would run what follows a ; or a $( in it. Each is held to the shape its kind of value has. Every value the runner
// sends today still passes (TestEveryCreateTheFarmsRunnerSendsTodayStillPasses).
func TestAFreeTextCapabilityIsHeldToItsShape(t *testing.T) {
	for name, extra := range map[string]string{
		"a language with a command after it": `"appium:language":"en;reboot"`,
		"a locale with a command in it":      `"appium:locale":"US$(id)"`,
		"a package with a space and more":    `"appium:appPackage":"com.example.lantern && id"`,
		"an activity with a backtick":        `"appium:appActivity":".Main` + "`id`" + `"`,
		"an activity with a new line":        `"appium:appActivity":".Main\nid"`,
		"a package, in appium:options":       `"appium:options":{"appPackage":"com.example;id"}`,
		"an empty language":                  `"appium:language":""`,
	} {
		t.Run(name, func(t *testing.T) {
			_, reader, send, appium, out, _ := askingTunnel(t, offeredAndroid)
			body := `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"` + offeredAndroid + `",` + extra + `}]}}`
			send(sessionFrame("f1", body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "nothing was sent to Appium")
		})
	}
}

// A capability given both in a set and inside its appium:options is two values for one name, and the agent judged
// whichever its map kept. Both are judged now, and a name given both ways is refused: Appium may read either.
func TestANameGivenInASetAndInItsOptionsIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"two offered phones, one name": `{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:udid":"` + offeredAndroid + `",` +
			`"appium:options":{"appium:udid":"` + offeredIPhone + `"}}}}`,
		"the same phone, both ways": `{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:udid":"` + offeredAndroid + `",` +
			`"appium:options":{"udid":"` + offeredAndroid + `"}}}}`,
		"a setting, both ways": `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:udid":"` + offeredAndroid + `","appium:noReset":true,` +
			`"appium:options":{"noReset":false}}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, reader, send, appium, out, _ := askingTunnel(t, offeredIPhone, offeredAndroid)
			send(sessionFrame("f1", body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "appium:options", "nothing was sent to Appium")
		})
	}
	// …and a phone that is not offered, given the other way, is refused every time, never by the luck of a map's order.
	_, reader, send, appium, out, _ := askingTunnel(t, offeredAndroid)
	for i := range 24 {
		id := fmt.Sprintf("f%d", i)
		send(sessionFrame(id, `{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:udid":"`+offeredAndroid+`",`+
			`"appium:options":{"appium:udid":"emulator-5554"}}}}`))
		refused(t, "a phone not offered, inside appium:options", answerOf(t, reader, id), appium, out, "nothing was sent to Appium")
	}
}

// ── A CLOUD SESSION'S CREATE ───────────────────────────────────────────────────────────────────────────────────────

// The vendor block that goes to BrowserStack with the customer's key is built from what this agent writes and a short list
// of labels; BrowserStack Local, other apps, uploaded media and logging options a farm's body asked for are left out, each
// named on the customer's terminal, and the session starts without them.
func TestACloudCreateCarriesOnlyTheVendorOptionsOnTheList(t *testing.T) {
	hub := &recorder{}
	hubServer := httptest.NewServer(hub.handler())
	defer hubServer.Close()
	appium := &recorder{}
	appiumServer := httptest.NewServer(appium.handler())
	defer appiumServer.Close()
	farm, farmServer := newStubFarmTunnel(t)
	out := cloudTunnel(t, farmServer.URL, appiumServer.URL, hubServer.URL, browserstack.Credentials{User: "acme-user", Key: testKey})
	conn := farm.waitConn(t)

	body := `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",` +
		`"appium:udid":"browserstack:Google Pixel 9|15.0","browserstack.local":true,"appium:otherApps":["bs://other"],` +
		`"bstack:options":{"local":true,"localIdentifier":"office","otherApps":["bs://other"],"midSessionInstallApps":["bs://x"],` +
		`"uploadMedia":["media://x"],"networkLogs":true,"projectName":"Acme","appiumVersion":"2.4.1"}}]}}`
	sendFrame(t, conn, sessionFrame("f1", body))
	if response := readFrame(t, conn); int(response["status"].(float64)) != http.StatusOK {
		t.Fatalf("the session must still start, without what was left out: %v", response)
	}
	seen := hub.seen()
	if len(seen) != 1 {
		t.Fatalf("the hub must receive the create: %+v", seen)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(seen[0].Body), &root); err != nil {
		t.Fatal(err)
	}
	set := root["capabilities"].(map[string]any)["firstMatch"].([]any)[0].(map[string]any)
	options, _ := set["bstack:options"].(map[string]any)
	for _, gone := range []string{"local", "localIdentifier", "otherApps", "midSessionInstallApps", "uploadMedia", "networkLogs"} {
		if _, present := options[gone]; present {
			t.Errorf("bstack:options.%s reached BrowserStack with the customer's key: %s", gone, seen[0].Body)
		}
	}
	for _, gone := range []string{"browserstack.local", "appium:otherApps"} {
		if _, present := set[gone]; present {
			t.Errorf("%s reached BrowserStack with the customer's key: %s", gone, seen[0].Body)
		}
	}
	if options["projectName"] != "Acme" || options["appiumVersion"] != "2.4.1" || options["accessKey"] != testKey {
		t.Fatalf("a label and the Appium version stay, and the session is authenticated: %v", options)
	}
	said := out.String()
	for _, named := range []string{"local", "localIdentifier", "otherApps", "midSessionInstallApps", "uploadMedia", "networkLogs", "browserstack.local", "appium:otherApps"} {
		if !strings.Contains(said, named) {
			t.Errorf("the customer's terminal must name %s as left out:\n%s", named, said)
		}
	}
	if strings.Contains(said, testKey) {
		t.Fatal("the key reached the agent's own narration")
	}
}

// ── NETWORK CAPTURE ────────────────────────────────────────────────────────────────────────────────────────────────

// A capture-start, and the farm's handback question, name a phone; the phone must be one this agent offers right now, as a
// session's must. A phone left out with --skip, one on Wi-Fi, or one never offered, is not proxied, recorded or asked
// about, and nothing is said about whether it is attached.
func TestCaptureOnlyTouchesAPhoneThisAgentOffers(t *testing.T) {
	capture := &fakeCapture{clean: true}
	appium, hits := appiumThatCounts(t)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appium.URL
		c.Capture = capture
		offeringPhones(c, offeredAndroid)
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)

	send(t, conn, map[string]any{"kind": "capture-start", "leaseId": captureLease, "serial": "192.168.1.20:5555"})
	send(t, conn, map[string]any{"kind": "capture-start", "leaseId": otherLease, "serial": offeredAndroid})
	waitFor(t, "the offered phone's start handed over", func() bool { return len(capture.said()) >= 1 })
	question := fmt.Sprintf(`{"udid":"R5CT99NOTOFR","leaseId":%q}`, captureLease)
	send(t, conn, handbackQuestion("22222222-0000-0000-0000-000000000001", map[string]string{"X-GuruFarm-Asks": "phone-state"}, question))
	answer := answerOf(t, reader, "22222222-0000-0000-0000-000000000001")
	got := strings.Join(capture.said(), " | ")
	if got != "start "+otherLease+" "+offeredAndroid {
		t.Fatalf("only the offered phone's start reaches the capture, and no handback runs adb on a phone not offered: %s", got)
	}
	said := bodyOf(t, answer)
	if answer["status"] != float64(http.StatusOK) || said["clean"] != false || !strings.Contains(fmt.Sprint(said["observation"]), "does not offer") {
		t.Fatalf("the handback for a phone not offered is answered not clean, in words: %v %v", answer["status"], bodyOf(t, answer))
	}
	if !strings.Contains(out.String(), "does not offer") {
		t.Fatalf("the customer's terminal says why the capture was not started:\n%s", out.String())
	}
	if hits.Load() != 0 {
		t.Fatal("a capture frame reached Appium")
	}
}

// ── THE LIVE VIEW ──────────────────────────────────────────────────────────────────────────────────────────────────

// A phone's mirror, opened while a session runs on it, ends when that session ends — by the client's own DELETE or by the
// agent's release of a silent one — so a viewer from one lease does not go on watching the phone into the next.
func TestAMirrorEndsWhenItsPhonesSessionEnds(t *testing.T) {
	const phone = "R5CT30ABCDE"
	for _, how := range []string{"the client's DELETE", "the agent's release of a silent session"} {
		t.Run(how, func(t *testing.T) {
			mirror := newFakeMirror(t, []byte(strings.Repeat("m", 8*1024)), 5*time.Millisecond, 0, false)
			appium := &recorder{reply: `{"value":{"sessionId":"local-session-1","capabilities":{}}}`}
			server := httptest.NewServer(appium.handler())
			t.Cleanup(server.Close)
			farm := newLiveFarm(t)
			var out syncWriter
			liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
				c.AppiumURL = server.URL
				c.Mirrors = offering(mirror.port)
				offeringPhones(c, phone)
				c.ReleaseMargin = 200 * time.Millisecond
			})
			conn := farm.next(t)
			reader := readFarmSide(conn)

			timeout := ""
			if how != "the client's DELETE" {
				timeout = `,"appium:newCommandTimeout":1`
			}
			send(t, conn, sessionFrame("create", `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",`+
				`"appium:udid":"`+phone+`"`+timeout+`}]}}`))
			if r := answerOf(t, reader, "create"); r["status"] != float64(http.StatusOK) {
				t.Fatalf("the session must start: %v %s", r["status"], decodeBody(t, r))
			}
			const id = "33333333-0000-0000-0000-0000000000a1"
			send(t, conn, streamStart(id, mirror.url()))
			reader.await(t, 5*time.Second, "a first chunk", ofStream(id, "stream-chunk"))
			if how == "the client's DELETE" {
				send(t, conn, inSession("delete", "DELETE", "/session/local-session-1", ""))
			}
			end := reader.await(t, 6*time.Second, "the End for a mirror whose session ended", ofStream(id, "stream-end"))
			if !strings.Contains(fmt.Sprint(end["error"]), "session") {
				t.Fatalf("a mirror whose session ended says so: %v", end)
			}
			mirror.awaitViewerLeft(t, 2*time.Second, "a mirror whose session ended")
		})
	}
}
