package nthagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// A TREE ANYONE CAN READ (AN_AGENT_READY_TO_BE_READ). This repository is published, so every file in it is read by people
// outside the company. Three rules hold for all of them, and the seals below keep them:
//
//  1. NO PERSONAL OR CUSTOMER VALUE. A device id, a serial, a team id, an e-mail address, a home path, a machine name, a
//     customer's name — fixtures carry invented values of the same shape. The values that were once here are refused by
//     their SHA-256 (this file must not carry them either), and the shapes that are safe to forbid are refused outright,
//     with the invented values this tree uses named on purpose.
//  2. NO POINTER A READER CANNOT FOLLOW. No internal audit id, no name of a private repository or of a server source
//     file, no sentence calling this repository private, and no sentence sending the reader to a document they cannot
//     open. A change keeps its short name in a code comment; a page a customer reads carries none.
//  3. THE LICENCE TRAVELS. LICENSE, NOTICE, SECURITY.md and CONTRIBUTING.md exist, and the npm package carries the licence.

// The invented values this tree uses where a real one has a shape worth forbidding. A new one is added here on purpose.
var (
	inventedUdids = map[string]bool{
		"00008140-0000c0ffee0000a1": true, "00008110-0000c0ffee0000b2": true, "00008130-0000c0ffee0000c3": true,
		"00008030-001a2b3c4d5e6f70": true, "00008101-000123456789abcd": true,
	}
	// Invented Android serials all start R5CT and a two-digit number, as the first ones here did.
	inventedSerial = regexp.MustCompile(`^R5CT\d{2}[0-9A-Z]{5}$`)
	inventedHomes  = map[string]bool{"ada": true, "x": true}
	inventedTemp   = "/var/folders/zz/zyxwvutsrqponmlkjihgfedcb0000gn"
	inventedPerson = "Ada"
)

var (
	emailShape     = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})`)
	homeShape      = regexp.MustCompile(`/(?:Users|home)/([A-Za-z0-9._-]+)`)
	tempShape      = regexp.MustCompile(`/var/folders/[A-Za-z0-9_]+/[A-Za-z0-9_]+`)
	udidShape      = regexp.MustCompile(`(?i)\b0000[0-9a-f]{4}-[0-9a-f]{16}\b`)
	serialShape    = regexp.MustCompile(`\bR[0-9A-Z]{10}\b`)
	macHostShape   = regexp.MustCompile(`\b([A-Za-z]+)-(?:MacBook|iMac|Mac-mini|Mac-Studio)\b`)
	ownedPhone     = regexp.MustCompile(`\b([A-Z][a-z]+)(?:'|\x{2019}|\\u2019)s (?:iPhone|iPad|MacBook|Mac|Galaxy|Pixel|phone)\b`)
	exampleDomains = regexp.MustCompile(`(?i)(?:^|\.)(?:example\.(?:com|org|net)|example|test|invalid|localhost|nthcase\.com)$`)
)

// treeFile is one file of the published tree: its path from the root, and its text.
type treeFile struct {
	path string
	text string
}

