package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	nthagent "github.com/unidotssoftwaresolutions/nth-agent"
)

// AN_AGENT_THAT_SHIPS_ITS_NOTICES — the notices travel with every artefact, and the agent can show them.
//
// Every binary this repository releases links Go and fifteen modules whose licences ask for their notices with every copy,
// and until this slice neither the GitHub release nor the npm package carried one. THIRD_PARTY_NOTICES.txt is now built
// from what the release links (the module root's notices_test.go); these seals hold the rest of the way to the reader:
// the binary prints it, every release build checks its binary against it, every archive and the release page carry it,
// and the npm package ships it — the tag's own file, the one its binary prints. Each workflow step is RUN here, with bash
// on a scratch directory laid out like the runner's, never only read: a refusal that is present and wrong passes a seal
// that looks for the word "refusing".

// workflowStep is one step's `run: |` script, dedented exactly as the runner receives it.
func workflowStep(t *testing.T, workflow, name string) string {
	t.Helper()
	lines := strings.Split(workflowText(t, workflow), "\n")
	start := -1
	for i, line := range lines {
		if line == "      - name: "+name {
			start = i
		}
	}
	if start < 0 || start+1 >= len(lines) || lines[start+1] != "        run: |" {
		t.Fatalf("%s has no step %q with a `run: |` block", workflow, name)
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

// expand fills a step's GitHub expressions the way the runner would for one case, and refuses to run a script that still
// carries one — bash would read `${{ … }}` as a bad substitution, and the case would fail for the wrong reason.
func expand(t *testing.T, script string, values map[string]string) string {
	t.Helper()
	for expr, value := range values {
		script = strings.ReplaceAll(script, "${{ "+expr+" }}", value)
	}
	if strings.Contains(script, "${{") {
		t.Fatalf("instrument guard: the step still carries an expression this seal did not fill:\n%s", script)
	}
	return script
}

func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("this seal runs a workflow step, which needs %s — as the runner has. It fails rather than skips: a seal "+
				"that quietly does not run reads green", tool)
		}
	}
}

// runStep runs a step's script with bash in dir; extra environment entries come last and win.
func runStep(t *testing.T, script, dir string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func noticesInTheTree(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "THIRD_PARTY_NOTICES.txt"))
	if err != nil {
		t.Fatalf("reading THIRD_PARTY_NOTICES.txt: %v — the release and the npm package carry it, and nothing else does", err)
	}
	return data
}

// ── THE AGENT CAN SHOW THEM ─────────────────────────────────────────────────────────────────────────────────────────

func TestTheNoticesCommandPrintsTheFileItCarries(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"notices"}, strings.NewReader(""), &stdout, &stderr); code != exitOK || stderr.Len() != 0 {
		t.Fatalf("`nth-agent notices` must answer with exit 0 and nothing on stderr, got %d %q", code, stderr.String())
	}
	if stdout.String() != nthagent.ThirdPartyNotices {
		t.Fatal("`nth-agent notices` does not print the notices the binary carries")
	}
	if !bytes.Equal(stdout.Bytes(), noticesInTheTree(t)) {
		t.Fatal("`nth-agent notices` prints something other than THIRD_PARTY_NOTICES.txt — the package and the binary would " +
			"carry two different lists")
	}
	if !strings.Contains(stdout.String(), "Module: ") || len(stdout.String()) < 10_000 {
		t.Fatalf("instrument guard: the notices printed are %d bytes with no module entry — the embed read the wrong file", stdout.Len())
	}
}

func TestTheVersionSaysWhereTheNoticesAre(t *testing.T) {
	var out bytes.Buffer
	answerQuestion("--version", &out)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("--version prints %d line(s) and must print two — the version, then where the notices are: %q", len(lines), out.String())
	}
	if lines[0] != "nth-agent "+version {
		t.Fatalf("--version's FIRST line is %q and must be `nth-agent <version>` alone — publish-npm.yml compares its last word "+
			"with the package's version", lines[0])
	}
	for _, says := range []string{"npx " + packageName(t) + "@latest notices", "THIRD_PARTY_NOTICES.txt"} {
		if !strings.Contains(lines[1], says) {
			t.Fatalf("--version's second line must say where the notices are (%q), and says %q", says, lines[1])
		}
	}
	if !verbTableHas("notices") {
		t.Fatal("--version names the `notices` command and the verb table has no such verb — a command that answers 'unknown command'")
	}
}

