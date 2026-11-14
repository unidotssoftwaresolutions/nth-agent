package capture

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST — a capture start the farm's handback or `capture disable` overtook touches
// nothing; the farm cannot grow this machine's memory or leave files in its folder with names it invents
//; and its words reach the console without terminal controls.

// heldAtThePeerCheck is a controller whose first start stops at its peer check — after it read the mark and the CA, and
// before it reserved anything — until release is closed. entered is closed when it gets there. A barrier, not a sleep.
func heldAtThePeerCheck(m *machine) (ctl *Controller, entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctl = m.controller(func(cfg *Config) {
		cfg.PeerCheck = func() error {
			first := false
			once.Do(func() { first = true })
			if first {
				close(entered)
				<-release
			}
			return nil
		}
	})
	return ctl, entered, release
}

func untouched(t *testing.T, m *machine, ctl *Controller, serial string) {
	t.Helper()
	if proxy := m.bench.proxyOf(serial); strings.HasPrefix(proxy, "127.0.0.1:") {
		t.Fatalf("the phone was pointed at a capture (%q) after this machine said it was clean or off", proxy)
	}
	if n := m.bench.reversesOf(serial); n != 0 {
		t.Fatalf("an adb reverse was set on the phone (%d)", n)
	}
	if ctl.Running() != 0 {
		t.Fatalf("a capture is running (%d)", ctl.Running())
	}
	if records, _ := m.store.Records(); len(records) != 0 {
		t.Fatalf("a handback record was left: %+v", records)
	}
}

// ── A START THE HANDBACK OVERTOOK ──────────────────────────────────────────────────────────────────────────────────

func TestAStartTheFarmsHandbackOvertookTouchesNothing(t *testing.T) {
	for _, c := range []struct {
		name     string
		overtake func(ctl *Controller, serial string) string
	}{
		{"the farm's handback question, answered clean", func(ctl *Controller, serial string) string {
			if clean, observation := ctl.HandBackForFarm(context.Background(), serial, leaseA); !clean {
				return fmt.Sprintf("the question must be answered clean (nothing was set yet): %q", observation)
			}
			return ""
		}},
		{"an older farm's capture-stop", func(ctl *Controller, _ string) string {
			ctl.Stop(context.Background(), leaseA)
			return ""
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			phone := samsung("R5CT30ABCDE")
			m := newMachine(t, phone)
			ctl, entered, release := heldAtThePeerCheck(m)
			done := make(chan struct{})
			go func() { ctl.Start(context.Background(), leaseA, phone.serial); close(done) }()
			<-entered
			if problem := c.overtake(ctl, phone.serial); problem != "" {
				close(release)
				<-done
				t.Fatal(problem)
			}
			close(release)
			<-done
			untouched(t, m, ctl, phone.serial)
			// …and the next lease on the phone still captures.
			ctl.Start(context.Background(), leaseB, phone.serial)
			t.Cleanup(func() { ctl.StopAll(context.Background()) })
			if ctl.Running() != 1 {
				t.Fatalf("the next lease's capture must still start:\n%s", m.out.String())
			}
		})
	}
}

// ── A START BESIDE capture disable ─────────────────────────────────────────────────────────────────────────────────

func TestAStartThatStraddlesCaptureDisableTouchesNothing(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl, entered, release := heldAtThePeerCheck(m)
	ctl.wasOn = true // a connect that has seen capture on
	done := make(chan struct{})
	go func() { ctl.Start(context.Background(), leaseA, phone.serial); close(done) }()
	<-entered                     // the start has read the mark: on
	must(t, m.store.removeMark()) // `capture disable`, in another terminal: the mark goes first
	ctl.watchMark(context.Background())
	close(release)
	<-done
	untouched(t, m, ctl, phone.serial)
	if !strings.Contains(m.out.String(), "network capture is switched off") {
		t.Fatalf("the run must be told why its capture did not start:\n%s", m.out.String())
	}
}

func TestEveryTickThatFindsTheMarkGoneStopsWhatRuns(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil) // ctl.wasOn is false: this connect has not yet ticked since `capture enable`
	ctl.Start(context.Background(), leaseA, phone.serial)
	if ctl.Running() != 1 {
		t.Fatalf("the capture must start:\n%s", m.out.String())
	}
	must(t, m.store.removeMark()) // …and `capture disable`, all within one tick
	ctl.watchMark(context.Background())
	if ctl.Running() != 0 || m.bench.proxyOf(phone.serial) != ":0" {
		t.Fatalf("a tick that finds the mark gone stops every capture (running %d, proxy %q)", ctl.Running(), m.bench.proxyOf(phone.serial))
	}
}

// ── NAMES THE FARM INVENTS ─────────────────────────────────────────────────────────────────────────────────────────

func TestTheFarmCannotGrowThisMachinesStateWithNamesItInvents(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil)

	// Handback questions for phones this machine has never had: answered as before, from adb, and nothing kept.
	for i := 0; i < 64; i++ {
		clean, observation := ctl.HandBackForFarm(context.Background(), fmt.Sprintf("INVENTED%04d", i), leaseA)
		if clean || !strings.HasPrefix(observation, "the phone was not reachable over adb") {
			t.Fatalf("a phone this machine cannot reach is not clean, and says why: %v %q", clean, observation)
		}
	}
	entries, _ := os.ReadDir(m.store.locksDir())
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), fmt.Sprintf("%x", "INVENTED")) {
			t.Fatalf("a lock file was left for a phone the farm invented: %s (%d files)", entry.Name(), len(entries))
		}
	}
	ctl.mu.Lock()
	gates := len(ctl.gates)
	ctl.mu.Unlock()
	if gates != 0 {
		t.Fatalf("a gate was kept for each phone the farm invented: %d", gates)
	}

	// A phone that IS on this machine is still asked about with its lock, as before.
	if clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA); !clean {
		t.Fatalf("a phone of this machine is answered from the phone: %q", observation)
	}

	// Refused starts, each for a fresh lease: what is kept to say each once stays bounded.
	for i := 0; i < sayOnceBound+500; i++ {
		ctl.Start(context.Background(), fmt.Sprintf("%08x-0000-4000-8000-000000000000", i), strings.Repeat("ab12cd34", 5))
	}
	ctl.mu.Lock()
	said, outcomes := len(ctl.said), len(ctl.outcomes)
	ctl.mu.Unlock()
	if said > sayOnceBound || outcomes > sayOnceBound {
		t.Fatalf("what the farm's leases make this machine keep must be bounded: %d lines, %d outcomes", said, outcomes)
	}
}

// ── A SERIAL FROM THE FARM, ON THE CONSOLE ─────────────────────────────────────────────────────────────────────────

func TestAFarmsSerialReachesTheConsoleWithoutControlsAndCut(t *testing.T) {
	m := newMachine(t)
	ctl := m.controller(nil)
	serial := "\x1b]0;owned\x07\x1b[2J\u009b31m‮" + strings.Repeat("A", 10000)
	ctl.Start(context.Background(), leaseA, serial)
	said := m.out.String()
	if strings.ContainsAny(said, "\x1b\x07\u009b‮") {
		t.Fatalf("a terminal control from the farm reached the console: %q", said[:min(len(said), 200)])
	}
	if !strings.Contains(said, "was not started") || len(said) > 1000 {
		t.Fatalf("the refusal is said, with the farm's serial cut short (%d bytes):\n%.300s", len(said), said)
	}
}
