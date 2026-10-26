package connect_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── THE MACHINE'S CAPTURE DECLARATION: sent at registration, and registered anew the tick it changes ────────────

// declarations answers a scripted declaration per call — `capture enable` or `disable` typed in another terminal while
// the agent runs.
type declarations struct {
	mu     sync.Mutex
	script []string
	calls  int
}

func (d *declarations) Availability() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	answer := d.script[min(d.calls, len(d.script)-1)]
	d.calls++
	return answer
}

func captureOf(t *testing.T, body []byte) string {
	t.Helper()
	var register struct {
		Capture *string `json:"captureAvailability"`
	}
	if err := json.Unmarshal(body, &register); err != nil || register.Capture == nil {
		t.Fatalf("the register body must declare capture: %v %s", err, body)
	}
	return *register.Capture
}

func TestTheRegistrationCarriesTheMachinesCaptureDeclaration(t *testing.T) {
	for _, declared := range []string{farm.CaptureDisabled, farm.CaptureAvailable, farm.CaptureUnavailable} {
		stub, server := newStubFarm(t)
		var out strings.Builder
		cfg := config(server, &out, 1)
		cfg.Capture = &declarations{script: []string{declared}}
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := captureOf(t, stub.lastRegisterBody.Load().([]byte)); got != declared {
			t.Fatalf("declared %q, registered %q", declared, got)
		}
	}
}

func TestCaptureTurnedOnWhileTheAgentRunsRegistersTheNodeAnew(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 4)
	// Off at the door and through the first tick; on from the second tick (`capture enable` in another terminal).
	cfg.Capture = &declarations{script: []string{farm.CaptureDisabled, farm.CaptureDisabled, farm.CaptureDisabled,
		farm.CaptureAvailable}}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stub.registers.Load() != 2 {
		t.Fatalf("the node must register once at the door and once when capture changed, registered %d times:\n%s", stub.registers.Load(), out.String())
	}
	if got := captureOf(t, stub.lastRegisterBody.Load().([]byte)); got != farm.CaptureAvailable {
		t.Fatalf("the farm must hear the new declaration, got %q", got)
	}
	if strings.Count(out.String(), "network capture is now available on this machine — registering this node anew so the farm hears it") != 1 {
		t.Fatalf("the console says why it registered, once:\n%s", out.String())
	}
}

func TestADeclarationThatDoesNotChangeRegistersOnce(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 5)
	cfg.Capture = &declarations{script: []string{farm.CaptureAvailable}}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stub.registers.Load() != 1 || strings.Contains(out.String(), "registering this node anew") {
		t.Fatalf("registered %d times:\n%s", stub.registers.Load(), out.String())
	}
}

// ── AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE rule 1: each present phone's readiness rides registration AND every heartbeat ──

// phoneDeclarations is a machine with capture on that answers per phone, as capture.Controller does.
type phoneDeclarations struct {
	declarations
	phones map[string]string
}

func (d *phoneDeclarations) Readiness(serial string) string { return d.phones[serial] }

func readinessIn(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var wire struct {
		Present []struct {
			ID        string  `json:"id"`
			Readiness *string `json:"captureReadiness"`
		} `json:"presentDevices"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	said := map[string]string{}
	for _, phone := range wire.Present {
		if phone.Readiness != nil {
			said[phone.ID] = *phone.Readiness
		}
	}
	return said
}

func TestEachPresentPhonesReadinessRidesRegistrationAndEveryHeartbeat(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = func(context.Context) []inventory.Outcome {
		return []inventory.Outcome{inventory.Answered(inventory.Android,
			[]inventory.Offerable{{ID: "R5CT30READY"}, {ID: "R5CT30NOTSET"}, {ID: "R5CT30QUIET"}}, nil)}
	}
	cfg.Capture = &phoneDeclarations{declarations: declarations{script: []string{farm.CaptureAvailable}},
		phones: map[string]string{"R5CT30READY": farm.PhoneCaptureReady, "R5CT30NOTSET": farm.PhoneCaptureNotSetUp}}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := map[string]string{"R5CT30READY": "Ready", "R5CT30NOTSET": "NotSetUp"}
	for name, body := range map[string][]byte{
		"registration":  stub.lastRegisterBody.Load().([]byte),
		"the heartbeat": stub.lastHeartbeat.Load().([]byte),
	} {
		said := readinessIn(t, body)
		if len(said) != 2 || said["R5CT30READY"] != want["R5CT30READY"] || said["R5CT30NOTSET"] != want["R5CT30NOTSET"] {
			t.Fatalf("%s must carry %v, got %v: %s", name, want, said, body)
		}
	}
}

func TestADeclarationWithoutPhonesSaysNothingPerPhone(t *testing.T) {
	// An older build's declaration (Availability only) leaves every present phone without the field: today's wire.
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Capture = &declarations{script: []string{farm.CaptureAvailable}}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if body := stub.lastRegisterBody.Load().([]byte); strings.Contains(string(body), "captureReadiness") {
		t.Fatalf("no phone may say a readiness nobody declared: %s", body)
	}
}