func verbTableHas(name string) bool {
	for _, v := range verbs {
		if v.Name == name {
			return true
		}
	}
	return false
}

// The README — the npm page — says where the notices are, in the half a customer reads.
func TestTheReadmeSaysWhereTheNoticesAre(t *testing.T) {
	customer, _, _ := strings.Cut(readmeText(t), readmeBoundary)
	_, section, found := strings.Cut(customer, "\n## Third-party notices\n")
	if !found {
		t.Fatal("the half of the README the npm page carries has no `## Third-party notices` section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, says := range []string{"`THIRD_PARTY_NOTICES.txt`", "`npx {{PACKAGE}}@latest notices`"} {
		if !strings.Contains(section, says) {
			t.Errorf("the README's third-party notices section does not say %s", says)
		}
	}
}

// ── EVERY RELEASE BUILD ASKS ITS OWN BINARY ─────────────────────────────────────────────────────────────────────────

// goVersionM is what `go version -m FILE` prints for a binary whose build info is bi: cmd/go prints the file and the Go
// version, then each line of debug.BuildInfo's own String form, tab-indented. The fake `go` below prints this, so the step
// reads the format Go itself writes rather than one typed here.
func goVersionM(file string, deps []*debug.Module) string {
	bi := debug.BuildInfo{
		Path: "github.com/unidotssoftwaresolutions/nth-agent/cmd/nth-agent",
		Main: debug.Module{Path: "github.com/unidotssoftwaresolutions/nth-agent", Version: "(devel)"},
		Deps: deps,
		Settings: []debug.BuildSetting{{Key: "-buildmode", Value: "exe"}, {Key: "-trimpath", Value: "true"},
			{Key: "CGO_ENABLED", Value: "0"}, {Key: "GOARCH", Value: "amd64"}, {Key: "GOOS", Value: "linux"}},
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: go1.27.0\n", file)
	for _, line := range strings.Split(strings.TrimSuffix(bi.String(), "\n"), "\n") {
		b.WriteString("\t" + line + "\n")
	}
	return b.String()
}

// noticedModules is every module the notices name, as a binary's build info would list it.
func noticedModules(t *testing.T) []*debug.Module {
	t.Helper()
	var mods []*debug.Module
	for _, line := range strings.Split(string(noticesInTheTree(t)), "\n") {
		if after, ok := strings.CutPrefix(line, "Module: "); ok {
			path, version, _ := strings.Cut(after, " ")
			mods = append(mods, &debug.Module{Path: path, Version: version, Sum: "h1:" + strings.Repeat("A", 43) + "="})
		}
	}
	if len(mods) < 10 {
		t.Fatalf("instrument guard: the notices name %d modules — the parse read the wrong file", len(mods))
	}
	return mods
}

func TestEveryReleaseBuildRefusesABinaryThatLinksAModuleTheNoticesDoNotName(t *testing.T) {
	needTools(t, "bash", "grep", "cat")
	step := expand(t, workflowStep(t, "release.yml", "The notices name every module this binary links"),
		map[string]string{"matrix.goos": "linux", "matrix.goarch": "amd64"})

	buildWith := func(deps []*debug.Module) (string, error) {
		work, fake, scratch := t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(work, "THIRD_PARTY_NOTICES.txt"), noticesInTheTree(t), 0o644)
		writeFile(t, filepath.Join(work, "dist", "nth-agent"), []byte("not a binary; the fake go reads nothing\n"), 0o755)
		info := filepath.Join(fake, "buildinfo.txt")
		writeFile(t, info, []byte(goVersionM("dist/nth-agent", deps)), 0o644)
		writeFile(t, filepath.Join(fake, "go"), []byte("#!/bin/sh\n[ \"$1 $2 $3\" = \"version -m dist/nth-agent\" ] || "+
			"{ echo \"fake go was asked: $*\" >&2; exit 2; }\ncat '"+info+"'\n"), 0o755)
		return runStep(t, step, work, "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+scratch)
	}

	named := noticedModules(t)
	out, err := buildWith(named)
	if err != nil || !strings.Contains(out, fmt.Sprintf("names all %d modules", len(named))) {
		t.Fatalf("a binary linking exactly the modules the notices name was refused, or not counted (%v):\n%s", err, out)
	}

	clone := func() []*debug.Module {
		var c []*debug.Module
		for _, m := range named {
			copied := *m
			c = append(c, &copied)
		}
		return c
	}
	extra := append(clone(), &debug.Module{Path: "example.com/not-noticed", Version: "v0.0.1", Sum: "h1:x="})
	moved := clone()
	moved[0].Version = "v9.9.9"
	replaced := clone()
	replaced[1].Replace = &debug.Module{Path: "example.com/a-fork", Version: "v1.0.0", Sum: "h1:y="}
	for _, c := range []struct {
		name  string
		deps  []*debug.Module
		names string
	}{
		{"a module the notices do not name", extra, "example.com/not-noticed v0.0.1"},
		{"a named module at another version", moved, named[0].Path + " v9.9.9"},
		{"a replaced module", replaced, "example.com/a-fork"},
		{"a binary that lists no module at all", nil, "no modules"},
	} {
		out, err := buildWith(c.deps)
		if err == nil {
			t.Errorf("%s: the release build went ahead:\n%s", c.name, out)
			continue
		}
		if !strings.Contains(out, "refusing") || !strings.Contains(out, c.names) {
			t.Errorf("%s: the step failed, but not with its refusal naming %q:\n%s", c.name, c.names, out)
		}
	}
}

// The step must stand between the build and the archive, in the build job — a check after the archive is a check of
// something already packed, and one in another job never sees the bytes.
func TestTheNoticesCheckStandsBetweenTheBuildAndTheArchive(t *testing.T) {
	workflow := releaseWorkflow(t)
	build := strings.Index(workflow, "\n      - name: Build\n")
	check := strings.Index(workflow, "\n      - name: The notices name every module this binary links\n")
	archive := strings.Index(workflow, "\n      - name: Archive\n")
	release := strings.Index(workflow, "\n  github-release:\n")
	if build < 0 || check < 0 || archive < 0 || release < 0 {
		t.Fatalf("instrument guard: a step is missing (build %d, check %d, archive %d, github-release %d)", build, check, archive, release)
	}
	if !(build < check && check < archive && archive < release) {
		t.Fatal("the notices check must run after the Build step and before the Archive step, in the build job")
	}
}

// ── EVERY ARCHIVE, AND THE RELEASE PAGE, CARRY THEM ─────────────────────────────────────────────────────────────────

// archiveMembers is what every archive release.yml builds holds. A_RELEASE_THAT_CARRIES_ITS_LICENCE: the agent's own code
// is under the Apache License 2.0, which asks that whoever receives the program in object form gets the licence and the
// NOTICE file with it, so LICENSE and NOTICE travel beside the binary as the third-party notices do.
var archiveMembers = []string{"nth-agent", "README.md", "THIRD_PARTY_NOTICES.txt", "LICENSE", "NOTICE"}

// ownFile is a file at the repository root, as the tag's tree holds it.
func ownFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("the repository root has no %s: %v", name, err)
	}
	return data
}