// publishedTree is every file under the repository root a fresh commit would hold: .git, the build outputs .gitignore
// names, and files holding a NUL byte (a binary, which no source file is) are left out, and the binaries are named.
func publishedTree(t *testing.T) []treeFile {
	t.Helper()
	var files []treeFile
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == ".git" || path == "dist" || path == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if path == "nth-agent" || path == "qanguru-agent" || strings.HasSuffix(path, ".exe") || strings.HasSuffix(path, ".test") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(b, 0) >= 0 {
			t.Logf("left out, holds a NUL byte: %s", path)
			return nil
		}
		files = append(files, treeFile{path, string(b)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 250 {
		t.Fatalf("instrument guard: the walk found %d files, and this tree has had over 300 — it is reading the wrong place", len(files))
	}
	return files
}

func lineOf(text string, offset int) int { return strings.Count(text[:offset], "\n") + 1 }

func sumOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestNoPersonalOrCustomerValueComesBack(t *testing.T) {
	var hits []string
	for _, f := range publishedTree(t) {
		hits = append(hits, farmtest.ForbiddenValuesIn(f.path, f.text)...)
		shape := func(rx *regexp.Regexp, allowed func(m []string) bool, what string) {
			for _, at := range rx.FindAllStringSubmatchIndex(f.text, -1) {
				var m []string
				for i := 0; i < len(at); i += 2 {
					if at[i] < 0 {
						m = append(m, "")
					} else {
						m = append(m, f.text[at[i]:at[i+1]])
					}
				}
				if !allowed(m) {
					hits = append(hits, f.path+":"+strconv.Itoa(lineOf(f.text, at[0]))+" — "+what+": "+m[0])
				}
			}
		}
		shape(emailShape, func(m []string) bool { return exampleDomains.MatchString(m[1]) },
			"an e-mail address outside the domains reserved for examples and the company's own")
		shape(homeShape, func(m []string) bool { return inventedHomes[m[1]] }, "a home directory with a real-looking account name")
		shape(tempShape, func(m []string) bool { return m[0] == inventedTemp }, "a machine's own temporary directory")
		shape(udidShape, func(m []string) bool { return inventedUdids[strings.ToLower(m[0])] },
			"an iPhone UDID that is not one of the invented ones named in this seal")
		shape(serialShape, func(m []string) bool {
			return strings.IndexFunc(m[0][strings.IndexAny(m[0], "0123456789")+1:], func(r rune) bool { return r >= '0' && r <= '9' }) < 0 ||
				inventedSerial.MatchString(m[0])
		}, "an Android serial that is not shaped like the invented ones (R5CT and two digits)")
		shape(macHostShape, func(m []string) bool { return m[1] == inventedPerson+"s" }, "a machine named after a person")
		shape(ownedPhone, func(m []string) bool { return m[1] == inventedPerson }, "a device named after a person")
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("%d personal or customer value(s) in a tree anyone can read — replace each with an invented value of the "+
			"same shape (and, for a shape, one named in this seal):\n  %s", len(hits), strings.Join(hits, "\n  "))
	}
}

// ── 2. NO POINTER A READER CANNOT FOLLOW ────────────────────────────────────────────────────────────────────────────────

// reportName is the shape of a name in capitals joined by underscores — how the company's internal documents are named.
var reportName = regexp.MustCompile(`\b[A-Z][A-Z0-9]+(?:_[A-Z0-9]+){2,}\b`)

// notAReport are names of that shape that are not documents: environment variables and build settings, which a reader
// sets or reads.
var notAReport = regexp.MustCompile(`^(?:NTH_AGENT_[A-Z_]+|THIRD_PARTY_NOTICES|ANDROID_[A-Z_]+|ADB_[A-Z_]+|APPIUM_[A-Z_]+|GITHUB_[A-Z_]+|NODE_AUTH_TOKEN|NPM_[A-Z_]+|PRODUCT_BUNDLE_IDENTIFIER|CODE_SIGN_IDENTITY|DEVELOPMENT_TEAM|ACTIONS_[A-Z_]+|RUNNER_[A-Z_]+)$`)

var pointerRules = []struct {
	rx   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`\bNV-\d+\b`), "an internal security-audit id"},
	{regexp.MustCompile(`\b[A-Za-z_][\w.]*\.cs\b`), "the name of a server source file"},
	{regexp.MustCompile(`(?i)\brepository\s+is\s+private\b|\binside\s+the\s+company\b|\binternal\s+github\s+release\b|\brelease\s+page(?:,|\s+is|,?\s+which\s+is)?\s+internal\b|\binternal\s+artifact\s+source\b|\bthis\s+page\s+is\s+internal\b`),
		"a sentence calling this repository private, or a page one for the company's own people"},
	{regexp.MustCompile(`\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\.md\b`), "the file name of a document a reader cannot open"},
	{regexp.MustCompile(`\b[A-Z0-9_]+_(?:REPORT|PROMPT|FINDINGS|SPIKE|DESIGN)\b`), "the name of an internal report"},
	{regexp.MustCompile(`\b(?i:see|read|reading)\s+(?:the\s+)?` + "`?" + `[A-Z][A-Z0-9]+(?:_[A-Z0-9]+){2,}`), "a sentence sending the reader to an internal report"},
	{regexp.MustCompile(`[A-Z][A-Z0-9]+(?:_[A-Z0-9]+){2,}` + "`?" + `\s+(?:report|§)`), "a sentence sending the reader to an internal report"},
	{regexp.MustCompile(`claude\.ai/` + `code`), "a link to a private session"},
}

// documentsAReaderOpens are the .md files this tree holds, which a reader can open.
// RELEASE_NOTES.md is a name a seal refuses to find in the tree: release notes are the changelog's own section.
var documentsAReaderOpens = map[string]bool{"README.md": true, "CHANGELOG.md": true, "SECURITY.md": true, "CONTRIBUTING.md": true,
	"RELEASE_NOTES.md": true}

