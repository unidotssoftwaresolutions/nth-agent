package browserstack_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// ── ITEM 2's INSTRUMENT: the access key is unloggable, asserted rather than intended ───────────────────────
//
// THE SHAPE IS THE TOKEN CENSUS'S (farm/transport_test.go), and it is here for the same reason: every place
// production code touches the credential is a potential way out of the process, so the POPULATION IS
// DISCOVERED rather than listed. A new read fails until its author says which exit it feeds and what guards
// it. A test that named three call sites would pass forever while a fourth was added beside it.

// walkProduction hands every non-test .go file in the repository to fn, parsed.
func walkProduction(t *testing.T, fn func(path string, parsed *ast.File)) {
	t.Helper()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, source, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		fn(path, parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}
}

// credentialNames are the identifiers a BrowserStack secret travels under in this codebase.
var credentialNames = map[string]bool{"Key": true, "AccessKey": true, "accessKey": true, "bsKey": true}

func TestEveryReadOfTheAccessKeyIsClassified(t *testing.T) {
	classified := map[string]string{
		"browserstack.go": "DEFINES Credentials.Key and writes it into bstack:options — the one intended exit, and it goes into a request BODY that is built here and sent from this machine to BrowserStack directly. It is never returned, never formatted into an error (Merge's failures name the SHAPE), and never part of Failure.Describe.",
		"main.go":         "reads NTH_AGENT_BROWSERSTACK_KEY / --browserstack-key into memory and hands it to tunnel.Config; never echoed (the usage text warns that argv is visible to `ps`, which is why the environment twin is preferred)",
		"devices.go":      "SETS HTTP basic auth on the device-list request — the THIRD exit, and the only one the agent makes on a NORMAL START: the configured device specs are checked against the account's own catalogue before any row is published. Same mechanism as upload.go (an Authorization header this file builds, sent from this machine to api-cloud.browserstack.com directly); the key is never returned (Catalogue yields device names and OS versions), never formatted into an error (the failures carry the HTTP status and the account's own words), and never narrated.",
		// ⚠️ THE MAP IS KEYED BY BASENAME, which is a real hazard now that the tree has an upload in two packages.
		// `internal/browserstack/upload.go` and a `cmd/nth-agent/upload.go` would collide here and one
		// classification would silently stand in for the other — so the command-side file is `uploadcmd.go`, and
		// this comment is why it is not called the obvious thing.
		"uploadfile.go": "SETS HTTP basic auth on the app-upload request for a LOCAL FILE — the same exit and the same mechanism as upload.go beside it, differing only in what fills the body (the build's bytes, streamed from this machine, rather than a URL for BrowserStack to fetch). The key goes into an Authorization header this file builds and nowhere else: it is not in the returned App (a bs:// id and a custom id, neither secret), not in any error (they name the local path, the HTTP status, or the account's own words), and not narrated.",
		"uploadcmd.go":  "READS NTH_AGENT_BROWSERSTACK_KEY / --browserstack-key into memory for the `upload` verb and hands it to browserstack.Uploader, which puts it in an Authorization header. The verb's own output prints the bs:// id, the custom id and the next command — none of which is a secret — and prints nothing else; the printing census below proves no credential-named value reaches a print call anywhere in the tree, this file included.",
		"hubanswer.go":  "READS the key only to FIND AND REMOVE it: every answer to a frame the tunnel sent to the hub is handed to ScrubAnswer before it is relayed to the farm, and every spelling of the key in it (as written, JSON-escaped, percent-encoded, hex, base64 alone, as user:key and mid-run) becomes KeyMask. Nothing here returns, formats or narrates the key; the spellings it builds live only for that replacement (A_CAPTURE_THAT_MASKS_WHAT_IT_KEEPS).",
		"upload.go":     "SETS HTTP basic auth on the app-upload request — a SECOND exit and a DIFFERENT mechanism from the hub's (which carries credentials inside the session body). The key goes into an Authorization header this file builds and is sent from this machine to api-cloud.browserstack.com directly. It is never returned (App carries only the bs:// id and the custom id, neither secret), never formatted into an error (the failures name the HTTP status and the account's own words), and never narrated.",
	}

	found := map[string]bool{}
	walkProduction(t, func(path string, parsed *ast.File) {
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				if credentialNames[node.Sel.Name] {
					found[filepath.Base(path)] = true
				}
			case *ast.KeyValueExpr:
				if ident, ok := node.Key.(*ast.Ident); ok && credentialNames[ident.Name] {
					found[filepath.Base(path)] = true
				}
			case *ast.Field:
				for _, name := range node.Names {
					if credentialNames[name.Name] {
						found[filepath.Base(path)] = true
					}
				}
			}
			return true
		})
	})

	// INSTRUMENT GUARD: the definition site must be in the population — a sweep that lost the file where the
	// credential lives is reading the wrong tree, and would pass by finding nothing.
	if !found["browserstack.go"] {
		t.Fatal("instrument guard: the sweep did not find the file that defines the credential")
	}
	for file := range found {
		if _, ok := classified[file]; !ok {
			t.Fatalf("%s touches the access key and is not classified — say which exit it feeds and what guards it, "+
				"then add it here. A new read of a credential is a new way out of the process.", file)
		}
	}
}

