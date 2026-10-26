package nthagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ── THE THIRD-PARTY NOTICES, MADE FROM WHAT THE RELEASE LINKS (AN_AGENT_THAT_SHIPS_ITS_NOTICES) ──────────────────────
//
// Neither the GitHub release nor the npm package carried a single notice, and every program they hold links Go's
// standard library and fifteen modules whose licences — MIT, BSD, ISC — ask for exactly that: their notice, with every
// copy. A list kept by hand is the shape this repository keeps deleting, so the file is BUILT here, from three things
// nobody types:
//
//   - which modules are linked: `go list -deps ./cmd/nth-agent` for every platform release.yml's matrix builds, with cgo
//     off and on (the darwin legs cross-compile with it off; the linux leg runs where a C compiler may turn it on), and
//     their union — an entry for a module only one of those links is the cheap mistake, a missing one the costly one;
//   - each module's licence, read out of the module cache (whose contents go.sum vouches for): every licence-shaped file
//     on the way up from each linked package's directory to its module's root;
//   - the code that is not a module: Go itself, with internal/zstd beside it, and the one function ported from mitmproxy.
//
// The test builds the file and compares it with the one in the tree, byte for byte. A linked module with no entry, an
// entry for a module no longer linked, a version moved, a licence text changed: each fails here by name. And release.yml
// asks the same question of every binary it actually builds (`go version -m`), so a release cannot ship a module this
// file does not name even if this test was never run.
//
// To rewrite the file:  go test . -run TestTheNoticesAreWhatTheReleaseLinks -update   — then read the diff.

var update = flag.Bool("update", false, "rewrite THIRD_PARTY_NOTICES.txt from what the release's programs link")

const noticesFile = "THIRD_PARTY_NOTICES.txt"

const rewrite = "go test . -run TestTheNoticesAreWhatTheReleaseLinks -update"

// licenceTexts names every licence text the file has been made from, BY ITS SHA-256, because a licence is its text: the
// same module can change it in a new version, and two modules can share one. A text that is not here stops the build of
// the file — a person reads it and adds its name. Nothing here guesses a licence from a module's path or a file's name.
var licenceTexts = map[string]string{
	// Go's LICENSE and PATENTS: beside internal/zstd, and in golang.org/x/crypto, net, sys and text.
	"911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad": "BSD-3-Clause",
	"96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc": "Go's patent grant",
	// mitmproxy 12.2.3's LICENSE, copied from its own installed package into internal/netcap/LICENSE.mitmproxy.
	"cc4ab04ed7180af6c0ede7147f9ec2aa05212c088d7b3c8e38bbffe91c9c8f82": "MIT",
	"2db9097f7b3750c1c9426996d21fe443d339131fb758c2167d2d5fabe8e6cbf9": "MIT",                                 // github.com/Masterminds/semver
	"3d180008e36922a4e8daec11c34c7af264fed5962d07924aea928c38e8663c94": "MIT",                                 // github.com/andybalholm/brotli
	"5c0476add4c38b55d0ed5ac11b85e00c38f26e1caee20dfe3ab58190103d1fbc": "MIT",                                 // github.com/cenkalti/backoff
	"cc0975a5f6305145bdd7b41ce9479632fdac3870e6ac4281f28017f18c767c4e": "ISC",                                 // github.com/coder/websocket
	"d368be6f632f8f928369c6f1923cf2f884a1ae42a5f0dd218acd96f9bcfa75b4": "MIT",                                 // github.com/danielpaulus/go-ios
	"0a8d61ed3cbfd5312326e8126c31ce9c627a283adc99131b56896d29ada04b2d": "BSD-3-Clause",                        // github.com/google/uuid
	"200e14ba92ed9bee1273b522c0931351bfb89c41177056e5d0cafc7e91e45c0a": "MIT",                                 // github.com/grandcat/zeroconf
	"f200ff9bad73385db2ed81e23ee2c1fe069eb5776e17fae4ee85ad0a9e1361a5": "BSD-3-Clause",                        // github.com/miekg/dns, LICENSE
	"66550c0ad5ca7ec1e08683e5f872cc45c741f311eee3b8ee484206ecbf9c740d": "copyright notices",                   // github.com/miekg/dns, COPYRIGHT
	"d01c6d371866b3c7a1a7e20994d88d2ce83f22974ec6d3596a6125b44495813d": "MIT",                                 // go.mozilla.org/pkcs7
	"20d9b7d9372369484ecadae229d3e3efee8029b259e11c9a1b07d2e0a3c82e39": "BSD-2-Clause-Views and BSD-3-Clause", // howett.net/plist
	"05c9cf1167d0584c9192f73a550a2089547e8366114da257f51279ec6a3fc568": "BSD-3-Clause",                        // software.sslmate.com/src/go-pkcs12
}

