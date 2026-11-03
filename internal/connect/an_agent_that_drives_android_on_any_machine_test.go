package connect_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// iOS ON WINDOWS (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): the probe set never asks usbmuxd there, and connect says
// so ONCE, in its start banner, as a note — never as a problem, and never again on later ticks.
func TestOnWindowsConnectSaysOnceThatIOSNeedsAMac(t *testing.T) {
	asked := 0
	list := func(context.Context) ([]ios.Device, error) { asked++; return nil, nil }
	adb := func(_ context.Context, args ...string) (string, error) {
		if len(args) == 2 && args[0] == "devices" {
			return "List of devices attached\nemulator-5554 device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1\n", nil
		}
		if len(args) == 5 && args[4] == "ro.boot.qemu.avd_name" {
			return "Pixel_8_API_35\n", nil
		}
		return "", nil
	}
	probes := connect.ProbeSetFor("windows", map[inventory.Platform]bool{}, adb, list, time.Second, false)

	outcomes := probes(context.Background())
	if asked != 0 || len(outcomes) != 2 || outcomes[1].NotHereBecause() != ios.NeedsAMac {
		t.Fatalf("on Windows the iOS probe never runs (asked %d): %+v", asked, outcomes)
	}
	if offered := outcomes[0].Offerable(); len(offered) != 1 || !offered[0].Emulator || offered[0].AVD != "Pixel_8_API_35" {
		t.Fatalf("the emulator on this machine is offered: %+v", offered)
	}

	_, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 3)
	cfg.Probes = probes
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.String()
	if strings.Count(text, "iOS: needs a Mac") != 1 || !strings.Contains(text, "Serves    Android · iOS: needs a Mac\n") {
		t.Fatalf("the banner says once that iOS needs a Mac:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "iOS") && (strings.HasPrefix(line, "✗") || strings.HasPrefix(line, "!") || strings.Contains(line, "not checked")) {
			t.Fatalf("iOS on Windows is never said as a problem: %q in\n%s", line, text)
		}
	}
	if asked != 0 {
		t.Fatalf("usbmuxd was asked %d times over three ticks on Windows", asked)
	}
}

// EVERY SCREEN WRITES Android AND iOS: connect's platform lines did say `android:` and `ios:`.
func TestConnectWritesThePlatformsAsAPersonWritesThem(t *testing.T) {
	_, server := newStubFarm(t)
	var out strings.Builder
	if err := connect.Run(context.Background(), config(server, &out, 2)); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Android: ") || !strings.Contains(text, "iOS: ") || strings.Contains(text, "android: ") || strings.Contains(text, "ios: ") {
		t.Fatalf("connect writes Android and iOS:\n%s", text)
	}
}
