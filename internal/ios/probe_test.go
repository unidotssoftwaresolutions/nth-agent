package ios_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

func lister(devices []ios.Device, err error) ios.Lister {
	return func(context.Context) ([]ios.Device, error) { return devices, err }
}

// THE SLICE'S WHOLE POINT, on the platform that could not express it before: a usbmuxd that is not
// running is COULD-NOT-ASK, never an empty inventory. The error text is the one captured from go-ios
// v1.3.2 on this machine against a dead socket.
func TestAnUnreachableDaemonIsNotAnEmptyList(t *testing.T) {
	outcome := ios.Probe{
		Enabled: true,
		List:    lister(nil, errors.New("dial unix /nonexistent/usbmuxd: connect: no such file or directory")),
	}.Outcome(context.Background())

	if !outcome.IsCouldNotAsk() {
		t.Fatalf("an unreachable daemon is COULD-NOT-ASK, got %s", outcome.KindString())
	}
	if len(outcome.Offerable()) != 0 || len(outcome.Unusable()) != 0 {
		t.Fatal("a failed probe carries no inventory at all")
	}
	if !strings.Contains(outcome.Reason(), "usbmuxd") {
		t.Fatalf("the reason names what could not be asked, got %q", outcome.Reason())
	}
	// NOT `TrimSpace(...) != ""` — inventory.CouldNotAsk supplies that; the marker's absence is what
	// can actually fail here (and the per-probe marker seal below drives every way in).
	if outcome.Remedy() == inventory.MissingRemedyMarker {
		t.Fatalf("this path ships a CouldNotAsk with no remedy of its own: %q", outcome.Remedy())
	}
}

// …and the companion, so the seal above is not vacuous: a HEALTHY daemon with nothing attached is a
// real answer with zero devices — the exact pair go-ios returns on this machine today.
func TestAHealthyDaemonWithNothingAttachedIsAnAnswer(t *testing.T) {
	outcome := ios.Probe{Enabled: true, List: lister(nil, nil)}.Outcome(context.Background())

	if !outcome.IsAnswered() {
		t.Fatalf("nothing attached is an ANSWER, got %s", outcome.KindString())
	}
	if len(outcome.Offerable()) != 0 {
		t.Fatalf("…with zero devices, got %d", len(outcome.Offerable()))
	}
}

// An attached-but-untrusted iPhone is SHOWN with its state and its remedy, and never offered.
func TestAnUntrustedDeviceIsShownAndNeverOffered(t *testing.T) {
	outcome := ios.Probe{Enabled: true, List: lister([]ios.Device{
		{UDID: "00008030-TRUSTED", State: "", Model: "iPhone 15", Connection: "USB"},
		{UDID: "00008030-PENDING", State: "PairingDialogResponsePending", Model: "iPhone 15", Connection: "USB"},
		{UDID: "00008030-LOCKED", State: "PasswordProtected", Model: "iPad", Connection: "USB"},
	}, nil)}.Outcome(context.Background())

	if len(outcome.Offerable()) != 1 || outcome.Offerable()[0].ID != "00008030-TRUSTED" {
		t.Fatalf("only the trusted device is offerable, got %+v", outcome.Offerable())
	}
	if len(outcome.Unusable()) != 2 {
		t.Fatalf("both attached-but-unusable devices must be reported, got %d", len(outcome.Unusable()))
	}
	seen := map[string]string{}
	for _, u := range outcome.Unusable() {
		if u.State != strings.ToLower(u.State) {
			t.Fatalf("states are stored lower-cased verbatim, got %q", u.State)
		}
		// Distinct per state — see the Android twin for why "non-empty" cannot fail here.
		if prior, repeated := seen[u.Remedy]; repeated {
			t.Fatalf("states %q and %q share one remedy", prior, u.State)
		}
		seen[u.Remedy] = u.State
	}
	if outcome.Unusable()[0].Remedy == "" || !strings.Contains(outcome.Unusable()[0].Remedy, "Trust") {
		t.Fatalf("the pending-trust device must be told to tap Trust, got %q", outcome.Unusable()[0].Remedy)
	}
}

// The verbatim-state rule on this platform too: a word go-ios has never printed survives the trip.
func TestAnUnknownIosStateSurvivesVerbatim(t *testing.T) {
	outcome := ios.Probe{Enabled: true, List: lister([]ios.Device{
		{UDID: "00008030-FUTURE", State: "WarpDriveEngaged", Connection: "USB"},
	}, nil)}.Outcome(context.Background())

	if len(outcome.Unusable()) != 1 {
		t.Fatalf("the unknown-state device must be reported, got %d", len(outcome.Unusable()))
	}
	if outcome.Unusable()[0].State != "warpdriveengaged" {
		t.Fatalf("want the platform's own word, got %q", outcome.Unusable()[0].State)
	}
	if strings.Contains(strings.ToLower(outcome.Unusable()[0].Remedy), "other") {
		t.Fatal("an unknown state must not be described as \"other\"")
	}
}

func TestDisabledIosProbeIsNotAsked(t *testing.T) {
	outcome := ios.Probe{Enabled: false, List: lister(nil, errors.New("must not be called"))}.Outcome(context.Background())
	if !outcome.IsNotAsked() {
		t.Fatalf("a switched-off probe is NOT-ASKED, got %s", outcome.KindString())
	}
}

