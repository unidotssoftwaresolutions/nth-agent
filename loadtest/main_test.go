package main

import (
	"bytes"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/farmstandin"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fleet"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

// THE FLEET STOPS CLEANLY ON SIGTERM, as the command runs it: the signal ends every connection, each one tells the farm
// it is leaving, and the command exits 0.
func TestTheFleetStopsCleanlyOnSigterm(t *testing.T) {
	const secret = "lt-sigterm-0123456789abcdef-never-printed"
	farm := farmstandin.New(secret, false)
	defer farm.Close()
	env := map[string]string{fleet.FarmVariable: farm.URL(), fleet.TokenVariable: secret}
	stdout, stderr := &lockedBuffer{}, &lockedBuffer{}
	code := make(chan int, 1)
	go func() {
		code <- run([]string{"fleet", "--connections", "2", "--phones", "2", "--state", t.TempDir(), "--delay-scale", "0"},
			stdout, stderr, func(name string) string { return env[name] })
	}()
	// The signal handler is in place before the first connection registers, so a signal after that is the fleet's.
	if !farm.WaitFor(30*time.Second, func(nodes map[string]farmstandin.Node, tunnels int) bool {
		phones := 0
		for _, n := range nodes {
			phones += len(n.Devices)
		}
		return len(nodes) == 2 && tunnels == 2 && phones == 4
	}) {
		t.Fatalf("the fleet never came up:\n%s\n%s", stdout.String(), stderr.String())
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("the fleet exited %d after SIGTERM:\n%s\n%s", c, stdout.String(), stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the fleet did not stop within 20 s of SIGTERM")
	}
	for id, n := range farm.Nodes() {
		if n.Leaves != 1 || len(n.Devices) != 0 {
			t.Errorf("node %s: %d goodbye(s), %d phone(s) left on the farm", id, n.Leaves, len(n.Devices))
		}
	}
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Fatal("the command printed the token")
	}
}

func TestTheDriverRefusesWithoutTheProductionFlag(t *testing.T) {
	stdout, stderr := &lockedBuffer{}, &lockedBuffer{}
	env := map[string]string{"NTH_AGENT_LOADTEST_API": "https://api.example.com",
		"NTH_AGENT_LOADTEST_API_KEYS": "00000000-0000-0000-0000-000000000001=gp_never_printed_key"}
	code := run([]string{"drive", "--scenario", "00000000-0000-4000-8000-000000000042", "--no-pause"}, stdout, stderr,
		func(name string) string { return env[name] })
	if code != 2 || !strings.Contains(stderr.String(), "--i-know-this-is-production") {
		t.Fatalf("drive without the flag exited %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "gp_never_printed_key") {
		t.Fatal("the driver printed the key")
	}
}
