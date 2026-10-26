package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 2: say goodbye on the way out ─────────────────────────────────────────────
//
// On Ctrl-C, SIGTERM or SIGHUP connect gave its phones back and exited without a word to the farm, which kept them Ready
// for the 30-45 s its heartbeat window takes to lapse. It now tells the farm it is leaving (POST /v1/nodes/{id}/leave), after
// the loop has stopped — a registration sent after the goodbye would bring the node back — and after the phones are back.
// The goodbye is bounded, and a farm that does not take it costs one line and nothing else.
//
// These seals run connect itself, as a process: this test binary, started again as its own child, runs `connect` against
// a farm the test serves on a port it opened, with its own HOME, so the connect lock and the node id are the test's, and
// with both platforms skipped, so no phone, adb or Appium is asked anything.

// TestConnectAsItsOwnChild is connect, when a seal below starts this binary as its child. Run any other way it does nothing.
func TestConnectAsItsOwnChild(t *testing.T) {
	if os.Getenv("NTH_AGENT_TEST_CONNECT_CHILD") != "1" {
		t.Skip("runs only as the child the goodbye seals start")
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(run(args, os.Stdin, os.Stdout, os.Stderr))
}

// goodbyeFarm is a farm with the leave door: it records every call in order, and answers the leave as the test says.
type goodbyeFarm struct {
	mu       sync.Mutex
	calls    []string
	register int
	leave    func(w http.ResponseWriter, r *http.Request)
	beat     chan struct{}
	// holdBeats holds each heartbeat open, once it has said it arrived, until connect lets go of it: a stop then lands in
	// a heartbeat (NTH_AGENT_0_4_4 item 2).
	holdBeats atomic.Bool
}

func newGoodbyeFarm(t *testing.T, register int, leave func(w http.ResponseWriter, r *http.Request)) (*goodbyeFarm, *httptest.Server) {
	t.Helper()
	f := &goodbyeFarm{register: register, leave: leave, beat: make(chan struct{}, 64)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			w.WriteHeader(f.register)
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"","apiKey":""}}`))
		case strings.HasSuffix(r.URL.Path, "/heartbeat") && f.holdBeats.Load():
			_, _ = io.Copy(io.Discard, r.Body) // read whole, so the server notices when connect hangs up
			f.beat <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-time.After(30 * time.Second):
			}
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			_, _ = w.Write([]byte(`{"success":true,"data":{"provisionTasks":[],"commands":[],"leasedDeviceIds":[]}}`))
			f.beat <- struct{}{}
		case strings.HasSuffix(r.URL.Path, "/leave"):
			f.leave(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return f, server
}

func (f *goodbyeFarm) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// lockedBuffer is the child's output, read while the child may still write it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// connectChild starts connect as this binary's child, against farmURL, with its own HOME, writing into a buffer.
func connectChild(t *testing.T, farmURL string, extra ...string) (*exec.Cmd, *lockedBuffer) {
	t.Helper()
	out := &lockedBuffer{}
	return connectChildTo(t, farmURL, out, extra...), out
}

// connectChildTo starts connect as this binary's child, against farmURL, with its own HOME, writing into output.
func connectChildTo(t *testing.T, farmURL string, output io.Writer, extra ...string) *exec.Cmd {
	t.Helper()
	home := t.TempDir()
	args := append([]string{"-test.run=^TestConnectAsItsOwnChild$", "-test.count=1", "--", "connect", "--farm", farmURL,
		"--skip", "android,ios"}, extra...)
	cmd := exec.Command(os.Args[0], args...)
	env := []string{"NTH_AGENT_TEST_CONNECT_CHILD=1", "NTH_AGENT_TOKEN=sk-agent-goodbye-test", "HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NTH_AGENT_") && !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

func waitForHeartbeat(t *testing.T, f *goodbyeFarm, out *lockedBuffer) {
	t.Helper()
	select {
	case <-f.beat:
	case <-time.After(20 * time.Second):
		t.Fatalf("connect never sent a heartbeat:\n%s", out.String())
	}
}

// ended waits for the child and returns its exit code and how long it took from now.
func ended(t *testing.T, cmd *exec.Cmd, out *lockedBuffer) (int, time.Duration) {
	t.Helper()
	started := time.Now()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("connect did not end cleanly: %v\n%s", err, out.String())
		}
		return cmd.ProcessState.ExitCode(), time.Since(started)
	case <-time.After(30 * time.Second):
		t.Fatalf("connect did not end:\n%s", out.String())
	}
	return 0, 0
}

func TestCtrlCSaysGoodbyeOnceTheLoopHasStopped(t *testing.T) {
	for _, s := range []struct {
		signal syscall.Signal
		code   int
	}{{syscall.SIGINT, 130}, {syscall.SIGTERM, 143}, {syscall.SIGHUP, 129}} {
		f, server := newGoodbyeFarm(t, http.StatusOK, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"x","outcome":"Left"},"message":"This node has left."}`))
		})
		cmd, out := connectChild(t, server.URL)
		waitForHeartbeat(t, f, out)
		if err := cmd.Process.Signal(s.signal); err != nil {
			t.Fatal(err)
		}
		code, _ := ended(t, cmd, out)
		if code != s.code {
			t.Fatalf("%v: connect exits as it did before, %d; got %d:\n%s", s.signal, s.code, code, out.String())
		}
		calls := f.seen()
		leaves := 0
		for i, call := range calls {
			if strings.HasSuffix(call, "/leave") {
				leaves++
				if call != "POST /v1/nodes/"+nodeIDIn(t, calls)+"/leave" || i != len(calls)-1 {
					t.Fatalf("%v: one goodbye, for this node, and nothing after it: %v", s.signal, calls)
				}
			}
		}
		if leaves != 1 {
			t.Fatalf("%v: the farm must hear the goodbye once, heard %d: %v\n%s", s.signal, leaves, calls, out.String())
		}
		if !strings.Contains(out.String(), "told the farm this machine is leaving") {
			t.Fatalf("%v: connect says that the farm took the goodbye:\n%s", s.signal, out.String())
		}
	}
}

