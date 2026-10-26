package farm_test

import (
	"os"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

func specs() []farm.CloudDeviceSpec {
	return []farm.CloudDeviceSpec{
		{DeviceName: "Google Pixel 9", PlatformVersion: "15.0", Wire: "browserstack:Google Pixel 9|15.0",
			Platform: farm.WireAndroid},
		{DeviceName: "Samsung Galaxy S23", PlatformVersion: "13.0", Wire: "browserstack:Samsung Galaxy S23|13.0",
			Platform: farm.WireAndroid},
	}
}

// ── the row, and what it must not claim ────────────────────────────────────────────────────────────────────

// KIND IS Physical, AND THAT WAS THE DECISION. A BrowserStack device IS physical hardware; a fourth DeviceKind
// value was rejected because Kind is read at 22 production sites, the only three that gate behaviour use it as a
// proxy for "can this machine adb to the device", and all nine billing comparisons are `== Physical` binaries
// that would not have failed to compile on a new value.
func TestACloudRowIsPhysicalHardwareAndSaysSo(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, specs())

	if len(report.Devices) != 2 {
		t.Fatalf("one row per configured device, got %d", len(report.Devices))
	}
	for _, d := range report.Devices {
		if d.Descriptor.Kind != "Physical" {
			t.Fatalf("a cloud row's Kind must be Physical — it IS physical hardware, and the three command gates "+
				"that read Kind would each answer differently on any other value; got %q", d.Descriptor.Kind)
		}
	}
}

// THE ONE FIELD THAT MAKES IT A CLOUD ROW. Without it the row is indistinguishable from a phone on somebody's
// desk — which is exactly the state the reconnaissance found live in the tree.
func TestACloudRowCarriesItsSpecAndALocalRowDoesNot(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, specs())

	for i, d := range report.Devices {
		if d.Descriptor.CloudSpec == nil {
			t.Fatalf("row %d carries no CloudSpec — it is then a local physical row as far as every reader is "+
				"concerned, including the three that would offer an adb teardown on it", i)
		}
		if *d.Descriptor.CloudSpec != specs()[i].Wire {
			t.Fatalf("row %d: spec is %q, want %q", i, *d.Descriptor.CloudSpec, specs()[i].Wire)
		}
		// THE IDENTIFIER IS THE SPEC, which is how the merge learns which device was leased: the platform copies
		// it into `appium:udid` and the agent reads it back off the session body.
		if d.Descriptor.Udid != specs()[i].Wire || d.LocalID != specs()[i].Wire {
			t.Fatalf("row %d: the identifier must be the spec (udid=%q localId=%q)", i, d.Descriptor.Udid, d.LocalID)
		}
	}
}

// THE ADDRESS IS A NAME THAT CANNOT RESOLVE, and that is deliberate. The farm's column is NOT NULL, the hub URL
// does not belong in a field whose meaning is "the relay's forwarding target on the node", and a blank one is
// read by a worker as "use the default" — its own loopback, the wrong-device defect the farm's rule cites.
func TestACloudRowsAddressFailsLoudlyIfAnybodyEverDialsIt(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, specs())

	for _, d := range report.Devices {
		if d.Descriptor.AppiumURL == "" {
			t.Fatal("a blank address is read as \"use the default\" — the default being the reader's own loopback")
		}
		if !strings.HasSuffix(d.Descriptor.AppiumURL, ".invalid") {
			t.Fatalf("the placeholder must be un-resolvable by construction (RFC 2606 .invalid), got %q",
				d.Descriptor.AppiumURL)
		}
		if strings.Contains(d.Descriptor.AppiumURL, "browserstack.com") {
			t.Fatal("the hub URL does not go here: this column's meaning is the node's own forwarding target, and " +
				"a value that IS used elsewhere invites somebody to use this one")
		}
	}
}

// AVAILABLE RESTS ON A CATALOGUE HERE. Sealed as a sentence at the code rather than a fourth DeviceStatus value:
// the parallel limit is a real fourth state, it has no numbers behind it yet, and a status is persisted as an int.
func TestTheWeaknessOfAvailableIsWrittenWhereTheRowIsBuilt(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, specs())
	for _, d := range report.Devices {
		if d.Descriptor.Status != "Available" {
			t.Fatalf("no fourth status was invented, got %q", d.Descriptor.Status)
		}
	}
	// CASE-INSENSITIVE: this asserts that the REASONING is written at the code, not how it is capitalised.
	source := strings.ToLower(readSource(t, "report.go"))
	for _, must := range []string{"catalogue", "parallel limit", "persisted as an int"} {
		if !strings.Contains(source, must) {
			t.Fatalf("the reason Available is weaker for a cloud row must be written where the row is built "+
				"(%q missing) — every existing writer of Offline means \"the node went quiet\", none probes a "+
				"device, and for a cloud row not even a local Appium answered", must)
		}
	}
}

