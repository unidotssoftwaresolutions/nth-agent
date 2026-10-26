package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A_VERSION_NOBODY_PUBLISHED_ON_EVERY_RELEASE_PAGE — what a release page, and the Actions list, tell a person.
//
// Every release this repository cut told its reader to pin `npx nthcase-agent@1.4.0` — a version nobody ever
// published — from an inline body that typed the package name by hand. And the job that wrote that page was called
// `publish`, beside publish-npm.yml's `publish`, the only job that reaches npm: reading a green `publish` in the
// Actions list, the operator had to ask whether the agent had just been published. It had not.
//
// The body now carries {{PACKAGE}} and {{VERSION}}, and the step that writes it fills them from package.json and the
// tag and REFUSES if any {{…}} token survives — the README's treatment. The job is `github-release`.

// releaseNotesStep returns the script of release.yml's "Write the release notes" step, dedented exactly as the runner
// receives it — parsed by indentation, as the other workflow seals are, because there is no YAML library here and a
// parse that guessed would be a seal reading the wrong thing.
func releaseNotesStep(t *testing.T) string {
	t.Helper()
	lines := strings.Split(releaseWorkflow(t), "\n")
	start := -1
	for i, line := range lines {
		if line == "      - name: Write the release notes" {
			start = i
		}
	}
	if start < 0 || start+1 >= len(lines) || lines[start+1] != "        run: |" {
		t.Fatal("release.yml has no `Write the release notes` step with a `run: |` block — the release body is not " +
			"derived where this seal can see it, and a body that is not derived is the invented version again")
	}
	var script []string
	for _, line := range lines[start+2:] {
		if strings.TrimSpace(line) == "" {
			script = append(script, "")
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			break
		}
		script = append(script, strings.TrimPrefix(line, "          "))
	}
	return strings.TrimRight(strings.Join(script, "\n"), "\n") + "\n"
}

// releaseBodyTemplate is the heredoc inside that step: what every release page says, before substitution.
func releaseBodyTemplate(t *testing.T) string {
	t.Helper()
	step := releaseNotesStep(t)
	_, afterOpen, found := strings.Cut(step, "<<'BODY'\n")
	if !found {
		t.Fatal("instrument guard: the release-notes step has no quoted `<<'BODY'` heredoc — the template this seal " +
			"reads is not where it was written")
	}
	template, _, found := strings.Cut(afterOpen, "\nBODY\n")
	if !found || strings.TrimSpace(template) == "" {
		t.Fatal("instrument guard: the release-notes heredoc is empty or unterminated — the parse is reading the " +
			"wrong thing, not a release with nothing to say")
	}
	return template
}

// SEAL 1 — NO INVENTED VERSION IN release.yml: not in the body, not in a comment. A comment is still read by a person.
func TestTheReleaseWorkflowPinsNoVersionNobodyPublished(t *testing.T) {
	workflow := releaseWorkflow(t)
	literal := regexp.MustCompile(`@\d+\.\d+(?:\.\d+)?`)
	for i, line := range strings.Split(workflow, "\n") {
		if m := literal.FindString(line); m != "" {
			t.Fatalf("release.yml:%d pins %q. Every release page this repository cuts would tell its reader to run "+
				"a version nobody has published — the defect this file already shipped twice. Write `@{{VERSION}}`, "+
				"which the release-notes step fills from the tag being released:\n  %s", i+1, m, strings.TrimSpace(line))
		}
	}
	if !strings.Contains(releaseBodyTemplate(t), "@{{VERSION}}") {
		t.Fatal("the release body no longer shows the pinned form `@{{VERSION}}` — a reader told there is no " +
			"self-update, beside an npx command that fetches the latest version, needs to see how to pin one")
	}
}

