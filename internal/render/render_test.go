package render_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// one outcome of each kind, built through the constructors (the only way to build one).
func samples() map[string]inventory.Outcome {
	return map[string]inventory.Outcome{
		"answered": inventory.Answered(inventory.Android,
			[]inventory.Offerable{{ID: "R5CT30ABCDE", Model: "SM_A546B"}},
			[]inventory.Unusable{inventory.NewUnusable("R5CT99ZZZZZ", "UNAUTHORIZED", "Tap Allow on the device.")}),
		"could_not_ask": inventory.CouldNotAsk(inventory.IOS, "usbmuxd could not be asked: dial unix …", "Start the daemon and re-run."),
		"not_asked":     inventory.NotAsked(inventory.Android),
	}
}

// ── THE CENSUS: every outcome has a DISTINCT human rendering and a DISTINCT JSON rendering ──────────
//
// Adding a fourth outcome without teaching both renderers fails here (its rendering would either be
// missing or collide with an existing one).
func TestEveryOutcomeRendersDistinctlyInBothRenderings(t *testing.T) {
	all := samples()
	if len(all) != len(inventory.AllKinds()) {
		t.Fatalf("the census must walk every declared kind: %d samples vs %d kinds", len(all), len(inventory.AllKinds()))
	}

	humanSeen := map[string]string{}
	jsonSeen := map[string]string{}
	for _, kindName := range inventory.AllKinds() {
		outcome, ok := all[kindName]
		if !ok {
			t.Fatalf("kind %q declared in AllKinds has no sample — teach the census, and both renderers", kindName)
		}
		var human, machine bytes.Buffer
		if err := render.Human(&human, []inventory.Outcome{outcome}, nil); err != nil {
			t.Fatalf("human rendering of %s: %v", kindName, err)
		}
		if err := render.JSON(&machine, []inventory.Outcome{outcome}, nil); err != nil {
			t.Fatalf("json rendering of %s: %v", kindName, err)
		}
		if prev, clash := humanSeen[human.String()]; clash {
			t.Fatalf("%s and %s render IDENTICALLY for a human — the renderings collapse what the type separates", kindName, prev)
		}
		if prev, clash := jsonSeen[machine.String()]; clash {
			t.Fatalf("%s and %s render IDENTICALLY in JSON", kindName, prev)
		}
		humanSeen[human.String()] = kindName
		jsonSeen[machine.String()] = kindName
	}
}

// THE INSTRUMENT'S OWN GUARD — and it is a SOURCE-TEXT PIN, not a compiler guarantee. Go has no
// exhaustive switch, so nothing forces a new `kind` constant to appear in AllKinds(); this test reads
// outcome.go, counts the constants declared in the `kind` block, and asserts that count equals
// len(AllKinds()). It catches "someone added a fourth kind and did not extend the list", which is the
// failure mode that would turn the census above into a hand-written list agreeing with itself. It
// does NOT prove the renderers handle the new kind — the census does that, once the kind is listed.
func TestTheDeclaredKindCountEqualsTheCensusList(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "inventory", "outcome.go"), nil, 0)
	if err != nil {
		t.Fatalf("parsing outcome.go: %v", err)
	}

	declared := 0
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// the kind block: `kindAnswered kind = iota` and its bare successors
			if ident, ok := value.Type.(*ast.Ident); ok && ident.Name == "kind" {
				declared += len(value.Names)
				continue
			}
			if value.Type == nil && len(value.Names) > 0 && strings.HasPrefix(value.Names[0].Name, "kind") {
				declared += len(value.Names)
			}
		}
		return true
	})

	if declared == 0 {
		t.Fatal("the guard found no kind constants — it is reading the wrong file or the wrong shape (a vacuous pin is worse than none)")
	}
	if declared != len(inventory.AllKinds()) {
		t.Fatalf("outcome.go declares %d kinds but AllKinds() lists %d — add the new kind to AllKinds AND teach both renderers", declared, len(inventory.AllKinds()))
	}
}

// ── the JSON shape seal ─────────────────────────────────────────────────────────────────────────────