// releaseTargets is the release's matrix, read from release.yml: one goos/goarch pair per archive it builds.
func releaseTargets(t *testing.T) [][2]string {
	t.Helper()
	var targets [][2]string
	var goos string
	for _, line := range strings.Split(releaseWorkflow(t), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if after, ok := strings.CutPrefix(line, "goos:"); ok {
			goos = strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(line, "goarch:"); ok && goos != "" {
			targets = append(targets, [2]string{goos, strings.TrimSpace(after)})
		}
	}
	if len(targets) < 3 {
		t.Fatalf("instrument guard: only %d targets parsed from release.yml — the parse is wrong, not the matrix", len(targets))
	}
	return targets
}

// Every archive, on every platform the matrix builds, and each file in it the tag's own, byte for byte.
func TestEveryArchiveCarriesTheNotices(t *testing.T) {
	needTools(t, "bash", "tar", "shasum")
	tree := map[string][]byte{
		"README.md":               []byte("readme\n"),
		"THIRD_PARTY_NOTICES.txt": noticesInTheTree(t),
		"LICENSE":                 ownFile(t, "LICENSE"),
		"NOTICE":                  ownFile(t, "NOTICE"),
	}
	for _, target := range releaseTargets(t) {
		goos, goarch := target[0], target[1]
		step := expand(t, workflowStep(t, "release.yml", "Archive"), map[string]string{"matrix.goos": goos, "matrix.goarch": goarch})
		work := t.TempDir()
		writeFile(t, filepath.Join(work, "dist", "nth-agent"), []byte("binary\n"), 0o755)
		for name, data := range tree {
			writeFile(t, filepath.Join(work, name), data, 0o644)
		}
		if out, err := runStep(t, step, work, "GITHUB_REF_NAME=v0.3.0"); err != nil {
			t.Fatalf("the %s/%s Archive step failed on a tree laid out like the runner's: %v\n%s", goos, goarch, err, out)
		}
		archive := filepath.Join(work, "nth-agent_v0.3.0_"+goos+"_"+goarch+".tar.gz")
		listing, err := exec.Command("tar", "-tzf", archive).Output()
		if err != nil {
			t.Fatalf("listing the %s/%s archive the step made: %v", goos, goarch, err)
		}
		for _, member := range archiveMembers {
			if !strings.Contains("\n"+string(listing), "\n"+member+"\n") {
				t.Fatalf("the %s/%s archive holds:\n%sand not %s — a download that arrives without it arrives without what it owes",
					goos, goarch, listing, member)
			}
		}
		for name, want := range tree {
			got, err := exec.Command("tar", "-xOzf", archive, name).Output()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("the %s/%s archive's %s is not the tag's own file (%v)", goos, goarch, name, err)
			}
		}
	}
}

