package farm_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// A PHONE WE CANNOT DRIVE IS STILL THEIR PHONE.
//
// Until this slice a healthy phone on a machine running without --appium survived the trip as the integer 1
// inside an outcome's offerableCount. A BROKEN phone had a serial, a state and a remedy; a LEASABLE phone had a
// whole row; the healthy-but-undrivable one — the operator's own — had nothing. These seals hold the new
// statement to the two rules that made the old silence correct: it names, and it cannot be leased.

func healthy() []inventory.Outcome {
	return []inventory.Outcome{inventory.Answered(inventory.Android, []inventory.Offerable{
		{ID: "R5CT64M346B", Model: "SM-M346B1", Manufacturer: "samsung", OsVersion: "16", FormFactor: "phone"},
	}, nil)}
}

func TestAPhoneTheMachineCannotOfferIsStillNamed(t *testing.T) {
	// No AttachDevices — the driver gate never opened, which is the operator's machine today.
	report := farm.BuildReport(healthy())

	if len(report.Devices) != 0 {
		t.Fatalf("the driver gate is shut: devices must stay empty, got %v", report.Devices)
	}
	if report.Present == nil {
		t.Fatal("a platform answered, so the present list is an ANSWER — nil means nobody established anything")
	}
	present := *report.Present
	if len(present) != 1 {
		t.Fatalf("the phone the probe saw must be named, got %v", present)
	}
	if present[0].ID != "R5CT64M346B" || present[0].Platform != farm.WireAndroid {
		t.Fatalf("the platform's own id and platform must survive, got %+v", present[0])
	}
	if present[0].Model == nil || *present[0].Model != "SM-M346B1" {
		t.Fatalf("the model the probe held must survive, got %+v", present[0])
	}
}

// THE FIELDS ARE THE PLATFORM'S OWN ANSWERS, AND AN UNANSWERED ONE IS ABSENT — never a listing token, never a
// default. An absent field a filter treats as "no match" is honest; a guessed one is not.
func TestAnUnansweredFieldIsOmittedFromAPresentDevice(t *testing.T) {
	report := farm.BuildReport([]inventory.Outcome{inventory.Answered(inventory.IOS, []inventory.Offerable{
		{ID: "00008030-ABC", Manufacturer: "Apple"}, // no model, no os version, no form factor
	}, nil)})

	body, err := json.Marshal((*report.Present)[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{`"model"`, `"osVersion"`, `"formFactor"`} {
		if strings.Contains(string(body), absent) {
			t.Fatalf("an unanswered field must be OMITTED, got %s", body)
		}
	}
	if !strings.Contains(string(body), `"manufacturer":"Apple"`) {
		t.Fatalf("an answered field must survive, got %s", body)
	}
}

// THE DOOR, AND THE ONE THING THIS TYPE MUST NEVER BECOME. A present device carries no address and no status of
// ours, so no runtime flag can promote it into a leasable row. This is asserted on the TYPE, because a check on
// one instance would pass while a field sat there waiting to be filled in.
func TestAPresentDeviceCannotCarryAnAddressOrAStatus(t *testing.T) {
	shape := reflect.TypeOf(farm.WirePresentDevice{})
	for i := 0; i < shape.NumField(); i++ {
		name := strings.ToLower(shape.Field(i).Name)
		for _, forbidden := range []string{"url", "uri", "addr", "host", "port", "endpoint", "status"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("WirePresentDevice.%s: a present device is not offered — it must carry no address and "+
					"no status word of ours, or a lease could be written against a phantom", shape.Field(i).Name)
			}
		}
	}
	// …and it is a different TYPE from the leasable row, so the compiler keeps them apart rather than a comment.
	if reflect.TypeOf(farm.WirePresentDevice{}) == reflect.TypeOf(farm.WireDevice{}) {
		t.Fatal("a present device and a leasable row must be different types")
	}
}

// A MACHINE THAT SAID NOTHING IS NOT A MACHINE WITH NOTHING ATTACHED. The same rule the unusable list already
// follows, and the coarse one-voice lie the farm's door stopped telling.
func TestAMachineThatEstablishedNothingSaysNothingAboutPresentDevices(t *testing.T) {
	report := farm.BuildReport([]inventory.Outcome{inventory.CouldNotAsk(inventory.Android, "adb missing", "install platform-tools")})

	if report.Present != nil {
		t.Fatalf("no platform answered, so there is no present-devices answer to give, got %v", *report.Present)
	}
	if report.Unusable != nil {
		t.Fatal("the unusable list follows the same rule and must also stay nil")
	}
}

func TestAPlatformThatAnsweredWithNoPhonesSaysSoWithAnEmptyList(t *testing.T) {
	report := farm.BuildReport([]inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)})

	if report.Present == nil {
		t.Fatal("android answered — 'I looked and there are none' is a real answer, not silence")
	}
	if len(*report.Present) != 0 {
		t.Fatalf("expected an empty answer, got %v", *report.Present)
	}
}

// THE THREE STATES, ON ONE MACHINE, AT ONCE — and no phone in two of them.
func TestOfferedPresentAndUnusableAreThreeDisjointStates(t *testing.T) {
	outcomes := []inventory.Outcome{inventory.Answered(inventory.Android,
		[]inventory.Offerable{{ID: "HEALTHY-1", Model: "SM-M346B1"}},
		[]inventory.Unusable{{ID: "BROKEN-1", State: "unauthorized", Remedy: "accept the USB debugging prompt"}})}

	// The driver gate OPEN: the healthy phone becomes offerable, and is still present.
	offered := farm.AttachDevices(farm.BuildReport(outcomes), outcomes, "http://127.0.0.1:4723", nil, nil, nil)
	if len(offered.Devices) != 1 || offered.Devices[0].LocalID != "HEALTHY-1" {
		t.Fatalf("the healthy phone must be offered when the gate is open, got %v", offered.Devices)
	}
	if len(*offered.Present) != 1 || (*offered.Present)[0].ID != "HEALTHY-1" {
		t.Fatalf("an offered phone is still a phone the machine HAS, got %v", *offered.Present)
	}
	if len(*offered.Unusable) != 1 || (*offered.Unusable)[0].Serial != "BROKEN-1" {
		t.Fatalf("the broken phone stays in its own list, got %v", *offered.Unusable)
	}

	// The broken phone is in NEITHER of the other two — the compiler's doing, not a status field's.
	for _, d := range offered.Devices {
		if d.LocalID == "BROKEN-1" {
			t.Fatal("an unusable phone must never become a leasable row")
		}
	}
	for _, p := range *offered.Present {
		if p.ID == "BROKEN-1" {
			t.Fatal("an unusable phone is not a present-and-healthy one — they are different statements")
		}
	}

	// The driver gate SHUT: the same phone is present and NOT offered. One predicate over one id decides which,
	// so the two states are complements and can never both be true.
	shut := farm.BuildReport(outcomes)
	if len(shut.Devices) != 0 {
		t.Fatalf("the gate is shut, got %v", shut.Devices)
	}
	if len(*shut.Present) != 1 || (*shut.Present)[0].ID != "HEALTHY-1" {
		t.Fatalf("the same phone must still be named, got %v", *shut.Present)
	}
}
