package fleet_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fakephone"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/farmstandin"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fleet"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/runnertest"
)

// The token every test here uses: long, and spelled so that any piece of it found anywhere is unmistakable.
const token = "lt-SENTINEL-0123456789abcdef-TOKEN-must-never-be-printed"

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.buf.String() }

// tripwires puts programs named like every driver tool first on PATH (and under ANDROID_HOME): any one started writes
// its name to the marker file. A fleet that never starts a program leaves it absent.
func tripwires(t *testing.T) (marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "started.txt")
	tools := filepath.Join(dir, "bin")
	platformTools := filepath.Join(dir, "sdk", "platform-tools")
	for _, d := range []string{tools, platformTools} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\necho \"$0 $*\" >> '" + marker + "'\nexit 1\n"
	for _, name := range []string{"adb", "xcrun", "xcodebuild", "idevice_id", "ideviceinfo", "idevicesyslog", "iproxy", "ios",
		"appium", "emulator", "simctl", "devicectl"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(platformTools, "adb"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANDROID_HOME", filepath.Join(dir, "sdk"))
	t.Setenv("ANDROID_SDK_ROOT", filepath.Join(dir, "sdk"))
	return marker
}

// sentinel stands in for a real Appium (or adb server, or WebDriverAgent) on this machine: a loopback listener that
// counts every connection made to it. The fleet is handed its address every way the agent is — and must never dial it.
type sentinel struct {
	listener net.Listener
	accepted atomic.Int32
}

func newSentinel(t *testing.T) *sentinel {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sentinel{listener: l}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() { l.Close() })
	return s
}

func (s *sentinel) port() int   { return s.listener.Addr().(*net.TCPAddr).Port }
func (s *sentinel) url() string { return fmt.Sprintf("http://127.0.0.1:%d", s.port()) }

type fleetRun struct {
	farm   *farmstandin.Farm
	out    *syncBuffer
	cancel context.CancelFunc
	done   chan error
}

func startFleet(t *testing.T, farm *farmstandin.Farm, connections, phones int, change func(*fleet.Config)) *fleetRun {
	t.Helper()
	options := fakephone.DefaultOptions()
	options.DelayScale = 0
	cfg := fleet.Config{
		FarmURL:     farm.URL(),
		Credential:  fleet.CredentialFromEnvironment(func(name string) string { return map[string]string{fleet.TokenVariable: token}[name] }),
		Connections: connections,
		PhonesEach:  phones,
		StateDir:    t.TempDir(),
		Phone:       options,
		TLS:         &tls.Config{RootCAs: farm.Roots()},
		Interval:    100 * time.Millisecond,
		StartGap:    10 * time.Millisecond,
	}
	if change != nil {
		change(&cfg)
	}
	out := &syncBuffer{}
	cfg.Out = out
	ctx, cancel := context.WithCancel(context.Background())
	run := &fleetRun{farm: farm, out: out, cancel: cancel, done: make(chan error, 1)}
	go func() { run.done <- fleet.Run(ctx, cfg) }()
	t.Cleanup(func() { run.stop(t) })
	if !farm.WaitFor(20*time.Second, func(nodes map[string]farmstandin.Node, tunnels int) bool {
		offered := 0
		for _, n := range nodes {
			offered += len(n.Devices)
		}
		return len(nodes) == connections && tunnels == connections && offered == connections*phones
	}) {
		t.Fatalf("the fleet never offered %d×%d phones over %d tunnels: %+v\n%s", connections, phones, connections, farm.Nodes(), out.String())
	}
	return run
}

func (r *fleetRun) stop(t *testing.T) error {
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	r.cancel = nil
	select {
	case err := <-r.done:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("the fleet did not stop within 20 s of being told to")
		return nil
	}
}