// isLicenceFile: a file that carries a licence or a notice — LICENSE, LICENCE, COPYING, COPYRIGHT, NOTICE, PATENTS, with
// any suffix after a dot, a dash or an underscore (LICENSE.txt, LICENSE-MIT, COPYING.LIB) — and never a source file:
// `license.go` is code. Too wide costs an extra page in the file; too narrow leaves a licence out, so the rule is wide.
var (
	licenceFileName = regexp.MustCompile(`(?i)^(licen[cs]es?|copying|copyright|notice|patents|unlicense)([-._][a-z0-9._-]*)?$`)
	sourceFileName  = regexp.MustCompile(`(?i)\.(go|s|c|h|cc|cpp|py|js|ts|sh|mod|sum)$`)
)

func isLicenceFile(name string) bool {
	return licenceFileName.MatchString(name) && !sourceFileName.MatchString(name)
}

type target struct{ goos, goarch string }

func (t target) String() string { return t.goos + "/" + t.goarch }

// releaseTargets reads release.yml's build matrix — the only statement of what a release compiles.
func releaseTargets(t *testing.T) []target {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("reading release.yml: %v — the notices describe what it builds", err)
	}
	var targets []target
	goos := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if after, ok := strings.CutPrefix(line, "goos:"); ok {
			goos = strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(line, "goarch:"); ok && goos != "" {
			targets = append(targets, target{goos, strings.TrimSpace(after)})
		}
	}
	if len(targets) < 3 {
		t.Fatalf("instrument guard: %d targets parsed from release.yml's matrix — the parse is wrong, not the matrix", len(targets))
	}
	return targets
}

// goVersion is the Go the release builds with: setup-go reads go.mod (`toolchain` when it is there, else `go`).
func goVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	version := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "toolchain" {
			return fields[1]
		}
		if len(fields) == 2 && fields[0] == "go" {
			version = "go" + fields[1]
		}
	}
	if version == "" {
		t.Fatal("go.mod names no Go version — the notices cannot say which Go the release carries")
	}
	return version
}

type module struct {
	Path, Version, Dir string
	packageDirs        map[string]bool
}

func (m *module) key() string { return m.Path + " " + m.Version }

// linked is every module, and every package of this module, that some program of the release links.
type linked struct {
	modules map[string]*module // by "path version"
	own     map[string]bool    // this module's own packages, by import path
}

