package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A RELEASE NOBODY CAN MOVE — no command a customer reads runs a package we do not hold.
//
// The package is nthcase-agent and the command it installs is nth-agent. The npm page tells people to run it as
// `npx nthcase-agent`, which puts nothing on their PATH, and then every other line, in the README, in the agent's own
// help and in its error messages, said `nth-agent <verb>`. Typed as written, that is "command not found". Typed with npx
// in front, the way the install line ran, it is `npx nth-agent <verb>`, and npm runs whatever package is named
// nth-agent, which on 2026-10-20 nobody held. So every command now reads `npx nthcase-agent <verb>`, and the name
// nth-agent gets a placeholder of ours (publish/npm-placeholder) that runs nothing and says where to go.

// bareCommand finds `nth-agent <verb>` used as a command: the word itself, not part of a path, an archive name or
// `nthcase-agent`, followed by one of the binary's verbs or its standard questions.
func bareCommand(t *testing.T) *regexp.Regexp {
	t.Helper()
	words := verbsIn(t, filepath.Join(repoRoot(t), "cmd", "nth-agent", "cli.go"))
	if len(words) < 7 {
		t.Fatalf("instrument guard: read %d verbs from cli.go — the walk is not reading the real table", len(words))
	}
	words = append(words, "--help", "-h", "--version")
	for i := range words {
		words[i] = regexp.QuoteMeta(words[i])
	}
	return regexp.MustCompile(`(^|[^\w./-])nth-agent\s+(` + strings.Join(words, "|") + `)($|[^\w-])`)
}

var npxOf = regexp.MustCompile("npx\\s+([^\\s`'\")]+)")

// npxNames is every name text runs through npx, with any `@version` taken off.
func npxNames(text string) []string {
	var names []string
	for _, m := range npxOf.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if at := strings.LastIndex(name, "@"); at > 0 {
			name = name[:at]
		}
		names = append(names, strings.TrimRight(name, ".,;:"))
	}
	return names
}

// commandProblems is every bare command, and every npx of a name other than the ones allowed, in text.
func commandProblems(where, text string, bare *regexp.Regexp, allowed ...string) []string {
	var problems []string
	for i, line := range strings.Split(text, "\n") {
		at := where
		if strings.Count(text, "\n") > 0 {
			at = where + ":" + itoa(i+1)
		}
		if m := bare.FindStringSubmatch(line); m != nil {
			problems = append(problems, at+" says `nth-agent "+m[2]+"`, which runs only after a global install nothing here tells "+
				"the reader to do, and under npx is a different package: "+strings.TrimSpace(line))
		}
		for _, name := range npxNames(line) {
			ok := false
			for _, a := range allowed {
				ok = ok || name == a
			}
			if !ok {
				problems = append(problems, at+" runs `npx "+name+"`, a package that is not ours: "+strings.TrimSpace(line))
			}
		}
	}
	return problems
}

func packageName(t *testing.T) string {
	t.Helper()
	name, _ := npmPackageJSON(t)["name"].(string)
	if name == "" || name == "nth-agent" {
		t.Fatalf("instrument guard: publish/npm/package.json names %q — this seal is about a package and a command with different names", name)
	}
	return name
}

// THE AGENT'S OWN WORDS: every string literal in the program — usage, --version, remedies, errors, and the texts the
// farm relays to the workspace. Comments are not read; nobody running the agent sees them.
func TestNoCommandTheAgentPrintsRunsAPackageWeDoNotHold(t *testing.T) {
	root := repoRoot(t)
	name := packageName(t)
	bare := bareCommand(t)
	var problems []string
	files, literals := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if base := d.Name(); path != root && (strings.HasPrefix(base, ".") || base == "testdata" || base == "publish") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, text := range stringTexts(file) {
			literals++
			problems = append(problems, commandProblems(rel, text, bare, name)...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 40 || literals < 1000 {
		t.Fatalf("instrument guard: read %d files and %d string literals — the walk is not reading the program", files, literals)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		problems = slices.Compact(problems)
		t.Fatalf("the agent tells people to run a command that, typed with npx, runs somebody else's package. Write it "+
			"as `npx %s <verb>`:\n  %s", name, strings.Join(problems, "\n  "))
	}
}

// stringTexts is every string literal in a file, and every run of literals joined by `+`, as one text: a sentence split
// across two literals ("…any time (nth-agent " + "capture disable…") is read whole. A value that is not a literal stands
// as a NUL, so it joins nothing.
func stringTexts(file *ast.File) []string {
	var texts []string
	var operands func(e ast.Expr) []ast.Expr
	operands = func(e ast.Expr) []ast.Expr {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return append(operands(b.X), operands(b.Y)...)
		}
		return []ast.Expr{e}
	}
	unquoted := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(lit.Value)
		return text, err == nil
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if text, ok := unquoted(n); ok {
				texts = append(texts, text)
			}
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			var joined strings.Builder
			literals := 0
			for _, e := range operands(n) {
				if text, ok := unquoted(e); ok {
					joined.WriteString(text)
					literals++
				} else {
					joined.WriteString("\x00")
				}
			}
			if literals > 1 {
				texts = append(texts, joined.String())
			}
		}
		return true
	})
	return texts
}

