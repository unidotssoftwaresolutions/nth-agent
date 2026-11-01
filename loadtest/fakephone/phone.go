// Package fakephone is a phone that is not there: an HTTP server that answers the Appium (UiAutomator2) protocol the
// way an Android phone running the NthCase Demo app does, for a load test of the path a phone run travels. It drives no
// device, starts no program and opens no connection: it is a state machine over the app's screens (app.go), and every
// answer waits a delay drawn from what a real phone takes (Options).
//
// A command this phone does not know is answered as Appium answers one — "unknown command" (404) or, for an execute
// script, "unknown method" (405) — and counted, so a run that asks for something new fails in words instead of passing
// on a phone that only pretended.
package fakephone

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// OsVersion is the Android release the fake phones report.
const OsVersion = "14"

// Identity is one fake phone as its agent offers it.
type Identity struct {
	Serial, Model, Manufacturer, OsVersion string
}

// Range is how long one kind of command takes on a real phone: each answer waits a duration drawn evenly from it.
type Range struct{ Min, Max time.Duration }

// Options is how the fake phones behave.
type Options struct {
	// Delays is the wait for each kind of command (the kinds are the keys of DefaultDelays).
	Delays map[string]Range
	// DelayScale multiplies every delay: 1 is a real phone, 0 answers at once (tests).
	DelayScale float64
	// ScreenshotBytes is about how large one screenshot's PNG is.
	ScreenshotBytes int
	// Sleep is the wait seam; nil waits on a timer and gives up when the request goes away.
	Sleep func(ctx context.Context, d time.Duration)
	// Arrived is a TEST SEAM: told of each request — its method and its path with the query — as it arrives, before it
	// waits for anything, so a test can see the order requests reached this machine's Appium in. nil = nobody is told.
	Arrived func(method, uri string)
}

// DefaultDelays is what a real Android phone takes, per kind of command. Two are measured on real phones through the
// farm's relay — a session start's handshake (12.3 s, with no install in it) and a page source (1.1 s, a discovery's 108
// reads in two minutes); the rest are UiAutomator2's usual answer times on a mid-range phone, and the harness's report
// says which is which. A session that installs its build waits "install" as well, as a phone does.
func DefaultDelays() map[string]Range {
	ms := func(lo, hi int) Range {
		return Range{time.Duration(lo) * time.Millisecond, time.Duration(hi) * time.Millisecond}
	}
	return map[string]Range{
		"status":    ms(0, 10),
		"create":    ms(10000, 14000),
		"delete":    ms(300, 900),
		"source":    ms(400, 1100),
		"shot":      ms(250, 600),
		"find":      ms(120, 450),
		"click":     ms(150, 400),
		"type":      ms(400, 900),
		"clear":     ms(100, 300),
		"read":      ms(30, 120),
		"launch":    ms(900, 2000),
		"terminate": ms(300, 800),
		"install":   ms(3000, 8000),
		"remove":    ms(800, 2000),
		"query":     ms(100, 300),
		"gesture":   ms(600, 1200),
		"back":      ms(200, 500),
		"log":       ms(50, 250),
	}
}

// DefaultScreenshotBytes: the demo app's screens, drawn by its own tests at 720×1560, are 69 KB at the median; the same
// flat drawings at 1080×2340 are about 160 KB.
const DefaultScreenshotBytes = 160 << 10

// DefaultOptions is a real phone's timing.
func DefaultOptions() Options {
	return Options{Delays: DefaultDelays(), DelayScale: 1, ScreenshotBytes: DefaultScreenshotBytes}
}

// Server is one machine's Appium with its fake phones behind it.
type Server struct {
	options  Options
	phones   []*phone
	bySerial map[string]*phone

	mu       sync.Mutex
	sessions map[string]*session
	rng      *mathrand.Rand
	shots    map[string][]byte // base64 of each screen's picture, drawn once
	counts   map[string]int
	bytes    map[string]int64
	unknown  map[string]int
}

type phone struct {
	identity Identity
	app      *app
	session  *session
}