// THE END-TO-END: two stand-in machines with three fake phones each, the agent's own connect loop and tunnel, and on the
// far side of each tunnel the recorded scenario played as the runner plays it — six runs at once, each installing the
// build the agent fetched from the farm and counted. Every one passes, and stopping the fleet takes every phone off the
// farm.
func TestTheRecordedScenarioPassesThroughTheRealTunnelOnEveryFakePhone(t *testing.T) {
	farm := farmstandin.New(token, true)
	defer farm.Close()
	run := startFleet(t, farm, 2, 3, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	runs := 0
	for id, node := range farm.Nodes() {
		if node.Version != fleet.AgentVersion || !strings.HasPrefix(node.HostName, "nthcase-loadtest-") {
			t.Errorf("node %s says it is %q on %q — the fleet's nodes must say they are the load test's", id, node.Version, node.HostName)
		}
		for _, d := range node.Devices {
			if d.Manufacturer != fleet.Manufacturer || !strings.HasPrefix(d.Model, "LoadTest Phone ") || d.MirrorPort != nil {
				t.Errorf("a fake phone is offered as %q by %q (mirror %v) — it must be named so nobody takes it for a real one, and offer no live view", d.Model, d.Manufacturer, d.MirrorPort)
			}
			runs++
			wg.Add(1)
			go func(nodeID, udid string) {
				defer wg.Done()
				outcome := runnertest.Run(farm.Runner(nodeID), runnertest.Session{Udid: udid, Build: farm.URL() + farmstandin.BuildPath,
					Timeout: 5 * time.Second, Poll: 20 * time.Millisecond})
				if !outcome.Passed {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("%s: %s — %s", udid, outcome.Step, outcome.Why))
					mu.Unlock()
				}
			}(id, d.Udid)
		}
	}
	wg.Wait()
	if runs != 6 || len(failures) > 0 {
		t.Fatalf("%d run(s), failures: %v\n%s", runs, failures, run.out.String())
	}
	if fetched := farm.Fetches(); fetched != 6 {
		t.Fatalf("each run's build is fetched by the agent from the farm once: %d fetches for 6 runs", fetched)
	}
	if err := run.stop(t); err != nil {
		t.Fatalf("the fleet ended with %v", err)
	}
	for id, node := range farm.Nodes() {
		if node.Leaves != 1 || len(node.Devices) != 0 {
			t.Errorf("node %s said goodbye %d time(s) and still lists %d phone(s) — a stopped fleet takes its phones off the farm", id, node.Leaves, len(node.Devices))
		}
	}
	if !strings.Contains(run.out.String(), "fleet: stopped") || !strings.Contains(run.out.String(), "off offer there now") {
		t.Fatalf("the fleet's last words are missing:\n%s", run.out.String())
	}
}