// printingCalls are the ways a string becomes output a person or a log file can keep.
func isPrintingCall(fun ast.Expr) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if ok {
		pkg, isIdent := selector.X.(*ast.Ident)
		if isIdent && (pkg.Name == "fmt" || pkg.Name == "log" || pkg.Name == "slog") {
			return true
		}
		// t.say(...) and friends — a narration method on the receiver.
		return strings.EqualFold(selector.Sel.Name, "say") || strings.EqualFold(selector.Sel.Name, "narrate")
	}
	if ident, isIdent := fun.(*ast.Ident); isIdent {
		return ident.Name == "println" || ident.Name == "print"
	}
	return false
}

// THE PROPERTY, NOT A CALL SITE. Item 2 asks that the key never appear in narration, in a refusal, in an error
// string or in the forwarded response — so this asserts that no printing call ANYWHERE in production code is
// handed anything named like the credential. The forwarded response is held BY BEHAVIOUR, not by this sweep:
// internal/tunnel's TestTheHubsAnswerNeverCarriesTheKey has a hub echo the key, in every spelling ScrubAnswer
// knows, and decodes what reaches the farm (A_CAPTURE_THAT_MASKS_WHAT_IT_KEEPS). The obvious next thing somebody adds is a debug print of "the
// capabilities we sent"; the merged body carries the key, and that print would put it in a log file.
func TestTheAccessKeyNeverReachesAPrintingCall(t *testing.T) {
	var offences []string
	walkProduction(t, func(path string, parsed *ast.File) {
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isPrintingCall(call.Fun) {
				return true
			}
			for _, arg := range call.Args {
				ast.Inspect(arg, func(inner ast.Node) bool {
					switch node := inner.(type) {
					case *ast.SelectorExpr:
						if credentialNames[node.Sel.Name] {
							offences = append(offences, filepath.Base(path)+": "+node.Sel.Name)
						}
					case *ast.Ident:
						if credentialNames[node.Name] {
							offences = append(offences, filepath.Base(path)+": "+node.Name)
						}
					}
					return true
				})
			}
			return true
		})
	})
	if len(offences) > 0 {
		t.Fatalf("a credential reached a printing call: %v — the key is read on the customer's machine and must "+
			"have nowhere to go but the request body it authenticates", offences)
	}
}

// THE VACUITY DIRECTION, and the sweep above is worthless without it: if the walk found no printing calls at
// all, it would pass on a codebase that prints the key on every line.
func TestThePrintingSweepActuallyFindsPrintingCalls(t *testing.T) {
	printing := 0
	walkProduction(t, func(_ string, parsed *ast.File) {
		ast.Inspect(parsed, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isPrintingCall(call.Fun) {
				printing++
			}
			return true
		})
	})
	if printing < 20 {
		t.Fatalf("the sweep found only %d printing calls in an agent whose whole job is telling a person what it "+
			"sees — it is reading the wrong tree, and its green means nothing", printing)
	}
}

// AND THE MERGED BODY IS NOT ECHOED EITHER. The key is inside it, so a print of the body is a print of the key
// by another name — which the identifier sweep above would not catch, because the variable is called `merged`.
func TestTheMergedBodyIsNeverPrinted(t *testing.T) {
	var offences []string
	walkProduction(t, func(path string, parsed *ast.File) {
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isPrintingCall(call.Fun) {
				return true
			}
			for _, arg := range call.Args {
				ast.Inspect(arg, func(inner ast.Node) bool {
					if ident, isIdent := inner.(*ast.Ident); isIdent {
						switch ident.Name {
						case "merged", "decoded", "body", "payload":
							offences = append(offences, filepath.Base(path)+": "+ident.Name)
						}
					}
					return true
				})
			}
			return true
		})
	})
	if len(offences) > 0 {
		t.Fatalf("a request/response body reached a printing call: %v — on the BrowserStack path the session "+
			"body carries the customer's key, so printing it is printing the key", offences)
	}
}

