package fakephone_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fakephone"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/runnertest"
)

var phones = []fakephone.Identity{
	{Serial: "loadtest-0a1b2c3d-01", Model: "LoadTest Phone 01", Manufacturer: "nthcase-loadtest", OsVersion: fakephone.OsVersion},
	{Serial: "loadtest-0a1b2c3d-02", Model: "LoadTest Phone 02", Manufacturer: "nthcase-loadtest", OsVersion: fakephone.OsVersion},
}

func quick() fakephone.Options {
	o := fakephone.DefaultOptions()
	o.DelayScale = 0
	return o
}

func serve(t *testing.T, o fakephone.Options) (*fakephone.Server, runnertest.Doer) {
	t.Helper()
	server := fakephone.NewServer(phones, o)
	web := httptest.NewServer(server)
	t.Cleanup(web.Close)
	do := func(method, path string, body []byte) (int, []byte, error) {
		req, err := http.NewRequest(method, web.URL+path, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := web.Client().Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		answer, err := io.ReadAll(resp.Body)
		return resp.StatusCode, answer, err
	}
	return server, do
}

func TestTheRecordedScenarioPassesOnAFakePhone(t *testing.T) {
	server, do := serve(t, quick())
	outcome := runnertest.Run(do, runnertest.Session{Udid: phones[0].Serial, Timeout: time.Second, Poll: 10 * time.Millisecond})
	if !outcome.Passed {
		t.Fatalf("the recorded scenario failed on a fake phone at %q: %s", outcome.Step, outcome.Why)
	}
	stats := server.Stats()
	t.Logf("what one run asked of its phone: %s", stats.Summary())
	if len(stats.Unknown) > 0 {
		t.Fatalf("the run asked for commands the phone does not know: %v", stats.Unknown)
	}
	if stats.Sessions != 0 {
		t.Fatalf("the quit left %d session(s) open", stats.Sessions)
	}
	for _, kind := range []string{"create", "source", "shot", "find", "click", "type", "launch", "delete"} {
		if stats.Commands[kind] == 0 {
			t.Errorf("the run never asked for %q — the replay is not the run it claims to be: %v", kind, stats.Commands)
		}
	}
}

// THE SAME RUN FAILS FOR THE REASON IT FAILS ON A PHONE: the Desk Lamp is the eighth of ten products, below the fold, so
// a plan that taps it without searching first finds nothing — the fake does not pass a step whose view is not on screen.
func TestATileBelowTheFoldIsNotFound(t *testing.T) {
	_, do := serve(t, quick())
	session := start(t, do, phones[1].Serial)
	for _, step := range [][2]string{{"accessibility id", "Use demo account"}, {"accessibility id", "Sign in"}} {
		click(t, do, session, findOne(t, do, session, step[0], step[1]))
	}
	status, body := call(t, do, "POST", "/session/"+session+"/element", map[string]any{"using": "accessibility id", "value": "Desk Lamp, $42.00"})
	if status != 404 || !strings.Contains(string(body), `"no such element"`) {
		t.Fatalf("the Desk Lamp is below the fold until the grid scrolls or the search narrows it; the find answered %d %s", status, body)
	}
	// …and a scroll brings it into view, as on the phone.
	call(t, do, "POST", "/session/"+session+"/execute/sync", map[string]any{"script": "mobile: scrollGesture", "args": []any{map[string]any{"direction": "down"}}})
	findOne(t, do, session, "accessibility id", "Desk Lamp, $42.00")
}

func TestAWrongPasswordStaysOnTheSignInScreen(t *testing.T) {
	_, do := serve(t, quick())
	session := start(t, do, phones[0].Serial)
	email := findOne(t, do, session, "id", "email_input")
	call(t, do, "POST", "/session/"+session+"/element/"+email+"/value", map[string]any{"text": "demo@nthcase.com"})
	password := findOne(t, do, session, "-android uiautomator", `new UiSelector().resourceId("com.nthcase.demo:id/password_input")`)
	call(t, do, "POST", "/session/"+session+"/element/"+password+"/value", map[string]any{"text": "letmein"})
	click(t, do, session, findOne(t, do, session, "xpath", `//android.widget.Button[@text='Sign in']`))
	findOne(t, do, session, "accessibility id", "Wrong e-mail or password.")
	_, activity := call(t, do, "GET", "/session/"+session+"/appium/device/current_activity", nil)
	if !strings.Contains(string(activity), ".SignInActivity") {
		t.Fatalf("a wrong password must leave the phone on the sign-in screen: %s", activity)
	}
}

// UNKNOWN COMMANDS ANSWER AS APPIUM DOES, NEVER A SILENT SUCCESS: a route this phone does not know is "unknown command"
// (404), a `mobile:` script it does not know is "unknown method" (405), and both are counted for the fleet's last words.
func TestAnUnknownCommandIsAnsweredAsAppiumAnswersIt(t *testing.T) {
	server, do := serve(t, quick())
	session := start(t, do, phones[0].Serial)
	cases := []struct {
		method, path string
		body         any
		status       int
		code         string
	}{
		{"POST", "/session/" + session + "/appium/device/shake", map[string]any{}, 404, "unknown command"},
		{"GET", "/session/" + session + "/network_connection", nil, 404, "unknown command"},
		{"POST", "/session/" + session + "/execute/sync", map[string]any{"script": "mobile: shell", "args": []any{map[string]any{"command": "ls"}}}, 405, "unknown method"},
		{"POST", "/session/" + session + "/execute/sync", map[string]any{"script": "return 1", "args": []any{}}, 405, "unknown method"},
		{"GET", "/sessions", nil, 404, "unknown command"},
	}
	for _, c := range cases {
		status, body := call(t, do, c.method, c.path, c.body)
		if status != c.status || !strings.Contains(string(body), `"error":"`+c.code+`"`) {
			t.Errorf("%s %s answered %d %s; Appium answers %d %q", c.method, c.path, status, body, c.status, c.code)
		}
	}
	if unknown := server.Stats().Unknown; len(unknown) != len(cases) {
		t.Fatalf("every unknown command must be counted, each shape once: %v", unknown)
	}
	if !strings.Contains(server.Stats().Summary(), "UNKNOWN COMMANDS") {
		t.Fatal("the summary must name the unknown commands")
	}
}

func TestASecondSessionOnABusyPhoneIsRefused(t *testing.T) {
	_, do := serve(t, quick())
	start(t, do, phones[0].Serial)
	status, body := call(t, do, "POST", "/session", createBody(phones[0].Serial))
	if status != 500 || !strings.Contains(string(body), "session not created") {
		t.Fatalf("a phone runs one session at a time: %d %s", status, body)
	}
	status, body = call(t, do, "POST", "/session", createBody("a-phone-this-machine-does-not-have"))
	if status != 500 || !strings.Contains(string(body), "session not created") {
		t.Fatalf("a phone this server does not have is not created: %d %s", status, body)
	}
}

// A SESSION NOBODY DRIVES ENDS, as Appium ends it at newCommandTimeout — the phone is free again.
func TestASessionNobodyDrivesEndsAtItsTimeout(t *testing.T) {
	_, do := serve(t, quick())
	body := createBody(phones[1].Serial)
	body["capabilities"].(map[string]any)["alwaysMatch"].(map[string]any)["appium:newCommandTimeout"] = 0.2
	status, answer := call(t, do, "POST", "/session", body)
	if status != 200 {
		t.Fatalf("create: %d %s", status, answer)
	}
	time.Sleep(600 * time.Millisecond)
	if status, _ := call(t, do, "POST", "/session", createBody(phones[1].Serial)); status != 200 {
		t.Fatal("the phone was not freed when its idle session timed out")
	}
}

// SCREENSHOTS HAVE A REAL PHONE'S SIZE: 1080×2340, and about the bytes asked for.
func TestAScreenshotIsAPhoneSizedPicture(t *testing.T) {
	o := quick()
	o.ScreenshotBytes = 160 << 10
	_, do := serve(t, o)
	session := start(t, do, phones[0].Serial)
	_, body := call(t, do, "GET", "/session/"+session+"/screenshot", nil)
	var answer struct{ Value string }
	json.Unmarshal(body, &answer)
	raw, err := base64.StdEncoding.DecodeString(answer.Value)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the screenshot is not a PNG: %v", err)
	}
	if b := picture.Bounds(); b.Dx() != fakephone.Width || b.Dy() != fakephone.Height {
		t.Fatalf("the screenshot is %v; a phone's is %dx%d", b, fakephone.Width, fakephone.Height)
	}
	if n := len(raw); n < 120<<10 || n > 220<<10 {
		t.Fatalf("the screenshot is %d bytes; it was asked to be about %d", n, 160<<10)
	}
}