// nodeIDIn is the node id connect's heartbeats were sent for.
func nodeIDIn(t *testing.T, calls []string) string {
	t.Helper()
	for _, call := range calls {
		if m := regexp.MustCompile(`^POST /v1/nodes/([0-9a-f-]{36})/heartbeat$`).FindStringSubmatch(call); m != nil {
			return m[1]
		}
	}
	t.Fatalf("no heartbeat named a node: %v", calls)
	return ""
}

// BOUNDED, AND SAID IN ONE LINE: a farm that never answers, one older than the door (404, empty body), and one that refuses.
func TestAGoodbyeTheFarmDoesNotTakeCostsOneLineAndTheBound(t *testing.T) {
	for name, leave := range map[string]func(w http.ResponseWriter, r *http.Request){
		"no answer":             func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
		"a farm without a door": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"a refusal": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"data":null,"message":"Invalid or missing node credential."}`))
		},
	} {
		f, server := newGoodbyeFarm(t, http.StatusOK, leave)
		cmd, out := connectChild(t, server.URL)
		waitForHeartbeat(t, f, out)
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		code, took := ended(t, cmd, out)
		if code != 130 || took > leaveBound+3*time.Second {
			t.Fatalf("%s: connect exits as before (130), within the goodbye's bound (%v); got %d after %v:\n%s", name, leaveBound, code, took, out.String())
		}
		said := 0
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.Contains(line, "goodbye") {
				said++
			}
		}
		if said != 1 || !strings.Contains(out.String(), "the farm did not take this machine's goodbye") {
			t.Fatalf("%s: one line says the farm did not take the goodbye:\n%s", name, out.String())
		}
	}
	if leaveBound > 5*time.Second {
		t.Fatalf("a few seconds at most: the goodbye is bounded at %v", leaveBound)
	}
}

// NOT A STOP, NO GOODBYE: a refused token ends the loop, and the farm would refuse the goodbye for the same reason.
func TestARefusedTokenEndsWithoutAGoodbye(t *testing.T) {
	f, server := newGoodbyeFarm(t, http.StatusUnauthorized, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	cmd, out := connectChild(t, server.URL)
	code, _ := ended(t, cmd, out)
	if code != 1 {
		t.Fatalf("a refused token ends connect with 1, got %d:\n%s", code, out.String())
	}
	for _, call := range f.seen() {
		if strings.HasSuffix(call, "/leave") {
			t.Fatalf("connect said goodbye after a refused token: %v", f.seen())
		}
	}
}

// THE PHONES FIRST: the goodbye is sent once the phones under capture are handed back, never before.
type orderedLeaver struct {
	captures *fakeCaptures
	order    *[]string
	status   farm.CallStatus
	err      error
}

func (l orderedLeaver) Leave(ctx context.Context, nodeID string) (farm.CallStatus, error) {
	if l.captures.stopped {
		*l.order = append(*l.order, "after the phones were back")
	} else {
		*l.order = append(*l.order, "before the phones were back")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		*l.order = append(*l.order, "without a bound")
	}
	return l.status, l.err
}

func TestTheGoodbyeComesAfterThePhonesAreBack(t *testing.T) {
	busy := &fakeCaptures{running: 1}
	var order []string
	var out strings.Builder
	goodbye(busy, orderedLeaver{captures: busy, order: &order, status: farm.CallOK}, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", &out, leaveBound)
	if strings.Join(order, ", ") != "after the phones were back" {
		t.Fatalf("the goodbye comes once the phones are handed back, and bounded: %v\n%s", order, out.String())
	}
	if !strings.Contains(out.String(), "handing the phones this machine is capturing back first") {
		t.Fatalf("the hand-back is said as before:\n%s", out.String())
	}
}

// IN THE SOURCE: the goodbye is reached only through a stop signal that ended a loop which had registered.
func TestTheGoodbyeIsSaidOnlyForAStop(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(source), "goodbye(captureController"); n != 1 {
		t.Fatalf("runConnect says goodbye in exactly one place, found %d", n)
	}
	if !regexp.MustCompile(`if code := interrupted\(\); code != 0 \{\n(\t+//.*\n)*\t+if runErr == nil && registered\.Load\(\) \{\n\t+goodbye\(captureController, cfg\.Client, nodeID, stdout, leaveBound\)`).Match(source) {
		t.Fatal("the goodbye must sit inside the stop signal's branch, and only for a loop that registered and ended without an error of its own")
	}
}

// ── item 1, in main: connect tells the loop how it was started, read from where its own program is ──────────────────

func TestConnectReadsHowItWasStartedFromItsOwnProgram(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "Install: connect.HowStarted(ownProgram(), os.Getenv, isFile),") {
		t.Fatal("runConnect must tell the loop how this agent was started, from its own program, its environment and the files beside it")
	}
	// …AND EVERY COMMAND READS THE SAME (NTH_AGENT_0_4_3): the process reads it once, before anything is printed, from the
	// same program, environment and files, and every command the agent prints is spelled from that (internal/command).
	spelled, err := os.ReadFile("spelled.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`func init\(\) \{\n\tcommand\.Use\(command\.HowStarted\(ownProgram\(\), os\.Getenv, isFile\)\)\n\}`).Match(spelled) {
		t.Fatal("spelled.go must read how this agent was started in init, before anything is printed, so every verb's commands are spelled from it")
	}
	if got, want := command.This(), connect.HowStarted(ownProgram(), os.Getenv, isFile); got != want {
		t.Fatalf("this process reads as %+v, and connect would tell the loop %+v", got, want)
	}
	if program := ownProgram(); program == "" || !filepath.IsAbs(program) {
		t.Fatalf("this program's own path, links followed: %q", program)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "package.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isFile(file) || isFile(dir) || isFile(filepath.Join(dir, "absent")) {
		t.Fatal("isFile answers for a file, and only for one")
	}
}