// NOTHING IS PUBLISHED FOR AN AGENT THAT CONFIGURED NOTHING — today's behaviour, byte for byte.
func TestNoSpecsPublishesNoRows(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, nil)
	if len(report.Devices) != 0 {
		t.Fatalf("an agent with no cloud devices must publish none, got %d", len(report.Devices))
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

// ── item 1: the platform is the account's answer, never ours ───────────────────────────────────────────────

// ⚠️ AN iOS SPEC MUST NEVER PRODUCE AN ANDROID ROW. The platform used to be hard-coded Android, so
// `--browserstack-device "iPhone 15|17.0"` — a spec BrowserStack really offers, so validation passed it —
// published a row asserting a platform nobody established. The row was not absent; it was WRONG.
func TestAnIosSpecPublishesAnIosRow(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, []farm.CloudDeviceSpec{
		{DeviceName: "iPhone 15", PlatformVersion: "17.0", Wire: "browserstack:iPhone 15|17.0",
			Platform: farm.WireIos},
	})

	if len(report.Devices) != 1 {
		t.Fatalf("the row must be published, got %d", len(report.Devices))
	}
	if got := report.Devices[0].Descriptor.Platform; got != farm.WireIos {
		t.Fatalf("an iPhone published as %q — the account's own catalogue says what a device is, and the read that "+
			"validates a spec already carries it", got)
	}
}

func TestAnAndroidSpecStillPublishesExactlyWhatItDidBefore(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, specs())
	for _, d := range report.Devices {
		if d.Descriptor.Platform != farm.WireAndroid {
			t.Fatalf("an android spec must be unchanged by the platform work, got %q", d.Descriptor.Platform)
		}
	}
}

// A SPEC WHOSE PLATFORM NOBODY ESTABLISHED PRODUCES NO ROW. Unreachable from the one producer — main derives it
// from the catalogue and publishes nothing when it could not read one — and checked anyway, because the
// alternative to a check is a row naming a platform nobody established.
func TestASpecWithNoPlatformIsNotPublished(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, []farm.CloudDeviceSpec{
		{DeviceName: "Google Pixel 9", PlatformVersion: "15.0", Wire: "browserstack:Google Pixel 9|15.0"},
	})
	if len(report.Devices) != 0 {
		t.Fatalf("a row with no established platform must not be published, got %d", len(report.Devices))
	}
}

// ── item 2: one fact, one carrier ──────────────────────────────────────────────────────────────────────────

// ⚠️ THE INVARIANT IS READ OFF A REALLY-BUILT ROW, not off one this test constructs. Two halves of the system
// read the same fact from two fields — the farm's node-command refusal from CloudSpec, the agent's session merge
// from the identifier it finds in appium:udid — and they agree only because one value builds all three here. A
// test that built the row by hand would be asserting its own arithmetic.
//
// This is the technique ourKeys() ended up using two slices ago: discover the produced value, then hold the
// property against it.
func TestTheIdentifierOfACloudRowIsItsSpec(t *testing.T) {
	report := farm.AttachCloudDevices(farm.Report{}, specs())

	for i, d := range report.Devices {
		if d.Descriptor.CloudSpec == nil {
			t.Fatalf("row %d is not a cloud row", i)
		}
		if d.Descriptor.Udid != *d.Descriptor.CloudSpec {
			t.Fatalf("row %d: udid %q and cloudSpec %q disagree — the farm would read this row as cloud and the "+
				"agent that offered it as local, so one would refuse an adb reboot on it while the other forwarded "+
				"its session to a local Appium", i, d.Descriptor.Udid, *d.Descriptor.CloudSpec)
		}
		if d.LocalID != *d.Descriptor.CloudSpec {
			t.Fatalf("row %d: localId %q is not the spec — the farm substitutes LocalId for a null udid at four "+
				"sites, so these two cannot be allowed to diverge either", i, d.LocalID)
		}
	}
}

// AND THE LOCAL DOOR NEVER PRODUCES A ROW THAT LOOKS CLOUD. This is the direction the farm cannot check without
// learning a vendor string — which it must not — so it is checked here, where the prefix lives.
func TestTheLocalDoorNeverEmitsASpecShapedIdentifier(t *testing.T) {
	report := farm.AttachDevices(farm.Report{}, localOutcomes(), "http://127.0.0.1:4723", nil, nil, nil)

	if len(report.Devices) == 0 {
		t.Fatal("instrument guard: the local door produced no rows, so this proves nothing")
	}
	for _, d := range report.Devices {
		if d.Descriptor.CloudSpec != nil {
			t.Fatalf("a local row carries a cloud spec: %v", d.Descriptor)
		}
		if strings.HasPrefix(d.Descriptor.Udid, "browserstack:") {
			t.Fatalf("a local row's identifier is spec-shaped (%q) — the agent's merge keys on that prefix and "+
				"would treat a phone on somebody's desk as a device on a BrowserStack account", d.Descriptor.Udid)
		}
	}
}

// localOutcomes is one ordinary Android phone, as the inventory probe would report it.
func localOutcomes() []inventory.Outcome {
	return []inventory.Outcome{inventory.Answered(inventory.Android,
		[]inventory.Offerable{{ID: "R5CT10ABCDE", Model: "Galaxy S23"}}, nil)}
}