// THE FLEET CANNOT REACH A REAL APPIUM, ADB, A SIMULATOR OR A PHONE. Every driver program is a tripwire on PATH; a
// sentinel listener stands in for a real Appium on this machine and its address is handed to the fleet every way the
// agent takes one (its environment variable, and the farm's own frames: a path that tries to leave the fake's address, a
// live view of the sentinel's port, a capture, a session for a phone it does not offer). The sentinel is never dialled,
// no program is started, and every hostile frame is refused in words.
func TestTheFleetOpensNoRealDriver(t *testing.T) {
	marker := tripwires(t)
	real := newSentinel(t)
	t.Setenv("NTH_AGENT_APPIUM", real.url())
	farm := farmstandin.New(token, true)
	defer farm.Close()
	run := startFleet(t, farm, 1, 2, nil)

	var nodeID string
	var device farmstandin.Device
	for id, n := range farm.Nodes() {
		nodeID, device = id, n.Devices[0]
	}
	if device.AppiumURL == real.url() || !strings.HasPrefix(device.AppiumURL, "http://127.0.0.1:") {
		t.Fatalf("the phones are offered at %q — the fleet's only Appium is the listener it opened for its fake phones", device.AppiumURL)
	}
	hostile := []map[string]any{
		{"method": "GET", "path": "//127.0.0.1:" + fmt.Sprint(real.port()) + "/status", "headers": map[string]string{}},
		{"method": "GET", "path": "@127.0.0.1:" + fmt.Sprint(real.port()) + "/status", "headers": map[string]string{}},
		{"kind": "stream-start", "method": "GET", "path": real.url() + "/", "headers": map[string]string{}},
		{"kind": "capture-start", "method": "POST", "path": "/session/x/capture", "headers": map[string]string{}},
		{"method": "POST", "path": "/session", "headers": map[string]string{"Content-Type": "application/json"},
			"bodyBase64": b64(`{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"R5CT64M346B"}}}`)},
		{"method": "POST", "path": "/session", "headers": map[string]string{"Content-Type": "application/json"},
			"bodyBase64": b64(`{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"00008110-0000c0ffee0000b2"}}}`)},
	}
	for _, frame := range hostile {
		answer, _ := farm.Send(nodeID, frame, 3*time.Second)
		if answer != nil && bytes.Contains(answer, []byte(`"status":200`)) {
			t.Errorf("the hostile frame %v was answered 200: %s", frame["path"], answer)
		}
	}
	outcome := runnertest.Run(farm.Runner(nodeID), runnertest.Session{Udid: device.Udid, Timeout: 5 * time.Second, Poll: 20 * time.Millisecond})
	if !outcome.Passed {
		t.Fatalf("the fake phone stopped answering after the hostile frames: %s — %s", outcome.Step, outcome.Why)
	}
	run.stop(t)
	if n := real.accepted.Load(); n != 0 {
		t.Fatalf("the fleet connected %d time(s) to the address standing in for a real Appium on this machine", n)
	}
	if started, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the fleet started a driver program:\n%s", started)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// THE TOKEN IS NEVER PRINTED. It crosses to the farm in the X-Agent-Token header and nowhere else — not in a URL, a body, a
// frame or another header — and nothing the fleet says carries it, whatever happens: a refused token included.
func TestTheTokenIsNeverPrinted(t *testing.T) {
	farm := farmstandin.New(token, true)
	defer farm.Close()
	run := startFleet(t, farm, 1, 1, nil)
	run.stop(t)

	refused := farmstandin.New("another-token-entirely", true)
	defer refused.Close()
	out := &syncBuffer{}
	options := fakephone.DefaultOptions()
	options.DelayScale = 0
	err := fleet.Run(context.Background(), fleet.Config{FarmURL: refused.URL(), Credential: fleet.CredentialFromEnvironment(func(string) string { return token }),
		Connections: 1, PhonesEach: 1, StateDir: t.TempDir(), Phone: options, TLS: &tls.Config{RootCAs: refused.Roots()}, Out: out,
		Interval: 50 * time.Millisecond})
	if err == nil || refused.Refused() == 0 {
		t.Fatalf("a refused token must end the fleet: %v", err)
	}
	credential := fleet.CredentialFromEnvironment(func(string) string { return token })
	formatted := fmt.Sprintf("%v %+v %#v %s %q %x %X %d", credential, credential, credential, credential, credential, credential, credential, credential)
	for name, text := range map[string]string{
		"the fleet's output": run.out.String(), "a refused fleet's output": out.String(), "its error": err.Error(),
		"what crossed the farm": string(farm.Crossed()), "what crossed the refusing farm": string(refused.Crossed()),
		"every fmt verb of the credential": formatted,
	} {
		for _, piece := range []string{token, token[:16], token[len(token)-16:], "SENTINEL-0123"} {
			if strings.Contains(text, piece) {
				t.Errorf("%s carries the token (%q)", name, piece)
			}
		}
	}
	if !strings.Contains(run.out.String(), fmt.Sprintf("the token is %d characters", len(token))) {
		t.Fatalf("the most the fleet says of its token is its length:\n%s", run.out.String())
	}
}

// THE TOKEN COMES FROM THE ENVIRONMENT ONLY, and a fleet without one, or with too much asked of it, is refused before
// anything is dialled.
func TestAFleetThatCannotStartIsRefusedFirst(t *testing.T) {
	farm := farmstandin.New(token, true)
	defer farm.Close()
	base := fleet.Config{FarmURL: farm.URL(), Credential: fleet.CredentialFromEnvironment(func(string) string { return "" }),
		Connections: 1, PhonesEach: 1, StateDir: t.TempDir(), Out: &syncBuffer{}}
	if err := fleet.Run(context.Background(), base); err == nil || !strings.Contains(err.Error(), fleet.TokenVariable) {
		t.Fatalf("no token: %v", err)
	}
	base.Credential = fleet.CredentialFromEnvironment(func(string) string { return token })
	for _, change := range []func(*fleet.Config){
		func(c *fleet.Config) { c.Connections = fleet.MaxConnections + 1 },
		func(c *fleet.Config) { c.PhonesEach = fleet.MaxPhonesEach + 1 },
		func(c *fleet.Config) { c.FarmURL = "http://203.0.113.7" },
		func(c *fleet.Config) { c.StateDir = "" },
	} {
		cfg := base
		change(&cfg)
		if err := fleet.Run(context.Background(), cfg); err == nil {
			t.Errorf("%+v was not refused", cfg)
		}
	}
	if len(farm.Nodes()) != 0 {
		t.Fatal("a refused fleet reached the farm")
	}
}

// A RESTART KEEPS ITS NODE IDS: the farm counts a workspace's nodes over a week, and a fleet that minted new ones at every
// start would use them up.
func TestARestartedFleetIsTheSameNodes(t *testing.T) {
	farm := farmstandin.New(token, true)
	defer farm.Close()
	state := t.TempDir()
	first := startFleet(t, farm, 2, 1, func(c *fleet.Config) { c.StateDir = state })
	first.stop(t)
	before := len(farm.Nodes())
	second := startFleet(t, farm, 2, 1, func(c *fleet.Config) { c.StateDir = state })
	second.stop(t)
	if before != 2 || len(farm.Nodes()) != 2 {
		t.Fatalf("a restart registered new nodes: %d then %d", before, len(farm.Nodes()))
	}
}

// TWO FAKE PHONES ON ONE CONNECTION, the shape a customer with several phones on one machine has: phone 2's session
// installs its build, which takes the phone three seconds, and phone 1's commands, sent all at once meanwhile, neither
// wait for it nor reach the phone out of the order they were sent in. Before one machine's phones stopped waiting for each
// other, every one of them waited out phone 2's install.
func TestASlowCreateOnOnePhoneDoesNotDelayAnotherPhoneOnTheSameConnection(t *testing.T) {
	const installTakes = 3 * time.Second
	farm := farmstandin.New(token, true)
	defer farm.Close()
	var mu sync.Mutex
	var arrived []string
	run := startFleet(t, farm, 1, 2, func(c *fleet.Config) {
		c.Phone.DelayScale = 1
		c.Phone.Delays = map[string]fakephone.Range{"install": {Min: installTakes, Max: installTakes}}
		c.Phone.Arrived = func(method, uri string) {
			mu.Lock()
			arrived = append(arrived, method+" "+uri)
			mu.Unlock()
		}
	})
	var nodeID string
	var udids []string
	for id, node := range farm.Nodes() {
		nodeID = id
		for _, d := range node.Devices {
			udids = append(udids, d.Udid)
		}
	}
	if len(udids) != 2 {
		t.Fatalf("one connection with two phones: %+v", farm.Nodes())
	}
	do := farm.Runner(nodeID)
	create := func(udid, build string) []byte {
		caps := map[string]any{"platformName": "Android", "appium:automationName": "UiAutomator2", "appium:udid": udid,
			"appium:newCommandTimeout": 120}
		if build != "" {
			caps["appium:app"] = build
		}
		raw, _ := json.Marshal(map[string]any{"capabilities": map[string]any{"alwaysMatch": caps, "firstMatch": []any{map[string]any{}}}})
		return raw
	}
	status, answer, err := do("POST", "/session", create(udids[0], ""))
	var started struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err != nil || status != 200 || json.Unmarshal(answer, &started) != nil || started.Value.SessionID == "" {
		t.Fatalf("phone 1's session must start: %d %v\n%s", status, err, run.out.String())
	}
	session := started.Value.SessionID

	installed := make(chan string, 1)
	go func() {
		began := time.Now()
		status, _, err := do("POST", "/session", create(udids[1], farm.URL()+farmstandin.BuildPath))
		installed <- fmt.Sprintf("%d %v after %s", status, err, time.Since(began).Round(100*time.Millisecond))
	}()
	twoCreates := func() bool {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, a := range arrived {
			if a == "POST /session" {
				n++
			}
		}
		return n == 2
	}
	for deadline := time.Now().Add(10 * time.Second); !twoCreates(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("phone 2's create never reached its phone\n%s", run.out.String())
		}
	}

	const commands = 20
	var frames []map[string]any
	for i := 1; i <= commands; i++ {
		frames = append(frames, map[string]any{"method": "GET", "path": fmt.Sprintf("/session/%s/source?n=%d", session, i),
			"headers": map[string]string{}, "bodyBase64": nil})
	}
	began := time.Now()
	answers, err := farm.SendInOrder(nodeID, frames, 30*time.Second)
	took := time.Since(began)
	if err != nil {
		t.Fatalf("phone 1's commands: %v", err)
	}
	for i, a := range answers {
		var frame struct {
			Status int `json:"status"`
		}
		if json.Unmarshal(a, &frame) != nil || frame.Status != 200 {
			t.Fatalf("phone 1's command %d answered %d", i+1, frame.Status)
		}
	}
	if took > installTakes/2 {
		t.Fatalf("phone 1's %d commands took %s: they waited behind phone 2's %s install on the same connection", commands, took.Round(100*time.Millisecond), installTakes)
	}
	mu.Lock()
	var order []string
	for _, a := range arrived {
		if strings.HasPrefix(a, "GET /session/"+session+"/source?n=") {
			order = append(order, strings.TrimPrefix(a, "GET /session/"+session+"/source?n="))
		}
	}
	mu.Unlock()
	for i, n := range order {
		if n != fmt.Sprint(i+1) || len(order) != commands {
			t.Fatalf("phone 1's commands reached it out of the order they were sent in: %v", order)
		}
	}
	if said := <-installed; !strings.HasPrefix(said, "200 <nil>") {
		t.Fatalf("phone 2's session must still start once its install ends: %s", said)
	}
}
