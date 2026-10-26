package android_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
)

// getpropReads discovers, from a probe source, every property this probe reads per device. The population is
// WALKED, never listed — a fifth getprop added tomorrow must appear here without anybody editing a test.
func getpropReads(source string) map[string]bool {
	found := map[string]bool{}
	for _, m := range regexp.MustCompile(`getprop\(ctx, [A-Za-z.]+, "([^"]+)"\)`).FindAllStringSubmatch(source, -1) {
		found[m[1]] = true
	}
	return found
}

// THE CENSUS (sweep b): every value the probe re-measures per tick is classified — what it costs, and whether
// anything would notice if it were read once.
func TestEveryPerTickReadIsClassified(t *testing.T) {
	source, err := os.ReadFile("probe.go")
	if err != nil {
		t.Fatalf("reading the probe's own source: %v", err)
	}

	// INSTRUMENT GUARD, the WALK: the same discovery over two inputs whose correct answers DIFFER — one constant
	// cannot be right for both, and a walk that stopped walking fails the planted half.
	planted := `p.getprop(ctx, d.ID, "ro.planted.walkguard")`
	for _, input := range []struct {
		name       string
		source     string
		mustHave   string
		mustntHave string
	}{
		{"the real probe", string(source), "ro.product.model", "ro.planted.walkguard"},
		{"a planted source", planted, "ro.planted.walkguard", "ro.product.model"},
	} {
		found := getpropReads(input.source)
		if !found[input.mustHave] {
			t.Fatalf("%s: the walk must see %s", input.name, input.mustHave)
		}
		if found[input.mustntHave] {
			t.Fatalf("%s: the walk must read the source it was handed, not a list beside it", input.name)
		}
	}

	classified := map[string]android.PerTickRead{}
	for _, r := range android.PerTickReads {
		classified[r.Value] = r
	}

	for prop := range getpropReads(string(source)) {
		entry, ok := classified["getprop "+prop]
		if !ok {
			t.Fatalf("getprop %s runs for every attached device every tick — classify it in PerTickReads: "+
				"what it costs, and whether anything would notice if it were read once", prop)
		}
		if entry.Changes == "" || entry.Cost == "" {
			t.Fatalf("getprop %s: say what would have to change for the answer to differ, and what it costs", prop)
		}
	}

	// STALE GUARD: an entry for a read the probe no longer makes describes a codebase that is gone.
	for _, r := range android.PerTickReads {
		if !strings.HasPrefix(r.Value, "getprop ") {
			continue
		}
		if !getpropReads(string(source))[strings.TrimPrefix(r.Value, "getprop ")] {
			t.Fatalf("%q is classified but the probe no longer reads it", r.Value)
		}
	}
}

// The standing fact this slice moved off the per-tick path is recorded as cached; the property reads are recorded
// as NOT cached, because that is a named decision (see pertick.go) and not an accident. Neither half may drift
// without this failing.
func TestTheCachedAndUncachedReadsAreBothStated(t *testing.T) {
	var cached, uncached int
	for _, r := range android.PerTickReads {
		if r.Cached {
			cached++
			if !strings.Contains(r.Value, "settings get") {
				t.Fatalf("the cached read should be the standing device fact, got %q", r.Value)
			}
		} else {
			uncached++
		}
	}
	if cached != 1 {
		t.Fatalf("exactly one read is cached by this slice, found %d", cached)
	}
	if uncached == 0 {
		t.Fatal("the reads still paid every tick must stay visible — that is the sweep's whole point")
	}
}