type session struct {
	id       string
	phone    *phone
	caps     map[string]any
	mu       sync.Mutex // one command at a time, as Appium runs them
	elements map[string]elementRef
	nextEl   int
	idle     time.Duration
	timer    *time.Timer
	ended    bool
}

// elementRef is how an element id finds its view again in a later dump: the path of child indexes from the root, and the
// view's class and key, which must still be there or the element is stale.
type elementRef struct {
	path  []int
	class string
	key   string
}

// NewServer makes the phones.
func NewServer(phones []Identity, options Options) *Server {
	if options.Delays == nil {
		options.Delays = DefaultDelays()
	}
	if options.ScreenshotBytes <= 0 {
		options.ScreenshotBytes = DefaultScreenshotBytes
	}
	s := &Server{options: options, bySerial: map[string]*phone{}, sessions: map[string]*session{},
		rng: mathrand.New(mathrand.NewSource(time.Now().UnixNano())), shots: map[string][]byte{},
		counts: map[string]int{}, bytes: map[string]int64{}, unknown: map[string]int{}}
	for _, id := range phones {
		p := &phone{identity: id, app: newApp()}
		s.phones = append(s.phones, p)
		s.bySerial[id.Serial] = p
	}
	return s
}

// Phones is the phones this server answers for.
func (s *Server) Phones() []Identity {
	var out []Identity
	for _, p := range s.phones {
		out = append(out, p.identity)
	}
	return out
}

// Alive answers the agent's "is Appium up" question without a request: this server is up while it exists.
func (s *Server) Alive() error { return nil }

// Stats is what this server was asked: commands by kind, the bytes each kind answered, and every unknown command.
type Stats struct {
	Commands map[string]int
	Bytes    map[string]int64
	Unknown  map[string]int
	Sessions int
}

func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyInts := func(m map[string]int) map[string]int {
		out := map[string]int{}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	b := map[string]int64{}
	for k, v := range s.bytes {
		b[k] = v
	}
	return Stats{Commands: copyInts(s.counts), Bytes: b, Unknown: copyInts(s.unknown), Sessions: len(s.sessions)}
}

// Summary is one line per kind of command, for the fleet's last words.
func (st Stats) Summary() string {
	var kinds []string
	for k := range st.Commands {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var parts []string
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d (%d KB)", k, st.Commands[k], st.Bytes[k]>>10))
	}
	line := strings.Join(parts, ", ")
	if len(st.Unknown) > 0 {
		var unknown []string
		for k, v := range st.Unknown {
			unknown = append(unknown, fmt.Sprintf("%s ×%d", k, v))
		}
		sort.Strings(unknown)
		line += "; UNKNOWN COMMANDS: " + strings.Join(unknown, ", ")
	}
	return line
}

