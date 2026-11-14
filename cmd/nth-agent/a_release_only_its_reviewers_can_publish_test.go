package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// A RELEASE ONLY ITS REVIEWERS CAN PUBLISH, BUILT FROM NOTHING A `main` RUN WROTE, ON THE GO go.mod NAMES — and a history CI
// reads as it reads the tree.
//
//   - npm: the job that runs `npm publish` runs in the `npm-publish` environment, whose required reviewers GitHub asks
//     before the job starts, and it publishes through npm's trusted publishing alone. No workflow names NPM_TOKEN, and no
//     job outside that environment can mint an OIDC token (`id-token: write`).
//   - the cache: setup-go saves the module and build cache a run on `main` writes, and restores it in any later run, a tag's
//     included. Nothing that builds what is released or published restores it (`cache: false`).
//   - Go: go.mod's `toolchain` line names one Go release; every job reads it and gives setup-go exactly that version, the
//     release refuses a binary that carries another, and CI asks govulncheck, at a pinned version, about every package.
//   - the history: a workflow on every push and pull request reads the commit messages that push adds with the tree's own
//     rules (the module root's a_history_anyone_can_read_test.go).
//
// Each step that decides something is RUN here, with bash on a scratch directory, not only read.

// workflowJobBlock is one job: its id, its own lines, and each step's lines, as the file has them.
type workflowJobBlock struct {
	file, id string
	text     string
	steps    []string
}

var stepStart = regexp.MustCompile(`^      - `)

func jobBlocks(t *testing.T, file string) []workflowJobBlock {
	t.Helper()
	lines := strings.Split(workflowText(t, file), "\n")
	var jobs []workflowJobBlock
	inJobs := false
	var job *workflowJobBlock
	for _, line := range lines {
		switch {
		case line == "jobs:":
			inJobs = true
			continue
		case !inJobs:
			continue
		case jobKey.MatchString(line):
			jobs = append(jobs, workflowJobBlock{file: file, id: jobKey.FindStringSubmatch(line)[1]})
			job = &jobs[len(jobs)-1]
			continue
		case job == nil:
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // a comment says nothing the runner reads
		}
		job.text += line + "\n"
		if stepStart.MatchString(line) {
			job.steps = append(job.steps, "")
		}
		if len(job.steps) > 0 && (strings.HasPrefix(line, "        ") || stepStart.MatchString(line) || strings.TrimSpace(line) == "") {
			job.steps[len(job.steps)-1] += line + "\n"
		}
	}
	if len(jobs) == 0 {
		t.Fatalf("instrument guard: %s has no jobs this seal could read", file)
	}
	return jobs
}

func everyJob(t *testing.T) []workflowJobBlock {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil || len(files) < 3 {
		t.Fatalf("instrument guard: %d workflows found (%v)", len(files), err)
	}
	var jobs []workflowJobBlock
	for _, f := range files {
		jobs = append(jobs, jobBlocks(t, filepath.Base(f))...)
	}
	return jobs
}

var (
	runsNpmPublish  = regexp.MustCompile(`(?m)(?:^|[\s;&|(])npm\s+publish\b`)
	inTheEnviroment = regexp.MustCompile(`(?m)^    environment:\s*npm-publish\s*$`)
	mintsAnIDToken  = regexp.MustCompile(`(?m)^      id-token:\s*write\b`)
	setupGo         = regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*actions/setup-go@`)
	setupNode       = regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*actions/setup-node@`)
	cacheOff        = regexp.MustCompile(`(?m)^          cache:\s*false\s*$`)
	goVersionFromGo = regexp.MustCompile(`(?m)^          go-version:\s*\$\{\{ steps\.go\.outputs\.version \}\}\s*$`)
	anyCacheAction  = regexp.MustCompile(`(?m)uses:\s*actions/cache(?:/[a-z]+)?@`)
)

// ── npm: only the publish's reviewers can publish ─────────────────────────────────────────────────────────────────────────

