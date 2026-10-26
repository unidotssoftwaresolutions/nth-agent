package tunnel_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// WHAT THE FARM MAY ASK OF THIS MACHINE (AN_AGENT_READY_TO_BE_READ). A session-create for a phone on this machine reaches
// the customer's Appium only when every capability in it is on the agent's allow-list (internal/allowed), its phone is
// one this agent offers right now, and its build is an https address on the farm's own host. Inside a session, a route or
// a `mobile:` command that runs a program, or reads or writes files, on this computer rather than on the phone is refused.
// Each refusal is a sentence on the agent's own output and an error to the farm, and nothing reaches Appium.

const (
	// The phones this agent offers in these seals: an iPhone and an Android phone, both invented (same shape as real ones).
	offeredIPhone  = "00008140-0000C0FFEE0000A1"
	offeredAndroid = "R5CT64M346B"
	// A phone on this machine that the agent does NOT offer: left out with --skip, or never offered.
	skippedIPhone = "00008110-0000C0FFEE0000B2"
	// A content key the farm's build cache hands out: a SHA-256 in hex and the build's extension.
	buildKey = "4f9c0a8b7e6d5c4b3a29181706f5e4d3c2b1a09f8e7d6c5b4a3928170605f4e3"
)

// askingTunnel starts an agent whose Appium is a recorder answering every create with session "local-session-1", and
// which offers exactly the given phones. It returns the farm's side, the recorder, the agent's output and the farm's host
// (the one an app address must be on).
func askingTunnel(t *testing.T, phones ...string) (*liveFarm, *farmReader, func(map[string]any), *recorder, *syncWriter, string) {
	t.Helper()
	appium := &recorder{reply: `{"value":{"sessionId":"local-session-1","capabilities":{}}}`}
	server := httptest.NewServer(appium.handler())
	t.Cleanup(server.Close)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
		c.AppiumURL = server.URL
		offeringPhones(c, phones...)
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)
	host := mustHost(t, farm.server.URL)
	return farm, reader, func(f map[string]any) { send(t, conn, f) }, appium, &out, host
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// farmBuild is the address the farm's build cache gives a phone's Appium: https, on the farm's own host, with the grant in
// the query.
func farmBuild(host, ext string) string {
	return "https://" + host + "/app-cache/" + buildKey + "." + ext + "?grant=1761480000.AbCdEf0123456789"
}

func answerOf(t *testing.T, reader *farmReader, id string) map[string]any {
	t.Helper()
	return reader.await(t, 5*time.Second, "the answer to "+id, func(f map[string]any) bool { return f["id"] == id })
}

func refused(t *testing.T, what string, response map[string]any, appium *recorder, out *syncWriter, says ...string) {
	t.Helper()
	if response["status"] == float64(http.StatusOK) {
		t.Fatalf("%s: must be refused, got %v %s", what, response["status"], decodeBody(t, response))
	}
	got := decodeBody(t, response)
	for _, s := range says {
		if !strings.Contains(got, s) {
			t.Fatalf("%s: the farm's error must say %q:\n%s", what, s, got)
		}
		if !strings.Contains(out.String(), s) {
			t.Fatalf("%s: the customer's terminal must say %q:\n%s", what, s, out.String())
		}
	}
	if seen := appium.seen(); len(seen) != 0 {
		t.Fatalf("%s: reached the customer's Appium: %+v", what, seen)
	}
}

