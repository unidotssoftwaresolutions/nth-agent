package farm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── ONE PHONE'S CAPTURE READINESS, IN THE FARM'S OWN WORDS (AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE rule 1) ────────────
//
// The farm withholds a capture lease's pull URL from a phone nobody set up, and says why before the run starts, only if
// the machine says so per phone. Additive: an older farm ignores a field it does not know, so the field is omitted
// whenever the machine has nothing to say — and only the farm's two words are ever sent, because the farm binds the field
// to its enum and a word it did not know would cost the whole heartbeat.

func machineWithPhones() farm.Report {
	return farm.BuildReport([]inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{
			{ID: "R5CT30READY", Model: "SM-A546B"}, {ID: "R5CT30NOTSET", Model: "Pixel 8"}, {ID: "R5CT30QUIET"},
		}, nil),
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030-001A2B3C4D5E6F70", Model: "iPhone 15"}}, nil),
	})
}

func readinessOn(t *testing.T, report farm.Report) map[string]string {
	t.Helper()
	body, err := json.Marshal(farm.HeartbeatRequest{PresentDevices: report.Present})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Present []map[string]any `json:"presentDevices"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	said := map[string]string{}
	for _, phone := range wire.Present {
		if word, ok := phone["captureReadiness"]; ok {
			said[phone["id"].(string)] = word.(string)
		}
	}
	return said
}

func TestEachPresentAndroidPhoneSaysItsCaptureReadiness(t *testing.T) {
	asked := map[string]int{}
	report := farm.DeclarePhoneCapture(machineWithPhones(), func(platform farm.WirePlatform, id string) string {
		asked[id]++
		switch id {
		case "R5CT30READY":
			return farm.PhoneCaptureReady
		case "R5CT30NOTSET":
			return farm.PhoneCaptureNotSetUp
		case "00008030-001A2B3C4D5E6F70":
			return farm.PhoneCaptureReady // never sent: capture is Android's
		}
		return "" // says nothing
	})
	said := readinessOn(t, report)
	want := map[string]string{"R5CT30READY": "Ready", "R5CT30NOTSET": "NotSetUp"}
	if len(said) != len(want) || said["R5CT30READY"] != want["R5CT30READY"] || said["R5CT30NOTSET"] != want["R5CT30NOTSET"] {
		t.Fatalf("the wire must carry exactly %v, got %v", want, said)
	}
	if asked["00008030-001A2B3C4D5E6F70"] != 0 {
		t.Fatal("an iPhone is never asked about: capture runs on Android phones only")
	}
}

func TestAWordTheFarmDoesNotKnowIsNeverSent(t *testing.T) {
	report := farm.DeclarePhoneCapture(machineWithPhones(), func(farm.WirePlatform, string) string { return "Maybe" })
	if said := readinessOn(t, report); len(said) != 0 {
		t.Fatalf("an unknown word must be omitted, got %v", said)
	}
}

func TestAMachineThatSaysNothingSendsTodaysWire(t *testing.T) {
	// A nil declaration, and a declaration that says nothing, both leave the present list byte for byte as it was.
	before, _ := json.Marshal(machineWithPhones().Present)
	for name, report := range map[string]farm.Report{
		"no declaration":        farm.DeclarePhoneCapture(machineWithPhones(), nil),
		"a declaration of none": farm.DeclarePhoneCapture(machineWithPhones(), func(farm.WirePlatform, string) string { return "" }),
	} {
		after, _ := json.Marshal(report.Present)
		if string(after) != string(before) || strings.Contains(string(after), "captureReadiness") {
			t.Fatalf("%s: the present list moved:\n%s\n%s", name, before, after)
		}
	}
	// A report in which no platform answered stays one: nil is "nobody established anything".
	if report := farm.DeclarePhoneCapture(farm.BuildReport(nil), func(farm.WirePlatform, string) string { return farm.PhoneCaptureReady }); report.Present != nil {
		t.Fatal("a declaration must not invent a present list")
	}
}

func TestADeclarationDoesNotWriteThroughToTheReportItCameFrom(t *testing.T) {
	original := machineWithPhones()
	_ = farm.DeclarePhoneCapture(original, func(farm.WirePlatform, string) string { return farm.PhoneCaptureReady })
	for _, phone := range *original.Present {
		if phone.CaptureReadiness != nil {
			t.Fatalf("the report it was given changed: %+v", phone)
		}
	}
}
