package ios_test

import (
	"context"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// iOS ON WINDOWS IS NEVER PROBED (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): the probe answers that iOS needs a Mac
// without asking usbmuxd anything — on Linux and macOS it asks as before.
func TestOnWindowsTheIOSProbeNeverRuns(t *testing.T) {
	asked := 0
	list := func(context.Context) ([]ios.Device, error) { asked++; return nil, nil }

	outcome := ios.Probe{List: list, Enabled: true, System: "windows"}.Outcome(context.Background())
	if asked != 0 {
		t.Fatalf("usbmuxd was asked %d times on Windows", asked)
	}
	if !outcome.IsNotAsked() || outcome.NotHereBecause() != "needs a Mac" || outcome.IsCouldNotAsk() {
		t.Fatalf("iOS on Windows is not asked, because it needs a Mac — never a failure: %+v", outcome)
	}

	for _, system := range []string{"darwin", "linux"} {
		asked = 0
		outcome := ios.Probe{List: list, Enabled: true, System: system}.Outcome(context.Background())
		if asked != 1 || !outcome.IsAnswered() || outcome.NotHereBecause() != "" {
			t.Fatalf("on %s the probe asks usbmuxd as before: asked %d, %+v", system, asked, outcome)
		}
	}
}
