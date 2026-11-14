package nthagent

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ── NO MODULE BELOW THE RELEASE THAT FIXES A FLAW GO'S DATABASE NAMES (WHAT_THE_FIRST_TRY_SHOWED) ─────────────────────
//
// CI asks govulncheck on every change, and it fails one whose code reaches a known flaw. It does not fail on a flaw in a
// module the code requires and does not call, and those are the ones a later change can start calling without anyone
// raising the module. So each advisory govulncheck has listed for this module, called or not, is held here by the release
// that fixes it, and go.mod may not fall below it. A row is added when a raise closes one, and never taken out.
var fixedIn = []struct{ module, floor, advisories string }{
	{"github.com/andybalholm/brotli", "v1.2.7", "GO-2026-6888"},
	{"golang.org/x/text", "v0.42.0", "GO-2026-5970, GO-2026-6629"},
	{"golang.org/x/net", "v0.62.0", "GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612, GO-2026-6617, GO-2026-5942, GO-2026-6866, GO-2026-6860"},
	{"golang.org/x/crypto", "v0.59.0", "GO-2026-6303, GO-2026-6354, GO-2026-6355, GO-2026-5932"},
}

func TestNoModuleIsBelowTheReleaseThatFixesItsKnownFlaws(t *testing.T) {
	source, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]string{}
	line := regexp.MustCompile(`(?m)^\s*(?:require\s+)?([a-z0-9][\w./-]*)\s+(v\d+\.\d+\.\d+)\S*`)
	for _, m := range line.FindAllStringSubmatch(string(source), -1) {
		required[m[1]] = m[2]
	}
	if len(required) < 10 {
		t.Fatalf("instrument guard: read %d requirements from go.mod — the reader is not reading it", len(required))
	}
	for _, f := range fixedIn {
		got, ok := required[f.module]
		if !ok {
			t.Errorf("%s is no longer required; take its row out only with the module, and say so", f.module)
			continue
		}
		if below(got, f.floor) {
			t.Errorf("%s is %s, below %s, the release that fixes %s", f.module, got, f.floor, f.advisories)
		}
	}
}

// below says whether version a (vX.Y.Z) is older than b.
func below(a, b string) bool {
	parts := func(v string) [3]int {
		var out [3]int
		for i, p := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
			out[i], _ = strconv.Atoi(p)
		}
		return out
	}
	x, y := parts(a), parts(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func TestTheFloorReaderComparesVersionsAsNumbers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.60.0", "v0.62.0", true}, {"v0.62.0", "v0.62.0", false}, {"v0.100.0", "v0.62.0", false}, {"v1.2.6", "v1.2.7", true}, {"v2.0.0", "v1.9.9", false}} {
		if got := below(c.a, c.b); got != c.want {
			t.Errorf("below(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
