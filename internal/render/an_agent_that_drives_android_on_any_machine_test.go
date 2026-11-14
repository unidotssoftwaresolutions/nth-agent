package render_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE: on Windows `devices` shows the emulator by its virtual device's name, as an
// Android emulator, and says iOS needs a Mac as a note; a live view that cannot open is a problem in three parts.
func TestDevicesOnWindowsShowsTheEmulatorAndThatIOSNeedsAMac(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{
			{ID: "emulator-5554", Model: "sdk_gphone64_arm64", OsVersion: "15", Emulator: true, AVD: "Pixel_8_API_35"},
			{ID: "R5CT30ABCDE", Model: "SM-A546B", OsVersion: "14"},
		}, nil),
		inventory.NotHere(inventory.IOS, "needs a Mac"),
	}
	held := func(_ inventory.Platform, id string) liveview.Availability {
		if id == "R5CT30ABCDE" {
			return liveview.Availability{Reason: "no mirror port was free on this machine",
				Remedy: liveview.FreePortRemedy("windows", 31002, func(int) (int, string, bool) { return 4242, "node.exe", true })}
		}
		return liveview.Availability{Available: true, Port: 35120, Reason: "its mirror is 127.0.0.1:35120, reserved on this machine for this phone"}
	}
	var out strings.Builder
	if err := render.Human(&out, outcomes, held); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"Pixel_8_API_35  Android emulator  15  ready to offer",
		"SM-A546B        Android           14  ready to offer",
		"· iOS: needs a Mac\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("devices says %q:\n%s", want, text)
		}
	}
	// The problem's three parts, compared with the report's folding taken out.
	flat := strings.Join(strings.Fields(text), " ")
	if !strings.Contains(flat, "! SM-A546B: no live view — no mirror port was free on this machine "+
		"Runs on it are still driven; nobody can watch them live. "+
		"→ free 31002 — node.exe (process 4242) holds it: stop it, then restart the agent") {
		t.Fatalf("a live view that cannot open is said in three parts, with this system's remedy:\n%s", text)
	}
	if strings.Contains(text, "what to do:") || strings.Contains(text, "✗ iOS") {
		t.Fatalf("no inline what-to-do, and iOS on Windows is no failure:\n%s", text)
	}

	var machine strings.Builder
	if err := render.JSON(&machine, outcomes, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"emulator": true`, `"avd": "Pixel_8_API_35"`, `"outcome": "not_asked"`, `"reason": "needs a Mac"`} {
		if !strings.Contains(machine.String(), want) {
			t.Fatalf("devices --json carries %s:\n%s", want, machine.String())
		}
	}
	if strings.Count(machine.String(), `"emulator"`) != 1 {
		t.Fatalf("only the emulator says it is one:\n%s", machine.String())
	}
}
