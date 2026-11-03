package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// ── AN_AGENT_THAT_SPEAKS_PLAINLY: `devices`, as golden text ──────────────────────────────────────────────────────────
//
// Two variants of one screen, plain and coloured (golden_colour_test.go), escape codes written out as `\x1b`. The phones:
// two offered Android phones with a live view each, one unauthorized, one emulator left out for not being on the cable,
// and an iPhone platform that could not be asked. `-update` rewrites them.

var update = flag.Bool("update", false, "rewrite the golden screens under testdata")

func golden(t *testing.T, name, got string) {
	t.Helper()
	visible := strings.NewReplacer("\x1b", `\x1b`, "\r", `\r`).Replace(got)
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(visible), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden screen %s (run with -update to write it): %v", path, err)
	}
	if visible != string(want) {
		t.Fatalf("%s: the screen changed.\n got:\n%s\nwant:\n%s", name, visible, want)
	}
}

func goldenDevices() []inventory.Outcome {
	return []inventory.Outcome{
		inventory.WithLeftOut(inventory.Answered(inventory.Android,
			[]inventory.Offerable{
				{ID: "R5CT30ABCDE", Model: "SM-A546B", OsVersion: "14"},
				{ID: "39051FDJH00ABC", Model: "Pixel 8", OsVersion: "15"},
			},
			[]inventory.Unusable{inventory.NewUnusable("R5CT32ABCDE", "unauthorized", "Tap Allow on the phone's USB debugging prompt, then re-run.")}),
			[]inventory.LeftOut{{ID: "emulator-5554", How: "an emulator"}}),
		inventory.CouldNotAsk(inventory.IOS, "usbmuxd did not answer within 20s", "unplug the iPhone and plug it back in, then re-run"),
	}
}

func goldenLiveView(_ inventory.Platform, id string) liveview.Availability {
	if id == "R5CT30ABCDE" {
		return liveview.Availability{Available: true, Port: 35118, Reason: "its mirror is 127.0.0.1:35118, reserved on this machine for this phone"}
	}
	return liveview.Availability{Reason: "no mirror port was free on this machine", Remedy: "free 31002 (lsof -nP -iTCP:31002 -sTCP:LISTEN names what holds it), then restart the agent"}
}

func TestGoldenDevicesOfferedAndLeftOutPlain(t *testing.T) {
	var out strings.Builder
	if err := render.Human(&out, goldenDevices(), goldenLiveView); err != nil {
		t.Fatal(err)
	}
	golden(t, "devices-mixed.plain.golden", out.String())
}
