package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// ── AN_AGENT_THAT_SPEAKS_PLAINLY: the command line says its problems in the agent's one voice ─────────────────────────
//
// THE CENSUS. In the files that parse the command line and start devices, doctor and connect, nothing is written to
// stderr except through refuse — which says it in three parts — and the usage that follows a mistake. Read with Go's own
// parser, so a new `fmt.Fprintf(stderr, …)` is a red here before it is a bare sentence in front of a person.
func TestEveryRefusalOnTheCommandLineSaysWhatToDo(t *testing.T) {
	refusals := 0
	for _, file := range []string{"cli.go", "main.go"} {
		files := token.NewFileSet()
		parsed, err := parser.ParseFile(files, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "refuse" {
				refusals++
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(selector.Sel.Name, "Fprint") || len(call.Args) == 0 {
				return true
			}
			if target, ok := call.Args[0].(*ast.Ident); !ok || target.Name != "stderr" {
				return true
			}
			// The usage after a mistake is the one other thing stderr is given.
			for _, arg := range call.Args[1:] {
				if inner, ok := arg.(*ast.CallExpr); ok {
					if fn, ok := inner.Fun.(*ast.Ident); ok && fn.Name == "usage" {
						return true
					}
				}
			}
			t.Errorf("%s: writes to stderr without refuse — a problem is said in three parts", files.Position(call.Pos()))
			return true
		})
	}
	if refusals < 15 {
		t.Fatalf("found %d refusals in cli.go and main.go — the census is not reading them", refusals)
	}
}

// A MISTAKE ON THE COMMAND LINE is the three parts, then the usage, on stderr — and plain, into a buffer.
func TestAMistakeIsWhatThenWhyThenWhatToDoThenTheUsage(t *testing.T) {
	code, out, errOut := runParse(t, "connect", "--bogus")
	if code != 2 || out != "" {
		t.Fatalf("a mistake exits 2 with nothing on stdout, got %d %q", code, out)
	}
	lines := strings.Split(errOut, "\n")
	if len(lines) < 4 || lines[0] != `✗ unknown option "--bogus"` || lines[1] != "  Nothing was run." ||
		!strings.HasPrefix(lines[2], "  → ") || !strings.Contains(errOut, "\n\n"+usage()) {
		t.Fatalf("what is wrong, why it matters, what to do, then the usage:\n%s", errOut)
	}
	if strings.Contains(errOut, "\033") {
		t.Fatalf("a buffer is no terminal: no colour:\n%q", errOut)
	}
}
