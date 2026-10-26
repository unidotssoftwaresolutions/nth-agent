package android_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// settingsRunner answers `adb devices -l` with one healthy device, every getprop blank, and the
// `settings get <ns> <key>` reads from the supplied map — keyed by namespace, missing = "null", which is
// what a real device prints for a key nobody has set.
func settingsRunner(byNamespace map[string]string) android.Runner {
	return func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "devices" {
			return "List of devices attached\nR5CT64M346B            device usb:2-1.1\n", nil
		}
		if len(args) >= 6 && args[2] == "shell" && args[3] == "settings" && args[4] == "get" {
			if value, ok := byNamespace[args[5]]; ok {
				return value + "\n", nil
			}
			return "null\n", nil
		}
		return "", nil
	}
}

// THE HEADLINE: a platform that has a device somebody may have to stand next to ANSWERS — and says what
// its answer does not cover. Answered, not CouldNotAsk: the offerable truth is established and every
// consumer must keep acting on it.
func TestADeviceCarriesTheInstallConfirmationCaveat(t *testing.T) {
	outcome := android.Probe{Run: settingsRunner(nil), Enabled: true}.Outcome(context.Background())

	if !outcome.IsAnswered() {
		t.Fatalf("a blind spot is NOT a failure — the platform answered, got %s", outcome.KindString())
	}
	if len(outcome.Offerable()) != 1 {
		t.Fatalf("the healthy device is still offered, got %d", len(outcome.Offerable()))
	}
	if !outcome.HasCaveat() {
		t.Fatal("a device that may require a person to confirm an install must SAY SO — no caveat was carried")
	}
	if !strings.Contains(outcome.Reason(), "without somebody at") || !strings.Contains(outcome.Reason(), "may wait for a tap") {
		t.Fatalf("the caveat must name the blind spot in words a reader can act on, got %q", outcome.Reason())
	}
	if outcome.Remedy() == inventory.MissingRemedyMarker || strings.TrimSpace(outcome.Remedy()) == "" {
		t.Fatalf("a caveat without a remedy is the support ticket this agent deletes, got %q", outcome.Remedy())
	}
}

// A platform with NOTHING attached has no install to be confirmed, so it says nothing extra. This is the
// other half of the walk guard: the same statement, two inputs whose correct answers DIFFER.
func TestNoDevicesMeansNoCaveat(t *testing.T) {
	empty := func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "devices" {
			return "List of devices attached\n", nil
		}
		return "", nil
	}
	outcome := android.Probe{Run: empty, Enabled: true}.Outcome(context.Background())

	if !outcome.IsAnswered() {
		t.Fatalf("nothing attached is an ANSWER, got %s", outcome.KindString())
	}
	if outcome.HasCaveat() {
		t.Fatalf("a caveat about nothing is noise on every screen that renders it, got %q", outcome.Reason())
	}
}

// "null" IS NOT A VALUE. `settings get` prints it for a key nobody has set, and reading it as a value would
// put a fact on a screen the device never stated. Measured on the operator's phone: the key is unset in all
// three namespaces.
func TestUnsetSettingIsUnreadableNotAValue(t *testing.T) {
	unset := android.Probe{Run: settingsRunner(nil), Enabled: true}.Outcome(context.Background())
	if !strings.Contains(unset.Reason(), "unset or unreadable on 1 of 1") {
		t.Fatalf("an unset key must be reported as unreadable, got %q", unset.Reason())
	}

	// The same read, a device that HAS the key set — a different correct answer from the same statement.
	set := android.Probe{Run: settingsRunner(map[string]string{"global": "0"}), Enabled: true}.Outcome(context.Background())
	if !strings.Contains(set.Reason(), "reads a value on every attached device") {
		t.Fatalf("a device that answered must be reported as having answered, got %q", set.Reason())
	}
	// …and the caveat SURVIVES a definite value, because the vendor gates are unread either way.
	if !set.HasCaveat() {
		t.Fatal("even a readable verifier setting leaves the vendor confirmation unread — the caveat must stand")
	}
}

// THE NAMESPACE TRAP, measured: package_verifier_user_consent reads null in `secure` and 1 in `global` —
// the same key, a different namespace. A probe that asked only one namespace would manufacture a false
// "unknown", so the read asks all three.
func TestTheReadAsksEveryNamespace(t *testing.T) {
	outcome := android.Probe{Run: settingsRunner(map[string]string{"system": "1"}), Enabled: true}.Outcome(context.Background())
	if !strings.Contains(outcome.Reason(), "reads a value on every attached device") {
		t.Fatalf("a value set in `system` alone must still be found, got %q", outcome.Reason())
	}
}

// A read that FAILS is unreadable — never a guessed value, the rule every field in this repo follows.
func TestAFailedReadIsUnreadable(t *testing.T) {
	failing := func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "devices" {
			return "List of devices attached\nR5CT64M346B            device usb:2-1.1\n", nil
		}
		if len(args) >= 5 && args[3] == "settings" {
			return "", errors.New("adb: device offline")
		}
		return "", nil
	}
	outcome := android.Probe{Run: failing, Enabled: true}.Outcome(context.Background())
	if !strings.Contains(outcome.Reason(), "unset or unreadable on 1 of 1") {
		t.Fatalf("a failed read is unreadable, got %q", outcome.Reason())
	}
}