func findPointers(path, text string) []string {
	var hits []string
	for _, rule := range pointerRules {
		for _, at := range rule.rx.FindAllStringIndex(text, -1) {
			found := text[at[0]:at[1]]
			if strings.HasSuffix(found, ".md") && documentsAReaderOpens[found] {
				continue
			}
			if m := reportName.FindString(found); m != "" && notAReport.MatchString(m) && !strings.Contains(rule.what, "audit") {
				continue
			}
			hits = append(hits, path+":"+strconv.Itoa(lineOf(text, at[0]))+" — "+rule.what+": "+found)
		}
	}
	return hits
}

// customerText is what a customer reads: the pages the npm package ships, the new public files, and every string literal
// a program in this tree prints (help text, messages, refusals) — read with Go's own scanner.
func customerText(t *testing.T, files []treeFile) []treeFile {
	t.Helper()
	var out []treeFile
	for _, f := range files {
		switch {
		case f.path == "README.md" || f.path == "CHANGELOG.md" || f.path == "SECURITY.md" || f.path == "CONTRIBUTING.md" || f.path == "NOTICE":
			out = append(out, f)
		case strings.HasPrefix(f.path, "publish/npm/"):
			out = append(out, f)
		case strings.HasSuffix(f.path, ".go") && !strings.HasSuffix(f.path, "_test.go") && !strings.Contains(f.path, "/farmtest/") && !strings.Contains(f.path, "testdata/"):
			var s scanner.Scanner
			fset := token.NewFileSet()
			file := fset.AddFile(f.path, fset.Base(), len(f.text))
			s.Init(file, []byte(f.text), nil, 0)
			var literals strings.Builder
			for {
				pos, tok, lit := s.Scan()
				if tok == token.EOF {
					break
				}
				if tok == token.STRING {
					if v, err := strconv.Unquote(lit); err == nil {
						if n := fset.Position(pos).Line - 1 - strings.Count(literals.String(), "\n"); n > 0 {
							literals.WriteString(strings.Repeat("\n", n))
						}
						literals.WriteString(strings.ReplaceAll(v, "\n", " ") + " ")
					}
				}
			}
			out = append(out, treeFile{f.path + " (printed text)", literals.String()})
		}
	}
	return out
}

func TestNoPrivateWordingAuditIdOrPointerALaterReaderCannotFollow(t *testing.T) {
	files := publishedTree(t)
	var hits []string
	for _, f := range files {
		hits = append(hits, findPointers(f.path, f.text)...)
	}
	// What a customer reads carries no internal report's name at all, not even as the short name of a change.
	for _, f := range customerText(t, files) {
		for _, at := range reportName.FindAllStringIndex(f.text, -1) {
			if name := f.text[at[0]:at[1]]; !notAReport.MatchString(name) {
				hits = append(hits, f.path+":"+strconv.Itoa(lineOf(f.text, at[0]))+" — the name of an internal report, in text a customer reads: "+name)
			}
		}
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("%d pointer(s) a reader of a public repository cannot follow — keep the reason, say it in plain words, "+
			"and drop the pointer:\n  %s", len(hits), strings.Join(hits, "\n  "))
	}
}

// The census reads what it is given: a planted line of each kind is found, and a line that only looks like one is not.
func TestThePointerCensusFindsEachKindAndNothingElse(t *testing.T) {
	for _, line := range []string{
		// Each is put together here, so this file does not hold the thing it refuses.
		"// (N" + "V-12) the audit said so", "// as Server.Api/Nodes" + "Controller.c" + "s does", "// this repository is " + "private, so",
		"for people inside the " + "company", "// see SOME_PRIVATE_" + "NOTES for why", "// (THE_PORT_BELONGS_TO_" + "THE_MACHINE report)",
		"// A_DESIGN_OF_" + "OURS.md §3", "// NETWORK_CAPTURE_LIVE_" + "FINDINGS", "Claude-Session: https://claude.ai/" + "code/session_x",
	} {
		if len(findPointers("planted", line)) == 0 {
			t.Errorf("the census did not find the pointer in %q", line)
		}
	}
	for _, line := range []string{
		"// see README.md and CHANGELOG.md", "set NTH_AGENT_IOS_TEAM_ID", "// the A_MACHINE_THAT_STAYS_THE_CUSTOMERS change",
		"THIRD_PARTY_NOTICES.txt", "PRODUCT_BUNDLE_IDENTIFIER=x",
	} {
		if hits := findPointers("planted", line); len(hits) != 0 {
			t.Errorf("the census found a pointer in %q, which is none: %v", line, hits)
		}
	}
	if len(farmtest.ForbiddenValuesIn("planted", "com.example.ada.WebDriverAgentRunner /gurufarm-node/android/capture-handback")) != 0 {
		t.Errorf("an invented name, or the farm's own route, was refused")
	}
}

