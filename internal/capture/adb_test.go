package capture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── WHAT THIS AGENT MAY ASK A PHONE, HELD FROM THE SOURCE ───────────────────────────────────────────────────────
//
// The bench enforces the list at run time; this holds it in the source, where a new command is added:
//
//   - every adb command capture runs is built by one of adb.go's *Args functions — a call to the Adb seam anywhere in this
//     package passes ctx and one builder's result, and nothing else;
//   - those builders say only the words below;
//   - and nothing in the whole tree names root, unroot, remount, su, or a certificate store.

// theWordsAPhoneIsSent is every literal adb.go's builders use: the proxy setting, a reverse, the certificate copied to
// Downloads, two properties, one address opened, the list of phones.
// (The pushed file's place on the phone is DeviceCertificatePath, a constant, pinned in setup_test.go.)
var theWordsAPhoneIsSent = []string{
	"--es", "--remove", "-a", "-d", "-s", ":0", "am", "android.intent.action.VIEW", "com.android.browser.application_id", "devices", "get",
	"getprop", "global", "http_proxy", "nthcase-capture-check", "push", "put", "reverse", "settings", "shell", "start", "tcp:",
}

func TestEveryAdbCommandIsOneOfTheBuilders(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files[name] = file
		}
	}
	builders := map[string]bool{}
	var words []string
	calls := 0
	{
		for name, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !strings.HasSuffix(fn.Name.Name, "Args") {
					continue
				}
				if filepath.Base(name) != "adb.go" {
					t.Errorf("%s builds adb arguments (%s) outside adb.go", name, fn.Name.Name)
				}
				builders[fn.Name.Name] = true
				ast.Inspect(fn, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						word, _ := strconv.Unquote(lit.Value)
						words = append(words, word)
					}
					return true
				})
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !callsTheAdbSeam(call) {
					return true
				}
				calls++
				if len(call.Args) != 2 || !call.Ellipsis.IsValid() {
					t.Errorf("%s: an adb call must be (ctx, someArgs(...)...)", fset.Position(call.Pos()))
					return true
				}
				builder := ""
				if inner, ok := call.Args[1].(*ast.CallExpr); ok {
					if ident, ok := inner.Fun.(*ast.Ident); ok {
						builder = ident.Name
					}
				}
				if !strings.HasSuffix(builder, "Args") {
					t.Errorf("%s: an adb call must take its arguments from one of adb.go's builders", fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
	// INSTRUMENT GUARDS: the population is the real one. (At least: a new builder is judged by the words it says, below.)
	if len(builders) < 9 || calls < 10 {
		t.Fatalf("found %d builders and %d adb calls — this census is reading the wrong package", len(builders), calls)
	}
	sort.Strings(words)
	words = slices.Compact(words)
	if strings.Join(words, " ") != strings.Join(theWordsAPhoneIsSent, " ") {
		t.Fatalf("the builders say %q\nand the list is %q — a new word a phone is sent is decided here", words, theWordsAPhoneIsSent)
	}
}

// callsTheAdbSeam: a call of something named Adb or adb — the Config's, the options', or the parameter.
func callsTheAdbSeam(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name == "Adb"
	case *ast.Ident:
		return fun.Name == "adb"
	}
	return false
}

func TestNothingInTheTreeAsksAPhoneForRootOrItsCertificateStore(t *testing.T) {
	forbidden := regexp.MustCompile(`"(root|unroot|remount|su|disable-verity)"|cacerts|/system/etc/security|/apex/com\.android\.conscrypt`)
	root := filepath.Join("..", "..")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if loc := forbidden.FindIndex(source); loc != nil {
			t.Errorf("%s says %q — this agent never roots a phone and never touches its certificate store", path, source[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("scanned %d files — the walk is reading the wrong tree", scanned)
	}
}
