package android_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1 — the agent offers the farm only phones on this machine's USB
// cable. adb prints a usb: key for a cabled transport and none for the others: a phone joined with `adb connect`, one
// reached by Wireless debugging (mDNS), and an emulator. Those are offered only when the customer says so.

// fourTransports is one listing with every kind of transport in state "device". Invented to adb's documented shape.
const fourTransports = "List of devices attached\n" +
	"DUMMYUSB01             device usb:1-1 product:p model:m device:d transport_id:1\n" +
	"192.0.2.10:5555        device product:p model:m device:d transport_id:2\n" +
	"adb-DUMMY0001-AbCdEf._adb-tls-connect._tcp device product:p model:m device:d transport_id:3\n" +
	"emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:4\n"

// listingRunner answers `devices -l` with the listing and every other command (getprop, the install reads) with nothing,
// and records which serials it was asked about.
func listingRunner(listing string, asked *[]string) android.Runner {
	return func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "-s" {
			*asked = append(*asked, args[1])
			return "", nil
		}
		return listing, nil
	}
}

func ids(offerable []inventory.Offerable) []string {
	out := []string{}
	for _, d := range offerable {
		out = append(out, d.ID)
	}
	return out
}

func TestOnlyAPhoneOnTheCableIsOfferedByDefault(t *testing.T) {
	var asked []string
	outcome := android.Probe{Run: listingRunner(fourTransports, &asked), Enabled: true}.Outcome(context.Background())

	if got := ids(outcome.Offerable()); !reflect.DeepEqual(got, []string{"DUMMYUSB01"}) {
		t.Fatalf("only the phone on the cable is offered, got %v", got)
	}
	want := []inventory.LeftOut{
		{ID: "192.0.2.10:5555", How: "reached over the network (adb connect)"},
		{ID: "adb-DUMMY0001-AbCdEf._adb-tls-connect._tcp", How: "reached over Wi-Fi (wireless debugging)"},
		{ID: "emulator-5554", How: "an emulator"},
	}
	if got := outcome.LeftOut(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the others are named as left out, each with how it is reached:\n got %+v\nwant %+v", got, want)
	}
	for _, serial := range asked {
		if serial != "DUMMYUSB01" {
			t.Fatalf("a phone that is left out is not asked anything either, but %s was", serial)
		}
	}
	if len(outcome.Unusable()) != 0 {
		t.Fatalf("a left-out phone is not an unusable one: %+v", outcome.Unusable())
	}
}

func TestTheFlagOffersEveryTransport(t *testing.T) {
	var asked []string
	outcome := android.Probe{Run: listingRunner(fourTransports, &asked), Enabled: true, OfferUncabled: true}.Outcome(context.Background())
	if got := ids(outcome.Offerable()); len(got) != 4 || len(outcome.LeftOut()) != 0 {
		t.Fatalf("with %s every transport is offered, got %v and left out %v", inventory.OfferUncabledFlag, got, outcome.LeftOut())
	}
}

func TestTheFlagIsNamedOnce(t *testing.T) {
	if inventory.OfferUncabledFlag != "--offer-uncabled-devices" || strings.Contains(inventory.OfferUncabledFlag, " ") {
		t.Fatalf("the flag's name is part of the customer's command line: %q", inventory.OfferUncabledFlag)
	}
}
