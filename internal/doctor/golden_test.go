package doctor

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── AN_AGENT_THAT_SPEAKS_PLAINLY: the doctor's main screen, as golden text ─────────────────────────────────────────
//
// Two variants of one screen: plain (a pipe, a file, NO_COLOR) and coloured (a terminal), the escape codes written out as
// `\x1b` so a person reading the golden sees exactly what a terminal is sent. The fixture is tonight(): a blocker, an
// unknown, the advisory, greens with caveats, and a check this machine does not need.
//
// `go test ./internal/doctor -run Golden -update` rewrites them; a diff in review is then the change a person will see.

var update = flag.Bool("update", false, "rewrite the golden screens under testdata")

// golden compares got with testdata/name, escape codes and carriage returns written out.
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

func TestGoldenDoctorWithAMixOfFindingsPlain(t *testing.T) {
	var out strings.Builder
	if err := Human(&out, tonight(), Options{}); err != nil {
		t.Fatal(err)
	}
	golden(t, "doctor-mixed.plain.golden", out.String())
}