// ── THE BUDGET: what proves a default test run cannot reach the customer's account ────────────────────────
//
// ⚠️ THIS SWEEP USED TO BE A HAND-KEPT LIST OF ONE. It searched for the literal `hub-cloud.browserstack.com`,
// so when this slice added `api-cloud.browserstack.com` — a second real host, on which an UPLOAD WRITES TO THE
// CUSTOMER'S ACCOUNT — the sweep was blind to it and would have stayed green. A curated population with no
// guard that it is complete is the defect the injection rounds in this programme have found four times.
//
// SO THE POPULATION IS DISCOVERED. Every distinct `*.browserstack.com` host in any .go file is extracted, and
// each one must be in the census below saying what it is and why it may appear where it does. The NEXT host is
// a red test rather than a silent gap.
func TestEveryBrowserStackHostInTheTreeIsAccountedFor(t *testing.T) {
	census := map[string]struct {
		what        string
		liveTagOnly bool
	}{
		"hub-cloud.browserstack.com": {
			what:        "the Appium hub: session creation and every in-session command. Reaching it can START A SESSION, which bills device-minutes.",
			liveTagOnly: true,
		},
		"api-cloud.browserstack.com": {
			// ⚠️ THIS HOST NOW SERVES TWO DIFFERENT KINDS OF CALL, and only one of them is dangerous. The app
			// upload WRITES to the customer's account, which is why the tag rule exists. The device list is a
			// free READ that writes nothing and starts no session — and the agent makes it on every normal start,
			// to check a configured spec against the account's own catalogue before publishing a row for it.
			//
			// So the rule stays "only the files that DEFINE the endpoints may name the host", and both of those
			// files are exempt by path below. What the tag protects is the TESTS: no test may reach this host
			// without somebody typing the tag, whichever endpoint it wants.
			what:        "the App Automate REST API: the app upload (WRITES to the customer's account) and the device list (a free read the agent makes at startup). Neither costs device-minutes; the upload's write is reason enough for the rule.",
			liveTagOnly: true,
		},
		"www.browserstack.com": {
			what:        "BrowserStack's own published sample build (WikipediaSample.apk). NOT an API and never fetched by this agent — the string is handed to BrowserStack, who fetch it themselves, and in the default tests it is handed to a stub server. Inert, so no tag is required.",
			liveTagOnly: false,
		},
	}

	// ASSEMBLED FROM PIECES so this file does not match its own sweep. The first version used a literal and the
	// regex matched ITSELF, reporting a host called ".browserstack.com" — a sweep whose first finding is its own
	// source is a sweep that would have kept finding it forever.
	needle := "browserstack" + "." + "com"
	root := filepath.Join("..", "..")
	var err error
	host := regexp.MustCompile(`(?:[a-z0-9-]+\.)+` + regexp.QuoteMeta(needle))

	// THE FILES ALLOWED TO NAME A REAL HOST WITHOUT THE LIVE BUILD TAG — each a full path, each with its reason.
	exemptPaths := map[string]bool{
		// the constants that DEFINE these hosts; everything else in the tree refers to them by name
		"internal/browserstack/browserstack.go": true,
		"internal/browserstack/upload.go":       true,
		"internal/browserstack/devices.go":      true,
		// THIS FILE: the census names every host in order to classify it, and dials none of them
		"internal/browserstack/leak_test.go": true,
	}

	found := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range host.FindAllString(string(source), -1) {
			found[match] = append(found[match], path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}

	// INSTRUMENT GUARD: a sweep that found nothing would report "no host is reachable" on a tree that reaches
	// them everywhere. Both API hosts must be in the population for this test to mean anything.
	for _, must := range []string{"hub-cloud.browserstack.com", "api-cloud.browserstack.com"} {
		if len(found[must]) == 0 {
			t.Fatalf("instrument guard: the sweep did not find %s — it is reading the wrong tree", must)
		}
	}

	// AND THE EXEMPTIONS ARE GUARDED TOO: a rename must turn this seal RED rather than quietly widening it, so
	// every exempt path has to still exist.
	for path := range exemptPaths {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("the exempt path %s no longer exists — an exemption for a file that is gone is an exemption "+
				"nobody is checking, and the file that replaced it is unguarded: %v", path, err)
		}
	}

	for name, files := range found {
		entry, known := census[name]
		if !known {
			t.Fatalf("%s appears in %v and is in no census — say what it is and whether reaching it costs the "+
				"customer anything, then add it. A host nobody classified is a host nobody bounded.", name, files)
		}
		if !entry.liveTagOnly {
			continue
		}
		for _, file := range files {
			// ⚠️ BY PATH, NOT BY BASENAME. This list used to compare `filepath.Base(file) == "upload.go"`, so ANY
			// second file anywhere in the tree called upload.go or leak_test.go inherited the exemption silently.
			// Discovering the population and then hand-keeping the exemptions leaves the second level exactly as
			// it was — which is the shape this very test was written to remove one level up.
			if exemptPaths[relative(t, file)] {
				continue
			}
			source, _ := os.ReadFile(file)
			if !strings.Contains(string(source), "//go:build browserstack_live") {
				t.Fatalf("%s names %s outside the live build tag — a routine `go test ./...` would %s",
					relative(t, file), name, entry.what)
			}
		}
	}
}

