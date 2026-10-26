//go:build darwin || linux

package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
)

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 4, as a person sees it: connect folds to the terminal, and only to a terminal ──
//
// connect with --capture-allow-local-network says capture.LocalNetworkOpen at its start, a line of over two hundred
// columns. Run in a 60-column terminal, every line a person sees fits it, folded at a space; run into a pipe, the line is
// whole.

func TestConnectFoldsToTheTerminalItRunsIn(t *testing.T) {
	terminal, screen := openTerminal(t)
	if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 60}); err != nil {
		t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
	var seen bytes.Buffer
	var reading sync.WaitGroup
	reading.Add(1)
	go func() { defer reading.Done(); _, _ = io.Copy(&seen, screen) }()

	f, server := newGoodbyeFarm(t, http.StatusOK, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	// THE STOP LANDS IN A HEARTBEAT, every run (NTH_AGENT_0_4_4 item 2): the farm holds the first one open until connect
	// lets go of it. Up to 0.4.3 a stop timed so said "cannot reach the farm (Post "<a URL of 80 columns>": context
	// canceled)", which no terminal of 60 columns can fold, and this seal failed whenever a stop happened to land there.
	f.holdBeats.Store(true)
	cmd := connectChildTo(t, server.URL, terminal, "--capture-allow-local-network")
	waitForHeartbeat(t, f, &lockedBuffer{})
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 130 {
		t.Fatalf("connect in a terminal ends on Ctrl-C as anywhere else: %v", err)
	}
	terminal.Close()
	reading.Wait()

	text := strings.ReplaceAll(seen.String(), "\r\n", "\n")
	if strings.Join(strings.Fields(text), " ") == "" || !strings.Contains(strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(capture.LocalNetworkOpen), " ")) {
		t.Fatalf("connect said what it says at its start:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if n := len([]rune(line)); n > 60 {
			t.Errorf("a %d-column line in a 60-column terminal: %q", n, line)
		}
	}
}

func TestConnectWritesEveryLineWholeIntoAPipe(t *testing.T) {
	f, server := newGoodbyeFarm(t, http.StatusOK, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	cmd, out := connectChild(t, server.URL, "--capture-allow-local-network")
	waitForHeartbeat(t, f, out)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	ended(t, cmd, out)
	for _, line := range strings.Split(out.String(), "\n") {
		if line == capture.LocalNetworkOpen {
			return
		}
	}
	t.Fatalf("into a pipe, every line stays whole:\n%s", out.String())
}