// ── EVERY CREATE THE FARM'S RUNNER SENDS TODAY STILL PASSES, BYTE FOR BYTE ──────────────────────────────────────────────
//
// Written from the runner's capability builders (AndroidCapabilities.Build, IosCapabilities.Build, the locale-restore
// session) for a phone on a customer's machine: the shape Selenium sends, one firstMatch entry holding every capability.
// What a customer's machine is never sent (a cloud device's facts, a WebDriverAgent build path, which no agent declares)
// is not in them; that is in the refused seals below.
func runnerCreates(host string) map[string]string {
	return map[string]string{
		"android, a warm session (live view)": `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",` +
			`"appium:udid":"` + offeredAndroid + `","appium:platformVersion":"16","appium:deviceName":"` + offeredAndroid + `",` +
			`"appium:language":"en","appium:locale":"US","appium:autoLaunch":false,"appium:appWaitActivity":"*",` +
			`"appium:clearDeviceLogsOnStart":true,"appium:noReset":true,"appium:newCommandTimeout":600,"appium:mjpegServerPort":36903}]}}`,
		"android, a headless run that installs its build": `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:app":"` + farmBuild(host, "apk") + `",` +
			`"appium:automationName":"UiAutomator2","appium:deviceName":"` + offeredAndroid + `","appium:platformVersion":"16","appium:udid":"` + offeredAndroid + `",` +
			`"appium:appWaitActivity":"*","appium:clearDeviceLogsOnStart":true,"appium:fullReset":true,"appium:autoGrantPermissions":true,"appium:newCommandTimeout":120}]}}`,
		"android, a preinstalled app with a webview": `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",` +
			`"appium:udid":"` + offeredAndroid + `","appium:deviceName":"` + offeredAndroid + `","appium:orientation":"LANDSCAPE",` +
			`"appium:appPackage":"com.example.lantern","appium:appActivity":".MainActivity","appium:appWaitActivity":"*",` +
			`"appium:clearDeviceLogsOnStart":true,"appium:noReset":true,"appium:autoGrantPermissions":false,"appium:newCommandTimeout":120,` +
			`"appium:chromedriverAutodownload":true}]}}`,
		"android, the locale-restore session": `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",` +
			`"appium:udid":"` + offeredAndroid + `","appium:language":"en","appium:locale":"US","appium:noReset":true,"appium:autoLaunch":false,"appium:newCommandTimeout":60}]}}`,
		"ios, a warm session on the customer's own phone": `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest",` +
			`"appium:platformVersion":"26.1","appium:udid":"` + offeredIPhone + `","appium:autoLaunch":false,"appium:noReset":true,"appium:autoAcceptAlerts":true,` +
			`"appium:newCommandTimeout":600,"appium:waitForIdleTimeout":0,"appium:waitForQuiescence":false,"appium:useNewWDA":true,"appium:mjpegServerPort":36903,` +
			`"appium:xcodeOrgId":"TEAM7QZ4X2","appium:xcodeSigningId":"Apple Development","appium:updatedWDABundleId":"com.example.WebDriverAgentRunner",` +
			`"appium:wdaLaunchTimeout":120000,"appium:wdaLocalPort":26903,"appium:allowProvisioningDeviceRegistration":false}]}}`,
		"ios, a headless run that installs its build": `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:app":"` + farmBuild(host, "ipa") + `",` +
			`"appium:automationName":"XCUITest","appium:platformVersion":"26.1","appium:deviceName":"Ada's iPhone","appium:udid":"` + offeredIPhone + `",` +
			`"appium:orientation":"PORTRAIT","appium:language":"sv","appium:locale":"sv_SE","appium:bundleId":"com.example.lantern","appium:fullReset":true,` +
			`"appium:autoAcceptAlerts":false,"appium:newCommandTimeout":120,"appium:waitForIdleTimeout":2000,"appium:waitForQuiescence":true,` +
			`"appium:useNewWDA":true,"appium:xcodeOrgId":"TEAM7QZ4X2","appium:xcodeSigningId":"Apple Development",` +
			`"appium:updatedWDABundleId":"com.example.WebDriverAgentRunner","appium:wdaLaunchTimeout":120000,"appium:wdaLocalPort":26903,` +
			`"appium:additionalWebviewBundleIds":["*"]}]}}`,
		"ios, a simulator-style default language": `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest",` +
			`"appium:udid":"` + offeredIPhone + `","appium:language":"en","appium:locale":"en_US","appium:skipSyncUiDialogTranslation":true,` +
			`"appium:noReset":true,"appium:autoAcceptAlerts":true,"appium:newCommandTimeout":120,"appium:waitForIdleTimeout":0,"appium:waitForQuiescence":false}]}}`,
	}
}

func TestEveryCreateTheFarmsRunnerSendsTodayStillPasses(t *testing.T) {
	_, reader, send, appium, out, host := askingTunnel(t, offeredIPhone, offeredAndroid)
	i := 0
	for name, body := range runnerCreates(host) {
		i++
		id := "create-" + string(rune('a'+i))
		before := len(appium.seen())
		send(sessionFrame(id, body))
		response := answerOf(t, reader, id)
		if response["status"] != float64(http.StatusOK) {
			t.Fatalf("%s: a create the runner sends today must pass, got %v: %s\n%s", name, response["status"], decodeBody(t, response), out.String())
		}
		seen := appium.seen()
		if len(seen) != before+1 || seen[len(seen)-1].Body != body {
			t.Fatalf("%s: Appium must receive the create byte-identical: %+v", name, seen)
		}
	}
}