// SEAL 2 — NO HAND-TYPED PACKAGE NAME IN THE RELEASE BODY: {{PACKAGE}}, derived from package.json.
func TestTheReleaseBodyTypesNoPackageName(t *testing.T) {
	name, _ := npmPackageJSON(t)["name"].(string)
	if name == "" {
		t.Fatal("instrument guard: package.json declares no name — there is nothing for the release body to derive")
	}
	template := releaseBodyTemplate(t)
	token := regexp.MustCompile(`(^|[^-\w])` + regexp.QuoteMeta(name) + `($|[^-\w.])`)
	if token.MatchString(template) {
		t.Fatalf("the release body types the package name %q by hand. It is declared once, in package.json, and "+
			"renaming it must stay that one edit — a second spelling on the release page is where the old name "+
			"survived the last rename. Write {{PACKAGE}}.", name)
	}
	if !strings.Contains(template, "{{PACKAGE}}") {
		t.Fatal("the release body no longer names the package at all — the page tells its reader what " +
			"customers run, and that sentence must come from package.json")
	}
	step := releaseNotesStep(t)
	if !strings.Contains(step, "require('./source/publish/npm/package.json').name") {
		t.Fatal("the release-notes step does not read the package name out of package.json the way publish-npm.yml " +
			"does — if it spells the name, the release page and the registry can disagree about what customers run")
	}
}

// SEAL 3 — THE SUBSTITUTION REFUSES A SURVIVING TOKEN. Run, not read: the step's own script, extracted verbatim from
// release.yml and executed with bash on a scratch copy — once as written, once with a token misspelt. A refusal that
// is present and wrong (a grep for the wrong pattern) passes any seal that only looks for the word "refusing".
func TestTheReleaseNotesStepRefusesASurvivingToken(t *testing.T) {
	for _, tool := range []string{"bash", "sed", "grep", "node"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("this seal runs release.yml's own release-notes step, which needs %s — as the release runner has. "+
				"It fails rather than skips: a seal that quietly does not run reads green", tool)
		}
	}
	step := releaseNotesStep(t)
	name, _ := npmPackageJSON(t)["name"].(string)

	written, out, err := runReleaseNotesStep(t, step)
	if err != nil {
		t.Fatalf("the release-notes step failed on the body as written, for tag v0.1.1 — a release could not be "+
			"cut at all:\n%s", out)
	}
	for _, want := range []string{"npx " + name + "@0.1.1", "run `npx " + name + "`", "Built from `v0.1.1`"} {
		if !strings.Contains(written, want) {
			t.Fatalf("for tag v0.1.1 the release page would not say %q — it says:\n%s", want, written)
		}
	}
	if strings.Contains(written, "{{") {
		t.Fatalf("a {{…}} token reached the release page for tag v0.1.1:\n%s", written)
	}

	// THE REFUSAL, driven. One token misspelt in the step's own template must stop the release, not reach the page.
	misspelt := strings.Replace(step, "@{{VERSION}}", "@{{VERSON}}", 1)
	if misspelt == step {
		t.Fatal("instrument guard: the template carries no `@{{VERSION}}` to misspell — the refusal cannot be driven")
	}
	written, out, err = runReleaseNotesStep(t, misspelt)
	if err == nil {
		t.Fatalf("a misspelt token survived into the release notes and the step let the release go ahead — every "+
			"reader of that page would see a raw {{…}} where the pin should be:\n%s", written)
	}
	if !strings.Contains(out, "refusing") {
		t.Fatalf("the step failed on a misspelt token, but not with its refusal — it broke for some other reason:\n%s", out)
	}

	// …and the release action is handed the SUBSTITUTED file, never the template.
	if !strings.Contains(releaseWorkflow(t), "body_path: ${{ runner.temp }}/release-notes.md") {
		t.Fatal("the release is not created from the file the release-notes step writes — whatever it is given " +
			"instead was never substituted, and never checked for a surviving token")
	}
}

