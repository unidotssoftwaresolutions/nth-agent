package android_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE HOME FOR THE PRODUCTION SEAM. android.AdbRunner is the Runner implementation, beside the probe that
// consumes it (ios.UsbmuxdLister already showed the shape). It lived in package main, where the seam test
// could only replicate it — so a change to the runner moved nothing.
//
// This pin keeps it from moving back: `package main` may WIRE the seam (naming android.AdbRunner) but must not
// DEFINE a runner of its own. Copying the implementation back would give the repo two runners, one of which
// nothing tests — which is exactly the state this carried-forward item corrected.
func TestTheAdbRunnerHasOneHome(t *testing.T) {
	mainPath := filepath.Join("..", "..", "cmd", "nth-agent", "main.go")
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("reading %s: %v", mainPath, err)
	}
	source := string(data)

	if !strings.Contains(source, "android.AdbRunner") {
		t.Fatal("main must WIRE the seam by naming android.AdbRunner — if it stopped, the production path is no longer the tested one")
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "main.go", source, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Type.Params == nil {
			return true
		}
		// A runner is (context.Context, ...string) (string, error). Detected by SHAPE, so a copy under any
		// name is caught — `adbRunner`, `runAdb`, `shellOut`.
		params := fn.Type.Params.List
		if len(params) != 2 || fn.Type.Results == nil || len(fn.Type.Results.List) != 2 {
			return true
		}
		if exprName(params[0].Type) != "context.Context" || !isVariadicString(params[1]) {
			return true
		}
		if exprName(fn.Type.Results.List[0].Type) == "string" && exprName(fn.Type.Results.List[1].Type) == "error" {
			t.Fatalf("main.go defines %s, a Runner of its own — the production runner has ONE home, "+
				"internal/android.AdbRunner, which the seam test actually calls", fn.Name.Name)
		}
		return true
	})
}

func isVariadicString(field *ast.Field) bool {
	ellipsis, ok := field.Type.(*ast.Ellipsis)
	return ok && exprName(ellipsis.Elt) == "string"
}

func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprName(e.X) + "." + e.Sel.Name
	case *ast.Ellipsis:
		return "..." + exprName(e.Elt)
	}
	return ""
}