// THE PACKAGE'S OWN FILES, AS THEY SHIP: the customer half of the README (with {{PACKAGE}} still in it, which the publish
// fills in), the CHANGELOG, the notices, and the shim and manifest.
func TestNoShippedTextRunsAPackageWeDoNotHold(t *testing.T) {
	root := repoRoot(t)
	name := packageName(t)
	bare := bareCommand(t)
	readme := readmeText(t)
	customer, _, found := strings.Cut(readme, "<!-- NOT-FOR-THE-REGISTRY -->")
	if !found {
		t.Fatal("instrument guard: the README has no NOT-FOR-THE-REGISTRY marker, so its customer half cannot be read")
	}
	problems := commandProblems("README.md", customer, bare, "{{PACKAGE}}")
	shipped := 0
	for _, rel := range []string{"CHANGELOG.md", "THIRD_PARTY_NOTICES.txt", "NOTICE", "SECURITY.md", "CONTRIBUTING.md", "publish/npm/index.js", "publish/npm/package.json"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		shipped++
		problems = append(problems, commandProblems(rel, string(data), bare, name)...)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("text the package ships tells people to run a command that, typed with npx, runs somebody else's "+
			"package:\n  %s", strings.Join(problems, "\n  "))
	}
	if len(npxNames(customer)) < 8 {
		t.Fatalf("instrument guard: the README's customer half runs npx %d times — the read is not finding its commands", len(npxNames(customer)))
	}
}