// runReleaseNotesStep runs the step in a scratch directory laid out as the release runner's is — the files the job
// checks out under source/ (package.json, and CHANGELOG.md, whose v0.1.1 section the notes now carry), the tag in
// GITHUB_REF_NAME — and returns what it wrote, its output and its error.
func runReleaseNotesStep(t *testing.T, script string) (string, string, error) {
	t.Helper()
	work, scratch := t.TempDir(), t.TempDir()
	pkgDir := filepath.Join(work, "source", "publish", "npm")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg, err := os.ReadFile(filepath.Join(repoRoot(t), "publish", "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	changelog, err := os.ReadFile(filepath.Join(repoRoot(t), "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "source", "CHANGELOG.md"), changelog, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GITHUB_REF_NAME=v0.1.1", "RUNNER_TEMP=" + scratch}
	var combined bytes.Buffer
	cmd.Stdout, cmd.Stderr = &combined, &combined
	runErr := cmd.Run()
	written, _ := os.ReadFile(filepath.Join(scratch, "release-notes.md"))
	return string(written), combined.String(), runErr
}

// SEAL 4 — THE SELF-UPDATE SEAL STILL READS THE BODY. TestTheSelfUpdateRefusalSaysWhatItActuallyRefuses walks every
// workflow; the body's refusal of self-update is inside release.yml, and the pin beside it is the derived one.
func TestTheSelfUpdateSealStillReadsTheReleaseBody(t *testing.T) {
	template := releaseBodyTemplate(t)
	if !strings.Contains(strings.ToLower(template), "self-update") {
		t.Fatal("the release body no longer refuses self-update — the page a reader opens stopped saying " +
			"that the agent never replaces itself")
	}
	if !strings.Contains(template, "npx {{PACKAGE}}@{{VERSION}}") {
		t.Fatal("the release body's pin is not the derived `npx {{PACKAGE}}@{{VERSION}}` — the self-update seal " +
			"accepts a literal too, and a literal is the version nobody published")
	}
	for _, other := range []string{"release-notes.md", "RELEASE_NOTES.md", "release-body.md"} {
		if _, err := os.Stat(filepath.Join(repoRoot(t), other)); err == nil {
			t.Fatalf("%s exists in the repository — a release body kept outside .github/workflows is outside the "+
				"self-update seal's sight, which is where a refusal nobody checks goes to rot", other)
		}
	}
}

// ── SEAL 5 AND THE CENSUS ────────────────────────────────────────────────────────────────────────────────────────

// workflowJobs returns each workflow's job ids and job `name:` values, parsed by indentation: a job is a two-space key
// under `jobs:`, and its display name is a four-space `name:` directly under it.
func workflowJobs(text string) []string {
	var jobs []string
	inJobs := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '#' {
			inJobs = false
			continue
		}
		if m := regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`).FindStringSubmatch(line); m != nil {
			jobs = append(jobs, m[1])
		}
		if m := regexp.MustCompile(`^    name:\s*["']?([^"'#]+?)["']?\s*$`).FindStringSubmatch(line); m != nil {
			jobs = append(jobs, m[1])
		}
	}
	return jobs
}

// SEAL 5 — NO TWO WORKFLOWS SHARE A JOB NAME. The Actions list shows jobs by name, so two workflows with a `publish`
// job are one word a person cannot tell apart — and only one of them reaches npm.
func TestNoTwoWorkflowsShareAJobName(t *testing.T) {
	fixture := "name: x\njobs:\n  publish:\n    runs-on: a\n  other:\n    name: Shown Name\n    steps:\n      - name: Not A Job\n"
	if got := strings.Join(workflowJobs(fixture), ","); got != "publish,other,Shown Name" {
		t.Fatalf("instrument guard: the job parse read %q from a fixture holding the jobs publish and other (shown as "+
			"\"Shown Name\") and one step — it would miss a collision, or invent one", got)
	}

	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the workflows: %v", err)
	}
	owners := map[string][]string{}
	read := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		read++
		for _, job := range workflowJobs(string(data)) {
			owners[job] = append(owners[job], entry.Name())
		}
	}
	if read < 3 || len(owners["publish"]) == 0 {
		t.Fatalf("instrument guard: read %d workflows and found publish-npm.yml's `publish` job in none of them — "+
			"the census is not reading the files that ship this agent", read)
	}

	var shared []string
	for job, files := range owners {
		if len(files) > 1 {
			sort.Strings(files)
			shared = append(shared, "`"+job+"` in "+strings.Join(files, " and "))
		}
	}
	if len(shared) > 0 {
		sort.Strings(shared)
		t.Fatalf("two workflows share a job name: %s. The Actions list shows a job by its name, so a person reading "+
			"a green one cannot tell which workflow ran — and when one of them publishes to npm and the other does "+
			"not, that is the question they have to ask. Rename the one that does not publish.", strings.Join(shared, "; "))
	}
}

// censusExempt: text that reaches a reader and names a version literal or the package name ON PURPOSE.
var censusExempt = map[string]string{
	".github/workflows/publish-npm.yml:version": "A comment recording the defect this repository removed — the invented pin, quoted beside the words 'a package that does not exist and a version nobody ever published' — so a reader of the workflow learns what the tokens replaced; no reader can take it as an instruction.",
	".github/workflows/publish-npm.yml:package": "Two comments telling a person which name the package was given, so the name they type into this workflow's confirm_name gate is the right one; the gate itself compares against package.json, never these comments.",
}

// THE CENSUS — every file whose text reaches a release page, npm, or a person reading the Actions list: every workflow,
// the README (the registry page), and the package's own files. No version literal, and the package name only where it
// is declared, anywhere in it — except the rows argued above.
func TestNothingThatReachesAReaderInventsAVersionOrTypesThePackage(t *testing.T) {
	root := repoRoot(t)
	name, _ := npmPackageJSON(t)["name"].(string)
	version := regexp.MustCompile(`@\d+\.\d+`)
	pkg := regexp.MustCompile(`(^|[^-\w])` + regexp.QuoteMeta(name) + `($|[^-\w.])`)

	// INSTRUMENT: the two patterns on text built here.
	if !version.MatchString("npx x@1.4.0") || version.MatchString("uses: actions/checkout@v4") || !pkg.MatchString("run `npx "+name+"`") || pkg.MatchString("nth-agent_v0.1.1_linux_amd64") {
		t.Fatal("instrument guard: the census's patterns do not tell a pinned version from an action's `@v4`, or the " +
			"package name from the binary's archive name")
	}

	// CHANGELOG.md and THIRD_PARTY_NOTICES.txt joined in AN_AGENT_THAT_SHIPS_ITS_NOTICES: both ship in the npm package and
	// with every release, and the changelog's sections ARE the release notes.
	population := []string{"README.md", "CHANGELOG.md", "THIRD_PARTY_NOTICES.txt", "NOTICE", "SECURITY.md", "CONTRIBUTING.md"}
	for _, dir := range []string{filepath.Join(".github", "workflows"), filepath.Join("publish", "npm")} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				population = append(population, filepath.ToSlash(filepath.Join(dir, entry.Name())))
			}
		}
	}
	for _, want := range []string{".github/workflows/release.yml", ".github/workflows/publish-npm.yml", "publish/npm/package.json", "README.md",
		"CHANGELOG.md", "THIRD_PARTY_NOTICES.txt"} {
		if !containsPath(population, want) {
			t.Fatalf("instrument guard: the census did not reach %s — it is not reading the files a reader meets", want)
		}
	}

	var problems []string
	usedExempt := map[string]bool{}
	for _, rel := range population {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if version.MatchString(line) {
				if _, ok := censusExempt[rel+":version"]; ok {
					usedExempt[rel+":version"] = true
				} else {
					problems = append(problems, rel+":"+itoa(i+1)+" pins a version literal — "+strings.TrimSpace(line))
				}
			}
			if pkg.MatchString(line) && rel != "publish/npm/package.json" {
				if _, ok := censusExempt[rel+":package"]; ok {
					usedExempt[rel+":package"] = true
				} else {
					problems = append(problems, rel+":"+itoa(i+1)+" types the package name — "+strings.TrimSpace(line))
				}
			}
		}
	}
	for key, reason := range censusExempt {
		if len(strings.Fields(reason)) < 12 {
			problems = append(problems, key+" is exempted in fewer than twelve words — an exemption with no argument declares nothing")
		}
		if !usedExempt[key] {
			problems = append(problems, key+" is exempted but nothing there needs it any more — the row is stale")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("text that reaches a reader carries a version nobody derived, or a name nobody read:\n  %s\nA version "+
			"typed into prose is a fact with no source, and the last one was on every release page. Use {{VERSION}} "+
			"and {{PACKAGE}} where a step substitutes them, or argue the line in censusExempt.", strings.Join(problems, "\n  "))
	}
}

func containsPath(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