// ── 3. THE LICENCE TRAVELS ──────────────────────────────────────────────────────────────────────────────────────────────

// apacheLicenseSum is the SHA-256 of the Apache License 2.0 text exactly as the Apache Software Foundation publishes it
// (LICENSE-2.0.txt: 202 lines, all nine sections, the end-of-terms line and the appendix, its fields left in brackets).
const apacheLicenseSum = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

const legalEntity = "Unidots Software Solutions LLC"

func TestTheLicenceFilesAreThereAndTheNpmPackageCarriesThem(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v — a public repository needs it", name, err)
		}
		return string(b)
	}
	license := read("LICENSE")
	if got := sumOf(license); got != apacheLicenseSum {
		t.Errorf("LICENSE is not the unmodified Apache License 2.0 text (sha256 %s)", got)
	}
	for _, part := range []string{"Apache License", "Version 2.0, January 2004", "1. Definitions.", "2. Grant of Copyright License.",
		"3. Grant of Patent License.", "4. Redistribution.", "5. Submission of Contributions.", "6. Trademarks.",
		"7. Disclaimer of Warranty.", "8. Limitation of Liability.", "9. Accepting Warranty or Additional Liability.",
		"END OF TERMS AND CONDITIONS", "APPENDIX: How to apply the Apache License to your work."} {
		if !strings.Contains(license, part) {
			t.Errorf("LICENSE is missing %q", part)
		}
	}
	notice := read("NOTICE")
	for _, part := range []string{"Copyright 2026 " + legalEntity, "Apache License, Version 2.0", "THIRD_PARTY_NOTICES.txt",
		"`notices` command", "trademarks of " + legalEntity} {
		if !strings.Contains(notice, part) {
			t.Errorf("NOTICE does not say %q", part)
		}
	}
	security := read("SECURITY.md")
	for _, part := range []string{"security@nthcase.com", "Report a vulnerability", "private vulnerability reporting"} {
		if !strings.Contains(security, part) {
			t.Errorf("SECURITY.md does not say %q", part)
		}
	}
	if !strings.Contains(read("CONTRIBUTING.md"), "not accept pull requests") {
		t.Errorf("CONTRIBUTING.md does not say that contributions are not accepted")
	}

	// The README a customer reads on npmjs.com is the part above the boundary, so the licence is said there.
	readme := read("README.md")
	customer, _, found := strings.Cut(readme, "<!-- NOT-FOR-THE-REGISTRY -->")
	if !found {
		t.Fatal("README.md has no boundary")
	}
	section := regexp.MustCompile(`(?s)\n## Licence\n(.*?)(?:\n## |\z)`).FindStringSubmatch(customer)
	if section == nil {
		t.Fatal("README.md has no `## Licence` section above the boundary, so it would not reach npm")
	}
	for _, part := range []string{"Apache License 2.0", "`LICENSE`", "trademarks of " + legalEntity, "not covered by that licence"} {
		if !strings.Contains(section[1], part) {
			t.Errorf("the README's licence section does not say %q", part)
		}
	}
	if strings.Contains(customer, "own code is not under those licences") && !strings.Contains(customer, "own code is under the Apache License 2.0") {
		t.Errorf("the README says what nth-agent's own code is not under, and not what it is under")
	}
	if !strings.Contains(customer, "own code is under the Apache License 2.0") {
		t.Errorf("the README's third-party notices do not say what nth-agent's own code is under")
	}

	var manifest struct {
		License string   `json:"license"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(read("publish/npm/package.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.License != "Apache-2.0" {
		t.Errorf(`publish/npm/package.json says "license": %q, want "Apache-2.0"`, manifest.License)
	}
	for _, want := range []string{"LICENSE", "NOTICE"} {
		if !contains(manifest.Files, want) {
			t.Errorf("publish/npm/package.json does not ship %s", want)
		}
	}
	// …and the workflow puts them there: the package is assembled from the tag's tree, and a file named in `files` that
	// nobody copies in is simply absent from what npm publishes.
	if !regexp.MustCompile(`(?m)^\s*cp THIRD_PARTY_NOTICES\.txt CHANGELOG\.md LICENSE NOTICE publish/npm/\s*$`).MatchString(read(".github/workflows/publish-npm.yml")) {
		t.Errorf("publish-npm.yml does not copy LICENSE and NOTICE into the package beside the notices and the changelog")
	}
}
