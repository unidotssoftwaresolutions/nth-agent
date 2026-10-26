package main

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ONE Ctrl-C, HEARD MORE THAN ONCE (before 0.3.0). Under `npx`, Ctrl-C reaches the agent from the terminal and again from
// the npm shim, which forwards SIGINT and SIGTERM to it. `connect` restored the system's own handling after the first, so
// the shim's copy, milliseconds later, would have ended the agent while it was handing its phones back. A repeat within
// duplicateSignalWindow is now the same request; after the window, a second signal still ends the process at once.

type watched struct {
	signals   chan os.Signal
	window    chan time.Time
	cancelled chan struct{}
	stopped   chan struct{}
	done      chan struct{}
	code      atomic.Int32
}

func watch(t *testing.T) *watched {
	t.Helper()
	w := &watched{signals: make(chan os.Signal, 4), window: make(chan time.Time), cancelled: make(chan struct{}),
		stopped: make(chan struct{}), done: make(chan struct{})}
	go func() {
		watchSignals(w.signals, func() { close(w.cancelled) }, func() { close(w.stopped) }, &w.code,
			func() <-chan time.Time { return w.window })
		close(w.done)
	}()
	return w
}

func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

func TestARepeatOfTheFirstSignalWithinTheWindowIsTheSameRequest(t *testing.T) {
	w := watch(t)
	w.signals <- os.Interrupt
	within(t, w.cancelled, "the first SIGINT cancelling the agent's context")
	if got := w.code.Load(); got != 130 {
		t.Fatalf("the first SIGINT must stand for exit 130, got %d", got)
	}

	// The shim's copy and anybody else's: absorbed, and the system's own handling is NOT back yet.
	w.signals <- os.Interrupt
	w.signals <- syscall.SIGTERM
	deadline := time.Now().Add(5 * time.Second)
	for len(w.signals) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(w.signals) > 0 {
		t.Fatal("the repeats were never read — they would wait for the system's handling to be restored")
	}
	select {
	case <-w.stopped:
		t.Fatal("a repeat within the window restored the system's handling — the next copy would kill the agent mid-handback")
	case <-time.After(50 * time.Millisecond):
	}
	if got := w.code.Load(); got != 130 {
		t.Fatalf("a repeat changed what the first signal stands for: %d", got)
	}

	// The window ends: from now on a second signal ends the process at once, as before.
	w.window <- time.Now()
	within(t, w.stopped, "the system's handling being restored when the window ends")
	within(t, w.done, "the watcher returning")
}

func TestTheFirstSigtermStandsFor143(t *testing.T) {
	w := watch(t)
	w.signals <- syscall.SIGTERM
	within(t, w.cancelled, "SIGTERM cancelling the agent's context")
	if got := w.code.Load(); got != 143 {
		t.Fatalf("SIGTERM must stand for exit 143, got %d", got)
	}
	w.window <- time.Now()
	within(t, w.done, "the watcher returning")
}

func TestNoSignalCancelsNothing(t *testing.T) {
	w := watch(t)
	close(w.signals)
	within(t, w.done, "the watcher returning on a closed channel")
	select {
	case <-w.cancelled:
		t.Fatal("a closed signal channel cancelled the agent")
	default:
	}
	if got := w.code.Load(); got != 0 {
		t.Fatalf("no signal must stand for 0, got %d", got)
	}
}

// The window must hold a forwarded copy (milliseconds) and give a person's own second Ctrl-C back soon after.
func TestTheDuplicateWindowIsLongEnoughForACopyAndShortEnoughForAPerson(t *testing.T) {
	if duplicateSignalWindow < 500*time.Millisecond || duplicateSignalWindow > 5*time.Second {
		t.Fatalf("duplicateSignalWindow is %v — under half a second a forwarded copy on a loaded machine is read as a second "+
			"Ctrl-C; over five a person pressing it again waits for nothing", duplicateSignalWindow)
	}
}