// ── A CAPABILITY OUTSIDE THE ALLOW-LIST IS REFUSED ──────────────────────────────────────────────────────────────────────

func TestACapabilityOutsideTheAllowListIsRefused(t *testing.T) {
	for name, extra := range map[string]string{
		"a capability nobody listed":          `"appium:somethingNew":true`,
		"one without the appium: prefix":      `"somethingNew":"x"`,
		"an emulator to start (avd)":          `"appium:avd":"Pixel_9"`,
		"a port of this machine's":            `"appium:systemPort":8201`,
		"a cloud lab's facts, sent locally":   `"qanguru:run":{"runId":"r"}`,
		"another automation":                  `"appium:automationName":"Espresso"`,
		"a listed name, in another case":      `"appium:NoReset":true`,
		"a listed name with the wrong kind":   `"appium:noReset":"rm -rf"`,
		"a browser to start":                  `"browserName":"Chrome"`,
		"an app beside the build (otherApps)": `"appium:otherApps":"https://FARM/app-cache/x.apk"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, reader, send, appium, out, host := askingTunnel(t, offeredAndroid)
			body := `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"` + offeredAndroid + `",` +
				strings.ReplaceAll(extra, "FARM", host) + `}]}}`
			send(sessionFrame("f1", body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "was refused, and nothing was sent to Appium")
		})
	}
}

// ── EACH NAMED DANGEROUS CAPABILITY IS REFUSED, WHEREVER APPIUM WOULD READ IT ───────────────────────────────────────────
//
// A program, a path or a URL on this machine, chosen by the farm. The allow-list refuses them by itself; this seal names
// them so that adding one to the list is a test going red, not a quiet edit.
var neverAllowed = []string{
	"chromedriverExecutable", "chromedriverExecutableDir", "webDriverAgentUrl", "derivedDataPath",
	"prebuiltWDAPath", "keychainPath", "keychainPassword",
}

func TestEveryProgramPathOrUrlOnThisMachineIsRefused(t *testing.T) {
	for _, name := range neverAllowed {
		for place, body := range map[string]string{
			"alwaysMatch":    `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:udid":"` + offeredIPhone + `","appium:` + name + `":"/tmp/x"}}}`,
			"firstMatch":     `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"` + offeredIPhone + `","appium:` + name + `":"/tmp/x"}]}}`,
			"appium:options": `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:options":{"udid":"` + offeredIPhone + `","` + name + `":"/tmp/x"}}}}`,
			"no prefix":      `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:udid":"` + offeredIPhone + `","` + name + `":"/tmp/x"}}}`,
			"legacy desired": `{"desiredCapabilities":{"platformName":"iOS","udid":"` + offeredIPhone + `","` + name + `":"/tmp/x"}}`,
		} {
			t.Run(name+" in "+place, func(t *testing.T) {
				_, reader, send, appium, out, _ := askingTunnel(t, offeredIPhone)
				send(sessionFrame("f1", body))
				refused(t, name, answerOf(t, reader, "f1"), appium, out, name)
			})
		}
	}
}

// ── A PHONE THIS AGENT DOES NOT OFFER IS REFUSED ────────────────────────────────────────────────────────────────────────