func TestOnlyTheNpmPublishEnvironmentCanPublishAndOnlyThroughTrustedPublishing(t *testing.T) {
	publishes := 0
	for _, job := range everyJob(t) {
		where := job.file + " job '" + job.id + "'"
		if runsNpmPublish.MatchString(job.text) {
			publishes++
			if !inTheEnviroment.MatchString(job.text) {
				t.Errorf("%s runs npm publish outside the npm-publish environment, so no reviewer is asked before it does", where)
			}
		}
		if mintsAnIDToken.MatchString(job.text) && !inTheEnviroment.MatchString(job.text) {
			t.Errorf("%s can mint an OIDC token (id-token: write) and does not run in the npm-publish environment", where)
		}
		if strings.Contains(job.text, "secrets.") && runsNpmPublish.MatchString(job.text) {
			t.Errorf("%s reads a secret: a trusted publish needs none, and a stored npm credential is what this replaces", where)
		}
	}
	if publishes != 1 {
		t.Errorf("instrument guard: %d jobs run npm publish, and one does", publishes)
	}
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	for _, f := range files {
		if strings.Contains(workflowText(t, filepath.Base(f)), "NPM_TOKEN") {
			t.Errorf("%s names NPM_TOKEN: npm takes this package from the npm-publish environment's trusted publisher and nothing "+
				"else, and a token stored for it would be a second way in", filepath.Base(f))
		}
	}
}

// ── the cache: nothing released is built from a cache a `main` run wrote ──────────────────────────────────────────────────

func TestNothingReleasedOrPublishedIsBuiltFromACache(t *testing.T) {
	checked := 0
	for _, file := range []string{"release.yml", "publish-npm.yml"} {
		for _, job := range jobBlocks(t, file) {
			if anyCacheAction.MatchString(job.text) {
				t.Errorf("%s job '%s' restores a cache (actions/cache)", file, job.id)
			}
			for _, step := range job.steps {
				switch {
				case setupGo.MatchString(step):
					checked++
					if !cacheOff.MatchString(step) {
						t.Errorf("%s job '%s': setup-go restores the module and build cache a run on main saved, unless it says "+
							"cache: false:\n%s", file, job.id, step)
					}
				case setupNode.MatchString(step):
					checked++
					if strings.Contains(step, "cache:") || strings.Contains(step, "package-manager-cache") {
						t.Errorf("%s job '%s': setup-node is asked to cache", file, job.id)
					}
				}
			}
		}
	}
	if checked < 2 {
		t.Fatalf("instrument guard: %d setup steps read in release.yml and publish-npm.yml", checked)
	}
}

// ── Go: the toolchain line, given to setup-go exactly, carried by every binary, and asked about by govulncheck ──────────────

// toolchainFloor is the newest Go 1.27 release when this was written (go.dev/doc/devel/release). go.mod may name a newer one,
// never an older one. AMENDED (N1b): raised to go1.27.4, Go's security release of November 2026. WAS: go1.27.3.
const toolchainFloor = "go1.27.4"

var toolchainLine = regexp.MustCompile(`(?m)^toolchain (go(\d+)\.(\d+)\.(\d+))\s*$`)
var goLine = regexp.MustCompile(`(?m)^go (\d+)\.(\d+)(?:\.(\d+))?\s*$`)

func versionParts(v string) [3]int {
	var p [3]int
	for i, s := range strings.SplitN(strings.TrimPrefix(v, "go"), ".", 3) {
		for _, r := range s {
			p[i] = p[i]*10 + int(r-'0')
		}
	}
	return p
}

func older(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func TestGoModNamesTheGoTheReleaseIsBuiltWith(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := toolchainLine.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("go.mod has no `toolchain goX.Y.Z` line, so the Go a release is built with is its `go` line's: a release " +
			"frozen at that point release, with every later standard-library fix left out")
	}
	g := goLine.FindStringSubmatch(string(data))
	if g == nil || g[1] != m[2] || g[2] != m[3] {
		t.Errorf("go.mod's toolchain line (%s) is not a release of the Go its go line names", m[1])
	}
	if older(versionParts(m[1]), versionParts(toolchainFloor)) {
		t.Errorf("go.mod's toolchain line names %s, older than %s", m[1], toolchainFloor)
	}
}

// theGoStep is the step every job runs before setup-go: it reads go.mod's toolchain line.
const theGoStep = "The Go go.mod names"