func linkedByTheRelease(t *testing.T, targets []target) linked {
	t.Helper()
	set := linked{modules: map[string]*module{}, own: map[string]bool{}}
	for _, tg := range targets {
		for _, cgo := range []string{"0", "1"} {
			cmd := exec.Command("go", "list", "-deps", "-json=ImportPath,Dir,Module", "./cmd/nth-agent")
			cmd.Env = append(os.Environ(), "GOOS="+tg.goos, "GOARCH="+tg.goarch, "CGO_ENABLED="+cgo,
				"GOFLAGS=-mod=readonly", "GOWORK=off")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list -deps ./cmd/nth-agent for %s with CGO_ENABLED=%s: %v\n%s", tg, cgo, err, stderr.String())
			}
			seen := map[string]bool{}
			decoder := json.NewDecoder(bytes.NewReader(out))
			for decoder.More() {
				var p struct {
					ImportPath, Dir string
					Module          *struct {
						Path, Version, Dir string
						Main               bool
						Replace            *struct{ Path, Version string }
					}
				}
				if err := decoder.Decode(&p); err != nil {
					t.Fatalf("go list's answer for %s does not parse: %v", tg, err)
				}
				switch {
				case p.Module == nil: // the standard library: Go's own entry
					continue
				case p.Module.Main:
					set.own[p.ImportPath] = true
					continue
				case p.Module.Replace != nil:
					t.Fatalf("%s is replaced by %s %s — the notices have never had to say whose code a replacement is, and the "+
						"release check reads `dep` lines only. Decide it before linking one.", p.Module.Path, p.Module.Replace.Path,
						p.Module.Replace.Version)
				}
				m := &module{Path: p.Module.Path, Version: p.Module.Version, Dir: p.Module.Dir}
				if known, ok := set.modules[m.key()]; ok {
					m = known
				} else {
					m.packageDirs = map[string]bool{}
					set.modules[m.key()] = m
				}
				seen[m.key()] = true
				if p.Module.Dir == "" || p.Dir == "" {
					t.Fatalf("go list gave no directory for %s (%s) — the module is not in the module cache, and a licence that "+
						"cannot be read there is a blocker, never a guess", p.ImportPath, m.key())
				}
				m.packageDirs[p.Dir] = true
			}
			if len(seen) < 10 {
				t.Fatalf("instrument guard: %s (CGO_ENABLED=%s) links %d modules — go list answered with something else", tg, cgo, len(seen))
			}
		}
	}
	return set
}

