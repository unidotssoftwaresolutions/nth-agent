package inventory_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// NEVER PUBLISH WHAT YOU CANNOT OFFER — asserted as a TYPE fact, not a runtime check.
//
// The compiler is the guard: Answered's second parameter is []Offerable, so an Unusable cannot be
// passed into it (`cannot use … as inventory.Offerable`), and Outcome's fields are unexported with no
// setter, so nothing can move an entry from one list to the other after construction. This test pins
// the SHAPE that makes that true — if someone merges the two into one type with a status field, the
// signature check below fails and the compiler stops being the guard.
func TestOfferableAndUnusableAreDifferentTypes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "outcome.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing outcome.go: %v", err)
	}

	var answeredParams []string
	var offerableFields, unusableFields int
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Name.Name == "Answered" {
				for _, p := range node.Type.Params.List {
					answeredParams = append(answeredParams, exprString(fset, p.Type))
				}
			}
		case *ast.TypeSpec:
			structType, ok := node.Type.(*ast.StructType)
			if !ok {
				return true
			}
			switch node.Name.Name {
			case "Offerable":
				offerableFields = len(structType.Fields.List)
			case "Unusable":
				unusableFields = len(structType.Fields.List)
			}
		}
		return true
	})

	if offerableFields == 0 || unusableFields == 0 {
		t.Fatal("both Offerable and Unusable must exist as distinct struct types — a vacuous pin is worse than none")
	}
	found := strings.Join(answeredParams, ", ")
	if !strings.Contains(found, "[]Offerable") {
		t.Fatalf("Answered must take []Offerable so the compiler rejects an Unusable, got (%s)", found)
	}
	if strings.Contains(found, "[]Unusable") == false {
		t.Fatalf("Answered must take the unusable list separately, got (%s)", found)
	}
}

// THE SUBSTITUTION, tested as what it is. A blank remedy becomes the LOUD MARKER — that is this
// constructor's own behaviour and it is worth asserting here.
//
// WHAT THIS TEST DOES NOT PROVE, stated so nobody reads it as the remedy guard: it says nothing about
// whether any PROBE supplies a real remedy. Its predecessor asserted `TrimSpace(Remedy()) != ""` and
// could not fail, because the line under test supplies that value — a check whose answer is supplied
// by the code under test measures that code's defaulting, not its behaviour. The guard that can fail
// is the marker's ABSENCE, driven per probe (marker_test.go finds every construction site).
func TestABlankRemedyBecomesTheLoudMarker(t *testing.T) {
	for _, remedy := range []string{"", "   ", "\n"} {
		outcome := inventory.CouldNotAsk(inventory.Android, "adb exploded", remedy)
		if outcome.Remedy() != inventory.MissingRemedyMarker {
			t.Fatalf("a blank remedy (%q) must become the loud marker, got %q", remedy, outcome.Remedy())
		}
	}
	real := inventory.CouldNotAsk(inventory.IOS, "usbmuxd is not running", "Start the daemon.")
	if real.Remedy() != "Start the daemon." {
		t.Fatalf("a real remedy must be carried verbatim, got %q", real.Remedy())
	}
	if real.Remedy() == inventory.MissingRemedyMarker {
		t.Fatal("…and must never be replaced by the marker")
	}
}

