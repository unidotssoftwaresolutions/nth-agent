package main

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// PASS, NOT SKIP (AN_AGENT_THAT_PASSES_ON_WINDOWS_ROUND_TWO). Some tests run only on Windows: a file named `…_windows_test.go`
// or built for windows alone, or a test that skips on every other system. CI's windows-unit-tests job is the one place they
// run, and its `go test ./...` prints `ok` for a package whose tests skipped exactly as for one whose tests passed. So the job
// runs them again by name, with -v, in the step below, and fails unless each says PASS.
//
// These seals hold that step to the tree: it names every such test, and only those, in the package each lives in; and its
// verdict, run here with bash and a stand-in `go`, refuses a SKIP, a FAIL, a name that did not run, and a failed run.

const windowsOnlyStep = "The Windows-only tests passed, not skipped"

// windowsOnlyTests is every test in the tree that runs only on Windows, with the package folder it lives in, relative to the
// repository and spelled with `/`.
func windowsOnlyTests(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	found := map[string]string{}
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		files++
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("instrument guard: %s does not parse: %v", path, err)
		}
		wholeFile := strings.HasSuffix(d.Name(), "_windows_test.go") || builtForWindowsAlone(file)
		dir, _ := filepath.Rel(root, filepath.Dir(path))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			if wholeFile || skipsOffWindows(fn) {
				found[fn.Name.Name] = filepath.ToSlash(dir)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 100 {
		t.Fatalf("instrument guard: read %d test files — the census is not reading the tree", files)
	}
	return found
}

// builtForWindowsAlone: a `//go:build` line that only a windows build satisfies.
func builtForWindowsAlone(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, c := range group.List {
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				continue
			}
			windows := expr.Eval(func(tag string) bool { return tag == "windows" })
			elsewhere := slices.ContainsFunc([]string{"darwin", "linux"}, func(goos string) bool {
				return expr.Eval(func(tag string) bool { return tag == goos || tag == "unix" })
			})
			return windows && !elsewhere
		}
	}
	return false
}

// skipsOffWindows: the test's first statement is `if runtime.GOOS != "windows" { … t.Skip… }`.
func skipsOffWindows(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	guard, ok := fn.Body.List[0].(*ast.IfStmt)
	if !ok {
		return false
	}
	cond, ok := guard.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return false
	}
	sel, ok := cond.X.(*ast.SelectorExpr)
	lit, isLit := cond.Y.(*ast.BasicLit)
	if !ok || !isLit || sel.Sel.Name != "GOOS" || lit.Value != strconv.Quote("windows") {
		return false
	}
	skips := false
	ast.Inspect(guard.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if s, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(s.Sel.Name, "Skip") {
				skips = true
			}
		}
		return !skips
	})
	return skips
}

var (
	stepTestName = regexp.MustCompile(`^  (Test\w+)$`)
	stepPackage  = regexp.MustCompile(`\./(internal/\w+)/`)
)

// windowsOnlyStepNames reads the step's list of tests (its `tests=(…)` array) and the package folders its `go test` names.
func windowsOnlyStepNames(t *testing.T, script string) (tests, packages []string) {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		if m := stepTestName.FindStringSubmatch(line); m != nil {
			tests = append(tests, m[1])
		}
		if strings.HasPrefix(line, "go test ") {
			for _, m := range stepPackage.FindAllStringSubmatch(line, -1) {
				packages = append(packages, m[1])
			}
		}
	}
	if len(tests) == 0 || len(packages) == 0 {
		t.Fatalf("instrument guard: read %d tests and %d packages from the step %q:\n%s", len(tests), len(packages), windowsOnlyStep, script)
	}
	return tests, packages
}

