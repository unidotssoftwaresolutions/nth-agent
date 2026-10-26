package android_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
)

// ── THE FIELDS A LEASE IS WRITTEN IN, read with the rack's meaning ──────────────────────────────────────────

// MEASURED on the operator's Samsung SM-M346B1 (2026-08-22, read-only getprop):
//
//	ro.product.model SM-M346B1 · ro.product.manufacturer samsung · ro.build.version.release 16 ·
//	ro.build.characteristics phone — while `adb devices -l` lists the same phone as model:SM_M346B1.
var samsungProps = map[string]string{
	"ro.product.model":         "SM-M346B1",
	"ro.product.manufacturer":  "samsung",
	"ro.build.version.release": "16",
	"ro.build.characteristics": "phone",
}

const samsungListing = "List of devices attached\nR5CT64M346B            device usb:2-1.1 product:m34xdeea model:SM_M346B1 device:m34x transport_id:18\n"

// propRunner answers the listing and per-serial getprop reads from a table; a prop absent from the table
// answers blank, exactly as a device that does not set it.
func propRunner(listing string, props map[string]string, fail map[string]error) android.Runner {
	return func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "devices" {
			return listing, nil
		}
		// adb -s <serial> shell getprop <prop>
		if len(args) == 5 && args[0] == "-s" && args[2] == "shell" && args[3] == "getprop" {
			if err, failed := fail[args[4]]; failed {
				return "", err
			}
			return props[args[4]] + "\n", nil
		}
		return "", errors.New("unexpected adb call: " + strings.Join(args, " "))
	}
}

func TestThePublishedModelIsTheDevicesOwnNotAdbsSanitisedToken(t *testing.T) {
	// THE LEASE-MATCHES-THE-PROBE SEAL, agent half: the model a lease is written in is ro.product.model
	// (SM-M346B1) — the rack's meaning — never the listing's SM_M346B1.
	out := android.Probe{Run: propRunner(samsungListing, samsungProps, nil), Enabled: true}.Outcome(context.Background())
	d := out.Offerable()[0]
	if d.ID != "R5CT64M346B" || d.Model != "SM-M346B1" || d.Manufacturer != "samsung" || d.OsVersion != "16" || d.FormFactor != "phone" {
		t.Fatalf("the descriptor must carry the device's own answers with the rack's meaning, got %+v", d)
	}
	if strings.Contains(d.Model, "_") {
		t.Fatal("the sanitised listing token leaked into Model")
	}
}

func TestAFieldTheDeviceDidNotAnswerStaysEmpty(t *testing.T) {
	// THE NO-INVENTION SEAL: blank or failed reads stay empty — no listing token, no "unknown", no guess.
	blank := map[string]string{"ro.product.model": "SM-M346B1"} // the other three are blank
	fail := map[string]error{"ro.build.version.release": errors.New("adb: device offline")}
	out := android.Probe{Run: propRunner(samsungListing, blank, fail), Enabled: true}.Outcome(context.Background())
	d := out.Offerable()[0]
	if d.Model != "SM-M346B1" {
		t.Fatalf("the answered field is kept, got %q", d.Model)
	}
	if d.Manufacturer != "" || d.OsVersion != "" || d.FormFactor != "" {
		t.Fatalf("unanswered fields must stay EMPTY, got manufacturer=%q os=%q form=%q", d.Manufacturer, d.OsVersion, d.FormFactor)
	}
}

func TestFormFactorIsDecidedFromAndroidsOwnWordOrLeftUnset(t *testing.T) {
	// The rack maps everything non-tablet to "phone" (a guess for a TV, a watch, a car); this agent does not.
	cases := map[string]string{
		"phone":             "phone",
		"tablet":            "tablet",
		"default":           "phone",
		"nosdcard,emulator": "phone",
		"tablet,nosdcard":   "tablet",
		"tv":                "",
		"watch":             "",
		"automotive":        "",
		"":                  "",
	}
	for in, want := range cases {
		if got := android.FormFactorFrom(in); got != want {
			t.Fatalf("FormFactorFrom(%q) = %q, want %q", in, got, want)
		}
	}
}
