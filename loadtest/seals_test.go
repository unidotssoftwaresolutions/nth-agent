package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/unidotssoftwaresolutions/nth-agent"

func root(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("the module root is not above loadtest/: %v", err)
	}
	return abs
}

// THE HARNESS IS NEVER PART OF THE PUBLISHED AGENT. The release builds one program, ./cmd/nth-agent, and nothing it links
// is under loadtest/; the npm package ships index.js and the binaries the release builds, and no file of loadtest/; and no
// package outside loadtest/ imports one inside it.
func TestTheHarnessIsNotInThePublishedAgent(t *testing.T) {
	dir := root(t)

	list := exec.Command("go", "list", "-deps", "./cmd/nth-agent")
	list.Dir = dir
	list.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOWORK=off")
	var stderr bytes.Buffer
	list.Stderr = &stderr
	deps, err := list.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/nth-agent: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(string(deps), module+"/internal/tunnel\n") {
		t.Fatal("instrument: the published program's dependencies do not hold its own tunnel — the list is of the wrong program")
	}
	for _, dep := range strings.Split(string(deps), "\n") {
		if strings.HasPrefix(dep, module+"/loadtest") {
			t.Errorf("the published agent links %s", dep)
		}
	}

	release, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	builds := regexp.MustCompile(`go build[^\n]*`).FindAllString(string(release), -1)
	if len(builds) == 0 {
		t.Fatal("instrument: release.yml builds nothing this seal can read")
	}
	for _, b := range builds {
		if !strings.HasSuffix(strings.TrimSpace(b), "./cmd/nth-agent") || strings.Contains(b, "loadtest") {
			t.Errorf("the release builds something other than ./cmd/nth-agent: %s", b)
		}
	}

	var pkg struct {
		Files []string `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "publish", "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &pkg); err != nil || len(pkg.Files) == 0 {
		t.Fatalf("instrument: the npm package names no files: %v", err)
	}
	for _, f := range pkg.Files {
		if strings.Contains(f, "loadtest") || f == "." || f == "*" || strings.HasPrefix(f, "..") {
			t.Errorf("the npm package ships %q", f)
		}
	}

	importers := importersOf(t, dir, func(path string) bool { return strings.HasPrefix(path, module+"/loadtest") })
	for file := range importers {
		if !strings.HasPrefix(file, "loadtest/") {
			t.Errorf("%s, outside the harness, imports it", file)
		}
	}
}

// importersOf is every Go file under dir that imports a path pick picks, and what it imports.
func importersOf(t *testing.T, dir string, pick func(string) bool) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		for _, spec := range file.Imports {
			p, _ := strconv.Unquote(spec.Path.Value)
			if pick(p) {
				found[filepath.ToSlash(rel)] = append(found[filepath.ToSlash(rel)], p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// THE HARNESS OPENS NO LOCAL DRIVER, by what it is made of: no program under loadtest/ imports the agent's drivers (adb,
// iOS, WebDriverAgent, the Appium probe, the live view, capture), go-ios, or os/exec, and none calls the agent's probe
// set. It reaches the agent's connect loop and tunnel, which link those, only through the doors fleet.go shows: its own
// probes, its own Appium address, no mirrors, no capture. TestTheFleetOpensNoRealDriver proves the same at run time.
func TestTheHarnessImportsNoDriver(t *testing.T) {
	dir := root(t)
	forbidden := []string{module + "/internal/android", module + "/internal/ios", module + "/internal/wda",
		module + "/internal/appium", module + "/internal/liveview", module + "/internal/capture", module + "/internal/netcap",
		module + "/internal/doctor", module + "/internal/browserstack", "github.com/danielpaulus/go-ios", "os/exec", "syscall/exec"}
	harness := filepath.Join(dir, "loadtest")
	err := filepath.WalkDir(harness, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		for _, spec := range file.Imports {
			p, _ := strconv.Unquote(spec.Path.Value)
			for _, f := range forbidden {
				if p == f || strings.HasPrefix(p, f+"/") {
					t.Errorf("%s imports %s", rel, p)
				}
			}
		}
		for _, call := range []string{"ProbeSet", "AdbRunner", "UsbmuxdLister", "LiveView:", "Mirrors:", "Capture:"} {
			if bytes.Contains(source, []byte(call)) {
				t.Errorf("%s names %s — the fleet's phones are its own fakes, and it offers no live view and no capture", rel, call)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// THE TEST HELPERS ARE NOT PROGRAMS' PARTS: the stand-in farm and the runner's replay are imported only by tests.
func TestTheTestHelpersAreImportedOnlyByTests(t *testing.T) {
	dir := root(t)
	importers := importersOf(t, dir, func(path string) bool {
		return path == module+"/loadtest/farmstandin" || path == module+"/loadtest/runnertest"
	})
	if len(importers) == 0 {
		t.Fatal("instrument: nothing imports the test helpers, not even their tests")
	}
	for file, paths := range importers {
		if !strings.HasSuffix(file, "_test.go") && !strings.HasPrefix(file, "loadtest/farmstandin/") {
			t.Errorf("%s imports %v and is not a test", file, paths)
		}
	}
}