// ── THE SEAL INJECTION 5 PROVED WAS MISSING ────────────────────────────────────────────────────────────────
//
// Injection 5 moved the session's DELETE out of its defer and to the end of the function — so any failure
// between creating a session and reaching that line leaves it running, billing against a 100-minute trial
// until idleTimeout expires. IT TURNED NOTHING RED. Nothing inspected the live test's shape, and the live test
// is behind a build tag, so `go test ./...` does not even compile it: the one file whose bug costs money was
// the one file no test could see.
//
// This runs in the DEFAULT suite and reads the tagged file as source. `go/parser` does not apply build
// constraints, which is exactly what makes it possible to assert the shape of code this binary will never run.
func TestTheLiveTestEndsItsSessionInADefer(t *testing.T) {
	path := filepath.Join("live_test.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the live test must exist to be sealed: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the live test: %v", err)
	}

	deletes, deferredDeletes := 0, 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		// Every mention of the DELETE verb, wherever it sits.
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "MethodDelete" {
			deletes++
		}
		// …and the ones inside a defer.
		if deferred, ok := n.(*ast.DeferStmt); ok {
			ast.Inspect(deferred, func(inner ast.Node) bool {
				if sel, isSel := inner.(*ast.SelectorExpr); isSel && sel.Sel.Name == "MethodDelete" {
					deferredDeletes++
				}
				return true
			})
		}
		return true
	})

	// VACUITY FIRST: a live test that no longer ends anything would satisfy "all deletes are deferred" perfectly.
	if deletes == 0 {
		t.Fatal("the live test does not end its session at all — a session nobody ends bills until it times out")
	}
	if deferredDeletes != deletes {
		t.Fatalf("%d of %d session-ending calls are not deferred — a t.Fatalf between creating the session and "+
			"reaching that line leaves a real device running on the customer's account, which is the one bug in "+
			"this slice that costs money rather than time", deletes-deferredDeletes, deletes)
	}
}

// ⚠️ THIS SEAL USED TO ASSERT A CAUSE THAT WAS MEASURED NOT TO HAPPEN, and its own failure message stated it:
// "idleTimeout is the first [line of defence], and it is what ends the session when the whole process dies and
// no defer runs at all". MEASURED 2026-09-03 (field test item 4, the vendor's own session log): a cloud session
// whose worker was SIGKILLed was torn down 121.6 s after our last answered command — not at the 60 s this
// constant asks for, and not at the vendor's 90 s default either, which their message quoted back at a session
// that carried 60. A seal whose because-clause is false teaches the next reader the false thing, and it would
// have gone on passing forever because the number never moved.
//
// WHAT IS ASSERTED NOW IS WHAT IS TRUE: the value is a REQUEST, and the one property of a request that is ours
// to keep is that it stays inside the range the vendor documents for it. Its EFFECT is asserted where the
// effect now lives — TestASessionThatGoesSilentIsEndedRatherThanLeftRunning, over the agent's own release.
func TestTheIdleTimeoutStaysInsideTheRangeTheVendorDocumentsForIt(t *testing.T) {
	if browserstack.IdleTimeoutSeconds <= 0 || browserstack.IdleTimeoutSeconds > browserstack.IdleTimeoutMaxSeconds {
		t.Fatalf("idleTimeout is %d — it must be a positive number no greater than the %d seconds BrowserStack "+
			"documents as the maximum for this capability; asking for a value outside their range is asking for "+
			"something we cannot predict the handling of",
			browserstack.IdleTimeoutSeconds, browserstack.IdleTimeoutMaxSeconds)
	}
	// AND IT IS STILL SENT. A constant nothing puts on the wire would be a number with no behaviour at all,
	// which is a different defect from the one corrected above.
	set := map[string]any{}
	merged, err := browserstack.Merge([]byte(`{"capabilities":{"alwaysMatch":{}}}`),
		browserstack.Credentials{User: "acme-user", Key: "bskey-ZZTOPSECRETZZ"}, "bs://app",
		browserstack.DeviceSpec{DeviceName: "iPhone 15", PlatformVersion: "17"})
	if err != nil {
		t.Fatalf("merging a minimal body: %v", err)
	}
	if err := json.Unmarshal(merged, &set); err != nil {
		t.Fatalf("merged body was not JSON: %v", err)
	}
	body, _ := json.Marshal(set)
	if !strings.Contains(string(body), `"idleTimeout"`) {
		t.Fatalf("the merged session no longer carries idleTimeout at all: %s", body)
	}
}

// relative turns a walked path into one comparable with the exempt list — repo-root-relative, slash-separated.
func relative(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(filepath.Join("..", ".."), path)
	if err != nil {
		t.Fatalf("making %s relative: %v", path, err)
	}
	return filepath.ToSlash(rel)
}
