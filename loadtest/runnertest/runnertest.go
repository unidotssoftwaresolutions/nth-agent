// Package runnertest plays the runner's side of a phone run, for the load harness's tests: the recorded "Find the Desk
// Lamp" scenario on NthCase Demo, as the sequence of Appium calls a run sends for it — the session, the launch, and for
// each step the warning probe, the screen reads, the find that polls, the visibility and position reads, the pictures
// before and after, the act, the app-scope check, and the screen check — ending with the quit.
//
// It is a test helper: no program imports it (a seal in the fleet's tests holds that).
package runnertest

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// Doer sends one Appium request and answers its status and body.
type Doer func(method, path string, body []byte) (int, []byte, error)

// Session is how the run starts its session.
type Session struct {
	Udid string
	// Build is the build's address (the run installs it: the `app` capability); empty starts the installed app by its
	// package, after asking whether it is there.
	Build string
	// Timeout bounds each step's polling (10 s in a run).
	Timeout time.Duration
	Poll    time.Duration
}

// Outcome is how the run went.
type Outcome struct {
	Passed bool
	Step   string // the step that failed
	Why    string
	Calls  int
}

// The recorded plan, step by step, the way the recorder writes NthCase Demo's selectors (accessibility id first, then
// resource id): sign in with the demo account, search for "lamp", check the Desk Lamp tile, open it, check its details.
const (
	demoAccount = "Use demo account"
	signIn      = "Sign in"
	search      = "com.nthcase.demo:id/search_input"
	lampTile    = "Desk Lamp, $42.00"
)

type runner struct {
	do      Doer
	session string
	calls   int
	timeout time.Duration
	poll    time.Duration
}

type failure struct{ step, why string }

func (f failure) Error() string { return f.step + ": " + f.why }

// Run plays the recorded scenario through do.
func Run(do Doer, s Session) (out Outcome) {
	r := &runner{do: do, timeout: s.Timeout, poll: s.Poll}
	if r.timeout <= 0 {
		r.timeout = 10 * time.Second
	}
	if r.poll <= 0 {
		r.poll = 250 * time.Millisecond
	}
	defer func() {
		out.Calls = r.calls
		if p := recover(); p != nil {
			f, isFailure := p.(failure)
			if !isFailure {
				panic(p)
			}
			out.Passed, out.Step, out.Why = false, f.step, f.why
		}
		if r.session != "" {
			r.calls++
			r.do("DELETE", "/session/"+r.session, nil) // the quit: a run sends it whatever happened, and reads nothing from it
		}
	}()

	r.start(s)
	r.launch()
	r.tap("1 tap Use demo account", "accessibility id", demoAccount, ".SignInActivity")
	r.tap("2 tap Sign in", "accessibility id", signIn, ".ProductsActivity")
	r.typeText("3 type lamp into Search products", "id", search, "lamp")
	r.assertExists("4 check the Desk Lamp tile", "accessibility id", lampTile)
	r.tap("5 tap the Desk Lamp", "accessibility id", lampTile, ".ProductDetailActivity")
	r.assertTextOnScreen("6 check the details show Desk Lamp", "Desk Lamp")
	return Outcome{Passed: true}
}

func (r *runner) call(step, method, path string, body any) (int, map[string]any) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	r.calls++
	status, answer, err := r.do(method, path, payload)
	if err != nil {
		panic(failure{step, fmt.Sprintf("%s %s: %v", method, path, err)})
	}
	var decoded map[string]any
	_ = json.Unmarshal(answer, &decoded)
	return status, decoded
}

func (r *runner) in(path string) string { return "/session/" + r.session + path }

// must is a call that has to succeed; a W3C error ends the step with the phone's own words.
func (r *runner) must(step, method, path string, body any) any {
	status, decoded := r.call(step, method, path, body)
	if status/100 != 2 {
		said := w3cWords(decoded)
		panic(failure{step, fmt.Sprintf("%s %s answered %d: %s", method, path, status, said)})
	}
	return decoded["value"]
}

// w3cWords is the error and message of a W3C error answer — what a failed step says, and nothing else of the answer.
func w3cWords(answer map[string]any) string {
	value, _ := answer["value"].(map[string]any)
	code, _ := value["error"].(string)
	message, _ := value["message"].(string)
	return code + ": " + message
}

func (r *runner) start(s Session) {
	caps := map[string]any{
		"platformName": "Android", "appium:automationName": "UiAutomator2", "appium:udid": s.Udid, "appium:deviceName": s.Udid,
		"appium:appWaitActivity": "*", "appium:clearDeviceLogsOnStart": true, "appium:newCommandTimeout": 120,
		"appium:autoGrantPermissions": true,
	}
	if s.Build != "" {
		caps["appium:app"] = s.Build
		caps["appium:noReset"] = true
	} else {
		caps["appium:appPackage"] = "com.nthcase.demo"
		caps["appium:appActivity"] = "com.nthcase.demo.SignInActivity"
		caps["appium:noReset"] = true
	}
	value := r.must("session", "POST", "/session", map[string]any{"capabilities": map[string]any{"alwaysMatch": caps, "firstMatch": []any{map[string]any{}}}})
	v, _ := value.(map[string]any)
	r.session, _ = v["sessionId"].(string)
	if r.session == "" {
		panic(failure{"session", "the answer named no session"})
	}
	if s.Build == "" {
		installed := r.must("session", "POST", r.in("/execute/sync"), script("mobile: isAppInstalled", map[string]any{"appId": "com.nthcase.demo"}))
		if installed != true {
			panic(failure{"session", "the app is not installed"})
		}
	}
	r.settle("session")
}