// WE CLAIM ONLY THE PLATFORMS CI BUILDS — and this seal now learns which those are.
//
// WHAT IT USED TO BE, and why that was worth nothing: it read three hand-listed files, and it began
// with `if strings.Contains(workflow, "windows") { t.Skip(…) }`. The workflow contains the sentence
// "Windows is deliberately absent" — so the seal survived only because that comment capitalises the W
// while the skip check was case-sensitive. Lower-case one comment and the whole seal stands down,
// silently, forever: a skipped test is a green test. And the three files it read were not where the
// platform names live — every remedy in the two probe packages names macOS or Linux.
//
// WHAT IT IS NOW:
//   - the built targets are PARSED FROM THE MATRIX (`goos:` values) — the seal widens when the matrix
//     grows, and can never stand down;
//   - the population is DISCOVERED: every non-test .go file plus the README, so a new user-visible
//     file is scanned the day it appears rather than the day someone remembers the list;
//   - the population has its own guard: it must contain the files where platform names actually live.
func TestNoUserVisibleStringClaimsAPlatformCiDoesNotBuild(t *testing.T) {
	root := filepath.Join("..", "..")

	built := builtGOOSFromCI(t, filepath.Join(root, ".github", "workflows"))
	if len(built) == 0 {
		t.Fatal("no GOOS values parsed from the CI matrix — the seal cannot know what we build (a vacuous seal is worse than none)")
	}
	// The vocabulary a reader would recognise, and what each name means as a build target. A platform
	// absent from the matrix must not be named; one present in it may be.
	platformWords := map[string]string{
		"windows": "windows",
		// ⚠️ NODE'S OWN SPELLING. `process.platform` says "win32", never "windows" — so the npm shim could have
		// named Windows in the one vocabulary this seal did not have, and the seal would have read it as prose.
		// A seal that polices a file has to speak that file's language.
		"win32":  "windows",
		"macos":  "darwin",
		"darwin": "darwin",
		"linux":  "linux",
	}

	// Which built platforms a reader is actually told about — filled by the forward pass below and checked
	// after it, so the two directions read the same population and cannot disagree about it.
	named := map[string]bool{}

	files := userVisibleFiles(t, root)
	if len(files) < 6 {
		t.Fatalf("the population guard: only %d user-visible files discovered — the walk is reading the wrong tree", len(files))
	}
	// …and it must include the files where platform names DO live today: the probes' remedies. A
	// population that misses them is the old three-file list wearing a new shape.
	for _, required := range []string{
		filepath.Join(root, "internal", "android", "probe.go"),
		filepath.Join(root, "internal", "ios", "probe.go"),
		filepath.Join(root, "README.md"),
		// The npm shim decides which platform a customer's `npx` will run, which makes it the most
		// user-visible statement about platforms this repository publishes. It was invisible here until
		// 2026-09-02: the walk collected .go and README.md, so a JavaScript file naming win32 would have
		// shipped past a seal whose whole subject is platform claims.
		filepath.Join(root, "publish", "npm", "index.js"),
		// The CHANGELOG ships in the npm package and is every release's notes (AN_AGENT_THAT_SHIPS_ITS_NOTICES), and it
		// names the platforms capture runs on.
		filepath.Join(root, "CHANGELOG.md"),
	} {
		if !slices.Contains(files, required) {
			t.Fatalf("the population guard: %s is user-visible and was not scanned", required)
		}
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		lowered := strings.ToLower(string(data))
		for word, goos := range platformWords {
			if !strings.Contains(lowered, word) {
				continue
			}
			if !slices.Contains(built, goos) {
				t.Fatalf("%s names %q (GOOS %q), which this repo's CI does not build — a platform we do "+
					"not build is a platform we do not claim; add the target to the matrix first", path, word, goos)
			}
			named[goos] = true
		}
	}

	// ⚠️ AND THE OTHER DIRECTION, WHICH THIS SEAL'S OWN COMMENT HAS CLAIMED SINCE IT WAS WRITTEN AND NEVER
	// ENFORCED. The workflow says "when we add the target we add the sentence, not before" — a two-way rule, of
	// which only "a sentence needs a target" was checked. A target with no sentence is the same defect pointed
	// the other way: a platform we SHIP and never tell anybody about, which is how a customer downloads a binary
	// for a machine no document says it runs on.
	//
	// The vocabulary is GOOS-level on purpose: adding linux/arm64 introduces no word a reader would recognise as
	// new, so it needs no new sentence. Adding windows/amd64 does.
	for _, goos := range built {
		if !named[goos] {
			t.Fatalf("a workflow builds GOOS %q and no user-visible file names it — we ship a platform nothing "+
				"tells a reader about. Add the sentence, which is what the matrix's own comment promises: "+
				"\"when we add the target we add the sentence, not before\"", goos)
		}
	}
}

