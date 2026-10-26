package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// THE_SIGNATURE_THAT_BELONGS_TO_THE_RACK item 4 — this agent never signs an app, and never will.
//
// The agent runs on a customer's own Mac, beside a customer's own phone, with the customer's own identity in the
// keychain. The ONE signing that happens there is xcodebuild building Appium's WebDriverAgent runner with THAT
// machine's identity (`setup-ios`, internal/wda) — Apple's toolchain signing Appium's test runner for the phone
// on the desk, which is what Appium itself does on every session. A customer's app is never touched: no
// `codesign`, no re-signing, no signing certificate or key is imported, unlocked or written by this tree. A
// build that re-signs the platform's way belongs on the platform's own rack, where the certificate is the
// platform's — the arrangement `ForeignRealDeviceRefusal` keeps from the other side.
//
// THE ONE CERTIFICATE THIS TREE MAKES (AN_AGENT_THAT_CAN_RECORD_ITS_PHONE rule 9, 2026-10-04). Until that slice this
// seal forbade creating any certificate or key, everywhere. Network capture needs one authority of the agent's own:
// made by `nth-agent capture enable` and only after a person types ENABLE, used only to sign the short-lived TLS
// certificates that stand in for the servers a test app talks to, its key never leaving the machine, ending after one
// year. So exactly one file — internal/netcap/ca.go — may say x509.CreateCertificate, rsa.GenerateKey and
// ecdsa.GenerateKey, and nothing else in this vocabulary: ed25519, pkcs12, a keychain, codesign — forbidden there as
// everywhere. TestTheOnlyCertificateMintingIsTheCaptureCA proves the same from the syntax tree, where an import alias
// cannot hide a call, and counts that file's minting calls one by one. The promise customers read changed with it, in
// the same words in the README, --help and doctor (internal/capture.ThePromise).
//
// THE POPULATION GUARD: the walk must read the real tree. Forty non-test Go files were measured on 2026-09-02;
// a walk that finds fewer than thirty has found the wrong tree, and a seal over the wrong tree is green for
// nothing.

// signingVocabulary is what a file that signs, re-signs or handles a credential would have to say. `codesign`
// is matched as a whole word so `codesigning` — the keychain POLICY name `security find-identity -p codesigning`
// reads, a read the doctor makes on purpose — does not match.
var signingVocabulary = []*regexp.Regexp{
	regexp.MustCompile(`\bcodesign\b`),
	regexp.MustCompile(`\bzsign\b`),
	regexp.MustCompile(`\bfastlane\b`),
	regexp.MustCompile(`"sigh"`),
	regexp.MustCompile(`"security",\s*"(import|unlock-keychain|add-certificates|create-keychain|set-key-partition-list|delete-keychain)"`),
	regexp.MustCompile(`(?i)\bpkcs ?#?12\b`),
	regexp.MustCompile(`x509\.CreateCertificate`),
	regexp.MustCompile(`\b(rsa|ecdsa|ed25519)\.GenerateKey\b`),
	regexp.MustCompile(`embedded\.mobileprovision`),
	regexp.MustCompile(`\.p12\b`),
}

// wdaBuildFile is the ONE file that may name a code-signing identity, because it builds WebDriverAgent.
const wdaBuildFile = "internal/wda/wda.go"

// captureCAFile is the ONE file that may create a certificate or a key, because it is network capture's own CA — and
// captureCAMayMint is exactly what it may say: these three words, as written, and no other match of signingVocabulary.
const captureCAFile = "internal/netcap/ca.go"

var captureCAMayMint = []string{"x509.CreateCertificate", "rsa.GenerateKey", "ecdsa.GenerateKey"}

func nonTestGoFiles(t *testing.T) (root string, files []string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root was not found above this package (looked for go.mod in %s)", root)
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 30 {
		t.Fatalf("the walk found %d non-test Go files under %s; the tree had 40 when this seal was written, so this walk is reading the wrong tree", len(files), root)
	}
	return root, files
}

func TestTheAgentNeverSignsAnApp(t *testing.T) {
	root, files := nonTestGoFiles(t)
	excused := 0
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, word := range signingVocabulary {
			// EVERY MATCH, not the first: with one file excused for three words, a forbidden word after an excused one
			// must still be seen.
			for _, loc := range word.FindAllIndex(source, -1) {
				said := string(source[loc[0]:loc[1]])
				if rel == captureCAFile && slices.Contains(captureCAMayMint, said) {
					excused++
					continue
				}
				line := 1 + strings.Count(string(source[:loc[0]]), "\n")
				t.Errorf("%s:%d says %q — this agent never signs or re-signs an app, never touches a signing identity, and creates a certificate or a key only in %s (network capture's CA); a build that must be re-signed belongs on the platform's rack", rel, line, said, captureCAFile)
			}
		}
	}
	// THE EXCUSE IS NOT A STANDING HOLE: it covers words the capture CA really says. If the file moved or stopped minting,
	// this excuse is excusing nothing, and the line above it is the one to delete.
	if excused == 0 {
		t.Fatalf("%s is excused for %v and says none of them — the excuse outlived its file; remove it", captureCAFile, captureCAMayMint)
	}
}

