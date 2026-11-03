package doctor_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// ── AN_AGENT_THAT_SPEAKS_PLAINLY: every problem the doctor can print says what to do ──────────────────────────────
//
// THE CENSUS. A problem is a check the doctor prints in red or yellow: one that stops a run, one it could not establish,
// and one that is true and stops nothing. Each is said in three parts — what is wrong, why it matters, what to do — and a
// problem with no "what to do" is a sentence that leaves a person where it found them.
//
// DISCOVERED, never enumerated: the population is what Diagnose returns on several machines, so a check added tomorrow
// joins it by existing; and every check NAME in checks.go is read from the source, so a blocker whose state no machine
// below reaches still has to have its "why it matters".

func problemMachines() map[string]doctor.Report {
	broken := machine{
		onPath:      map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium"},
		appiumURL:   "http://127.0.0.1:4723",
		appiumError: errors.New("dial tcp 127.0.0.1:4723: connect: connection refused"),
		devModeErr:  errors.New("could not reach the phone"),
	}
	stuck := []inventory.Outcome{
		inventory.WithLeftOut(inventory.Answered(inventory.Android, nil,
			[]inventory.Unusable{inventory.NewUnusable("4b0f92aa", "unauthorized", "tap Allow on the phone")}),
			[]inventory.LeftOut{{ID: "emulator-5554", How: "an emulator"}}),
		inventory.CouldNotAsk(inventory.IOS, "usbmuxd did not answer", "re-plug the phone"),
	}
	unknownPorts := equippedMachine().env()
	unknownPorts.MirrorPortFor = func(string) liveview.Reservation { return liveview.Reservation{} }
	return map[string]doctor.Report{
		"bare":              doctor.Diagnose(context.Background(), machine{}.env(), bothPlatformsSeen()),
		"equipped":          doctor.Diagnose(context.Background(), equippedMachine().env(), bothPlatformsSeen()),
		"appium down":       doctor.Diagnose(context.Background(), broken.env(), bothPlatformsSeen()),
		"stuck phones":      doctor.Diagnose(context.Background(), machine{}.env(), stuck),
		"ports not checked": doctor.Diagnose(context.Background(), unknownPorts, bothPlatformsSeen()),
	}
}

func isProblem(c doctor.Check) bool {
	return c.Blocks() || c.State == doctor.Unknown || (c.State.Blocking() && c.Advisory)
}

func TestEveryProblemTheDoctorCanPrintSaysWhatToDo(t *testing.T) {
	seen := 0
	for machineName, report := range problemMachines() {
		for _, c := range report.Checks {
			if !isProblem(c) {
				continue
			}
			seen++
			if strings.TrimSpace(c.Remedy) == "" {
				t.Errorf("%s: %q (%s) is printed as a problem and says nothing to do", machineName, c.Name, c.State)
			}
			if doctor.WhatItStops(c) == "" {
				t.Errorf("%s: %q (%s) is printed as a problem and does not say why it matters", machineName, c.Name, c.State)
			}
			// AND THE RENDERED REPORT CARRIES IT ON AN ARROW LINE OF ITS OWN.
			var out strings.Builder
			if err := doctor.Human(&out, doctor.Report{Checks: []doctor.Check{c}}, doctor.Options{}); err != nil {
				t.Fatal(err)
			}
			if c.Remedy != "" && !strings.Contains(out.String(), "  → ") {
				t.Errorf("%s: %q's what-to-do is not on a → line:\n%s", machineName, c.Name, out.String())
			}
		}
	}
	if seen < 10 {
		t.Fatalf("only %d problems across the machines — the census is not reaching the checks", seen)
	}
}

// EVERY BLOCKER'S "WHY IT MATTERS" IS ITS OWN: read every Name in checks.go and ask for the sentence that check would say
// if it stopped a run. A check nobody wrote a sentence for is a red here before it is a blank line in front of a customer.
func TestEveryCheckNameHasItsOwnWhyItMatters(t *testing.T) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "checks.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Name" {
			return true
		}
		value := kv.Value
		if binary, ok := value.(*ast.BinaryExpr); ok {
			value = binary.X // "Appium driver " + driver: the prefix every such check carries
		}
		if literal, ok := value.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			name, _ := strconv.Unquote(literal.Value)
			names = append(names, name)
		}
		return true
	})
	if len(names) < 12 {
		t.Fatalf("read only %d check names from checks.go — the census is not reading the source: %v", len(names), names)
	}
	generic := doctor.WhatItStops(doctor.Check{Name: "a check nobody named", State: doctor.Missing})
	if generic != "" {
		t.Fatalf("an unnamed blocker must get no sentence, so the census can see it: %q", generic)
	}
	for _, name := range names {
		if why := doctor.WhatItStops(doctor.Check{Name: name + "x", State: doctor.Missing}); strings.HasSuffix(name, " ") && why == "" {
			t.Errorf("checks named %q… have no sentence saying what they stop", name)
		}
		if why := doctor.WhatItStops(doctor.Check{Name: name, State: doctor.Missing}); !strings.HasSuffix(name, " ") && why == "" {
			t.Errorf("%q has no sentence saying what it stops", name)
		}
	}
}

// THE INSTALL STEP IS SAID FOR THIS MACHINE: brew on a Mac, the step any distribution can take on Linux.
func TestAdbsInstallStepIsTheOneForThisSystem(t *testing.T) {
	for system, want := range map[string]string{
		"darwin": "brew install --cask android-platform-tools",
		"linux":  "https://developer.android.com/tools/releases/platform-tools",
	} {
		env := machine{}.env()
		env.System = system
		report := doctor.Diagnose(context.Background(), env, bothPlatformsSeen())
		for _, c := range report.Checks {
			if c.Name != "adb" {
				continue
			}
			if !strings.Contains(c.Remedy, want) {
				t.Errorf("%s: adb's remedy must say %q, got %q", system, want, c.Remedy)
			}
			if system == "linux" && strings.Contains(c.Remedy, "brew") {
				t.Errorf("linux: brew is a Mac's step, got %q", c.Remedy)
			}
			if system == "darwin" && strings.Contains(c.Remedy, "Linux") {
				t.Errorf("darwin: a Mac is told the Mac's step only, got %q", c.Remedy)
			}
		}
	}
}

// THE MOST IMPORTANT THING FIRST: the first line of the report is its answer — red with the count, or green.
func TestTheReportStartsWithItsAnswer(t *testing.T) {
	reports := problemMachines()
	reports["all green"] = doctor.Report{Checks: []doctor.Check{{Name: "adb", Need: "n", State: doctor.Satisfied}}}
	for name, want := range map[string]string{
		"bare": "things stop runs on this machine",
		// SOME CHECKS ARE UNKNOWN on the equipped machine (no signing identities to read here), so not green: unknown is
		// not clear, and the first line does not say "Ready".
		"equipped":  "Nothing here stops a run",
		"all green": "Ready: nothing here stops a run",
	} {
		var out strings.Builder
		if err := doctor.Human(&out, reports[name], doctor.Options{}); err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(out.String(), "\n")
		if !strings.Contains(first, want) {
			t.Errorf("%s: the first line is the answer (%q), got %q", name, want, first)
		}
	}
	var coloured strings.Builder
	_ = doctor.Human(&coloured, problemMachines()["bare"], doctor.Options{Mode: ui.Mode{Color: true}})
	if !strings.HasPrefix(coloured.String(), "\033[1m\033[31m") {
		t.Fatalf("a machine that cannot run starts in bold red: %q", coloured.String()[:40])
	}
}