// A tag whose tree lacks either file is refused before anything is built, with a sentence that names the file it lacks.
func TestATagWithoutItsLicenceIsRefusedBeforeAnythingIsBuilt(t *testing.T) {
	needTools(t, "bash")
	const name = "The tag carries its licence"
	step := workflowStep(t, "release.yml", name)
	with := func(files ...string) (string, error) {
		work := t.TempDir()
		for _, f := range files {
			writeFile(t, filepath.Join(work, f), ownFile(t, f), 0o644)
		}
		return runStep(t, step, work)
	}
	if out, err := with("LICENSE", "NOTICE"); err != nil {
		t.Fatalf("a tag with both LICENSE and NOTICE was refused: %v\n%s", err, out)
	}
	for _, c := range []struct {
		name    string
		files   []string
		missing map[string]bool
	}{
		{"a tag with no LICENSE", []string{"NOTICE"}, map[string]bool{"LICENSE": true}},
		{"a tag with no NOTICE", []string{"LICENSE"}, map[string]bool{"NOTICE": true}},
		{"a tag with neither", nil, map[string]bool{"LICENSE": true, "NOTICE": true}},
	} {
		out, err := with(c.files...)
		if err == nil {
			t.Errorf("%s: the release went on to build:\n%s", c.name, out)
			continue
		}
		for _, f := range []string{"LICENSE", "NOTICE"} {
			named := regexp.MustCompile(`(?m)^refusing: .*\b` + f + `\b`).MatchString(out)
			if named != c.missing[f] {
				t.Errorf("%s: the refusal names %s %v, and must name exactly the file the tag lacks:\n%s", c.name, f, named, out)
			}
		}
	}

	// …and it stands in the build job before the Build step, and nothing lets the job go on past it.
	workflow := releaseWorkflow(t)
	job := strings.Index(workflow, "\n  build:\n")
	refusal := strings.Index(workflow, "\n      - name: "+name+"\n")
	build := strings.Index(workflow, "\n      - name: Build\n")
	if job < 0 || refusal < 0 || build < 0 {
		t.Fatalf("instrument guard: a step is missing (build job %d, refusal %d, Build %d)", job, refusal, build)
	}
	if !(job < refusal && refusal < build) {
		t.Fatal("the licence check must run in the build job before the Build step — after it, something has been built")
	}
	own, _, _ := strings.Cut(workflow[refusal+1:], "\n      - ")
	if strings.Contains(own, "continue-on-error") || strings.Contains(own, "\n        if:") {
		t.Fatalf("the licence check can be passed over:\n%s", own)
	}
}