func TestAPhoneThisAgentDoesNotOfferIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"a phone left out with --skip": `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"` + skippedIPhone + `"}]}}`,
		"a phone on Wi-Fi":             `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"192.168.1.20:5555"}]}}`,
		"an emulator":                  `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"emulator-5554"}]}}`,
		"no phone named at all":        `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2"}]}}`,
		"an offered phone in one set, none in the other": `{"capabilities":{"alwaysMatch":{"platformName":"Android"},` +
			`"firstMatch":[{"appium:udid":"` + offeredAndroid + `"},{"appium:automationName":"UiAutomator2"}]}}`,
		"an offered phone, and another one beside it": `{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:udid":"` + offeredAndroid + `",` +
			`"appium:options":{"udid":"emulator-5554"}}}}`,
		"the phone as a number": `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:udid":12345}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, reader, send, appium, out, _ := askingTunnel(t, offeredIPhone, offeredAndroid)
			send(sessionFrame("f1", body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "phone", "was refused, and nothing was sent to Appium")
		})
	}
}

// ── A BUILD THAT IS NOT THE FARM'S IS REFUSED ───────────────────────────────────────────────────────────────────────────

func TestABuildThatIsNotAnHttpsAddressOnTheFarmsHostIsRefused(t *testing.T) {
	for name, app := range map[string]string{
		"a local path":                       "/Users/ada/Downloads/app.apk",
		"a file URL":                         "file:///Users/ada/Downloads/app.apk",
		"a relative path":                    "app.apk",
		"another host":                       "https://builds.example.com/app-cache/" + buildKey + ".apk",
		"plain http on the farm":             "http://FARM/app-cache/" + buildKey + ".apk",
		"the farm with a user":               "https://someone@FARM/app-cache/" + buildKey + ".apk",
		"the farm on another port":           "https://FARMHOST:1/app-cache/" + buildKey + ".apk",
		"a host that starts like the farm's": "https://FARM.example.com/app-cache/" + buildKey + ".apk",
		"an empty address":                   "",
	} {
		t.Run(name, func(t *testing.T) {
			_, reader, send, appium, out, host := askingTunnel(t, offeredAndroid)
			hostOnly := strings.Split(host, ":")[0]
			app = strings.ReplaceAll(strings.ReplaceAll(app, "FARMHOST", hostOnly), "FARM", host)
			for _, key := range []string{"appium:app", "app"} {
				body := `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"` + offeredAndroid + `","` + key + `":"` + app + `"}]}}`
				send(sessionFrame("f-"+key, body))
				refused(t, name+" as "+key, answerOf(t, reader, "f-"+key), appium, out, "farm")
			}
		})
	}
}

// ── INSIDE A SESSION: WHAT RUNS ON THIS COMPUTER IS REFUSED, WHAT ACTS ON THE PHONE PASSES ──────────────────────────────

func inSession(id, method, path, body string) map[string]any {
	f := map[string]any{"id": id, "method": method, "path": path, "headers": map[string]string{"Content-Type": "application/json"}}
	if body != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(body))
		f["bodyBase64"] = &encoded
	}
	return f
}

// startedSession creates session local-session-1 on the agent's Appium and forgets what that create looked like, so every
// later request the recorder sees is the one under test.
func startedSession(t *testing.T) (*farmReader, func(map[string]any), *recorder, *syncWriter, string) {
	t.Helper()
	_, reader, send, appium, out, host := askingTunnel(t, offeredAndroid)
	send(sessionFrame("create", `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"`+offeredAndroid+`"}]}}`))
	if r := answerOf(t, reader, "create"); r["status"] != float64(http.StatusOK) {
		t.Fatalf("the session under test must start: %v %s", r["status"], decodeBody(t, r))
	}
	appium.mu.Lock()
	appium.requests = nil
	appium.mu.Unlock()
	return reader, send, appium, out, host
}

func TestWhatRunsOnThisComputerInsideASessionIsRefused(t *testing.T) {
	const s = "/session/local-session-1"
	for name, c := range map[string]struct{ method, path, body string }{
		"execute_driver":                    {"POST", s + "/appium/execute_driver", `{"script":"return 1","type":"webdriverio"}`},
		"mobile: shell":                     {"POST", s + "/execute/sync", `{"script":"mobile: shell","args":[{"command":"id"}]}`},
		"mobile: shell, spelled loosely":    {"POST", s + "/execute/sync", `{"script":"  MOBILE:shell ","args":[{"command":"id"}]}`},
		"mobile: shell, the older route":    {"POST", s + "/execute", `{"script":"mobile: shell","args":[{"command":"id"}]}`},
		"mobile: shell, async":              {"POST", s + "/execute/async", `{"script":"mobile: shell","args":[]}`},
		"an emulator console command":       {"POST", s + "/execute/sync", `{"script":"mobile: execEmuConsoleCommand","args":[{"command":"kill"}]}`},
		"screen streaming (gstreamer here)": {"POST", s + "/execute/sync", `{"script":"mobile: startScreenStreaming","args":[]}`},
		"a camera image into the emulator":  {"POST", s + "/execute/sync", `{"script":"mobile: injectEmulatorCameraImage","args":[{"payload":"x"}]}`},
		"a recording uploaded from here":    {"POST", s + "/execute/sync", `{"script":"mobile: stopMediaProjectionRecording","args":[{"remotePath":"https://x.example/"}]}`},
		"xctrace on this computer":          {"POST", s + "/execute/sync", `{"script":"mobile: startPerfRecord","args":[]}`},
		"ffmpeg on this computer (audio)":   {"POST", s + "/execute/sync", `{"script":"mobile: startAudioRecording","args":[]}`},
		"ffmpeg on this computer (screen)":  {"POST", s + "/execute/sync", `{"script":"mobile: startScreenRecording","args":[]}`},
		"an XCTest run from this computer":  {"POST", s + "/execute/sync", `{"script":"mobile: runXCTest","args":[]}`},
		"an XCTest bundle from here":        {"POST", s + "/execute/sync", `{"script":"mobile: installXCTestBundle","args":[]}`},
		"simctl":                            {"POST", s + "/execute/sync", `{"script":"mobile: simctl","args":[{"command":"spawn"}]}`},
		"a certificate served from here":    {"POST", s + "/execute/sync", `{"script":"mobile: installCertificate","args":[{"content":"x"}]}`},
		"the screen recording route":        {"POST", s + "/appium/start_recording_screen", `{"options":{}}`},
		"the recording's upload":            {"POST", s + "/appium/stop_recording_screen", `{"options":{"remotePath":"https://x.example/"}}`},
		"Appium's own log":                  {"POST", s + "/se/log", `{"type":"server"}`},
		"Appium's own log, the older route": {"POST", s + "/log", `{"type":"server"}`},
		"a local file installed":            {"POST", s + "/appium/device/install_app", `{"appPath":"/Users/ada/app.apk"}`},
		"a local file, by mobile: command":  {"POST", s + "/execute/sync", `{"script":"mobile: installApp","args":[{"app":"/Users/ada/app.ipa"}]}`},
		"several local files":               {"POST", s + "/execute/sync", `{"script":"mobile: installMultipleApks","args":[{"apks":["/Users/ada/a.apk"]}]}`},
		"a body that is not JSON":           {"POST", s + "/execute/sync", `mobile: shell`},
	} {
		t.Run(name, func(t *testing.T) {
			reader, send, appium, out, _ := startedSession(t)
			send(inSession("f1", c.method, c.path, c.body))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "this computer")
		})
	}
}

// What the runner does inside a session — taps, gestures, sensors, page source, screenshots, the phone's own log, and
// installing the build the farm sent — still passes, byte for byte.
func TestWhatActsOnThePhoneInsideASessionStillPasses(t *testing.T) {
	const s = "/session/local-session-1"
	reader, send, appium, out, host := startedSession(t)
	for i, c := range []struct{ method, path, body string }{
		{"GET", s + "/source", ""},
		{"GET", s + "/screenshot", ""},
		{"POST", s + "/execute/sync", `{"script":"mobile: clickGesture","args":[{"x":10,"y":20}]}`},
		{"POST", s + "/execute/sync", `{"script":"mobile: sensorSet","args":[{"sensorType":"acceleration","value":"0:9.8:0"}]}`},
		{"POST", s + "/execute/sync", `{"script":"mobile: activeAppInfo","args":[]}`},
		{"POST", s + "/execute/sync", `{"script":"return window.devicePixelRatio","args":[]}`},
		{"POST", s + "/execute/sync", `{"script":"mobile: installApp","args":[{"app":"` + farmBuild(host, "ipa") + `"}]}`},
		{"POST", s + "/appium/device/install_app", `{"appPath":"` + farmBuild(host, "apk") + `","options":{"grantPermissions":true}}`},
		{"POST", s + "/appium/device/remove_app", `{"appId":"com.example.lantern"}`},
		{"POST", s + "/se/log", `{"type":"logcat"}`},
		{"POST", s + "/appium/settings", `{"settings":{"waitForIdleTimeout":0}}`},
		{"DELETE", s, ""},
	} {
		id := "p" + string(rune('a'+i))
		before := len(appium.seen())
		send(inSession(id, c.method, c.path, c.body))
		response := answerOf(t, reader, id)
		if response["status"] != float64(http.StatusOK) {
			t.Fatalf("%s %s must pass, got %v: %s\n%s", c.method, c.path, response["status"], decodeBody(t, response), out.String())
		}
		seen := appium.seen()
		if len(seen) != before+1 || seen[len(seen)-1].Body != c.body || seen[len(seen)-1].Path != c.path {
			t.Fatalf("%s %s must reach Appium byte for byte: %+v", c.method, c.path, seen)
		}
	}
}