func (s *Server) delay(ctx context.Context, kind string) {
	r, ok := s.options.Delays[kind]
	if !ok || s.options.DelayScale <= 0 {
		return
	}
	s.mu.Lock()
	d := r.Min
	if r.Max > r.Min {
		d += time.Duration(s.rng.Int63n(int64(r.Max - r.Min)))
	}
	s.mu.Unlock()
	d = time.Duration(float64(d) * s.options.DelayScale)
	if s.options.Sleep != nil {
		s.options.Sleep(ctx, d)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// ── the protocol ───────────────────────────────────────────────────────────────────────────────────────────────────

type answer struct {
	status int
	value  any
	kind   string
	detail string // for an unknown execute script, the script, so each one is counted by name
}

func ok(kind string, value any) answer {
	return answer{status: http.StatusOK, value: value, kind: kind}
}

// w3cError is an error in the W3C shape Appium answers with.
func w3cError(status int, code, message string) answer {
	return answer{status: status, value: map[string]any{"error": code, "message": message, "stacktrace": ""}, kind: "error"}
}

func noSuchElement() answer {
	return w3cError(404, "no such element", "An element could not be located on the page using the given search parameters.")
}

func staleElement(id string) answer {
	return w3cError(404, "stale element reference", fmt.Sprintf("The element '%s' does not exist in DOM anymore", id))
}

func unknownCommand() answer {
	return w3cError(404, "unknown command", "The requested resource could not be found, or a request was received using an HTTP "+
		"method that is not supported by the mapped resource")
}

func invalidArgument(message string) answer { return w3cError(400, "invalid argument", message) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.options.Arrived != nil {
		s.options.Arrived(r.Method, r.URL.RequestURI())
	}
	path := strings.TrimPrefix(r.URL.Path, "/wd/hub")
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	a := s.route(r.Context(), r.Method, path, body)
	payload, err := json.Marshal(map[string]any{"value": a.value})
	if err != nil {
		payload = []byte(`{"value":null}`)
	}
	s.mu.Lock()
	s.counts[a.kind]++
	s.bytes[a.kind] += int64(len(payload))
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(a.status)
	w.Write(payload)
}

func (s *Server) unknownSeen(method, path string) {
	shape := method + " " + sessionShape(path)
	s.mu.Lock()
	s.unknown[shape]++
	s.mu.Unlock()
}

// sessionShape writes a path with its ids as placeholders, so one unknown command is counted once however many sessions
// asked it.
func sessionShape(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		if i > 0 && (parts[i-1] == "session" || parts[i-1] == "element") && parts[i] != "" {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func (s *Server) route(ctx context.Context, method, path string, body []byte) answer {
	switch {
	case method == http.MethodGet && path == "/status":
		s.delay(ctx, "status")
		return ok("status", map[string]any{"ready": true, "message": "The server is ready to accept new connections",
			"build": map[string]any{"version": "2.19.0"}})
	case method == http.MethodPost && path == "/session":
		return s.create(ctx, body)
	}
	rest, found := strings.CutPrefix(path, "/session/")
	if !found {
		s.unknownSeen(method, path)
		return unknownCommand()
	}
	id, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess == nil {
		return w3cError(404, "invalid session id", "A session is either terminated or not started")
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.ended {
		return w3cError(404, "invalid session id", "A session is either terminated or not started")
	}
	sess.touch(s)
	if sub == "" {
		switch method {
		case http.MethodDelete:
			s.delay(ctx, "delete")
			s.end(sess)
			return ok("delete", nil)
		case http.MethodGet:
			s.delay(ctx, "read")
			return ok("read", sess.caps)
		}
	}
	a := s.command(ctx, sess, method, sub, body)
	if a.kind == "unknown" {
		s.unknownSeen(method, strings.TrimSpace(path+" "+a.detail))
	}
	return a
}

// create starts a session on the phone the capabilities name — the one phone that is free and offered here — and
// installs and launches the app the way UiAutomator2 does for these capabilities.
func (s *Server) create(ctx context.Context, body []byte) answer {
	var request struct {
		Capabilities struct {
			AlwaysMatch map[string]any   `json:"alwaysMatch"`
			FirstMatch  []map[string]any `json:"firstMatch"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return invalidArgument("The capabilities are not a JSON object: " + err.Error())
	}
	caps := map[string]any{}
	for k, v := range request.Capabilities.AlwaysMatch {
		caps[strings.TrimPrefix(k, "appium:")] = v
	}
	if len(request.Capabilities.FirstMatch) > 0 {
		for k, v := range request.Capabilities.FirstMatch[0] {
			caps[strings.TrimPrefix(k, "appium:")] = v
		}
	}
	notCreated := func(message string) answer {
		return w3cError(500, "session not created", "A new session could not be created. Details: "+message)
	}
	if name, _ := caps["platformName"].(string); !strings.EqualFold(name, "android") {
		return notCreated("this server drives Android phones only")
	}
	if automation, _ := caps["automationName"].(string); !strings.EqualFold(automation, "UiAutomator2") {
		return notCreated(fmt.Sprintf("Could not find a driver for automationName '%v' and platformName 'android'", caps["automationName"]))
	}
	serial, _ := caps["udid"].(string)
	sess := &session{id: newSessionID(), caps: caps, elements: map[string]elementRef{}, idle: 60 * time.Second}
	s.mu.Lock()
	p := s.bySerial[serial]
	busy := p != nil && p.session != nil
	if p != nil && !busy {
		p.session = sess // held while it starts, so a second start for the same phone is refused
	}
	s.mu.Unlock()
	if p == nil {
		return notCreated(fmt.Sprintf("Device %s was not in the list of connected devices", serial))
	}
	if busy {
		return notCreated(fmt.Sprintf("Device %s is busy with another session", serial))
	}
	sess.phone = p
	started := false
	defer func() {
		if !started {
			s.mu.Lock()
			if p.session == sess {
				p.session = nil
			}
			s.mu.Unlock()
		}
	}()
	s.delay(ctx, "create")

	a := p.app
	if build, _ := caps["app"].(string); build != "" {
		// THE BUILD IS THE AGENT'S OWN FILE by now (it fetched and counted it); a phone installs what is there.
		info, err := os.Stat(build)
		if err != nil || info.Size() == 0 || !strings.HasSuffix(strings.ToLower(build), ".apk") {
			return notCreated(fmt.Sprintf("The application at '%s' does not exist or is not accessible", build))
		}
		if full, _ := caps["fullReset"].(bool); full {
			a.installed = false
		}
		s.delay(ctx, "install")
		a.installed = true
		a.terminate()
	} else if pkg, _ := caps["appPackage"].(string); pkg != "" && !(pkg == Package && a.installed) {
		return notCreated(fmt.Sprintf("Activity used to start app doesn't exist or cannot be launched! Make sure it exists. "+
			"'%s' is not installed", pkg))
	}
	launch := true
	if auto, isBool := caps["autoLaunch"].(bool); isBool && !auto {
		launch = false
	}
	if launch && (caps["app"] != nil || caps["appPackage"] != nil) {
		a.terminate()
		a.launch()
	}

	if timeout, isNumber := caps["newCommandTimeout"].(float64); isNumber && timeout > 0 {
		sess.idle = time.Duration(timeout * float64(time.Second))
	}
	described := map[string]any{}
	for k, v := range caps {
		described[k] = v
	}
	described["deviceName"] = serial
	described["deviceUDID"] = serial
	described["platformVersion"] = p.identity.OsVersion
	described["deviceScreenSize"] = fmt.Sprintf("%dx%d", Width, Height)
	described["deviceModel"] = p.identity.Model
	described["deviceManufacturer"] = p.identity.Manufacturer
	sess.caps = described
	s.mu.Lock()
	s.sessions[sess.id] = sess
	s.mu.Unlock()
	started = true
	sess.mu.Lock()
	sess.touch(s)
	sess.mu.Unlock()
	return ok("create", map[string]any{"sessionId": sess.id, "capabilities": described})
}

// touch restarts the session's idle clock: Appium ends a session nobody has driven for newCommandTimeout. The caller holds
// sess.mu.
func (sess *session) touch(s *Server) {
	if sess.timer != nil {
		sess.timer.Stop()
	}
	sess.timer = time.AfterFunc(sess.idle, func() {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if !sess.ended {
			s.end(sess)
		}
	})
}

// end deletes the session and frees its phone. The caller holds sess.mu.
func (s *Server) end(sess *session) {
	sess.ended = true
	if sess.timer != nil {
		sess.timer.Stop()
	}
	s.mu.Lock()
	delete(s.sessions, sess.id)
	if sess.phone.session == sess {
		sess.phone.session = nil
	}
	s.mu.Unlock()
}

func newSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

const elementKey = "element-6066-11e4-a52e-4f735466cecf"

func (sess *session) remember(root, n *node) map[string]any {
	path := pathTo(root, n)
	sess.nextEl++
	id := fmt.Sprintf("00000000-0000-0000-%04x-%012x", len(path), sess.nextEl)
	sess.elements[id] = elementRef{path: path, class: n.class, key: n.key}
	return map[string]any{elementKey: id, "ELEMENT": id}
}

func pathTo(root, target *node) []int {
	var path []int
	var rec func(n *node) bool
	rec = func(n *node) bool {
		if n == target {
			return true
		}
		for i, c := range n.children {
			path = append(path, i)
			if rec(c) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	rec(root)
	return append([]int(nil), path...)
}

// element finds an element id's view in the current screen, or nil when the screen no longer holds it.
func (sess *session) element(root *node, id string) *node {
	ref, known := sess.elements[id]
	if !known {
		return nil
	}
	n := root
	for _, i := range ref.path {
		if i >= len(n.children) {
			return nil
		}
		n = n.children[i]
	}
	if n.class != ref.class || n.key != ref.key {
		return nil
	}
	return n
}

func (s *Server) screenshot(a *app) string {
	name := a.screen
	if !a.foreground() {
		name = screenLauncher
	}
	s.mu.Lock()
	shot, drawn := s.shots[name]
	s.mu.Unlock()
	if drawn {
		return string(shot)
	}
	seed := int64(len(name)*131 + int(name[0]))
	encoded := base64.StdEncoding.EncodeToString(screenshotPNG(Width, Height, s.options.ScreenshotBytes, seed))
	s.mu.Lock()
	s.shots[name] = []byte(encoded)
	s.mu.Unlock()
	return encoded
}

// command answers one in-session command. The caller holds sess.mu.
func (s *Server) command(ctx context.Context, sess *session, method, sub string, body []byte) answer {
	a := sess.phone.app
	var args map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &args)
	}
	get, post := method == http.MethodGet, method == http.MethodPost

	switch {
	case get && sub == "source":
		s.delay(ctx, "source")
		return ok("source", a.render().pageSource())
	case get && sub == "screenshot":
		s.delay(ctx, "shot")
		return ok("shot", s.screenshot(a))
	case get && (sub == "window/rect" || sub == "window/current/size"):
		s.delay(ctx, "read")
		return ok("read", map[string]any{"x": 0, "y": 0, "width": Width, "height": Height})
	case get && sub == "appium/device/current_activity":
		s.delay(ctx, "read")
		return ok("read", a.activity())
	case get && sub == "appium/device/current_package":
		s.delay(ctx, "read")
		return ok("read", a.packageName())
	case get && sub == "orientation":
		s.delay(ctx, "read")
		return ok("read", "PORTRAIT")
	case get && sub == "contexts":
		return ok("read", []string{"NATIVE_APP"})
	case get && sub == "context":
		return ok("read", "NATIVE_APP")
	case get && sub == "timeouts":
		return ok("read", map[string]any{"implicit": 0, "pageLoad": 300000, "script": 30000})
	case post && sub == "timeouts":
		return ok("read", nil)
	case get && sub == "appium/settings":
		return ok("read", map[string]any{"waitForIdleTimeout": 10000, "ignoreUnimportantViews": false})
	case post && sub == "appium/settings":
		return ok("read", nil)
	case get && sub == "appium/device/is_keyboard_shown":
		return ok("read", false)
	case post && sub == "appium/device/hide_keyboard":
		return ok("read", nil)
	case post && sub == "back":
		s.delay(ctx, "back")
		a.back()
		return ok("back", nil)
	case get && sub == "se/log/types", get && sub == "log/types":
		return ok("log", []string{"logcat", "bugreport", "server"})
	case post && (sub == "se/log" || sub == "log"):
		s.delay(ctx, "log")
		return ok("log", []any{})
	case post && (sub == "element" || sub == "elements"):
		return s.findCommand(ctx, sess, a.render(), nil, sub == "elements", args)
	case get && sub == "element/active":
		root := a.render()
		var focused *node
		root.walk(func(n *node, _, _, _, _ int) {
			if n.focused && focused == nil {
				focused = n
			}
		})
		if focused == nil {
			return noSuchElement()
		}
		return ok("find", sess.remember(root, focused))
	case post && sub == "actions":
		s.delay(ctx, "gesture")
		return s.actions(sess, args)
	case method == http.MethodDelete && sub == "actions":
		return ok("read", nil)
	case post && (sub == "execute/sync" || sub == "execute"):
		return s.execute(ctx, sess, args)
	case post && strings.HasPrefix(sub, "appium/device/"):
		return s.deviceCommand(ctx, sess, strings.TrimPrefix(sub, "appium/device/"), args)
	case strings.HasPrefix(sub, "element/"):
		return s.elementCommand(ctx, sess, method, strings.TrimPrefix(sub, "element/"), args)
	}
	return answer{status: 404, value: unknownCommand().value, kind: "unknown"}
}

func (s *Server) findCommand(ctx context.Context, sess *session, root, within *node, many bool, args map[string]any) answer {
	using, _ := args["using"].(string)
	value, _ := args["value"].(string)
	if using == "" {
		return invalidArgument("'using' must be a non-empty string")
	}
	s.delay(ctx, "find")
	search := root
	if within != nil {
		search = within
	}
	found, err := find(search, using, value)
	if err != nil {
		var invalid errInvalidSelector
		if errors.As(err, &invalid) {
			return w3cError(400, "invalid selector", invalid.message)
		}
		return w3cError(500, "unknown error", err.Error())
	}
	if within != nil {
		found = without(found, within)
	}
	if selector, perr := parseUiSelector(value); using == "-android uiautomator" && perr == nil && selector.scroll && len(found) == 0 {
		// A UiScrollable scrolls the list until its target is there, as far as the list goes.
		for i := 0; i < 6 && len(found) == 0; i++ {
			if !sess.phone.app.scrollBy(900) {
				break
			}
			root = sess.phone.app.render()
			found, _ = find(root, using, value)
		}
	}
	if many {
		out := []any{}
		for _, n := range found {
			out = append(out, sess.remember(root, n))
		}
		return ok("find", out)
	}
	if len(found) == 0 {
		return noSuchElement()
	}
	return ok("find", sess.remember(root, found[0]))
}

func without(found []*node, self *node) []*node {
	var out []*node
	for _, n := range found {
		if n != self {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) elementCommand(ctx context.Context, sess *session, method, rest string, args map[string]any) answer {
	id, what, _ := strings.Cut(rest, "/")
	a := sess.phone.app
	root := a.render()
	n := sess.element(root, id)
	get, post := method == http.MethodGet, method == http.MethodPost
	if n == nil {
		if _, known := sess.elements[id]; known || what != "" {
			return staleElement(id)
		}
		return unknownCommand()
	}
	l, t, r, b := root.bounds(n)
	switch {
	case post && what == "click":
		s.delay(ctx, "click")
		if key := root.clickTarget(n); key != "" {
			a.tap(key)
		}
		return ok("click", nil)
	case post && what == "value":
		s.delay(ctx, "type")
		text, _ := args["text"].(string)
		if text == "" {
			if keys, isList := args["value"].([]any); isList {
				for _, k := range keys {
					if piece, isString := k.(string); isString {
						text += piece
					}
				}
			}
		}
		if !a.typeInto(n.key, text, false) {
			return w3cError(400, "invalid element state", "Cannot set the element to '"+text+"'. Did you interact with the correct element?")
		}
		return ok("type", nil)
	case post && what == "clear":
		s.delay(ctx, "clear")
		a.clear(n.key)
		return ok("clear", nil)
	case get && what == "text":
		s.delay(ctx, "read")
		return ok("read", n.text)
	case get && what == "displayed":
		s.delay(ctx, "read")
		return ok("read", r > l && b > t)
	case get && what == "enabled":
		s.delay(ctx, "read")
		return ok("read", true)
	case get && what == "selected":
		s.delay(ctx, "read")
		return ok("read", false)
	case get && what == "name":
		s.delay(ctx, "read")
		return ok("read", n.class)
	case get && what == "rect":
		s.delay(ctx, "read")
		return ok("read", map[string]any{"x": l, "y": t, "width": r - l, "height": b - t})
	case get && (what == "location" || what == "location_in_view"):
		s.delay(ctx, "read")
		return ok("read", map[string]any{"x": l, "y": t})
	case get && what == "size":
		s.delay(ctx, "read")
		return ok("read", map[string]any{"width": r - l, "height": b - t})
	case get && strings.HasPrefix(what, "attribute/"):
		s.delay(ctx, "read")
		value, known := root.attribute(n, strings.TrimPrefix(what, "attribute/"))
		if !known {
			return ok("read", nil)
		}
		return ok("read", value)
	case get && what == "screenshot":
		s.delay(ctx, "shot")
		return ok("shot", s.screenshot(a))
	case post && (what == "element" || what == "elements"):
		return s.findCommand(ctx, sess, root, n, what == "elements", args)
	}
	return answer{status: 404, value: unknownCommand().value, kind: "unknown"}
}

// actions reads a W3C pointer sequence as a tap where the pointer went down and came up in the same place, and as a
// swipe of the product list when it moved.
func (s *Server) actions(sess *session, args map[string]any) answer {
	sources, _ := args["actions"].([]any)
	a := sess.phone.app
	for _, source := range sources {
		src, _ := source.(map[string]any)
		if src["type"] != "pointer" {
			continue
		}
		steps, _ := src["actions"].([]any)
		var x, y, downX, downY float64
		down := false
		for _, step := range steps {
			st, _ := step.(map[string]any)
			switch st["type"] {
			case "pointerMove":
				x, _ = st["x"].(float64)
				y, _ = st["y"].(float64)
			case "pointerDown":
				down, downX, downY = true, x, y
			case "pointerUp":
				if !down {
					continue
				}
				down = false
				if abs(x-downX) < 20 && abs(y-downY) < 20 {
					root := a.render()
					if hit := root.at(int(x), int(y)); hit != nil {
						if key := root.clickTarget(hit); key != "" {
							a.tap(key)
						}
					}
				} else {
					a.scrollBy(int(downY - y))
				}
			}
		}
	}
	return ok("gesture", nil)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// execute answers the `mobile:` scripts a run uses. Any other script is "unknown method", as UiAutomator2 answers one it
// does not support — never a silent success.
func (s *Server) execute(ctx context.Context, sess *session, args map[string]any) answer {
	script, _ := args["script"].(string)
	var params map[string]any
	if list, isList := args["args"].([]any); isList && len(list) > 0 {
		params, _ = list[0].(map[string]any)
	}
	if params == nil {
		params = map[string]any{}
	}
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(script), "mobile:"))
	if !strings.HasPrefix(strings.TrimSpace(script), "mobile:") {
		return answer{405, w3cError(405, "unknown method", "Method has not yet been implemented").value, "unknown", "(a script)"}
	}
	switch name {
	case "activateApp", "terminateApp", "isAppInstalled", "installApp", "removeApp", "queryAppState", "getCurrentPackage",
		"getCurrentActivity", "startActivity":
		return s.appCommand(ctx, sess, name, params)
	case "scrollGesture", "swipeGesture", "flingGesture":
		s.delay(ctx, "gesture")
		direction, _ := params["direction"].(string)
		dy := 900
		if strings.EqualFold(direction, "up") {
			dy = -900
		}
		if name == "swipeGesture" {
			dy = -dy // a swipe up moves the content down the list
		}
		return ok("gesture", sess.phone.app.scrollBy(dy))
	case "clickGesture", "longClickGesture", "doubleClickGesture":
		s.delay(ctx, "click")
		root := sess.phone.app.render()
		var target *node
		if id, _ := params["elementId"].(string); id != "" {
			target = sess.element(root, id)
			if target == nil {
				return staleElement(id)
			}
		} else {
			x, _ := params["x"].(float64)
			y, _ := params["y"].(float64)
			target = root.at(int(x), int(y))
		}
		if target != nil && name != "longClickGesture" {
			if key := root.clickTarget(target); key != "" {
				sess.phone.app.tap(key)
			}
		}
		return ok("click", nil)
	case "hideKeyboard":
		return ok("read", true)
	case "isKeyboardShown":
		return ok("read", false)
	case "isLocked":
		// What the runner asks before its first step; these phones are never locked, and an unlocked Android says false.
		return ok("read", false)
	case "setGeolocation", "resetGeolocation":
		s.delay(ctx, "query")
		return ok("query", nil)
	case "getDeviceTime":
		return ok("read", time.Now().Format(time.RFC3339))
	case "deviceInfo":
		return ok("read", map[string]any{"model": sess.phone.identity.Model, "manufacturer": sess.phone.identity.Manufacturer,
			"platformVersion": sess.phone.identity.OsVersion, "realDisplaySize": fmt.Sprintf("%dx%d", Width, Height)})
	}
	return answer{405, w3cError(405, "unknown method", fmt.Sprintf("Unsupported execute method '%s'. Available methods are: "+
		"activateApp, terminateApp, isAppInstalled, installApp, removeApp, queryAppState, getCurrentPackage, scrollGesture, "+
		"swipeGesture, clickGesture", strings.TrimSpace(script))).value, "unknown", strings.TrimSpace(script)}
}

// deviceCommand is the older spelling of the same app commands (/appium/device/…).
func (s *Server) deviceCommand(ctx context.Context, sess *session, what string, params map[string]any) answer {
	names := map[string]string{"activate_app": "activateApp", "terminate_app": "terminateApp", "app_installed": "isAppInstalled",
		"install_app": "installApp", "remove_app": "removeApp", "app_state": "queryAppState"}
	if name, known := names[what]; known {
		if params == nil {
			params = map[string]any{}
		}
		if v, has := params["bundleId"]; has && params["appId"] == nil {
			params["appId"] = v
		}
		return s.appCommand(ctx, sess, name, params)
	}
	return answer{status: 404, value: unknownCommand().value, kind: "unknown"}
}

func (s *Server) appCommand(ctx context.Context, sess *session, name string, params map[string]any) answer {
	a := sess.phone.app
	appID, _ := params["appId"].(string)
	if appID == "" {
		appID, _ = params["bundleId"].(string)
	}
	ours := appID == Package
	switch name {
	case "getCurrentPackage":
		s.delay(ctx, "read")
		return ok("read", a.packageName())
	case "getCurrentActivity":
		s.delay(ctx, "read")
		return ok("read", a.activity())
	case "isAppInstalled":
		s.delay(ctx, "query")
		return ok("query", ours && a.installed)
	case "queryAppState":
		s.delay(ctx, "query")
		switch {
		case !ours || !a.installed:
			return ok("query", 0)
		case a.foreground():
			return ok("query", 4)
		case a.running:
			return ok("query", 3)
		}
		return ok("query", 1)
	case "activateApp", "startActivity":
		s.delay(ctx, "launch")
		if name == "startActivity" {
			if component, _ := params["component"].(string); strings.HasPrefix(component, Package+"/") {
				ours = true
			}
		}
		if !ours || !a.installed {
			return w3cError(500, "unknown error", fmt.Sprintf("The app '%s' is not installed on the device", appID))
		}
		a.activate()
		return ok("launch", nil)
	case "terminateApp":
		s.delay(ctx, "terminate")
		if ours && a.running {
			a.terminate()
			return ok("terminate", true)
		}
		return ok("terminate", false)
	case "installApp":
		s.delay(ctx, "install")
		path, _ := params["appPath"].(string)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return w3cError(500, "unknown error", fmt.Sprintf("The application at '%s' does not exist or is not accessible", path))
		}
		a.installed = true
		return ok("install", nil)
	case "removeApp":
		s.delay(ctx, "remove")
		if ours && a.installed {
			a.installed = false
			a.terminate()
			return ok("remove", true)
		}
		return ok("remove", false)
	}
	return answer{status: 404, value: unknownCommand().value, kind: "unknown"}
}
