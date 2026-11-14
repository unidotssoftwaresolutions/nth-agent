package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── EVERY JOB ON A NAMED IMAGE, EVERY ACTION AT A NAMED RELEASE (WHAT_THE_FIRST_TRY_SHOWED) ─────────────────────────────
//
// `ubuntu-latest` and `windows-latest` are names GitHub moves: the image a job ran on yesterday is not the one it runs on
// after the move, and nothing in this repository changes when it happens. Each job names its image's version instead, so a
// new image is an edit somebody reads, like a new action.
//
// The pin seal (actions_test.go) holds every `uses:` to a commit and a version in a comment; nothing held the two to each
// other, so a comment could name one release and the SHA another. Each action is now held to one release and the commit its
// tag names, read with `git ls-remote --tags` from the action's own repository on 2026-11-14 (a tag object is read through
// to its commit). The releases are the ones whose own release notes moved the action to Node 24, where GitHub runs actions
// now; see the CHANGELOG for the one left on its release.

// pinnedReleases is each action's release and its commit.
var pinnedReleases = map[string]struct{ version, sha string }{
	"actions/checkout":            {"v5.1.0", "fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09"},
	"actions/setup-go":            {"v6.5.0", "924ae3a1cded613372ab5595356fb5720e22ba16"},
	"actions/setup-node":          {"v5.0.0", "a0853c24544627f65ddf259abe73b1d18a591444"},
	"actions/upload-artifact":     {"v6.0.0", "b7c566a772e6b6bfb58ed0dc250532a479d7789f"},
	"actions/download-artifact":   {"v7.0.0", "37930b1c2abaa49bbe596cd826c3c89aef350131"},
	"softprops/action-gh-release": {"v2.6.2", "3bb12739c298aeb8a4eeaf626c5b8d85266b0e65"},
}

// runnerImages are the images a job may name: ubuntu-24.04, the image every Linux job here has run on, and windows-2025, the
// one windows-latest has named since 2025.
var runnerImages = map[string]bool{"ubuntu-24.04": true, "windows-2025": true}

var (
	runsOn    = regexp.MustCompile(`^\s*runs-on:\s*(.*?)\s*$`)
	usesPin   = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*([^@\s]+)@([0-9a-f]{40})\s+#\s+(v\d+\.\d+\.\d+)\s*$`)
	usesAnyAt = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)`)
)

// imageAndReleaseProblems reads one workflow: every runs-on a named image, every action at its release and commit.
func imageAndReleaseProblems(file, text string) (problems []string, jobs, uses int) {
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := runsOn.FindStringSubmatch(line); m != nil {
			jobs++
			if !runnerImages[m[1]] {
				problems = append(problems, file+":"+strconv.Itoa(i+1)+": runs-on "+m[1]+", which is not one of the named images")
			}
		}
		if usesAnyAt.MatchString(line) {
			uses++
			m := usesPin.FindStringSubmatch(line)
			if m == nil {
				problems = append(problems, file+":"+strconv.Itoa(i+1)+": a `uses:` this reading cannot hold to a release: "+strings.TrimSpace(line))
				continue
			}
			want, known := pinnedReleases[m[1]]
			switch {
			case !known:
				problems = append(problems, file+":"+strconv.Itoa(i+1)+": "+m[1]+" is an action the release list does not name")
			case m[2] != want.sha || m[3] != want.version:
				problems = append(problems, file+":"+strconv.Itoa(i+1)+": "+m[1]+"@"+m[2]+" # "+m[3]+", where the list holds "+want.version+
					" at "+want.sha)
			}
		}
	}
	return problems, jobs, uses
}

func TestEveryJobRunsOnANamedImageAndEveryActionAtItsRelease(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	jobs, uses := 0, 0
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		found, j, u := imageAndReleaseProblems(entry.Name(), string(data))
		problems, jobs, uses = append(problems, found...), jobs+j, uses+u
		for _, line := range strings.Split(string(data), "\n") {
			if m := usesPin.FindStringSubmatch(line); m != nil {
				seen[m[1]] = true
			}
		}
	}
	if jobs < 7 || uses < 15 {
		t.Fatalf("instrument guard: read %d jobs and %d `uses:` lines — the seal is not reading the real files", jobs, uses)
	}
	for action := range pinnedReleases {
		if !seen[action] {
			problems = append(problems, "the release list names "+action+", which no workflow uses any more")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("the workflows run on an image or an action nobody named:\n  %s", strings.Join(problems, "\n  "))
	}
}

// SETUP-NODE CACHES BY ITSELF FROM v5 ON when the package.json at the checkout's root names a package manager; the publish job
// must restore no cache (a_release_only_its_reviewers_can_publish_test.go), so no package.json at the root names one.
func TestThePublishJobsCheckoutGivesSetupNodeNothingToCache(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "package.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal(data, &root) == nil && root["packageManager"] != nil {
		t.Fatal("package.json at the root names a packageManager, so setup-node would restore a cache in the publish job")
	}
}

// THE GUARD: the reading above, on planted files whose answers are known.
func TestTheImageAndReleaseReadingFindsWhatIsPlanted(t *testing.T) {
	checkout := pinnedReleases["actions/checkout"]
	clean := "jobs:\n  build:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/checkout@" + checkout.sha + " # " +
		checkout.version + "\n      # - uses: actions/checkout@v4 is a comment\n  win:\n    runs-on: windows-2025\n"
	if problems, jobs, uses := imageAndReleaseProblems("clean.yml", clean); len(problems) != 0 || jobs != 2 || uses != 1 {
		t.Fatalf("a clean file read as %d jobs, %d uses and problems %v", jobs, uses, problems)
	}
	for _, c := range []struct{ name, text, want string }{
		{"ubuntu-latest", strings.Replace(clean, "ubuntu-24.04", "ubuntu-latest", 1), "runs-on ubuntu-latest"},
		{"windows-latest", strings.Replace(clean, "windows-2025", "windows-latest", 1), "runs-on windows-latest"},
		{"another SHA", strings.Replace(clean, checkout.sha, strings.Repeat("a", 40), 1), "where the list holds"},
		{"another version", strings.Replace(clean, "# "+checkout.version, "# v4.4.0", 1), "where the list holds"},
		{"an action nobody listed", strings.Replace(clean, "actions/checkout@", "someone/else@", 1), "does not name"},
		{"a tag", strings.Replace(clean, "@"+checkout.sha+" # "+checkout.version, "@v5", 1), "cannot hold to a release"},
	} {
		problems, _, _ := imageAndReleaseProblems("planted.yml", c.text)
		if !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%s was not refused with %q: %v", c.name, c.want, problems)
		}
	}
}
