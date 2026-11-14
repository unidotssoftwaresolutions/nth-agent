package ui_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ── THE CENSUS: every problem the code can print says what to do ─────────────────────────────────────────────────────
//
// A problem is a ui.Problem, and every one the module builds is a composite literal somewhere in its source. Each must
// carry what is wrong, why it matters and what to do — `What`, `Why` and `Do`, none of them an empty string — read with
// Go's own parser over every non-test file, so a problem added tomorrow is read too.
//
// ONE STATED EXCEPTION: a literal handed straight to the connect narrator's driverDown may leave out `Why`, because
// driverDown fills in the one sentence every "devices are not being offered" shares (that the phones stay visible).
func TestEveryProblemLiteralSaysWhatIsWrongWhyAndWhatToDo(t *testing.T) {
	root := filepath.Join("..", "..")
	found := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files := token.NewFileSet()
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		var stack []ast.Node
		ast.Inspect(parsed, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			literal, ok := n.(*ast.CompositeLit)
			if !ok || !isProblemType(literal.Type) {
				return true
			}
			found++
			fields := map[string]ast.Expr{}
			for _, element := range literal.Elts {
				if kv, ok := element.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok {
						fields[key.Name] = kv.Value
					}
				}
			}
			at := files.Position(literal.Pos())
			for _, part := range []string{"What", "Why", "Do"} {
				value, present := fields[part]
				if !present && part == "Why" && insideCallTo(stack, "driverDown") {
					continue
				}
				if !present || emptyString(value) {
					t.Errorf("%s: a problem with no %s — every problem says what is wrong, why it matters and what to do", at, part)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found < 30 {
		t.Fatalf("found only %d problems in the module — the census is not reading where they are written", found)
	}
}

func isProblemType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == "Problem"
	case *ast.Ident:
		return e.Name == "Problem"
	}
	return false
}

func emptyString(expr ast.Expr) bool {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && strings.TrimSpace(value) == ""
}

func insideCallTo(stack []ast.Node, name string) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		call, ok := stack[i].(*ast.CallExpr)
		if !ok {
			continue
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == name {
			return true
		}
	}
	return false
}
