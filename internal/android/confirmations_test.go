package android_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
)

// countingRunner answers `adb devices -l` from a MUTABLE device list and counts what it was asked, so a seal can
// see how many round trips a tick actually costs.
type countingRunner struct {
	mu       sync.Mutex
	serials  []string
	settings int
	getprops int
	failRead bool
}

func (r *countingRunner) run() android.Runner {
	return func(_ context.Context, args ...string) (string, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(args) >= 2 && args[0] == "devices" {
			out := "List of devices attached\n"
			for _, s := range r.serials {
				out += s + "            device usb:2-1.1\n" // on the cable (decision 1: a listing without usb: is left out)
			}
			return out, nil
		}
		if len(args) >= 5 && args[3] == "settings" {
			r.settings++
			if r.failRead {
				return "", errors.New("adb: device offline")
			}
			return "null\n", nil
		}
		if len(args) >= 4 && args[3] == "getprop" {
			r.getprops++
			return "", nil
		}
		return "", nil
	}
}

func (r *countingRunner) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.settings, r.getprops
}

func (r *countingRunner) attach(serials ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.serials = serials
}

// THE HEADLINE: a standing device fact is read ONCE, not seven adb round trips per device every ten seconds.
// The four property reads still run every tick — that is a named decision, not an oversight (see the report's
// sweep (b)) — and this seal pins the difference so neither half can drift silently.
func TestTheStandingDeviceFactIsReadOncePerAttachedDevice(t *testing.T) {
	runner := &countingRunner{serials: []string{"R5CT64M346B"}}
	cache := android.NewInstallConfirmations()
	probe := android.Probe{Run: runner.run(), Enabled: true, Confirmations: cache}

	probe.Outcome(context.Background())
	settingsAfterFirst, propsAfterFirst := runner.counts()
	if settingsAfterFirst == 0 {
		t.Fatal("the first tick must actually ask the device")
	}

	for tick := 0; tick < 5; tick++ {
		probe.Outcome(context.Background())
	}
	settings, props := runner.counts()

	if settings != settingsAfterFirst {
		t.Fatalf("a standing fact must not be re-measured every tick: %d reads after 1 tick, %d after 6", settingsAfterFirst, settings)
	}
	if props <= propsAfterFirst {
		t.Fatalf("the property reads are NOT cached by this slice — they must still run (%d then %d)", propsAfterFirst, props)
	}
}

// …and the cache must not go stale against a CHANGED device set: a new device is asked, and the caveat's count —
// which counts devices — is computed from THIS tick's attached set, never from what the cache happens to hold.
func TestAChangedDeviceSetIsAskedAgainAndCountedNow(t *testing.T) {
	runner := &countingRunner{serials: []string{"phone-a"}}
	cache := android.NewInstallConfirmations()
	probe := android.Probe{Run: runner.run(), Enabled: true, Confirmations: cache}

	first := probe.Outcome(context.Background())
	if !strings.Contains(first.Reason(), "1 of 1") {
		t.Fatalf("one attached device, one counted: %q", first.Reason())
	}
	afterOne, _ := runner.counts()

	runner.attach("phone-a", "phone-b")
	second := probe.Outcome(context.Background())

	afterTwo, _ := runner.counts()
	if afterTwo <= afterOne {
		t.Fatal("a device the cache has never seen must be asked")
	}
	if !strings.Contains(second.Reason(), "2 of 2") {
		t.Fatalf("the count must come from THIS tick's devices, not the cache: %q", second.Reason())
	}
	if cache.Size() != 2 {
		t.Fatalf("both attached devices should be remembered, holding %d", cache.Size())
	}
}

// A device that LEAVES is forgotten, so the same phone plugged back in — possibly after somebody changed the very
// setting this reads — is asked again rather than answered from memory.
func TestAGoneDeviceIsForgotten(t *testing.T) {
	runner := &countingRunner{serials: []string{"phone-a"}}
	cache := android.NewInstallConfirmations()
	probe := android.Probe{Run: runner.run(), Enabled: true, Confirmations: cache}

	probe.Outcome(context.Background())
	runner.attach() // unplugged
	probe.Outcome(context.Background())
	if cache.Size() != 0 {
		t.Fatalf("an unattached device must be evicted, holding %d", cache.Size())
	}

	before, _ := runner.counts()
	runner.attach("phone-a") // plugged back in
	probe.Outcome(context.Background())
	after, _ := runner.counts()
	if after <= before {
		t.Fatal("a device that comes back must be asked again — the setting may have changed while it was away")
	}
}

// A FAILED READ IS NOT A FACT. Caching one would freeze a transient adb hiccup into this device's answer for the
// whole time it stays attached.
func TestAFailedReadIsNeverRemembered(t *testing.T) {
	runner := &countingRunner{serials: []string{"phone-a"}, failRead: true}
	cache := android.NewInstallConfirmations()
	probe := android.Probe{Run: runner.run(), Enabled: true, Confirmations: cache}

	probe.Outcome(context.Background())
	if cache.Size() != 0 {
		t.Fatalf("a failed read must not be remembered, holding %d", cache.Size())
	}

	before, _ := runner.counts()
	probe.Outcome(context.Background())
	after, _ := runner.counts()
	if after <= before {
		t.Fatal("the next tick must try again")
	}
}

// A nil cache is valid and reads every time — the single-shot `devices` verb needs no memory, and the probe must
// not require one to work.
func TestNoCacheStillWorks(t *testing.T) {
	runner := &countingRunner{serials: []string{"phone-a"}}
	probe := android.Probe{Run: runner.run(), Enabled: true}

	outcome := probe.Outcome(context.Background())
	if !outcome.HasCaveat() {
		t.Fatal("a probe without a cache must still carry the caveat")
	}
	before, _ := runner.counts()
	probe.Outcome(context.Background())
	after, _ := runner.counts()
	if after <= before {
		t.Fatal("without a cache, every tick reads")
	}
}