func TestEveryJobBuildsWithExactlyTheToolchainLinesGo(t *testing.T) {
	jobs := 0
	for _, job := range everyJob(t) {
		for i, step := range job.steps {
			if !setupGo.MatchString(step) {
				continue
			}
			jobs++
			where := job.file + " job '" + job.id + "'"
			if strings.Contains(step, "go-version-file:") {
				t.Errorf("%s gives setup-go go-version-file, which reads go.mod's go line, not the toolchain line", where)
			}
			if !goVersionFromGo.MatchString(step) {
				t.Errorf("%s does not give setup-go the version the step %q read from go.mod", where, theGoStep)
			}
			if i == 0 || !strings.Contains(job.steps[i-1], "- name: "+theGoStep+"\n") || !strings.Contains(job.steps[i-1], "id: go\n") {
				t.Errorf("%s: the step before setup-go is not %q (id: go)", where, theGoStep)
			}
			if !regexp.MustCompile(`(?m)^      GOTOOLCHAIN:\s*local(?:\s+#.*)?$`).MatchString(job.text) {
				t.Errorf("%s does not say GOTOOLCHAIN: local, so a go command there could fetch another Go than setup-go's", where)
			}
		}
	}
	if jobs < 5 {
		t.Fatalf("instrument guard: %d jobs with setup-go read", jobs)
	}
}

// The step itself, run on go.mod files whose answers are known.
func TestTheGoStepReadsTheToolchainLineAndRefusesAGoModWithout(t *testing.T) {
	needTools(t, "bash", "awk", "grep")
	script := workflowStep(t, "release.yml", theGoStep)
	for _, c := range []struct {
		gomod, want string
		ok          bool
	}{
		{"module x\n\ngo 1.27.0\n\ntoolchain go1.27.4\n", "version=1.27.4", true}, // AMENDED (N1b) WAS: go1.27.3, version=1.27.3
		{"module x\n\ngo 1.27.0\n", "refusing", false},
		{"module x\n\ngo 1.27.0\n\ntoolchain go1.27.4-rc\n", "refusing", false}, // AMENDED (N1b) WAS: go1.27.3-rc
		{"module x\n\ngo 1.27.0\n\ntoolchain default\n", "refusing", false},
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), []byte(c.gomod), 0o644)
		output := filepath.Join(dir, "github-output")
		out, err := runStep(t, script, dir, "GITHUB_OUTPUT="+output)
		written, _ := os.ReadFile(output)
		if c.ok && (err != nil || strings.TrimSpace(string(written)) != c.want) {
			t.Errorf("%q: the step said %v and wrote %q, want %q:\n%s", c.gomod, err, written, c.want, out)
		}
		if !c.ok && (err == nil || !strings.Contains(out, c.want) || len(written) != 0) {
			t.Errorf("%q: the step did not refuse (%v, wrote %q):\n%s", c.gomod, err, written, out)
		}
	}
	// The same step in every job: one reading of go.mod, not several that could drift.
	for _, file := range []string{"ci.yml", "publish-npm.yml", "commit-messages.yml"} {
		text := workflowText(t, file)
		if strings.Contains(text, "- name: "+theGoStep) {
			for _, job := range jobBlocks(t, file) {
				for _, step := range job.steps {
					if strings.Contains(step, "- name: "+theGoStep+"\n") && !strings.Contains(step, strings.TrimRight(indent(script), "\n")) {
						t.Errorf("%s job '%s' reads go.mod with another script than release.yml's", file, job.id)
					}
				}
			}
		}
	}
}

func indent(script string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(script, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("          " + line + "\n")
	}
	return b.String()
}

// theBinarysGoStep is the release step that reads the Go each built binary carries.
const theBinarysGoStep = "The binary carries the Go go.mod names"

