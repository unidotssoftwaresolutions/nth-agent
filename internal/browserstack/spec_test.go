package browserstack_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

func catalogue() []browserstack.CatalogueDevice {
	return []browserstack.CatalogueDevice{
		{OS: "android", OSVersion: "15.0", Device: "Google Pixel 9", Real: true},
		{OS: "android", OSVersion: "14.0", Device: "Google Pixel 9", Real: true},
		{OS: "android", OSVersion: "13.0", Device: "Samsung Galaxy S23 Ultra", Real: true},
	}
}

// ── the spec is what the customer typed, and a bad one is a mistake not a guess ────────────────────────────

func TestASpecIsANameAndAVersion(t *testing.T) {
	spec, err := browserstack.ParseSpec("Google Pixel 9|15.0")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if spec.DeviceName != "Google Pixel 9" || spec.PlatformVersion != "15.0" {
		t.Fatalf("got %+v", spec)
	}
	if spec.Wire() != "browserstack:Google Pixel 9|15.0" {
		t.Fatalf("the wire form is what travels as the row's identifier, got %q", spec.Wire())
	}
}

func TestAMalformedSpecIsRefusedWithAnExample(t *testing.T) {
	for _, bad := range []string{"", "Google Pixel 9", "|15.0", "Google Pixel 9|", "   "} {
		_, err := browserstack.ParseSpec(bad)
		if err == nil {
			t.Fatalf("%q must be refused — a half-typed device is a mistake, and guessing the other half is how "+
				"somebody ends up running on a phone they did not ask for", bad)
		}
		if !strings.Contains(err.Error(), "Google Pixel 9") {
			t.Fatalf("%q: the refusal must show the shape, got %v", bad, err)
		}
	}
}

// ── the wire form round-trips, and a serial is left alone ──────────────────────────────────────────────────

// FALSE IS THE ORDINARY ANSWER. This is asked of every session the agent forwards, and every local device's
// identifier is a serial — so "not a spec" must be total and must leave the value untouched.
func TestOnlyASpecParsesBackOffTheWire(t *testing.T) {
	spec, ok := browserstack.ParseWire("browserstack:Google Pixel 9|15.0")
	if !ok || spec.DeviceName != "Google Pixel 9" {
		t.Fatalf("a spec must round-trip, got %+v ok=%v", spec, ok)
	}
	for _, notASpec := range []string{
		"R5CT10ABCDE",           // an adb serial
		"emulator-5554",         // an emulator
		"00008030-001A2B3C4D5E", // an iPhone udid
		"browserstack:",         // the prefix and nothing else
		"browserstack:no-separator",
		"",
	} {
		if _, ok := browserstack.ParseWire(notASpec); ok {
			t.Fatalf("%q is not a cloud spec and must not be read as one", notASpec)
		}
	}
}

// ── item 1's refusal, and the seal injection 6 proved was missing ──────────────────────────────────────────

// A DEVICE THE ACCOUNT DOES NOT OFFER IS REFUSED ON THE CUSTOMER'S OWN MACHINE. The hub's answer for this
// mistake is "Platform and Browser not valid.", which names a platform and a browser that are both fine and
// never mentions the device list.
func TestASpecTheAccountDoesNotOfferIsRefusedWithTheNearestMatches(t *testing.T) {
	spec, _ := browserstack.ParseSpec("Google Pixel 9|16.0") // a version the account does not have
	refusal := browserstack.RefuseUnofferedSpecs([]browserstack.DeviceSpec{spec}, catalogue())

	if refusal == "" {
		t.Fatal("a device the account does not offer must stop the agent before any row is published — otherwise " +
			"the customer learns about it from a hub message that names the wrong thing")
	}
	// THE REFUSAL IS ONLY AS GOOD AS ITS LIST. "Not on your account" sends somebody to a web page; naming the
	// versions that ARE there ends the question.
	for _, must := range []string{"Google Pixel 9|15.0", "Google Pixel 9|14.0"} {
		if !strings.Contains(refusal, must) {
			t.Fatalf("the refusal must name the nearest matches (%q missing): %s", must, refusal)
		}
	}
}

func TestEveryOfferedSpecPasses(t *testing.T) {
	var specs []browserstack.DeviceSpec
	for _, raw := range []string{"Google Pixel 9|15.0", "Samsung Galaxy S23 Ultra|13.0"} {
		spec, err := browserstack.ParseSpec(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		specs = append(specs, spec)
	}
	if refusal := browserstack.RefuseUnofferedSpecs(specs, catalogue()); refusal != "" {
		t.Fatalf("devices the account offers must be accepted, got: %s", refusal)
	}
	// …and no specs at all is not a refusal either.
	if refusal := browserstack.RefuseUnofferedSpecs(nil, catalogue()); refusal != "" {
		t.Fatalf("an agent with no cloud devices has nothing to refuse, got: %s", refusal)
	}
}

// AND THE ONE THAT IS WRONG IS THE ONE NAMED, not the first in the list.
func TestTheRefusalNamesTheDeviceThatIsActuallyWrong(t *testing.T) {
	good, _ := browserstack.ParseSpec("Google Pixel 9|15.0")
	bad, _ := browserstack.ParseSpec("Nokia 3310|1.0")

	refusal := browserstack.RefuseUnofferedSpecs([]browserstack.DeviceSpec{good, bad}, catalogue())
	if !strings.Contains(refusal, "Nokia 3310") {
		t.Fatalf("the refusal must name the device that is wrong, got: %s", refusal)
	}
	if strings.Contains(refusal, "does not offer \"Google Pixel 9") {
		t.Fatalf("…and not one that is fine: %s", refusal)
	}
}
