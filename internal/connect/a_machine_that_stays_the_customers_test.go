package connect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — an Appium port another account holds offers nothing, and connect says why
// in words that send the person to the right place: not "start Appium there", which is running already.
func TestAnAppiumAnotherAccountRunsOffersNothingAndSaysWhose(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Probes = driverOutcomes
	refused := &localconn.NotThisAccount{What: "your Appium", Address: "127.0.0.1:4723", UID: 1002, Found: true}
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723",
		Probe:     func(context.Context) error { return fmt.Errorf("Get %q: %w", "http://127.0.0.1:4723/status", refused) },
		TunnelUp:  func() bool { return true },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body)
	if devices := body["devices"].([]any); len(devices) != 0 {
		t.Fatalf("an Appium another account runs must offer nothing, got %v", devices)
	}
	for _, want := range []string{
		"the Appium endpoint at http://127.0.0.1:4723 was not used: the program on 127.0.0.1:4723 runs as another account on this machine (uid 1002)",
		"start Appium yourself, as this account, on that port",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("connect says %q, got:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "is not answering") {
		t.Fatalf("it answered; it is not yours:\n%s", out.String())
	}
}

// DECISION 1: a phone that is not on the cable is not offered, and connect says which one and names the flag — once, not
// every ten seconds.
func TestALeftOutPhoneIsNamedOnceWithTheFlag(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 3)
	cfg.Probes = func(context.Context) []inventory.Outcome {
		return []inventory.Outcome{
			inventory.WithLeftOut(inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT30ABCDE"}}, nil),
				[]inventory.LeftOut{{ID: "emulator-5554", How: "an emulator"}, {ID: "192.0.2.10:5555", How: "reached over the network (adb connect)"}}),
			inventory.WithLeftOut(inventory.Answered(inventory.IOS, nil, nil),
				[]inventory.LeftOut{{ID: "00008030-WIFI", How: "reached over Wi-Fi or the network"}}),
		}
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"not offered, because the agent offers the farm only phones on this machine's USB cable:",
		"  android emulator-5554 — an emulator",
		"  android 192.0.2.10:5555 — reached over the network (adb connect)",
		"  ios 00008030-WIFI — reached over Wi-Fi or the network",
		"  to offer these too, restart with: npx nthcase-agent@latest connect … --offer-uncabled-devices",
	} {
		if strings.Count(text, want) != 1 {
			t.Fatalf("connect says %q exactly once over three ticks, got:\n%s", want, text)
		}
	}
	var body map[string]any
	_ = json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body)
	if strings.Contains(string(stub.lastRegisterBody.Load().([]byte)), "emulator-5554") ||
		strings.Contains(string(stub.lastRegisterBody.Load().([]byte)), "00008030-WIFI") {
		t.Fatalf("a left-out phone is never named to the farm: %s", stub.lastRegisterBody.Load())
	}
}

// the advice on a register 404 led to two connects on one folder — it said to remove node-id while the first
// connect was still running. It now says to stop every running connect first.
func TestThe404AdviceSaysToStopTheRunningConnectFirst(t *testing.T) {
	stub, server := newStubFarm(t)
	stub.registerStatus.Store(http.StatusNotFound)
	var out strings.Builder
	cfg := config(server, &out, 1)
	_ = connect.Run(context.Background(), cfg)
	text := out.String()
	stop := strings.Index(text, "stop every npx nthcase-agent connect running on this machine")
	remove := strings.Index(text, "remove the node-id file")
	if stop < 0 || remove < 0 || stop > remove {
		t.Fatalf("the advice says to stop the running connect before removing the identity:\n%s", text)
	}
}
