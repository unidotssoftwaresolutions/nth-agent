package connect_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

func driverOutcomes(ctx context.Context) []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT30ABCDE", Model: "SM_A546B"}}, nil),
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030-HEALTHY", Model: "iPhone 15"}},
			[]inventory.Unusable{inventory.NewUnusable("00008030-PENDING", "PairingDialogResponsePending", "tap Trust")}),
	}
}

// ── THE PROBE-BEFORE-OFFER SEAL: a flag is a claim; a dead Appium offers NOTHING ────────────────────────────

func TestADeadAppiumOffersNoDevices(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Probes = driverOutcomes
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:1/",
		Probe:     func(context.Context) error { return errors.New("dial tcp 127.0.0.1:1: connect: connection refused") },
		TunnelUp:  func() bool { return true },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	var body map[string]any
	json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body)
	if devices := body["devices"].([]any); len(devices) != 0 {
		t.Fatalf("--appium at a dead endpoint must not re-create the phantom by configuration, got %v", devices)
	}
	if !strings.Contains(out.String(), "not answering") || !strings.Contains(out.String(), "what to do") {
		t.Fatalf("the refusal carries a reason and a remedy, got %q", out.String())
	}
	// THE STATED LIMITATION STILL REACHES THE CUSTOMER AT THE REFUSAL — what CHANGED (2026-08-23) is what it
	// says. It used to promise that "the bundled-runtime slice removes the prerequisite"; we decided not to
	// bundle a runtime, because we do not put software we do not own on hardware we do not own. So the sentence
	// now names the prerequisite as permanent and points at the command that reports what is missing. This
	// assertion moved rather than went: a refusal with no limitation stated is the silence, one level down.
	if !strings.Contains(out.String(), "prerequisite and stays one") {
		t.Fatalf("the refusal must state the limitation as the standing one it is, got %q", out.String())
	}
	if !strings.Contains(out.String(), "npx nthcase-agent@latest doctor") {
		t.Fatalf("the refusal must point at the command that says what this machine is missing, got %q", out.String())
	}
	if strings.Contains(strings.ToLower(out.String()), "bundled") {
		t.Fatalf("a promise we decided not to keep may not survive here either, got %q", out.String())
	}
}

// ── THE DRIVABLE-MEANS-REACHABLE SEAL: no tunnel, no offer — and the flag flipping empties the report ──────

func TestDevicesAreOfferedOnlyWhileTheTunnelIsUp(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = driverOutcomes
	// A SIGNING IDENTITY IS NOW PART OF "EVERYTHING UP" FOR AN iPHONE (AN_IPHONE_YOU_CAN_ACTUALLY_RUN): this
	// fixture's second device is an iPhone, and a machine that declares no identity no longer offers one. The
	// seal here is about the TUNNEL, so the identity is declared and the tunnel is what moves.
	cfg.IosTeamID = "TEAM123456"
	cfg.IosWdaBundleID = "com.example.WebDriverAgentRunner"
	// Registration and tick 1: everything up — the devices are REAL rows with the Appium endpoint on them.
	// Tick 2: the tunnel "drops" — the next report carries no rows, which is what lets the farm's reconcile
	// offline them (the bounded path).
	//
	// THE DROP IS READ FROM THE FARM'S OWN COUNT, NOT FLIPPED BY A GOROUTINE. The stub counts a heartbeat before
	// it answers it, and the loop builds tick 2's report only after tick 1's answer has arrived, so the tunnel
	// is down for tick 2 on every run. It used to be a goroutine that spun on that count and then flipped a
	// flag, racing tick 2's report; on a loaded runner the goroutine lost and heartbeat 2 still carried both
	// devices. CI failed that way on f0e0294, and with four CPU burners beside it the old shape failed 123 runs
	// of 300 under -race.
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723",
		Probe:     func(context.Context) error { return nil },
		TunnelUp:  func() bool { return stub.heartbeats.Load() < 1 },
	}

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	var first map[string]any
	json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &first)
	devices := first["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("with Appium answering and the tunnel up, both offerable devices are registered, got %d", len(devices))
	}
	android := devices[0].(map[string]any)
	descriptor := android["descriptor"].(map[string]any)
	if android["localId"] != "R5CT30ABCDE" || descriptor["udid"] != "R5CT30ABCDE" ||
		descriptor["appiumUrl"] != "http://127.0.0.1:4723" || descriptor["status"] != "Available" || descriptor["kind"] != "Physical" {
		t.Fatalf("the registration must be real and complete, got %v", android)
	}

	var last map[string]any
	json.Unmarshal(stub.lastHeartbeat.Load().([]byte), &last)
	if lastDevices := last["devices"].([]any); len(lastDevices) != 0 {
		t.Fatalf("a dropped tunnel must empty the next report — the farm offlines the rows from exactly that absence, got %v", lastDevices)
	}
}

// ── THE UNCHANGED-DEFAULT SEAL: without --appium, yesterday's report, byte for byte ─────────────────────────