func TestTheReleaseRefusesABinaryBuiltWithAnotherGo(t *testing.T) {
	needTools(t, "bash", "awk", "go")
	script := workflowStep(t, "release.yml", theBinarysGoStep)
	if !strings.Contains(script, "go version -m") {
		t.Errorf("the step does not print go version -m of the binary it judges")
	}
	// A binary of this machine's own Go, built here: the step reads it as the runner reads the release's.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "go.mod"), []byte("module hello\n\ngo 1.21\n"), 0o644)
	writeFile(t, filepath.Join(dir, "src", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "dist", "nth-agent"), ".")
	build.Dir = filepath.Join(dir, "src")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building a binary for the step to read: %v\n%s", err, out)
	}
	// The job says GOTOOLCHAIN: local (TestEveryJobBuildsWithExactlyTheToolchainLinesGo), and the step runs with it here too:
	// without it a go command reading a go.mod that names another Go would go and fetch that Go.
	ours := runtime.Version()
	expanded := func() string {
		return expand(t, script, map[string]string{"matrix.goos": "linux", "matrix.goarch": "amd64"})
	}
	writeFile(t, filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27.0\n\ntoolchain "+ours+"\n"), 0o644)
	if out, err := runStep(t, expanded(), dir, "RUNNER_TEMP="+t.TempDir(), "GOTOOLCHAIN=local"); err != nil || !strings.Contains(out, ours) {
		t.Fatalf("a binary built with %s, the toolchain line's Go, was refused (%v):\n%s", ours, err, out)
	}
	other := "go1.27.99"
	writeFile(t, filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27.0\n\ntoolchain "+other+"\n"), 0o644)
	out, err := runStep(t, expanded(), dir, "RUNNER_TEMP="+t.TempDir(), "GOTOOLCHAIN=local")
	if err == nil || !strings.Contains(out, "refusing") || !strings.Contains(out, ours) || !strings.Contains(out, other) {
		t.Fatalf("a binary built with %s, where go.mod's toolchain line names %s, was not refused by both names (%v):\n%s",
			ours, other, err, out)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27.0\n"), 0o644)
	if out, err := runStep(t, expanded(), dir, "RUNNER_TEMP="+t.TempDir(), "GOTOOLCHAIN=local"); err == nil || !strings.Contains(out, "refusing") {
		t.Fatalf("a go.mod with no toolchain line was not refused (%v):\n%s", err, out)
	}
	if !strings.Contains(workflowText(t, "release.yml"), "      - name: "+theBinarysGoStep) {
		t.Fatal("instrument guard")
	}
}

var govulncheck = regexp.MustCompile(`go run golang\.org/x/vuln/cmd/govulncheck@v\d+\.\d+\.\d+ \./\.\.\.`)

func TestCIAsksGovulncheckAtAPinnedVersion(t *testing.T) {
	for _, job := range jobBlocks(t, "ci.yml") {
		if job.id != "build-and-test" {
			continue
		}
		for _, step := range job.steps {
			if govulncheck.MatchString(step) {
				if strings.Contains(step, "@latest") {
					t.Fatalf("govulncheck is run at @latest")
				}
				return
			}
		}
	}
	t.Fatal("ci.yml's build-and-test job runs no govulncheck at a pinned version (go run golang.org/x/vuln/cmd/govulncheck@vX.Y.Z ./...)")
}

// ── the history: every push and pull request has its commit messages read ──────────────────────────────────────────────────

func TestEveryPushAndPullRequestHasItsCommitMessagesRead(t *testing.T) {
	const file = "commit-messages.yml"
	path := filepath.Join(repoRoot(t), ".github", "workflows", file)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no %s: the commit messages a push or a pull request adds are read by nothing — the tree's census skips .git", file)
	}
	text := workflowText(t, file)
	on, _, _ := strings.Cut(text, "\njobs:")
	for _, trigger := range []string{"\n  push:", "\n  pull_request:"} {
		if !strings.Contains(on, trigger) {
			t.Errorf("%s does not run on%s", file, strings.TrimPrefix(trigger, "\n "))
		}
	}
	jobs := jobBlocks(t, file)
	if len(jobs) != 1 {
		t.Fatalf("%s has %d jobs, and this seal reads one", file, len(jobs))
	}
	job := jobs[0]
	for _, want := range []string{
		"fetch-depth: 0",
		"NTH_AGENT_COMMIT_RANGE: ${{ steps.range.outputs.range }}",
		"NTH_AGENT_FORBIDDEN_VALUES: ${{ secrets.NTH_AGENT_FORBIDDEN_VALUES }}",
		"-run '^TestNoCommitMessageCarriesAPointerOrAValue$'",
		"--- PASS: TestNoCommitMessageCarriesAPointerOrAValue/no_value_of_the_forbidden_list",
	} {
		if !strings.Contains(job.text, want) {
			t.Errorf("%s's job does not hold %q", file, want)
		}
	}
	if !strings.Contains(job.text, "- name: The forbidden list is given\n") {
		t.Errorf("%s does not refuse to run without the forbidden list", file)
	}
}

// The range step, run on a scratch repository: a push to a branch reads what it adds, a new branch or a rewritten history
// reads all it reaches, and a pull request reads its own commits.
func TestTheRangeStepNamesWhatThePushOrPullRequestAdds(t *testing.T) {
	needTools(t, "bash", "git")
	script := workflowStep(t, "commit-messages.yml", "The range this push or pull request adds")
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=x", "GIT_AUTHOR_EMAIL=x@example.com", "GIT_COMMITTER_NAME=x",
			"GIT_COMMITTER_EMAIL=x@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")
	first := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "two")
	second := git("rev-parse", "HEAD")
	zeros := strings.Repeat("0", 40)
	missing := strings.Repeat("ab", 20)
	for _, c := range []struct {
		name string
		env  []string
		want string
		ok   bool
	}{
		{"a push to a branch", []string{"EVENT=push", "BEFORE=" + first, "AFTER=" + second}, first + ".." + second, true},
		{"a new branch or tag", []string{"EVENT=push", "BEFORE=" + zeros, "AFTER=" + second}, second, true},
		{"a rewritten history", []string{"EVENT=push", "BEFORE=" + missing, "AFTER=" + second}, second, true},
		{"a pull request", []string{"EVENT=pull_request", "BASE=" + first, "HEAD=" + second}, first + ".." + second, true},
		{"another event", []string{"EVENT=workflow_dispatch", "AFTER=" + second}, "refusing", false},
		{"a pull request with no base", []string{"EVENT=pull_request", "BASE=", "HEAD=" + second}, "refusing", false},
	} {
		output := filepath.Join(t.TempDir(), "out")
		out, err := runStep(t, script, dir, append(c.env, "GITHUB_OUTPUT="+output)...)
		written, _ := os.ReadFile(output)
		if c.ok && (err != nil || strings.TrimSpace(string(written)) != "range="+c.want) {
			t.Errorf("%s: wrote %q (%v), want range=%s:\n%s", c.name, written, err, c.want, out)
		}
		if !c.ok && (err == nil || !strings.Contains(out, c.want)) {
			t.Errorf("%s: not refused (%v):\n%s", c.name, err, out)
		}
	}
}