// THE PAGE SOURCE IS A REAL DUMP'S SHAPE: the hierarchy element, and every view's attributes in UiAutomator2's order.
func TestThePageSourceCarriesUiAutomator2sAttributesInItsOrder(t *testing.T) {
	_, do := serve(t, quick())
	session := start(t, do, phones[0].Serial)
	_, body := call(t, do, "GET", "/session/"+session+"/source", nil)
	var answer struct{ Value string }
	json.Unmarshal(body, &answer)
	source := answer.Value
	if !strings.HasPrefix(source, "<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>\n<hierarchy index=\"0\" class=\"hierarchy\" rotation=\"0\" width=\"1080\" height=\"2340\">") {
		t.Fatalf("the dump does not open as UiAutomator2's does: %.200s", source)
	}
	order := []string{"index", "package", "class", "text", "content-desc", "resource-id", "checkable", "checked", "clickable",
		"enabled", "focusable", "focused", "long-clickable", "password", "scrollable", "selected", "bounds", "displayed",
		"a11y-important", "screen-reader-focusable", "drawing-order", "showing-hint", "text-entry-key", "dismissable",
		"a11y-focused", "heading", "live-region", "context-clickable", "content-invalid", "window-id"}
	attribute := regexp.MustCompile(` ([a-z0-9-]+)="`)
	views := 0
	for _, line := range strings.Split(source, "\n") {
		if !strings.Contains(line, ` index="`) || strings.Contains(line, "<hierarchy") {
			continue
		}
		views++
		at := 0
		for _, m := range attribute.FindAllStringSubmatch(line, -1) {
			for at < len(order) && order[at] != m[1] {
				at++
			}
			if at == len(order) {
				t.Fatalf("attribute %q is out of UiAutomator2's order in: %s", m[1], line)
			}
		}
	}
	for _, want := range []string{`resource-id="com.nthcase.demo:id/use_demo_account"`, `content-desc="Use demo account"`,
		`resource-id="com.nthcase.demo:id/email_input"`, `text="E-mail: demo@nthcase.com&#10;Password: demo1234"`} {
		if !strings.Contains(source, want) {
			t.Errorf("the sign-in screen's dump lacks %s", want)
		}
	}
	if views < 15 {
		t.Fatalf("the sign-in screen dumps %d views; the app's layout has more", views)
	}
}

