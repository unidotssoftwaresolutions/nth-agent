package inventory_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EVERY PRODUCTION CouldNotAsk SITE MUST BE DRIVEN BY A MARKER SEAL — and this is the guard that
// keeps that claim true as the repo grows.
//
// The marker seals themselves live beside the probes they drive (android/ios), because each needs
// that probe's seam. What cannot live there is the POPULATION: a third probe added tomorrow, with a
// CouldNotAsk of its own and no seal, would leave both existing seals green. So this test discovers
// every `inventory.CouldNotAsk(` call site under the repo's production code and asserts each one's
// package carries a marker seal — a test file that asserts MissingRemedyMarker is absent.
func TestEveryPackageThatBuildsACouldNotAskHasAMarkerSeal(t *testing.T) {
	root := filepath.Join("..", "..")
	builders := map[string]string{} // package dir → the file that builds a CouldNotAsk
	sealed := map[string]bool{}     // package dir → a _test.go asserts the marker's absence

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(data)
		dir := filepath.Dir(path)
		if strings.HasSuffix(path, "_test.go") {
			// THE DISCHARGE SIDE IS NOW AS STRICT AS THE DETECTION SIDE. It used to accept any test file that
			// merely CONTAINED "MissingRemedyMarker" and an "!=" ANYWHERE in it — a file could mention the
			// marker in a comment, compare two unrelated things, and count as sealed. Now the file must
			// contain an actual COMPARISON against the marker, found the same way the obligation is: by AST.
			if assertsAgainstTheMarker(text) {
				sealed[dir] = true
			}
			return nil
		}
		// Production code that CONSTRUCTS a CouldNotAsk (the constructor's own file excepted).
		//
		// DETECTED BY AST, not by substring: a first draft matched the text "CouldNotAsk(" and flagged
		// internal/render, which merely READS the outcome via `o.IsCouldNotAsk()` — the accessor's name
		// ends in the constructor's name. A guard that cannot tell a call from a read reports files
		// that build nothing, and its author starts adding seals to satisfy it.
		if !strings.HasSuffix(path, "outcome.go") && buildsACouldNotAsk(text) {
			builders[dir] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}

	if len(builders) == 0 {
		t.Fatal("the guard found no CouldNotAsk construction sites — it is reading the wrong tree (a vacuous guard is worse than none)")
	}
	for dir, file := range builders {
		if !sealed[dir] {
			t.Fatalf("%s builds a CouldNotAsk but %s has no marker seal — drive that probe through its own "+
				"seam and assert inventory.MissingRemedyMarker is NOT what the reader gets; a "+
				"TrimSpace(...) != \"\" check is answered by the constructor, not by this code", file, dir)
		}
	}

	// …and the marker is a single declared constant, not a string repeated where it can drift.
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "outcome.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing outcome.go: %v", err)
	}
	declared := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, name := range spec.Names {
			if name.Name == "MissingRemedyMarker" {
				declared = true
			}
		}
		return true
	})
	if !declared {
		t.Fatal("MissingRemedyMarker must be a declared constant — the seals assert its absence by name")
	}
}

// buildsACouldNotAsk reports whether the source CALLS inventory.CouldNotAsk (or, inside the inventory
// package itself, CouldNotAsk) — a call expression, never a name that merely appears in text.
func buildsACouldNotAsk(source string) bool {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "src.go", source, 0)
	if err != nil {
		return false // unparseable production Go would fail the build long before this guard
	}
	found := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			pkg, isIdent := fn.X.(*ast.Ident)
			if isIdent && pkg.Name == "inventory" && fn.Sel.Name == "CouldNotAsk" {
				found = true
			}
		case *ast.Ident:
			if fn.Name == "CouldNotAsk" {
				found = true
			}
		}
		return true
	})
	return found
}

// assertsAgainstTheMarker reports whether the test source contains a comparison (== or !=) with
// inventory.MissingRemedyMarker (or the bare identifier inside the inventory package) on one side — a real
// assertion about the marker, not a mention of it.
func assertsAgainstTheMarker(source string) bool {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "src_test.go", source, 0)
	if err != nil {
		return false
	}
	names := func(expr ast.Expr) bool {
		switch e := expr.(type) {
		case *ast.SelectorExpr:
			pkg, ok := e.X.(*ast.Ident)
			return ok && pkg.Name == "inventory" && e.Sel.Name == "MissingRemedyMarker"
		case *ast.Ident:
			return e.Name == "MissingRemedyMarker"
		}
		return false
	}
	found := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		binary, ok := n.(*ast.BinaryExpr)
		if !ok || (binary.Op != token.NEQ && binary.Op != token.EQL) {
			return true
		}
		if names(binary.X) || names(binary.Y) {
			found = true
		}
		return true
	})
	return found
}