// licenceFilesOf is every licence-shaped file on the way up from each linked package's directory to the module's root,
// root first — so a package that carries a licence of its own brings it, and the module's own is never left out.
func licenceFilesOf(t *testing.T, m *module) []string {
	t.Helper()
	found := map[string]bool{}
	for dir := range m.packageDirs {
		rel, err := filepath.Rel(m.Dir, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("%s is outside its module %s (%s)", dir, m.key(), m.Dir)
		}
		for {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("reading %s: %v", dir, err)
			}
			for _, e := range entries {
				if !e.IsDir() && isLicenceFile(e.Name()) {
					path, _ := filepath.Rel(m.Dir, filepath.Join(dir, e.Name()))
					found[filepath.ToSlash(path)] = true
				}
			}
			if filepath.Clean(dir) == filepath.Clean(m.Dir) {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	files := make([]string, 0, len(found))
	for f := range found {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		di, dj := strings.Count(files[i], "/"), strings.Count(files[j], "/")
		if di != dj {
			return di < dj
		}
		if ri, rj := noticeRank(files[i]), noticeRank(files[j]); ri != rj {
			return ri < rj
		}
		return files[i] < files[j]
	})
	return files
}

// noticeRank puts the licence itself before the notices beside it (COPYRIGHT, NOTICE, PATENTS) at one level.
func noticeRank(path string) int {
	name := strings.ToLower(path[strings.LastIndex(path, "/")+1:])
	if strings.HasPrefix(name, "licen") || strings.HasPrefix(name, "copying") || strings.HasPrefix(name, "unlicense") {
		return 0
	}
	return 1
}

type licenceFile struct {
	label, text, licence string
}

type entry struct {
	heading string // "Module: <path> <version>" for a module — the exact line release.yml looks for
	short   string // the contents table's left column
	about   string // a paragraph, for the code that is not a module
	files   []licenceFile
}

func (e entry) licences(withFiles bool) string {
	var parts []string
	for _, f := range e.files {
		if withFiles {
			parts = append(parts, f.licence+" ("+f.label+")")
		} else if !contains(parts, f.licence) {
			parts = append(parts, f.licence)
		}
	}
	return strings.Join(parts, ", ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// readLicence reads one licence file and names it from licenceTexts; an unnamed text is collected, never guessed.
func readLicence(t *testing.T, path, label string, unnamed *[]string) licenceFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	name, ok := licenceTexts[hex.EncodeToString(sum[:])]
	if !ok {
		*unnamed = append(*unnamed, fmt.Sprintf("%s (sha256 %s)", path, hex.EncodeToString(sum[:])))
	}
	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return licenceFile{label: label, text: text, licence: name}
}

// theCodeThatIsNotAModule is Go, with internal/zstd beside it, and mitmproxy's one function — each read from this tree, and
// each held to still being here: an entry for code that is no longer linked is a stale entry like any other.
func theCodeThatIsNotAModule(t *testing.T, set linked, version string, unnamed *[]string) []entry {
	t.Helper()
	const self = "github.com/unidotssoftwaresolutions/nth-agent/"
	for _, pkg := range []string{"internal/zstd", "internal/netcap"} {
		if !set.own[self+pkg] {
			t.Fatalf("%s is no longer linked into the release's programs, and the notices carry an entry for code in it", pkg)
		}
	}
	decode, err := os.ReadFile(filepath.Join("internal", "netcap", "decode.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decode, []byte("func isMostlyBinary(")) || !bytes.Contains(decode, []byte("mitmproxy's strutils.is_mostly_bin")) {
		t.Fatal("internal/netcap/decode.go no longer carries isMostlyBinary as mitmproxy's is_mostly_bin — the mitmproxy entry " +
			"describes code that is not there; remove it, and internal/netcap/LICENSE.mitmproxy with it")
	}
	return []entry{
		{
			heading: "Go: the standard library and runtime (" + version + "), and internal/zstd",
			short:   "Go: the standard library and runtime (" + version + "), and internal/zstd",
			about: "Every Go program carries Go's standard library and runtime. internal/zstd is Go's own zstd decoder, copied\n" +
				"byte for byte from the same toolchain's src/internal/zstd, with Go's LICENSE and PATENTS beside it; they are these.\n",
			files: []licenceFile{
				readLicence(t, filepath.Join("internal", "zstd", "LICENSE"), "LICENSE", unnamed),
				readLicence(t, filepath.Join("internal", "zstd", "PATENTS"), "PATENTS", unnamed),
			},
		},
		{
			heading: "mitmproxy 12.2.3: is_mostly_bin, ported into internal/netcap",
			short:   "mitmproxy 12.2.3: is_mostly_bin, ported into internal/netcap",
			about: "How a recorded body is judged text or binary (isMostlyBinary, in internal/netcap/decode.go) is a port of\n" +
				"mitmproxy's strutils.is_mostly_bin, and mitmproxy's licence travels with it.\n",
			files: []licenceFile{
				readLicence(t, filepath.Join("internal", "netcap", "LICENSE.mitmproxy"), "LICENSE", unnamed),
			},
		},
	}
}

const rule = "================================================================================\n"

// buildNotices is the whole file. It is the only writer of THIRD_PARTY_NOTICES.txt.
func buildNotices(t *testing.T) (string, linked) {
	t.Helper()
	targets := releaseTargets(t)
	set := linkedByTheRelease(t, targets)

	var unnamed, unlicensed []string
	entries := theCodeThatIsNotAModule(t, set, goVersion(t), &unnamed)

	keys := make([]string, 0, len(set.modules))
	for k := range set.modules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := set.modules[k]
		files := licenceFilesOf(t, m)
		if len(files) == 0 {
			unlicensed = append(unlicensed, k+" ("+m.Dir+")")
			continue
		}
		e := entry{heading: "Module: " + k, short: k}
		for _, f := range files {
			e.files = append(e.files, readLicence(t, filepath.Join(m.Dir, filepath.FromSlash(f)), f, &unnamed))
		}
		entries = append(entries, e)
	}
	if len(unlicensed) > 0 {
		t.Fatalf("no licence file in the module cache for:\n  %s\nThat is a blocker to report, not something to guess: the "+
			"notices cannot say what these modules may be used under.", strings.Join(unlicensed, "\n  "))
	}
	if len(unnamed) > 0 {
		t.Fatalf("a licence text nobody here has read:\n  %s\nRead each one, and add its sha256 and its name to licenceTexts "+
			"in notices_test.go — a name is never guessed from a file's or a module's.", strings.Join(unnamed, "\n  "))
	}

	platforms := make([]string, len(targets))
	for i, tg := range targets {
		platforms[i] = tg.String()
	}
	var b strings.Builder
	b.WriteString("Third-party notices for nth-agent\n")
	b.WriteString("=================================\n\n")
	b.WriteString("nth-agent contains code written by others. The licences that code comes under ask that their notices travel\n")
	b.WriteString("with every copy of it, so they are here: for each piece of it, where it comes from, its version, and its\n")
	b.WriteString("licence in full. Everything else in nth-agent is its own code, which these licences do not cover.\n\n")
	b.WriteString("This list is made from what is linked into each program a release builds\n")
	b.WriteString("(" + joinAnd(platforms) + "), not kept by hand. The agent's `notices` command prints it.\n\n")
	b.WriteString("Contents\n\n")
	width := 0
	for _, e := range entries {
		width = max(width, len(e.short))
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "  %-*s   %s\n", width, e.short, e.licences(false))
	}
	for _, e := range entries {
		b.WriteString("\n" + rule)
		b.WriteString(e.heading + "\n")
		b.WriteString("Licence: " + e.licences(true) + "\n")
		if e.about != "" {
			b.WriteString("\n" + e.about)
		}
		for _, f := range e.files {
			b.WriteString("\n----- " + f.label + " -----\n")
			b.WriteString(f.text)
		}
	}
	return b.String(), set
}

func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// moduleLines is every "Module: <path> <version>" line of a notices file, as a set.
func moduleLines(text string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if after, ok := strings.CutPrefix(line, "Module: "); ok {
			set[after] = true
		}
	}
	return set
}

func TestTheNoticesAreWhatTheReleaseLinks(t *testing.T) {
	want, set := buildNotices(t)
	if *update {
		if err := os.WriteFile(noticesFile, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s: %d modules, %d bytes", noticesFile, len(set.modules), len(want))
	}
	data, err := os.ReadFile(noticesFile)
	if err != nil {
		t.Fatalf("reading %s: %v — the release and the npm package carry it, and nothing else carries the notices. Run: %s",
			noticesFile, err, rewrite)
	}
	got := string(data)
	if got == want {
		return
	}

	var problems []string
	have, need := moduleLines(got), moduleLines(want)
	for _, k := range sortedKeys(need) {
		if !have[k] {
			problems = append(problems, k+" is linked into the release's programs and has no entry")
		}
	}
	for _, k := range sortedKeys(have) {
		if !need[k] {
			problems = append(problems, k+" has an entry and is no longer linked")
		}
	}
	if len(problems) == 0 {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := 0; i < max(len(gotLines), len(wantLines)); i++ {
			g, w := lineAt(gotLines, i), lineAt(wantLines, i)
			if g != w {
				problems = append(problems, fmt.Sprintf("line %d is %q and would be %q — a licence text, the Go version, the "+
					"platforms or the wording changed", i+1, g, w))
				break
			}
		}
	}
	t.Fatalf("%s is not what the release's programs link:\n  %s\nRewrite it with `%s`, then read the diff before committing it.",
		noticesFile, strings.Join(problems, "\n  "), rewrite)
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "(the end of the file)"
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// THE INSTRUMENT: the comparison names a missing module and a stale one, not only "the bytes differ" — a seal whose only
// sentence is "something changed" teaches a person to run -update without reading.
func TestTheNoticeComparisonNamesWhatIsMissingAndWhatIsStale(t *testing.T) {
	have := moduleLines("Module: example.com/kept v1.0.0\nModule: example.com/gone v0.1.0\n")
	need := moduleLines("Module: example.com/kept v1.0.0\nModule: example.com/new v2.0.0\n")
	if !have["example.com/gone v0.1.0"] || !need["example.com/new v2.0.0"] || len(have) != 2 || len(need) != 2 {
		t.Fatalf("moduleLines read %v and %v", have, need)
	}
	for _, name := range []string{"LICENSE", "LICENSE.txt", "LICENCE", "license.md", "COPYING", "COPYING.LIB", "PATENTS",
		"LICENSE-MIT", "LICENSE.BSD", "NOTICE", "NOTICE.md", "COPYRIGHT", "LICENSES.txt"} {
		if !isLicenceFile(name) {
			t.Errorf("%s carries a licence or a notice and the walk would not take it", name)
		}
	}
	for _, name := range []string{"license.go", "notice.go", "licensed.txt", "patents_test.go", "copyright.c", "README.md"} {
		if isLicenceFile(name) {
			t.Errorf("%s was taken for a licence", name)
		}
	}
}
