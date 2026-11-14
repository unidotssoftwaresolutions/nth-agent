package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ── THE CAPTURE CA'S KEY NEVER LEAVES THIS MACHINE, AND THE SOURCE IS HOW (AN_AGENT_THAT_CAN_RECORD_ITS_PHONE rule 3) ──
//
// A key leaves a machine only by being turned into bytes and those bytes going somewhere. So this census reads the whole
// tree's syntax — import aliases resolved, so a renamed package hides nothing — for every way a private key becomes bytes
// or comes back from them, every PEM block named for a private key, and every reader of the key's file, and holds each to
// the one place it may be:
//
//   - the key is serialised once, in netcap's writeKeyFile, into a file created owner-only; and read back once, in
//     readKeyFile — both in internal/netcap/ca.go, both unexported;
//   - writeKeyFile is called only by CreateCA (`capture enable`), readKeyFile only by LoadCA;
//   - LoadCA is called only where a capture proxy starts: the controller's Start and setup's trust check — never by
//     doctor, never by a status or a declaration, which read the certificate alone;
//   - the key file's path is asked for only to check its permissions (the controller's caProblem);
//   - the CA's key field is read only to sign a leaf (leafFor).
//
// The behaviour half is internal/capture/keyleak_test.go: a whole capture driven on the bench, every output it can see
// searched for the key in every encoding it could leave in.

type keySite struct{ file, function, what string }

// theKeysOnlyWays is every key site in the tree, as it must be.
var theKeysOnlyWays = map[keySite]int{
	{"internal/netcap/ca.go", "writeKeyFile", "crypto/x509.MarshalPKCS8PrivateKey"}: 1,
	{"internal/netcap/ca.go", "writeKeyFile", `"PRIVATE KEY"`}:                      1,
	{"internal/netcap/ca.go", "readKeyFile", "crypto/x509.ParsePKCS8PrivateKey"}:    1,
	{"internal/netcap/ca.go", "readKeyFile", `"PRIVATE KEY"`}:                       1,
	{"internal/netcap/ca.go", "CreateCA", "writeKeyFile"}:                           1,
	{"internal/netcap/ca.go", "LoadCA", "readKeyFile"}:                              1,
	{"internal/capture/controller.go", "Start", "netcap.LoadCA"}:                    1,
	{"internal/capture/setup.go", "trustCheck", "netcap.LoadCA"}:                    1,
	{"internal/capture/controller.go", "caProblem", "netcap.KeyPathIn"}:             1,
	{"internal/netcap/ca.go", "leafFor", "CA.key"}:                                  1,
}

// keyBytesCalls is every standard-library function that turns a private key into bytes or back, by package path.
var keyBytesCalls = map[string][]string{
	"crypto/x509": {"MarshalPKCS8PrivateKey", "MarshalPKCS1PrivateKey", "MarshalECPrivateKey", "ParsePKCS8PrivateKey",
		"ParsePKCS1PrivateKey", "ParseECPrivateKey", "EncryptPEMBlock", "DecryptPEMBlock"},
	"crypto/tls":   {"X509KeyPair", "LoadX509KeyPair"},
	"crypto/ecdh":  {"*"},
	"crypto/mlkem": {"*"},
}

var pemPrivateKeyType = regexp.MustCompile(`^[A-Z0-9 ]*PRIVATE KEY$`)

func TestTheCaptureKeyHasOneWayOutAndItIsAFileOnThisMachine(t *testing.T) {
	root, files := nonTestGoFiles(t)
	found := map[keySite]int{}
	for _, path := range files {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		inNetcap := strings.HasPrefix(rel, "internal/netcap/")

		named := map[string]string{} // the name this file gives each package it imports
		for _, spec := range parsed.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			name := importPath[strings.LastIndex(importPath, "/")+1:]
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if name == "." && (keyBytesCalls[importPath] != nil || strings.HasSuffix(importPath, "/internal/netcap")) {
				t.Errorf("%s dot-imports %s — its calls cannot be read by this census", rel, importPath)
			}
			named[name] = importPath
		}

		for _, decl := range parsed.Decls {
			function := "(package level)"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				function = fn.Name.Name
			}
			site := func(what string) { found[keySite{rel, function, what}]++ }
			ast.Inspect(decl, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.BasicLit:
					if node.Kind == token.STRING {
						if value, err := strconv.Unquote(node.Value); err == nil && pemPrivateKeyType.MatchString(value) {
							site(node.Value)
						}
					}
				case *ast.CallExpr:
					if ident, ok := node.Fun.(*ast.Ident); ok && inNetcap && (ident.Name == "readKeyFile" || ident.Name == "writeKeyFile") {
						site(ident.Name)
					}
				case *ast.SelectorExpr:
					if pkg, ok := node.X.(*ast.Ident); ok {
						importPath := named[pkg.Name]
						for _, call := range keyBytesCalls[importPath] {
							if call == "*" || call == node.Sel.Name {
								site(importPath + "." + node.Sel.Name)
							}
						}
						if strings.HasSuffix(importPath, "/internal/netcap") && (node.Sel.Name == "LoadCA" || node.Sel.Name == "KeyPathIn") {
							site("netcap." + node.Sel.Name)
						}
					}
					// THE FIELD: inside netcap, a selector named key is the CA's (no other netcap type has one).
					if inNetcap && node.Sel.Name == "key" {
						site("CA.key")
					}
				case *ast.Ident:
					// caKeyFile, the key file's name, is netcap's own; only ca.go may spell it (and does, where it builds
					// the path). Elsewhere in netcap it would be a second way to the file.
					if inNetcap && node.Name == "caKeyFile" && rel != "internal/netcap/ca.go" {
						site("caKeyFile")
					}
				}
				return true
			})
		}
	}

	// INSTRUMENT GUARD: the census found the sites that are known to be there — a walk that missed them is reading the
	// wrong tree, and would be green for nothing.
	if found[keySite{"internal/netcap/ca.go", "writeKeyFile", "crypto/x509.MarshalPKCS8PrivateKey"}] == 0 {
		t.Fatal("instrument guard: the census did not find the one place the key is serialised")
	}
	for _, s := range slices.SortedFunc(maps.Keys(found), func(a, b keySite) int {
		return strings.Compare(a.file+a.function+a.what, b.file+b.function+b.what)
	}) {
		if want, known := theKeysOnlyWays[s]; !known || found[s] != want {
			t.Errorf("%s, in %s: %s (%d) — the capture CA's key has one way to the disk and one way back, both in internal/netcap/ca.go, and is loaded only where a capture proxy starts", s.file, s.function, s.what, found[s])
		}
	}
	for s, want := range theKeysOnlyWays {
		if found[s] != want {
			t.Errorf("%s, in %s: %s is there %d times, and the census holds it at %d — a site that moved is a decision made here", s.file, s.function, s.what, found[s], want)
		}
	}
}
