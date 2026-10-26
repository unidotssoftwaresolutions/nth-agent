package ios_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1 — usbmuxd names a phone it reaches over Wi-Fi ("Network") beside
// the ones on the cable ("USB"). Only the cabled ones are offered, and named to the farm, unless the customer says so; a
// phone listed both ways is one phone, on its cable.
func TestOnlyAnIphoneOnTheCableIsOfferedByDefault(t *testing.T) {
	list := func(context.Context) ([]ios.Device, error) {
		return []ios.Device{
			{UDID: "00008030-CABLED", Connection: "USB", Model: "iPhone 15"},
			{UDID: "00008030-WIFI", Connection: "Network", Model: "A person's iPhone"},
			{UDID: "00008030-BOTH", Connection: "Network"},
			{UDID: "00008030-BOTH", Connection: "USB", Model: "iPhone 14"},
			{UDID: "00008030-NOWORD", Connection: ""},
		}, nil
	}
	outcome := ios.Probe{List: list, Enabled: true}.Outcome(context.Background())
	got := []string{}
	for _, d := range outcome.Offerable() {
		got = append(got, d.ID+"/"+d.Model)
	}
	if !reflect.DeepEqual(got, []string{"00008030-CABLED/iPhone 15", "00008030-BOTH/iPhone 14"}) {
		t.Fatalf("only phones on the cable are offered, each once: %v", got)
	}
	want := []inventory.LeftOut{
		{ID: "00008030-WIFI", How: "reached over Wi-Fi or the network"},
		{ID: "00008030-NOWORD", How: "not on a USB cable, as far as usbmuxd says"},
	}
	if left := outcome.LeftOut(); !reflect.DeepEqual(left, want) {
		t.Fatalf("the others are named as left out:\n got %+v\nwant %+v", left, want)
	}

	all := ios.Probe{List: list, Enabled: true, OfferUncabled: true}.Outcome(context.Background())
	if len(all.Offerable()) != 4 || len(all.LeftOut()) != 0 {
		t.Fatalf("with the flag every phone is offered, still once each: %+v", all.Offerable())
	}
}
