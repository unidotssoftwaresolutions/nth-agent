package liveview_test

import (
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
)

// HOW TO FREE A PORT, ON THIS SYSTEM (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): `lsof` on macOS and Linux; on Windows,
// the program that holds it, named by the owner lookup, or the `netstat` line when Windows would not say.
func TestTheFreePortRemedyIsThisSystems(t *testing.T) {
	holder := func(int) (int, string, bool) { return 4242, "node.exe", true }
	nobody := func(int) (int, string, bool) { return 0, "", false }
	unnamed := func(int) (int, string, bool) { return 4242, "", true }
	for _, c := range []struct {
		system string
		holder func(int) (int, string, bool)
		want   string
	}{
		{"darwin", holder, "free 31002 (lsof -nP -iTCP:31002 -sTCP:LISTEN names what holds it), then restart the agent"},
		{"linux", nil, "free 31002 (lsof -nP -iTCP:31002 -sTCP:LISTEN names what holds it), then restart the agent"},
		{"windows", holder, "free 31002 — node.exe (process 4242) holds it: stop it, then restart the agent"},
		{"windows", unnamed, "free 31002 — a program (process 4242) holds it: stop it, then restart the agent"},
		{"windows", nobody, "free 31002 (netstat -ano -p tcp | findstr :31002 names the process that holds it), then restart the agent"},
	} {
		if got := liveview.FreePortRemedy(c.system, 31002, c.holder); got != c.want {
			t.Errorf("%s: got %q, want %q", c.system, got, c.want)
		}
	}
	judged := liveview.Judge(liveview.Facts{Platform: inventory.Android, System: "windows", Holder: holder,
		Reservation: liveview.Reservation{Derived: 31002, Tried: 3}})
	if judged.Available || judged.Remedy != "free 31002 — node.exe (process 4242) holds it: stop it, then restart the agent" {
		t.Fatalf("Judge says the Windows remedy on Windows: %+v", judged)
	}
}