// UNKNOWN IS NOT EMPTY: a could-not-ask must NOT serialise as "devices": [] — the same rule
// HeartbeatRequest.UnusableDevices states on the farm's wire (null is not an empty list).
func TestCouldNotAskNeverSerialisesAsAnEmptyDeviceList(t *testing.T) {
	var buf bytes.Buffer
	if err := render.JSON(&buf, []inventory.Outcome{samples()["could_not_ask"]}, nil); err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("the machine rendering must be valid JSON: %v", err)
	}
	entry := decoded[0]
	if _, present := entry["devices"]; present {
		t.Fatalf("could_not_ask must carry NO devices key at all, got %v", buf.String())
	}
	if entry["outcome"] != "could_not_ask" {
		t.Fatalf("the outcome must be machine-readable without prose, got %v", entry["outcome"])
	}
	if entry["reason"] == nil || entry["remedy"] == nil {
		t.Fatalf("a failure carries both what happened and what to do, got %v", entry)
	}

	// …and the answered case DOES carry the key, so a consumer can tell zero-devices from unknown.
	buf.Reset()
	if err := render.JSON(&buf, []inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"devices": []`) {
		t.Fatalf("answered-with-zero must carry an EMPTY list (that is the answer), got %s", buf.String())
	}
}

func TestTheVerbatimStateReachesTheJson(t *testing.T) {
	var buf bytes.Buffer
	outcome := inventory.Answered(inventory.Android, nil,
		[]inventory.Unusable{inventory.NewUnusable("FUTURE0001", "Quarantined", "Check the state's meaning.")})
	if err := render.JSON(&buf, []inventory.Outcome{outcome}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"state": "quarantined"`) {
		t.Fatalf("the platform's own word must reach the wire verbatim, got %s", buf.String())
	}
	if strings.Contains(strings.ToLower(buf.String()), `"other"`) {
		t.Fatal("no state may be flattened to \"other\"")
	}
}

// ── the narration seal ──────────────────────────────────────────────────────────────────────────────

// A probe that could not ask must NEVER print "no devices found" — it prints what happened and what
// to do. This is the sentence the C# node got wrong on the evening a Samsung sat at the authorize
// dialog.
func TestAProbeThatCouldNotAskNeverNarratesAnEmptyInventory(t *testing.T) {
	var buf bytes.Buffer
	if err := render.Human(&buf, []inventory.Outcome{samples()["could_not_ask"]}, nil); err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(buf.String())
	for _, forbidden := range []string{"no devices found", "no devices attached", "0 usable"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("a failed probe must not narrate an inventory (%q): %s", forbidden, buf.String())
		}
	}
	if !strings.Contains(text, "could not check") || !strings.Contains(text, "what to do") {
		t.Fatalf("it must say what happened AND what to do: %s", buf.String())
	}
}

// An attached-but-unusable device is PRINTED, with its reason — an absence is never the answer.
func TestAnUnusableDeviceIsPrintedWithItsRemedy(t *testing.T) {
	var buf bytes.Buffer
	if err := render.Human(&buf, []inventory.Outcome{samples()["answered"]}, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "R5CT99ZZZZZ") || !strings.Contains(out, "unauthorized") {
		t.Fatalf("the unusable device and its verbatim state must be shown: %s", out)
	}
	if !strings.Contains(out, "Tap Allow on the device.") {
		t.Fatalf("…with what the reader does about it: %s", out)
	}
}

// ── the exit-code seal ──────────────────────────────────────────────────────────────────────────────

// Exit 0 means "I established the truth", not "I finished running".
func TestExitCodeDistinguishesAnsweringFromRunning(t *testing.T) {
	all := samples()
	if code := render.ExitCode([]inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)}); code != 0 {
		t.Fatalf("answered-with-zero-devices is a SUCCESS, got %d", code)
	}
	if code := render.ExitCode([]inventory.Outcome{all["could_not_ask"], all["could_not_ask"]}); code != 1 {
		t.Fatalf("every probe failing is NOT a success, got %d", code)
	}
	if code := render.ExitCode([]inventory.Outcome{all["not_asked"], all["not_asked"]}); code != 1 {
		t.Fatalf("nobody asked anybody — that is not an established truth, got %d", code)
	}
	if code := render.ExitCode([]inventory.Outcome{all["could_not_ask"], all["answered"]}); code != 0 {
		t.Fatalf("one probe answering is enough to have established a truth, got %d", code)
	}
}