// THE BOUND IS REAL ON THIS PLATFORM TOO. A lister that never returns must produce a CouldNotAsk
// naming the timeout, with a remedy — not a command that hangs. (The wrapper bounds the WAIT, not
// go-ios's uncancellable call; listBounded's comment says exactly what that does and does not stop.)
func TestAListerThatNeverReturnsBecomesACouldNotAsk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	released := make(chan struct{})
	defer close(released) // let the abandoned goroutine finish when the test ends

	done := make(chan struct{})
	var outcome inventory.Outcome
	go func() {
		outcome = ios.Probe{Enabled: true, List: func(context.Context) ([]ios.Device, error) {
			<-released // models a usbmuxd that accepts the connection and never answers
			return nil, nil
		}}.Outcome(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe never returned — the deadline is not enforced on this path")
	}

	if !outcome.IsCouldNotAsk() {
		t.Fatalf("a hung daemon is COULD-NOT-ASK, got %s", outcome.KindString())
	}
	if !strings.Contains(strings.ToLower(outcome.Reason()), "deadline") {
		t.Fatalf("the reason must name the timeout, got %q", outcome.Reason())
	}
	if strings.TrimSpace(outcome.Remedy()) == "" {
		t.Fatal("…and still carry a remedy")
	}
}

// THE MARKER SEAL for this probe — see its Android twin for why a non-empty check proves nothing.
func TestEveryIosCouldNotAskCarriesARealRemedy(t *testing.T) {
	ways := map[string]ios.Lister{
		"no usbmuxd socket": func(context.Context) ([]ios.Device, error) {
			return nil, errors.New("dial unix /nonexistent/usbmuxd: connect: no such file or directory")
		},
		"daemon refuses the connection": func(context.Context) ([]ios.Device, error) {
			return nil, errors.New("connect: connection refused")
		},
		"bare error": func(context.Context) ([]ios.Device, error) { return nil, errors.New("") },
	}
	for name, list := range ways {
		outcome := ios.Probe{Enabled: true, List: list}.Outcome(context.Background())
		if !outcome.IsCouldNotAsk() {
			t.Fatalf("%s: expected COULD-NOT-ASK, got %s", name, outcome.KindString())
		}
		if outcome.Remedy() == inventory.MissingRemedyMarker {
			t.Fatalf("%s: the reader got the marker — this probe ships a CouldNotAsk with no remedy", name)
		}
	}

	// …and the timeout path, which builds its CouldNotAsk through the same constructor.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	released := make(chan struct{})
	defer close(released)
	timedOut := ios.Probe{Enabled: true, List: func(context.Context) ([]ios.Device, error) {
		<-released
		return nil, nil
	}}.Outcome(ctx)
	if timedOut.Remedy() == inventory.MissingRemedyMarker {
		t.Fatal("the timeout path ships a CouldNotAsk with no remedy")
	}
}

// ── A ROW THAT CANNOT BE FILTERED CANNOT BE CHOSEN, the iOS half ───────────────────────────────────────────

func TestAConnectionTypeIsNotAModel(t *testing.T) {
	// usbmuxd's listing establishes the udid and the cable ("USB"); it used to file the cable as the Model,
	// which then reached the farm as the phone's model. Model/OsVersion/FormFactor stay UNSET until a
	// lockdown read (the pairing-probe slice) can establish them; Manufacturer is definitional.
	offerable, _ := ios.Classify([]ios.Device{{UDID: "00008140-0000C0FFEE0000A1", State: "", Connection: "USB"}})
	d := offerable[0]
	if d.Model != "" || d.OsVersion != "" || d.FormFactor != "" {
		t.Fatalf("fields usbmuxd cannot establish must stay unset, got %+v", d)
	}
	if d.Model == "USB" || d.Manufacturer != "Apple" {
		t.Fatalf("the cable is not the model and Apple is the manufacturer, got %+v", d)
	}
}

// ── THE PHONE'S OWN NAME ────────────────────────────────────────────────────────────────────────────────────
//
// The operator's iPhone rendered as "Ios" — the platform's name where the phone's name belongs. usbmuxd's listing
// establishes a udid and a cable type; the name needs a lockdown value read, which UsbmuxdLister now makes.

func TestAnIosDevicesOwnNameAndVersionReachTheOfferable(t *testing.T) {
	offerable, unusable := ios.Classify([]ios.Device{
		{UDID: "00008140-0000C0FFEE0000A1", Model: "Ada's iPhone", OsVersion: "26.2", Connection: "USB"},
	})

	if len(unusable) != 0 {
		t.Fatalf("a paired device is offerable, got %v", unusable)
	}
	if len(offerable) != 1 {
		t.Fatalf("expected one offerable, got %v", offerable)
	}
	if offerable[0].Model != "Ada's iPhone" {
		t.Fatalf("the device's OWN name must survive, got %q", offerable[0].Model)
	}
	if offerable[0].OsVersion != "26.2" {
		t.Fatalf("the device's own ProductVersion must survive, got %q", offerable[0].OsVersion)
	}
	if offerable[0].Manufacturer != "Apple" {
		t.Fatalf("Apple is definitional for a usbmuxd device, got %q", offerable[0].Manufacturer)
	}
}

// AND AN UNANSWERED FIELD STAYS EMPTY — the Android rule, unchanged. A lockdown read that could not be made (an
// untrusted or locked device) leaves the name absent rather than filling it with the platform, the cable type, or
// anything else we happen to know.
func TestAnIosDeviceThatAnsweredNothingKeepsAnEmptyName(t *testing.T) {
	offerable, _ := ios.Classify([]ios.Device{{UDID: "00008030-ABC", Connection: "USB"}})

	if len(offerable) != 1 {
		t.Fatalf("expected one offerable, got %v", offerable)
	}
	if offerable[0].Model != "" {
		t.Fatalf("an unestablished name must stay EMPTY — never the cable type or the platform, got %q", offerable[0].Model)
	}
	if offerable[0].OsVersion != "" {
		t.Fatalf("an unestablished version must stay EMPTY, got %q", offerable[0].OsVersion)
	}
}