// builtGOOSFromCI reads the `goos:` values out of EVERY workflow's build matrix. Deliberately literal — a
// matrix is the only statement of what we compile, so the seal must read IT rather than a list a human keeps
// beside it.
//
// ⚠️ IT USED TO READ ONE FILE. `ci.yml` was the only workflow, so naming it was the same as reading them all —
// until `release.yml` arrived with a matrix of its own, at which point a target could have been SHIPPED without
// this seal ever seeing it. The population is the directory now, for the same reason the file population below
// is discovered rather than listed: a new workflow is scanned the day it appears.
func builtGOOSFromCI(t *testing.T, workflowDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatalf("reading the workflow directory: %v", err)
	}
	var goos []string
	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(workflowDir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		scanned++
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			if !strings.HasPrefix(line, "goos:") {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(line, "goos:"))
			if value != "" && !slices.Contains(goos, value) {
				goos = append(goos, value)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no workflows scanned — the seal cannot know what we build, and a vacuous seal is worse than none")
	}
	return goos
}

// userVisibleFiles discovers every file that can produce text a reader sees: all non-test Go sources
// (every remedy, reason prefix, usage line and marker is printed verbatim) plus the README. Test
// files are excluded — they are not shipped and they must be free to NAME the platforms they forbid.
func userVisibleFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	theToolchains := toolchainZstdDir(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata":
				return filepath.SkipDir // fixtures are captured machine output, not our prose
			case "node_modules":
				// Somebody else's packages are not our claims, and one `npm install` in publish/npm would
				// otherwise drop thousands of files into this population — several of which certainly name
				// Windows. A seal that can be turned red by installing a dependency is a seal people delete.
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, "_test.go"):
			return nil
		// ⚠️ .js AND package.json JOINED THE POPULATION ON 2026-09-02, because the npm package is now the
		// documented way a customer obtains this agent. The shim's platform table IS a platform claim — it is
		// the code that decides what `npx` runs — and package.json's `os`/`cpu` fields would be another, which
		// is one reason they are absent (the cross product of two darwin arches and two linuxes claims four
		// combinations where three are built).
		// ⚠️ THE GO TOOLCHAIN'S OWN zstd DECODER, CARRIED VERBATIM (AN_AGENT_THAT_CAN_RECORD_ITS_PHONE, 2026-10-04).
		// internal/zstd is src/internal/zstd copied byte for byte (internal/zstd/provenance_test.go holds that), and its
		// `windowSize` lowercases to a platform word. Like node_modules, somebody else's code is not our claim — and the
		// decoder speaks to no one: its errors never reach a sentence (netcap's left-out sentences are the agent's own).
		// But only WHILE it is somebody else's code: a file is left out only when it is byte-equal to the toolchain's
		// file of the same name, so one edited line puts the whole file back under this census.
		case strings.HasSuffix(name, ".go") && filepath.Base(filepath.Dir(path)) == "zstd" &&
			sameBytes(path, filepath.Join(theToolchains, name)):
			return nil
		// ⚠️ CHANGELOG.md JOINED ON 2026-10-04 (AN_AGENT_THAT_SHIPS_ITS_NOTICES): it ships beside the README and its sections are
		// the release notes. THIRD_PARTY_NOTICES.txt did NOT: it is other people's licences, generated, and its own header
		// names the release matrix it is built from — the same reason internal/zstd and node_modules are left out.
		case strings.HasSuffix(name, ".go"), strings.HasSuffix(name, ".js"),
			name == "README.md", name == "package.json", name == "CHANGELOG.md":
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}
	return files
}

// toolchainZstdDir is where this Go toolchain keeps its own zstd decoder's sources.
func toolchainZstdDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("go env GOROOT: %v — without the toolchain's decoder to compare with, nothing can be left out as its copy", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "src", "internal", "zstd")
}

// sameBytes: both files exist and hold the same bytes. Any read failure is "not the same", so the file stays scanned.
func sameBytes(ours, theirs string) bool {
	a, errA := os.ReadFile(ours)
	b, errB := os.ReadFile(theirs)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func exprString(fset *token.FileSet, expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.ArrayType:
		return "[]" + exprString(fset, e.Elt)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(fset, e.X) + "." + e.Sel.Name
	default:
		return "?"
	}
}