// mintingCalls is every function in the standard library that makes a certificate or a private key, by package path —
// read through each file's own imports, so `import r "crypto/rsa"; r.GenerateKey(...)` is the same call. A package that
// mints only through values (crypto/ecdh's curves, crypto/mlkem's keys) is listed with "*": any use of it is minting.
var mintingCalls = map[string][]string{
	"crypto/x509":    {"CreateCertificate", "CreateCertificateRequest", "CreateRevocationList"},
	"crypto/rsa":     {"GenerateKey", "GenerateMultiPrimeKey"},
	"crypto/ecdsa":   {"GenerateKey"},
	"crypto/ed25519": {"GenerateKey", "NewKeyFromSeed"},
	"crypto/dsa":     {"GenerateKey", "GenerateParameters"},
	"crypto/ecdh":    {"*"},
	"crypto/mlkem":   {"*"},
	"crypto/mldsa":   {"*"},
	"crypto/slhdsa":  {"*"},
}

// theCaptureCAMints is ALL the minting in the tree, function by function: the CA's own key and certificate, made once by
// `capture enable` (CreateCA), and a host's short-lived leaf (leafFor). Each count is a decision; a new call is a new one.
var theCaptureCAMints = map[string]int{
	"CreateCA: crypto/rsa.GenerateKey":        1,
	"CreateCA: crypto/x509.CreateCertificate": 1,
	"leafFor: crypto/ecdsa.GenerateKey":       1,
	"leafFor: crypto/x509.CreateCertificate":  1,
}

func TestTheOnlyCertificateMintingIsTheCaptureCA(t *testing.T) {
	root, files := nonTestGoFiles(t)
	found := map[string]map[string]int{} // file → "function: package.Name" → count
	for _, path := range files {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		// The names this file gives the minting packages.
		named := map[string]string{}
		for _, spec := range parsed.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if _, mints := mintingCalls[importPath]; !mints {
				continue
			}
			name := importPath[strings.LastIndex(importPath, "/")+1:]
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if name == "." {
				t.Errorf("%s dot-imports %s — its calls cannot be told apart from the file's own, so this seal cannot read them", rel, importPath)
				continue
			}
			named[name] = importPath
		}
		if len(named) == 0 {
			continue
		}
		for _, decl := range parsed.Decls {
			function := "(package level)"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				function = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				selector, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				importPath, ok := named[pkg.Name]
				if !ok {
					return true
				}
				for _, mint := range mintingCalls[importPath] {
					if mint == "*" || mint == selector.Sel.Name {
						if found[rel] == nil {
							found[rel] = map[string]int{}
						}
						found[rel][function+": "+importPath+"."+selector.Sel.Name]++
					}
				}
				return true
			})
		}
	}

	for file, calls := range found {
		if file != captureCAFile {
			t.Errorf("%s makes a certificate or a key (%v) — only %s may, and only as network capture's CA", file, calls, captureCAFile)
		}
	}
	got := found[captureCAFile]
	for call, want := range theCaptureCAMints {
		if got[call] != want {
			t.Errorf("%s: %q is called %d times, and the capture CA makes it %d — every minting call is counted", captureCAFile, call, got[call], want)
		}
	}
	for call, count := range got {
		if _, known := theCaptureCAMints[call]; !known {
			t.Errorf("%s makes something new: %q (%d) — a new minting call is a decision made in this seal, not in that file", captureCAFile, call, count)
		}
	}
}

func TestTheOnlySigningIsWebDriverAgentBuiltByXcodebuildOnThisMachine(t *testing.T) {
	root, files := nonTestGoFiles(t)
	// CODE_SIGN_IDENTITY is the xcodebuild argument that picks the certificate — the act of signing. DEVELOPMENT_TEAM
	// is deliberately NOT in this expression: the doctor READS it off the WebDriverAgent project to say which team
	// a build would use, and a read is not a signature.
	identity := regexp.MustCompile(`CODE_SIGN_IDENTITY`)
	var namesAnIdentity []string
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Match(source) {
			rel, _ := filepath.Rel(root, path)
			namesAnIdentity = append(namesAnIdentity, filepath.ToSlash(rel))
		}
	}
	if len(namesAnIdentity) != 1 || namesAnIdentity[0] != wdaBuildFile {
		t.Fatalf("a code-signing identity is named in %v; only %s may, and only for the WebDriverAgent build", namesAnIdentity, wdaBuildFile)
	}

	wda, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(wdaBuildFile)))
	if err != nil {
		t.Fatal(err)
	}
	// It is xcodebuild building the RUNNER for the phone on the desk — build-for-testing of WebDriverAgent — and
	// it never names an app archive: the customer's build is not an input to anything in this tree.
	for _, must := range []string{`"xcodebuild"`, `"build-for-testing"`} {
		if !strings.Contains(string(wda), must) {
			t.Errorf("%s no longer says %s — the one permitted signing is the WebDriverAgent build, and this file has stopped being that", wdaBuildFile, must)
		}
	}
	if strings.Contains(string(wda), ".ipa") || strings.Contains(string(wda), "Payload/") {
		t.Errorf("%s names an app archive — the WebDriverAgent build must not take a customer's build as an input", wdaBuildFile)
	}
}