// ── CI gives the list, and refuses to run without it ──────────────────────────────────────────────────────────────────

func TestCIGivesTheForbiddenListAndFailsWithoutIt(t *testing.T) {
	var job *workflowJobBlock
	for _, j := range jobBlocks(t, "ci.yml") {
		if j.id == "build-and-test" {
			job = &j
		}
	}
	if job == nil {
		t.Fatal("instrument guard: ci.yml has no build-and-test job")
	}
	guard, read := -1, -1
	for i, step := range job.steps {
		if strings.Contains(step, "- name: The forbidden list is given\n") {
			guard = i
		}
		if strings.Contains(step, "- name: The forbidden list was read, not skipped\n") {
			read = i
		}
	}
	if guard < 0 {
		t.Fatal("ci.yml's build-and-test job does not refuse to run without the forbidden list (a step \"The forbidden list is given\")")
	}
	if read < 0 {
		t.Fatal("ci.yml's build-and-test job does not check that the seals reading the list passed rather than skipped")
	}
	for _, step := range []string{job.steps[guard], job.steps[read]} {
		if !strings.Contains(step, "NTH_AGENT_FORBIDDEN_VALUES: ${{ secrets.NTH_AGENT_FORBIDDEN_VALUES }}") {
			t.Errorf("a step of the list does not get it from the repository secret:\n%s", step)
		}
	}
	// Every seal that reads the list, by name: a SKIP of any of them fails the job.
	for _, name := range []string{"TestNoValueOfTheForbiddenListComesBack", "TestTheCustomerHalfOfTheReadmeIsTheirsAlone",
		"TestNoCapabilityTheAgentWritesCarriesAProductName", "TestThePointerCensusFindsEachKindAndNothingElse"} {
		if !strings.Contains(job.steps[read], name) {
			t.Errorf("the check that the list was read does not name %s", name)
		}
	}
	// The guard step itself, run: no list, an empty one, and a list.
	needTools(t, "bash")
	script := workflowStep(t, "ci.yml", "The forbidden list is given")
	for _, c := range []struct {
		value string
		ok    bool
	}{{"", false}, {"  \n", false}, {"whole qzvxk an invented value\n", true}} {
		out, err := runStep(t, script, t.TempDir(), "NTH_AGENT_FORBIDDEN_VALUES="+c.value)
		if (err == nil) != c.ok || (!c.ok && !strings.Contains(out, "refusing")) {
			t.Errorf("list %q: the guard said %v:\n%s", c.value, err, out)
		}
		if strings.Contains(out, "qzvxk") {
			t.Errorf("the guard printed the list:\n%s", out)
		}
	}
}