func TestWithoutAppiumTheReportIsByteForByteYesterdays(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Probes = driverOutcomes
	cfg.Driver = nil // no --appium

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	// THE GOLDEN BYTES — the register body THE_AGENT_KNOCKS shipped, for these outcomes, verbatim.
	// A driver slice that changes one byte of the no-driver path fails here.
	// GOLDEN MOVED ONCE, 2026-08-22 (WE_NEVER_REMOVE_WHAT_WE_DID_NOT_INSTALL item 4): the body gained
	// "captureAvailability":"Unavailable" — this agent cannot run network capture and now SAYS so, so the farm
	// withholds the HAR pull URL with a named reason instead of promising one it would refuse. It is a standing
	// fact about this binary, not about --appium, so the no-driver body carries it too. Everything else is
	// unchanged: devices still [], the outcomes and the unusable list identical.
	// GOLDEN MOVED A THIRD TIME, 2026-08-23 (A_PHONE_WE_CANNOT_DRIVE_IS_STILL_THEIR_PHONE item 1): the body
	// gained "presentDevices" — and this diff IS the defect it fixes. Both phones were already in this golden
	// as the integer 1 inside an offerableCount; now they have names. devices stays [] (no --appium here), so
	// this is precisely the state that had none: present, healthy, and un-offerable.
	// GOLDEN MOVED AGAIN, 2026-08-23 (A_RUN_THAT_CANNOT_BE_WATCHED item 1): the body gained
	// "liveViewAvailability":"Unavailable" — the same sentence about a different capability. This agent refuses
	// every stream-start frame, so it says so and a consumer is told "no live view, and why" instead of being
	// handed a mirror that never fills. Standing fact about this binary, so the no-driver body carries it too;
	// it rides AFTER captureAvailability because the two are separate declarations, not one.
	// GOLDEN MOVED AGAIN, 2026-10-04 (AN_AGENT_THAT_CAN_RECORD_ITS_PHONE rule 2): "captureAvailability" went from
	// "Unavailable" to "Disabled". This binary CAN capture now — once a person here turns it on — so "cannot" stopped
	// being true, and the farm's own word for "off on this machine" is Disabled (NodeCaptureAvailability), which it
	// answers with NodeCaptureDisabled instead of NodeCannotCapture. Off is the default, so the body without a capture
	// controller says it. One word moved; every other byte is yesterday's.
	expected := `{"hostName":"omers-macbook","devices":[],"nodeId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",` +
		`"unusableDevices":[{"serial":"00008030-PENDING","state":"pairingdialogresponsepending","platform":"Ios","remedy":"tap Trust"}],` +
		`"platformOutcomes":[{"platform":"Android","status":"Answered","offerableCount":1},` +
		`{"platform":"Ios","status":"Answered","offerableCount":1}],"captureAvailability":"Disabled",` +
		`"liveViewAvailability":"Unavailable",` +
		`"presentDevices":[{"id":"R5CT30ABCDE","platform":"Android","model":"SM_A546B"},` +
		`{"id":"00008030-HEALTHY","platform":"Ios","model":"iPhone 15"}]}`
	got := string(stub.lastRegisterBody.Load().([]byte))
	if got != expected {
		t.Fatalf("the customer without Appium must see EXACTLY yesterday's bytes.\nwant %s\ngot  %s", expected, got)
	}
}

// ── REACHABILITY IS A FACT ABOUT THE NODE, NOT ABOUT THIS POD ──────────────────────────────────────────────

// A configured driver declares tunnel-only on EVERY tick — register and heartbeat — and the declaration is
// about the machine's shape, so it is made even on a tick where the Appium probe fails.
func TestADriverDeclaresTunnelOnlyOnEveryTick(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = driverOutcomes
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:1/",
		Probe:     func(context.Context) error { return errors.New("connection refused") }, // down — still declares
		TunnelUp:  func() bool { return false },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	for name, raw := range map[string][]byte{
		"register":  stub.lastRegisterBody.Load().([]byte),
		"heartbeat": stub.lastHeartbeat.Load().([]byte),
	} {
		var body map[string]any
		json.Unmarshal(raw, &body)
		if body["reachability"] != "TunnelOnly" {
			t.Fatalf("%s must carry the declaration derived from the driver, got %v", name, body["reachability"])
		}
		if devices := body["devices"].([]any); len(devices) != 0 {
			t.Fatalf("%s: declaring does not offer — the probe gate still holds, got %v", name, devices)
		}
	}
}

// THE ONE-HOME SEAL: the declaration has exactly one source — the driver's existence, which main creates only
// where it dials the tunnel. No second switch exists that could disagree with dialling.
func TestTheDeclarationHasOneHome(t *testing.T) {
	// (a) connect.Config carries no reachability switch: the only way to make the agent declare is a Driver.
	cfgType := reflect.TypeOf(connect.Config{})
	for i := 0; i < cfgType.NumField(); i++ {
		name := strings.ToLower(cfgType.Field(i).Name)
		if strings.Contains(name, "reach") || strings.Contains(name, "tunnel") {
			t.Fatalf("connect.Config gained a second home for the declaration: field %q — derive it from Driver", cfgType.Field(i).Name)
		}
	}

	// (b) main makes no declaration of its own (a labelled SOURCE PIN): it configures the driver and dials the
	// tunnel in ONE block under ONE condition, and leaves the declaration to connect.buildReport.
	main := string(mustRead(t, "../../cmd/nth-agent/main.go"))
	for _, forbidden := range []string{"Reachability", "TunnelOnly", "DeclareTunnelOnly"} {
		if strings.Contains(main, forbidden) {
			t.Fatalf("main.go mentions %q — the declaration must be derived in connect from the driver, not configured here", forbidden)
		}
	}
	gate := strings.Index(main, `if appiumURL != ""`)
	if gate < 0 || strings.Count(main, `if appiumURL != ""`) != 1 {
		t.Fatal("main.go must gate the driver on exactly one condition: the --appium flag")
	}
	tunnelDial := strings.Index(main, "tunnel.New(")
	driverBuild := strings.Index(main, "&connect.Driver{")
	if tunnelDial < gate || driverBuild < gate {
		t.Fatal("the tunnel dial and the driver must both sit inside the --appium block — one condition, one home")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