func TestTheWindowsJobNamesEveryTestThatRunsOnlyOnWindows(t *testing.T) {
	census := windowsOnlyTests(t)
	// Instrument guard: one test of each kind the census reads — a `_windows_test.go` file, and a test that skips elsewhere.
	if census["TestOnWindowsAPrivateFileListsThisAccountAlone"] != "internal/private" ||
		census["TestOnWindowsAProjectInstallIsReadFromWhereItsProgramIs"] != "internal/command" {
		t.Fatalf("instrument guard: the census missed a known Windows-only test: %v", census)
	}
	// …and a test that only mentions Windows is not one: this one runs everywhere, with a C:\ name on other systems.
	if _, ok := census["TestASessionWhoseBuildIsAWindowsPathStarts"]; ok {
		t.Fatalf("instrument guard: the census took a test that runs on every system: %v", census)
	}

	tests, packages := windowsOnlyStepNames(t, stepScript(t, "ci.yml", windowsOnlyStep))
	var problems []string
	for name, dir := range census {
		if !slices.Contains(tests, name) {
			problems = append(problems, name+" ("+dir+") runs only on Windows and the step does not name it")
		} else if !slices.Contains(packages, dir) {
			problems = append(problems, name+" lives in "+dir+", which the step's go test does not name")
		}
	}
	for _, name := range tests {
		if _, ok := census[name]; !ok {
			problems = append(problems, "the step names "+name+", which is not a test that runs only on Windows")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("the Windows job must read each Windows-only test as passed; its list and the tree disagree:\n%s", strings.Join(problems, "\n"))
	}
}

func TestTheWindowsJobFailsUnlessEachWindowsOnlyTestSaysPass(t *testing.T) {
	needTools(t, "bash", "grep", "tee") // and skips on Windows, where this very step runs for real
	script := stepScript(t, "ci.yml", windowsOnlyStep)
	tests, _ := windowsOnlyStepNames(t, script)
	passed := func(names ...string) string {
		var out strings.Builder
		for _, name := range names {
			out.WriteString("=== RUN   " + name + "\n--- PASS: " + name + " (0.01s)\n")
		}
		return out.String()
	}
	first, rest := tests[0], tests[1:]
	for _, c := range []struct {
		name, out string
		exit      int
		ok        bool
		says      string
	}{
		{"every test passed", passed(tests...) + "PASS\nok  \tx\t0.1s\n", 0, true, "PASS " + first},
		{"one skipped", "--- SKIP: " + first + " (0.00s)\n" + passed(rest...), 0, false, first + " was skipped on Windows"},
		{"one not run at all", passed(rest...), 0, false, first + " did not run"},
		{"one failed", "--- FAIL: " + first + " (0.00s)\n" + passed(rest...), 1, false, first + " failed"},
		{"one only as a longer name and a subtest", "--- PASS: " + first + "Again (0.00s)\n    --- PASS: " + first + "/x (0.00s)\n" +
			passed(rest...), 0, false, first + " did not run"},
		{"every test passed and go test still failed", passed(tests...) + "FAIL\tx [build failed]\n", 1, false, "PASS " + first},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			writeFile(t, filepath.Join(dir, "said.txt"), []byte(c.out), 0o644)
			// The stand-in go: records its arguments, says what this case's run said, and ends as it ended.
			writeFile(t, filepath.Join(bin, "go"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\ncat said.txt\nexit "+
				strconv.Itoa(c.exit)+"\n"), 0o755)
			writeFile(t, filepath.Join(dir, "step.sh"), []byte(script), 0o644)
			// As the runner runs a `shell: bash` step.
			cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "step.sh")
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + dir}
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.ok || !strings.Contains(string(out), c.says) {
				t.Fatalf("the step must end %s and say %q; it ended %v and said:\n%s", map[bool]string{true: "well", false: "failed"}[c.ok], c.says, err, out)
			}
			args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
			if err != nil {
				t.Fatalf("instrument guard: the stand-in go was never run: %v", err)
			}
			want := "-run\n^(" + strings.Join(tests, "|") + ")$\n"
			if !strings.Contains(string(args), "\n-v\n") || !strings.Contains(string(args), "-count=1\n") || !strings.Contains(string(args), want) {
				t.Fatalf("the step runs each test by name, with -v, uncached; go was given:\n%s", args)
			}
		})
	}
}