// EVERY ANSWER WAITS WHAT A REAL PHONE TAKES: each delay is drawn from its kind's range, times the scale.
func TestEveryAnswerWaitsItsKindsDelay(t *testing.T) {
	var mu sync.Mutex
	waited := map[time.Duration]bool{}
	o := fakephone.DefaultOptions()
	o.DelayScale = 0.5
	o.Sleep = func(_ context.Context, d time.Duration) {
		mu.Lock()
		waited[d] = true
		mu.Unlock()
	}
	_, do := serve(t, o)
	start(t, do, phones[0].Serial)
	create := fakephone.DefaultDelays()["create"]
	mu.Lock()
	defer mu.Unlock()
	found := false
	for d := range waited {
		if d >= create.Min/2 && d <= create.Max/2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("a session start waited none of [%s, %s] at scale 0.5: %v", create.Min/2, create.Max/2, waited)
	}
}

// A BUILD THE AGENT FETCHED IS INSTALLED FROM ITS FILE; one that is not there is refused as Appium refuses it.
func TestABuildIsInstalledFromTheAgentsOwnFile(t *testing.T) {
	_, do := serve(t, quick())
	build := filepath.Join(t.TempDir(), "0123456789abcdef.apk")
	if err := os.WriteFile(build, []byte("PK an apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome := runnertest.Run(do, runnertest.Session{Udid: phones[1].Serial, Build: build, Timeout: time.Second, Poll: 10 * time.Millisecond})
	if !outcome.Passed {
		t.Fatalf("a run that installs its build failed at %q: %s", outcome.Step, outcome.Why)
	}
	body := createBody(phones[0].Serial)
	body["capabilities"].(map[string]any)["alwaysMatch"].(map[string]any)["appium:app"] = filepath.Join(t.TempDir(), "gone.apk")
	if status, answer := call(t, do, "POST", "/session", body); status != 500 || !strings.Contains(string(answer), "does not exist") {
		t.Fatalf("a build that is not there: %d %s", status, answer)
	}
}

// ── helpers ──

func createBody(udid string) map[string]any {
	return map[string]any{"capabilities": map[string]any{"alwaysMatch": map[string]any{
		"platformName": "Android", "appium:automationName": "UiAutomator2", "appium:udid": udid,
		"appium:appPackage": "com.nthcase.demo", "appium:appActivity": "com.nthcase.demo.SignInActivity",
	}, "firstMatch": []any{map[string]any{}}}}
}

func call(t *testing.T, do runnertest.Doer, method, path string, body any) (int, []byte) {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	status, answer, err := do(method, path, payload)
	if err != nil {
		t.Fatal(err)
	}
	return status, answer
}

func start(t *testing.T, do runnertest.Doer, udid string) string {
	t.Helper()
	status, body := call(t, do, "POST", "/session", createBody(udid))
	var answer struct {
		Value struct{ SessionID string }
	}
	json.Unmarshal(body, &answer)
	if status != 200 || answer.Value.SessionID == "" {
		t.Fatalf("create: %d %s", status, body)
	}
	return answer.Value.SessionID
}

func findOne(t *testing.T, do runnertest.Doer, session, using, value string) string {
	t.Helper()
	status, body := call(t, do, "POST", "/session/"+session+"/element", map[string]any{"using": using, "value": value})
	var answer struct {
		Value map[string]string
	}
	json.Unmarshal(body, &answer)
	id := answer.Value["element-6066-11e4-a52e-4f735466cecf"]
	if status != 200 || id == "" {
		t.Fatalf("find %s %q: %d %s", using, value, status, body)
	}
	return id
}

func click(t *testing.T, do runnertest.Doer, session, element string) {
	t.Helper()
	if status, body := call(t, do, "POST", "/session/"+session+"/element/"+element+"/click", map[string]any{}); status != 200 {
		t.Fatalf("click: %d %s", status, body)
	}
}
