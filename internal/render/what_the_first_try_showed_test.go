package render_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// WHAT_THE_FIRST_TRY_SHOWED. With no iPhone on the cable (one was paired over Wi-Fi), `devices` said "iOS: no devices
// attached", while the farm's Devices page, quoting this agent's own registration, said "iPhones are attached and are not
// offered …": the registration added the signing sentence to every iOS answer of a machine with no signing identity,
// iPhone or not. The two now reach one answer from one probe: the sentence only when an iPhone this machine would offer
// is held back for want of an identity.
func TestDevicesAndTheFarmSayTheSameAboutIphones(t *testing.T) {
	android := inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1"}}, nil)
	onTheCable := inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008140-0000C0FFEE0000A1", Model: "iPhone"}}, nil)
	for _, c := range []struct {
		name     string
		ios      inventory.Outcome
		identity bool
		devices  string // what devices says about iOS
		caveat   bool   // whether the registration carries the signing sentence
	}{
		{"no iPhone at all", inventory.Answered(inventory.IOS, nil, nil), false, "iOS: no devices attached", false},
		{"an iPhone only on Wi-Fi, left out", inventory.WithLeftOut(inventory.Answered(inventory.IOS, nil, nil),
			[]inventory.LeftOut{{ID: "00008110-0000C0FFEE0000B2", How: "reached over Wi-Fi"}}), false, "iOS: 0 usable", false},
		{"an iPhone on the cable, no identity", onTheCable, false, "iOS: 1 usable", true},
		{"an iPhone on the cable, an identity", onTheCable, true, "iOS: 1 usable", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			outcomes := []inventory.Outcome{android, c.ios}
			var out strings.Builder
			if err := render.Human(&out, outcomes, nil); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), c.devices) {
				t.Fatalf("devices must say %q:\n%s", c.devices, out.String())
			}
			report := farm.BuildReport(outcomes)
			if c.identity {
				report = farm.DeclareIosSigning(report, "TEAM123456", "com.example.WebDriverAgentRunner", "")
			}
			report = farm.AttachDevices(report, outcomes, "http://127.0.0.1:4723", nil, nil, nil)
			said := ""
			for _, o := range report.Outcomes {
				if o.Platform == farm.WireIos && o.Reason != nil {
					said = *o.Reason
				}
			}
			if got := strings.Contains(said, farm.IosSigningReason); got != c.caveat {
				t.Fatalf("the registration's iOS answer carries the signing sentence: %v, want %v (devices said %q):\n%q",
					got, c.caveat, c.devices, said)
			}
			if strings.Contains(out.String(), "attached and are not offered") {
				t.Fatalf("devices never says the farm's sentence:\n%s", out.String())
			}
		})
	}
}
