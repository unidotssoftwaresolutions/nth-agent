package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// THE ONE PLACE SPELLS THE PACKAGE IT SHIPS: the npm package's name and the command it installs are publish/npm/package.json's,
// and the npx form is that name with @latest.
func TestTheOnePlaceSpellsThePackageItShips(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "publish", "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name string            `json:"name"`
		Bin  map[string]string `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != Package || len(manifest.Bin) != 1 || manifest.Bin[Program] == "" {
		t.Fatalf("package.json names %q and installs %v; this package spells %q and %q", manifest.Name, manifest.Bin, Package, Program)
	}
	if npxLatest != "npx "+Package+"@latest" {
		t.Fatalf("the npx form is %q, and must be the package's name with @latest", npxLatest)
	}
}

// EACH INSTALL, SPELLED: at this machine, and on another screen (never a path of this machine).
func TestEachInstallIsSpelledTheWayItRuns(t *testing.T) {
	cases := []struct {
		name          string
		install       Install
		here, thatOne string
	}{
		{"unknown", Install{}, "npx nthcase-agent@latest doctor", "npx nthcase-agent@latest doctor"},
		{"npx", Install{Started: StartedByNpx}, "npx nthcase-agent@latest doctor", "npx nthcase-agent@latest doctor"},
		{"global", Install{Started: StartedFromGlobal}, "nth-agent doctor", "nth-agent doctor"},
		{"global run by npx", Install{Started: StartedFromGlobalByNpx}, "npx nthcase-agent@latest doctor", "npx nthcase-agent@latest doctor"},
		{"project", Install{Started: StartedFromProject, Project: "/home/ada/bench"},
			"/home/ada/bench/node_modules/.bin/nth-agent doctor", "npx nthcase-agent@latest doctor"},
		{"project, a path of plain marks", Install{Started: StartedFromProject, Project: "/srv/qa_bench-2.0/app+web@x"},
			"/srv/qa_bench-2.0/app+web@x/node_modules/.bin/nth-agent doctor", "npx nthcase-agent@latest doctor"},
	}
	// A path a shell would need quoted, a terminal could not show, or that is not absolute, is not spelled.
	for _, project := range []string{"/Users/ada/my bench", "/tmp/it's", "/tmp/$HOME", "/tmp/100%", "/tmp/a\x1b[2Jb", "/tmp/caf\u00e9",
		"/tmp/\xff", "relative/bench", "", "/tmp/a;rm", "/tmp/`x`", "/tmp/a\nb", "/tmp/*"} {
		cases = append(cases, struct {
			name          string
			install       Install
			here, thatOne string
		}{"project at " + project, Install{Started: StartedFromProject, Project: project}, "npx nthcase-agent@latest doctor", "npx nthcase-agent@latest doctor"})
	}
	for _, c := range cases {
		if got := c.install.Line("doctor"); got != c.here {
			t.Errorf("%s: Line = %q, want %q", c.name, got, c.here)
		}
		if got := c.install.OnThatMachine("doctor"); got != c.thatOne {
			t.Errorf("%s: OnThatMachine = %q, want %q", c.name, got, c.thatOne)
		}
	}
	if got := (Install{Started: StartedFromGlobal}).Line(""); got != "nth-agent" {
		t.Errorf("Line(\"\") is the command alone, got %q", got)
	}
}

// THIS PROCESS: before Use, the npx form with @latest; after it, what Use was given.
func TestEveryCommandReadsHowThisProcessWasStarted(t *testing.T) {
	before := This()
	t.Cleanup(func() { Use(before) })
	Use(Install{})
	if Line("notices") != "npx nthcase-agent@latest notices" || OnThatMachine("notices") != "npx nthcase-agent@latest notices" {
		t.Fatalf("an agent that cannot tell how it was started spells %q", Line("notices"))
	}
	Use(Install{Started: StartedFromProject, Project: "/home/ada/bench"})
	if Line("notices") != "/home/ada/bench/node_modules/.bin/nth-agent notices" || OnThatMachine("notices") != "npx nthcase-agent@latest notices" {
		t.Fatalf("a project's agent spells %q here and %q elsewhere", Line("notices"), OnThatMachine("notices"))
	}
}