// settle reads the page source until it holds something, as a run does before its first step.
func (r *runner) settle(step string) {
	deadline := time.Now().Add(4 * time.Second)
	for {
		source, _ := r.must(step, "GET", r.in("/source"), nil).(string)
		if len(source) >= 200 || time.Now().After(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func script(name string, args map[string]any) map[string]any {
	return map[string]any{"script": name, "args": []any{args}}
}

func (r *runner) launch() {
	r.must("0 launch", "POST", r.in("/execute/sync"), script("mobile: terminateApp", map[string]any{"appId": "com.nthcase.demo"}))
	r.must("0 launch", "POST", r.in("/execute/sync"), script("mobile: activateApp", map[string]any{"appId": "com.nthcase.demo"}))
	r.settle("0 launch")
	r.must("0 launch", "GET", r.in("/window/rect"), nil)
}

func (r *runner) probeWarnings(step string) {
	r.must(step, "POST", r.in("/elements"), map[string]any{"using": "-android uiautomator",
		"value": `new UiSelector().packageName("android").resourceId("android:id/message")`})
}

// find polls until the selector matches, as a step does within its timeout.
func (r *runner) find(step, using, value string) string {
	deadline := time.Now().Add(r.timeout)
	for {
		found, _ := r.must(step, "POST", r.in("/elements"), map[string]any{"using": using, "value": value}).([]any)
		if len(found) > 0 {
			el, _ := found[0].(map[string]any)
			id, _ := el["element-6066-11e4-a52e-4f735466cecf"].(string)
			return id
		}
		if time.Now().After(deadline) {
			panic(failure{step, fmt.Sprintf("no element matches %s %q", using, value)})
		}
		time.Sleep(r.poll)
	}
}

func (r *runner) evidence(step string) {
	r.must(step, "GET", r.in("/screenshot"), nil)
	r.must(step, "GET", r.in("/screenshot"), nil)
}

// screen polls the current activity until it is the expected one, as the screen check does.
func (r *runner) screen(step, activity string) {
	deadline := time.Now().Add(r.timeout)
	for {
		r.must(step, "GET", r.in("/source"), nil)
		now, _ := r.must(step, "GET", r.in("/appium/device/current_activity"), nil).(string)
		if now == activity {
			return
		}
		if time.Now().After(deadline) {
			panic(failure{step, fmt.Sprintf("expected %s and the phone shows %s", activity, now)})
		}
		time.Sleep(r.poll)
	}
}

func (r *runner) tap(step, using, value, expect string) {
	r.probeWarnings(step)
	r.must(step, "GET", r.in("/source"), nil)
	id := r.find(step, using, value)
	if shown, _ := r.must(step, "GET", r.in("/element/"+id+"/displayed"), nil).(bool); !shown {
		panic(failure{step, "the element is not shown"})
	}
	r.must(step, "GET", r.in("/element/"+id+"/rect"), nil)
	r.must(step, "GET", r.in("/screenshot"), nil)
	r.must(step, "POST", r.in("/element/"+id+"/click"), map[string]any{})
	if pkg, _ := r.must(step, "POST", r.in("/execute/sync"), script("mobile: getCurrentPackage", map[string]any{})).(string); pkg != "com.nthcase.demo" {
		panic(failure{step, "the tap left the app: " + pkg})
	}
	r.screen(step, expect)
	r.evidence(step)
}

func (r *runner) typeText(step, using, value, text string) {
	r.probeWarnings(step)
	r.must(step, "GET", r.in("/source"), nil)
	id := r.find(step, using, value)
	r.must(step, "POST", r.in("/element/"+id+"/click"), map[string]any{})
	r.must(step, "POST", r.in("/element/"+id+"/clear"), map[string]any{})
	r.must(step, "POST", r.in("/element/"+id+"/value"), map[string]any{"text": text, "value": strings.Split(text, "")})
	if got, _ := r.must(step, "GET", r.in("/element/"+id+"/text"), nil).(string); got != text {
		panic(failure{step, fmt.Sprintf("the field reads %q after typing %q", got, text)})
	}
	r.screen(step, ".ProductsActivity")
	r.evidence(step)
}

func (r *runner) assertExists(step, using, value string) {
	r.probeWarnings(step)
	r.find(step, using, value)
	r.evidence(step)
}

// assertTextOnScreen is a check with no selector: the source, read for a view on screen whose text or description holds
// the words.
func (r *runner) assertTextOnScreen(step, words string) {
	deadline := time.Now().Add(r.timeout)
	for {
		source, _ := r.must(step, "GET", r.in("/source"), nil).(string)
		r.must(step, "GET", r.in("/window/rect"), nil)
		if onScreen(source, words) {
			r.evidence(step)
			return
		}
		if time.Now().After(deadline) {
			panic(failure{step, fmt.Sprintf("no view on screen says %q", words)})
		}
		time.Sleep(r.poll)
	}
}

func onScreen(source, words string) bool {
	decoder := xml.NewDecoder(strings.NewReader(source))
	for {
		item, err := decoder.RawToken()
		if err != nil {
			return false
		}
		start, isStart := item.(xml.StartElement)
		if !isStart {
			continue
		}
		var text, desc, bounds string
		for _, a := range start.Attr {
			switch a.Name.Local {
			case "text":
				text = a.Value
			case "content-desc":
				desc = a.Value
			case "bounds":
				bounds = a.Value
			}
		}
		var l, t, r, b int
		if _, err := fmt.Sscanf(bounds, "[%d,%d][%d,%d]", &l, &t, &r, &b); err != nil || r <= l || b <= t {
			continue
		}
		if strings.Contains(text, words) || strings.Contains(desc, words) {
			return true
		}
	}
}
