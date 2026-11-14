package main

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
)

// A CLEAN EXIT ON EVERY PATH (the audit's rejected capture/unclean-exit-leaves-phone-proxied, fixed as robustness). A phone
// under capture points at a proxy that dies with the agent; it is handed back however connect ends — a signal, the terminal
// closing (SIGHUP), or the loop ending on its own (a refused token).

func TestClosingTheTerminalEndsConnectLikeCtrlC(t *testing.T) {
	found := false
	for _, s := range exitSignals {
		if s == syscall.SIGHUP {
			found = true
		}
	}
	if !found {
		t.Fatalf("SIGHUP (the terminal closing) must end connect through the same hand-back as Ctrl-C: %v", exitSignals)
	}
	w := watch(t)
	w.signals <- syscall.SIGHUP
	within(t, w.cancelled, "SIGHUP cancelling the agent's context")
	if got := w.code.Load(); got != 129 {
		t.Fatalf("SIGHUP stands for exit 129 (128+1), got %d", got)
	}
	close(w.window)
	within(t, w.done, "the watcher ending")
}

type fakeCaptures struct {
	running int
	stopped bool
}

func (f *fakeCaptures) Running() int                { return f.running }
func (f *fakeCaptures) StopAll(ctx context.Context) { f.stopped = ctx.Err() == nil }

// EVERY RETURN OF connect AFTER THE CONTROLLER EXISTS HANDS BACK: held in the source, because runConnect needs a farm.
func TestConnectHandsThePhonesBackOnEveryWayOut(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "captureController := capture.NewController(")
	end := strings.Index(text[start:], "\n}\n") + start
	body := text[start:end]
	if !strings.Contains(body, "defer handBackOnExit(captureController, stdout)") {
		t.Fatal("runConnect must defer the hand-back right after the controller is built, so no return skips it")
	}

	idle := &fakeCaptures{}
	handBackOnExit(idle, &strings.Builder{})
	if idle.stopped {
		t.Fatal("nothing running, nothing to hand back")
	}
	busy := &fakeCaptures{running: 1}
	var out strings.Builder
	handBackOnExit(busy, &out)
	if !busy.stopped || !strings.Contains(out.String(), "handing the phones this machine is capturing back first") {
		t.Fatalf("a running capture is handed back, with a bound that has not run out, and said: %v %q", busy.stopped, out.String())
	}
}