// THE GUARD: both patterns on text built here, and the walk over a planted source.
func TestTheCommandReadingFindsWhatIsPlanted(t *testing.T) {
	bare := bareCommand(t)
	planted, err := parser.ParseFile(token.NewFileSet(), "planted.go", `package p
// nth-agent doctor in a comment is read by nobody running the agent
const split = "you can remove it any time (nth-agent " +
	"capture disable, then delete it)"
func f(id string) string { return "Run 'nth-agent doctor --id " + id + "' now" }
func g(v string) string { return "nth-agent " + v + "\n" }
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, text := range stringTexts(planted) {
		found = append(found, commandProblems("planted.go", text, bare, "ours")...)
	}
	joined := strings.Join(found, "\n")
	if !strings.Contains(joined, "`nth-agent capture`") || !strings.Contains(joined, "`nth-agent doctor`") || strings.Contains(joined, "comment") {
		t.Errorf("the walk over a planted source found:\n%s", joined)
	}
	for _, text := range []string{"run nth-agent doctor", "`nth-agent capture enable`", "nth-agent --version", "(nth-agent notices)", "'nth-agent setup-ios'"} {
		if len(commandProblems("x", text, bare, "ours")) != 1 {
			t.Errorf("%q was not found", text)
		}
	}
	for _, text := range []string{"npx nth-agent connect", "npx nth-agent@0.3.0 doctor", "npx other --version"} {
		if len(commandProblems("x", text, bare, "ours")) == 0 {
			t.Errorf("%q was not found", text)
		}
	}
	for _, text := range []string{"npx ours doctor", "npx ours@1.0.0 --version", "nth-agent v0.3.0", "dist/nth-agent", "./nth-agent notices",
		"nth-agent_v0.3.0_linux_amd64.tar.gz", "another nth-agent process", "nth-agent never signs", "nthcase-agent doctor",
		`filepath.Join(dir, "nth-agent", "capture")`, "nth-agent-darwin-arm64"} {
		if p := commandProblems("x", text, bare, "ours"); len(p) != 0 {
			t.Errorf("%q was taken for a command: %v", text, p)
		}
	}
}

// THE NAME ITSELF: a placeholder of ours, so `npx nth-agent` reaches something that says where to go and does nothing
// else. It is published by hand, once; no workflow publishes it.
func TestTheBareCommandsNpmNameIsAPlaceholderOfOurs(t *testing.T) {
	needTools(t, "node")
	root := repoRoot(t)
	name := packageName(t)
	dir := filepath.Join(root, "publish", "npm-placeholder")

	var bins map[string]any
	bins, _ = npmPackageJSON(t)["bin"].(map[string]any)
	if len(bins) != 1 {
		t.Fatalf("instrument guard: the agent's package installs %d commands, and this seal is about its one", len(bins))
	}
	var command string
	for key := range bins {
		command = key
	}

	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("there is no placeholder for the npm name %s: %v", command, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("publish/npm-placeholder/package.json: %v", err)
	}
	// 0.0.2 since NTH_AGENT_0_4_4: its words say @latest after the agent's name (internal/command's placeholder_test.go).
	if manifest["name"] != command || manifest["version"] != "0.0.2" {
		t.Fatalf("the placeholder is %v@%v, and must be %s@0.0.2 — the name `npx %s` would fetch", manifest["name"], manifest["version"], command, command)
	}
	for _, key := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "bundleDependencies",
		"bundledDependencies", "scripts", "gypfile", "workspaces"} {
		if _, has := manifest[key]; has {
			t.Errorf("the placeholder declares %q — it must install nothing and run nothing on install", key)
		}
	}
	if manifest["private"] == true {
		t.Error("the placeholder is private, so it can never hold the name")
	}
	bin, _ := manifest["bin"].(map[string]any)
	if len(bin) != 1 || bin[command] != "index.js" {
		t.Fatalf("the placeholder's bin is %v, and must be {%q: \"index.js\"}", manifest["bin"], command)
	}
	files, _ := manifest["files"].([]any)
	listed := map[string]bool{"package.json": true}
	for _, f := range files {
		listed[f.(string)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !listed[e.Name()] {
			t.Errorf("publish/npm-placeholder holds %s, which its `files` does not list", e.Name())
		}
		delete(listed, e.Name())
	}
	for missing := range listed {
		t.Errorf("the placeholder's `files` lists %s, which is not there", missing)
	}

	source, err := os.ReadFile(filepath.Join(dir, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"process.env", "require(", "import ", "import(", "fs.", "child_process", "fetch(", "http"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("the placeholder's index.js contains %q — it reads nothing, touches no file and reaches nothing", forbidden)
		}
	}

	// Run it the way npx would, in an empty directory with nothing in the environment worth reading.
	work, home := t.TempDir(), t.TempDir()
	node, _ := exec.LookPath("node")
	cmd := exec.Command(node, filepath.Join(dir, "index.js"), "connect", "--farm", "https://farm.example")
	cmd.Dir = work
	cmd.Env = []string{"HOME=" + home, "NTH_AGENT_TOKEN=must-not-be-read"}
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() == 0 {
		t.Fatalf("the placeholder must end non-zero, and ended with %v", err)
	}
	said := strings.TrimRight(string(out), "\n")
	if strings.Contains(said, "\n") || !strings.HasSuffix(said, "npx "+name+"@latest") || strings.Contains(said, "must-not-be-read") {
		t.Fatalf("the placeholder must print one line pointing to `npx %s@latest`, and printed:\n%s", name, out)
	}
	for _, d := range []string{work, home} {
		if left, _ := os.ReadDir(d); len(left) != 0 {
			t.Errorf("the placeholder left %d files behind in %s", len(left), d)
		}
	}

	// Published by a person, by hand: no workflow names it.
	workflows, _ := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	for _, w := range workflows {
		if strings.Contains(workflowText(t, w.Name()), "npm-placeholder") {
			t.Errorf("%s names the placeholder — it is published once, by hand", w.Name())
		}
	}
}