// The README's release-page section says what every archive holds, and it is what the archive holds.
func TestTheReadmeSaysWhatEveryArchiveHolds(t *testing.T) {
	_, ours, found := strings.Cut(readmeText(t), readmeBoundary)
	if !found {
		t.Fatal("instrument guard: the README has no boundary")
	}
	_, says, found := strings.Cut(ours, "\nEach archive holds ")
	if !found {
		t.Fatal("the README's release-page section no longer says what each archive holds")
	}
	says, _, _ = strings.Cut(says, "\n\n")
	says = strings.Join(strings.Fields(says), " ")
	says, _, _ = strings.Cut(says, ". ") // the sentence itself: a file named later in the paragraph is not said to be inside
	for _, member := range archiveMembers {
		named := map[string]string{"nth-agent": "the binary", "README.md": "this README"}[member]
		if named == "" {
			named = "`" + member + "`"
		}
		if !strings.Contains(says, named) {
			t.Errorf("the README says each archive holds %q and does not name %s", says, named)
		}
	}
}

func TestTheReleasePageAttachesTheTagsNoticesBesideTheArchives(t *testing.T) {
	needTools(t, "bash", "cp")
	workflow := releaseWorkflow(t)
	_, job, found := strings.Cut(workflow, "\n  github-release:\n")
	if !found {
		t.Fatal("instrument guard: no github-release job")
	}
	for _, want := range []string{"            publish/npm/package.json\n", "            CHANGELOG.md\n", "            THIRD_PARTY_NOTICES.txt\n"} {
		if !strings.Contains(job, "sparse-checkout: |\n") || !strings.Contains(job, want) {
			t.Fatalf("the github-release job's checkout does not take %q — it reads the tag's own copy, never a file from elsewhere",
				strings.TrimSpace(want))
		}
	}
	_, files, found := strings.Cut(job, "          files: |\n")
	if !found {
		t.Fatal("instrument guard: the release step lists no files")
	}
	files, _, _ = strings.Cut(files, "          fail_on_unmatched_files: true")
	if !strings.Contains(files, "            THIRD_PARTY_NOTICES.txt\n") {
		t.Fatalf("the release attaches:\n%sand not THIRD_PARTY_NOTICES.txt", files)
	}

	// …and the file it attaches is the tag's: the step copies source/'s, and refuses a tree without one.
	step := workflowStep(t, "release.yml", "Put the notices beside the archives")
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "source", "THIRD_PARTY_NOTICES.txt"), noticesInTheTree(t), 0o644)
	if out, err := runStep(t, step, work); err != nil {
		t.Fatalf("the step failed with the tag's notices in place: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(work, "THIRD_PARTY_NOTICES.txt"))
	if err != nil || !bytes.Equal(got, noticesInTheTree(t)) {
		t.Fatalf("the notices beside the archives are not the tag's own file (%v)", err)
	}
	if out, err := runStep(t, step, t.TempDir()); err == nil || !strings.Contains(out, "refusing") {
		t.Fatalf("a tag whose tree has no notices was not refused (%v):\n%s", err, out)
	}
}

// ── THE npm PACKAGE SHIPS THEM, AND THEY ARE THE BINARY'S OWN ───────────────────────────────────────────────────────

func TestTheNpmPackageShipsTheNoticesAndTheChangelog(t *testing.T) {
	files, _ := npmPackageJSON(t)["files"].([]any)
	shipped := map[string]bool{}
	for _, f := range files {
		name, _ := f.(string)
		shipped[name] = true
	}
	for _, want := range []string{"THIRD_PARTY_NOTICES.txt", "CHANGELOG.md"} {
		if !shipped[want] {
			t.Fatalf("package.json's `files` does not ship %s — an npm install is a copy, and npm packs only what `files` names", want)
		}
		if _, err := os.Stat(filepath.Join(repoRoot(t), want)); err != nil {
			t.Fatalf("the package ships %s and the repository root has none: %v", want, err)
		}
	}
	// AN_AGENT_READY_TO_BE_READ: the operator licensed the agent's own code under the Apache License 2.0, so the pin that
	// held it UNLICENSED until somebody decided now holds the decision. The third-party licences still travel in
	// THIRD_PARTY_NOTICES.txt.
	if license, _ := npmPackageJSON(t)["license"].(string); license != "Apache-2.0" {
		t.Fatalf("package.json's license is %q — the agent's own code is under the Apache License 2.0 (LICENSE), and the "+
			"third-party licences travel in THIRD_PARTY_NOTICES.txt", license)
	}
}

func TestThePublishPutsTheTagsNoticesAndChangelogInThePackage(t *testing.T) {
	needTools(t, "bash", "cmp", "cp")
	step := expand(t, workflowStep(t, "publish-npm.yml", "Put the notices and the changelog in the package"),
		map[string]string{"inputs.tag": "v0.3.0"})
	notices := noticesInTheTree(t)
	layout := func(release, tree []byte, changelog bool) string {
		work := t.TempDir()
		if release != nil {
			writeFile(t, filepath.Join(work, "dist", "THIRD_PARTY_NOTICES.txt"), release, 0o644)
		}
		writeFile(t, filepath.Join(work, "THIRD_PARTY_NOTICES.txt"), tree, 0o644)
		if changelog {
			writeFile(t, filepath.Join(work, "CHANGELOG.md"), []byte("# Changelog\n\n## 0.3.0\n\n- it\n"), 0o644)
		}
		for _, own := range []string{"LICENSE", "NOTICE"} {
			text, err := os.ReadFile(filepath.Join(repoRoot(t), own))
			if err != nil {
				t.Fatalf("the repository root has no %s: %v", own, err)
			}
			writeFile(t, filepath.Join(work, own), text, 0o644)
		}
		if err := os.MkdirAll(filepath.Join(work, "publish", "npm"), 0o755); err != nil {
			t.Fatal(err)
		}
		return work
	}

	work := layout(notices, notices, true)
	if out, err := runStep(t, step, work); err != nil {
		t.Fatalf("the step refused a release whose notices are the tag's: %v\n%s", err, out)
	}
	for _, f := range []string{"THIRD_PARTY_NOTICES.txt", "CHANGELOG.md", "LICENSE", "NOTICE"} {
		if _, err := os.Stat(filepath.Join(work, "publish", "npm", f)); err != nil {
			t.Fatalf("after the step the package has no %s: %v", f, err)
		}
	}
	withoutTheLicence := layout(notices, notices, true)
	if err := os.Remove(filepath.Join(withoutTheLicence, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	withoutTheNotice := layout(notices, notices, true)
	if err := os.Remove(filepath.Join(withoutTheNotice, "NOTICE")); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		work string
	}{
		{"a release with no notices beside its archives", layout(nil, notices, true)},
		{"a release whose notices are not the tag's", layout(append([]byte("another list\n"), notices...), notices, true)},
		{"a tag with no CHANGELOG.md", layout(notices, notices, false)},
		{"a tag with no LICENSE", withoutTheLicence},
		{"a tag with no NOTICE", withoutTheNotice},
	} {
		if out, err := runStep(t, step, c.work); err == nil || !strings.Contains(out, "refusing") {
			t.Errorf("%s was not refused (%v):\n%s", c.name, err, out)
		}
	}

	// …and the step runs before the publish, after the release's files are fetched.
	workflow := workflowText(t, "publish-npm.yml")
	fetch := strings.Index(workflow, "\n      - name: Fetch the release built by release.yml\n")
	put := strings.Index(workflow, "\n      - name: Put the notices and the changelog in the package\n")
	publish := strings.Index(workflow, "\n      - name: Publish\n")
	if fetch < 0 || put < 0 || publish < 0 || !(fetch < put && put < publish) {
		t.Fatalf("the notices must be put in the package after the release is fetched and before the publish (fetch %d, put %d, publish %d)",
			fetch, put, publish)
	}
	fetchStep, _, _ := strings.Cut(workflow[fetch+1:], "\n      - name: ")
	if !strings.Contains(fetchStep, "gh release download") || !strings.Contains(fetchStep, "--pattern 'THIRD_PARTY_NOTICES.txt'") {
		t.Fatal("the fetch does not download the release's THIRD_PARTY_NOTICES.txt, so the step above can never find it")
	}
}

// fakeAgent is a stand-in for the linux binary the publish runs: it answers --version and `notices` with what it is given.
func fakeAgent(t *testing.T, binDir, versionOut, noticesOut string) {
	t.Helper()
	answers := t.TempDir()
	writeFile(t, filepath.Join(answers, "version.out"), []byte(versionOut), 0o644)
	writeFile(t, filepath.Join(answers, "notices.out"), []byte(noticesOut), 0o644)
	writeFile(t, filepath.Join(binDir, "nth-agent-linux-amd64"), []byte("#!/bin/sh\ncase \"$1\" in\n"+
		"  --version) cat '"+filepath.Join(answers, "version.out")+"' ;;\n"+
		"  notices) cat '"+filepath.Join(answers, "notices.out")+"' ;;\n"+
		"  *) exit 2 ;;\nesac\n"), 0o755)
}

// THE BUG THIS SLICE WOULD HAVE SHIPPED: `--version` gained a second line, and the publish's awk printed the last word of
// EVERY line — two words, never equal to the package's version, so every publish would have been refused. Run against the
// binary's real answer, not a line typed here.
func TestThePublishComparesTheVersionLineAndNothingElse(t *testing.T) {
	needTools(t, "bash", "awk", "node")
	step := workflowStep(t, "publish-npm.yml", "The binary must report the version the package claims")
	var answer bytes.Buffer
	answer.WriteString(versionAnswer("v0.3.0"))
	if strings.Count(answer.String(), "\n") < 2 {
		t.Fatalf("instrument guard: --version answered %q — this seal is for an answer of more than one line", answer.String())
	}

	publishWith := func(packageVersion string) (string, error) {
		work := t.TempDir()
		writeFile(t, filepath.Join(work, "publish", "npm", "package.json"), []byte(`{"name":"x","version":"`+packageVersion+`"}`), 0o644)
		fakeAgent(t, filepath.Join(work, "publish", "npm", "bin"), answer.String(), "")
		return runStep(t, step, work)
	}
	if out, err := publishWith("0.3.0"); err != nil {
		t.Fatalf("the publish refused a binary that reports the package's own version, with --version as the agent prints it: %v\n%s", err, out)
	}
	if out, err := publishWith("0.3.1"); err == nil || !strings.Contains(out, "refusing") {
		t.Fatalf("a package claiming 0.3.1 around a binary that says v0.3.0 was published (%v):\n%s", err, out)
	}
}

func TestThePublishRefusesAPackageWhoseNoticesAreNotTheBinarysOwn(t *testing.T) {
	needTools(t, "bash", "cmp")
	step := workflowStep(t, "publish-npm.yml", "The notices the package ships are the ones its binary carries")
	notices := string(noticesInTheTree(t))
	publishWith := func(binarySays string) (string, error) {
		work := t.TempDir()
		writeFile(t, filepath.Join(work, "publish", "npm", "THIRD_PARTY_NOTICES.txt"), []byte(notices), 0o644)
		fakeAgent(t, filepath.Join(work, "publish", "npm", "bin"), "", binarySays)
		return runStep(t, step, work)
	}
	if out, err := publishWith(notices); err != nil {
		t.Fatalf("the publish refused a package whose notices are the binary's own: %v\n%s", err, out)
	}
	if out, err := publishWith(strings.Replace(notices, "Module: ", "Module:  ", 1)); err == nil || !strings.Contains(out, "refusing") {
		t.Fatalf("a package whose notices differ from the binary's by one byte was published (%v):\n%s", err, out)
	}
	workflow := workflowText(t, "publish-npm.yml")
	if strings.Index(workflow, "\n      - name: The notices the package ships are the ones its binary carries\n") >
		strings.Index(workflow, "\n      - name: Publish\n") {
		t.Fatal("the notices comparison runs after the publish — it would compare a package already on the registry")
	}
}

// THE PACKAGE IS THE TAG'S. The README a customer reads on npmjs.com, the CHANGELOG and the notices come from the checkout,
// the binaries from the tag's release; a checkout of the branch the button was pressed on paired them with another commit.
func TestThePublishChecksOutTheTagItPublishes(t *testing.T) {
	workflow := workflowText(t, "publish-npm.yml")
	_, steps, found := strings.Cut(workflow, "\n    steps:\n")
	if !found {
		t.Fatal("instrument guard: no steps in publish-npm.yml")
	}
	// The checkout is pinned to a commit like every action (actions_test.go), and names the tag as a tag: `refs/tags/`, so
	// a branch with the same name can never be what is checked out, and the commit here is the one the archives are
	// checked against.
	checkout := regexp.MustCompile(`(?m)^      - uses: actions/checkout@[0-9a-f]{40} # v\d+\.\d+\.\d+\n        with:\n          ref: refs/tags/\$\{\{ inputs\.tag \}\}\n`)
	if !checkout.MatchString(steps) {
		t.Fatal("publish-npm.yml does not check out the tag it publishes — the README, CHANGELOG and notices it ships would be " +
			"another commit's than the binaries'")
	}
	if strings.Count(steps, "uses: actions/checkout") != 1 {
		t.Fatal("publish-npm.yml checks out more than once — one of them is not the tag")
	}
}
