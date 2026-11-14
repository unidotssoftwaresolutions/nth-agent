package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// AN_AGENT_THAT_SHIPS_ITS_NOTICES — THE RELEASE SAYS WHAT IS IN IT.
//
// No release this repository cut said what changed: the notes were a fixed text about checksums and Gatekeeper, and the
// npm package carried nothing at all. CHANGELOG.md now has a section per release, the npm package ships it, and
// release.yml makes the tag's own section the release notes — and refuses a tag with none, before anything is published.

var versionHeading = regexp.MustCompile(`^## (\d+)\.(\d+)\.(\d+)(?: .*)?$`)

type changelogSection struct {
	version string
	parts   [3]int
	body    string
}

// changelogSections reads CHANGELOG.md the way the release step does: a `## <version>` heading, alone or followed by a
// space, and everything under it up to the next `## `.
func changelogSections(t *testing.T, text string) []changelogSection {
	t.Helper()
	var sections []changelogSection
	var current *changelogSection
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			m := versionHeading.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("CHANGELOG.md has the heading %q, which names no version — the release finds its notes by "+
					"`## <version>`, and a heading it cannot name is one no release will ever show", line)
			}
			sections = append(sections, changelogSection{version: m[1] + "." + m[2] + "." + m[3]})
			current = &sections[len(sections)-1]
			for i := range 3 {
				current.parts[i], _ = strconv.Atoi(m[i+1])
			}
			continue
		}
		if current != nil {
			current.body += line + "\n"
		}
	}
	return sections
}

func changelogText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("reading CHANGELOG.md: %v — the npm package ships it and every release's notes are a section of it", err)
	}
	return string(data)
}

func newer(a, b [3]int) bool {
	for i := range 3 {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func TestTheChangelogHasOneSectionPerReleaseNewestFirst(t *testing.T) {
	text := changelogText(t)
	if !strings.HasPrefix(text, "# Changelog\n") {
		t.Fatal("CHANGELOG.md does not open with its title — it is read on its own, in the npm package, by people who did not come from the README")
	}
	sections := changelogSections(t, text)
	if len(sections) < 4 {
		t.Fatalf("instrument guard: %d sections read — 0.1.0, 0.1.1, 0.2.0 and 0.3.0 each have one", len(sections))
	}
	for i, s := range sections {
		if strings.TrimSpace(s.body) == "" {
			t.Errorf("the %s section is empty — the release would be refused for it, and a reader learns nothing", s.version)
		}
		if i > 0 && !newer(sections[i-1].parts, s.parts) {
			t.Errorf("%s comes after %s: the sections must be newest first, each version once", s.version, sections[i-1].version)
		}
	}

	// Every release this clone has a tag for has its section. A CI checkout carries no tags, so there this half checks
	// nothing — and says so, rather than passing as if it had: the release step's refusal is what holds a NEW tag.
	tags := releaseTags(t)
	have := map[string]bool{}
	for _, s := range sections {
		have[s.version] = true
	}
	for _, tag := range tags {
		if !have[strings.TrimPrefix(tag, "v")] {
			t.Errorf("this clone has the release tag %s and CHANGELOG.md has no section for it", tag)
		}
	}
	t.Logf("%d release tag(s) in this clone, each with its section", len(tags))
}

// releaseTags is every vX.Y.Z tag this clone holds, loose or packed, read as files — no process, nothing written.
func releaseTags(t *testing.T) []string {
	t.Helper()
	gitDir := filepath.Join(repoRoot(t), ".git")
	release := regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	var tags []string
	if entries, err := os.ReadDir(filepath.Join(gitDir, "refs", "tags")); err == nil {
		for _, e := range entries {
			if release.MatchString(e.Name()) {
				tags = append(tags, e.Name())
			}
		}
	}
	if packed, err := os.ReadFile(filepath.Join(gitDir, "packed-refs")); err == nil {
		for _, line := range strings.Split(string(packed), "\n") {
			if _, ref, ok := strings.Cut(line, " refs/tags/"); ok && release.MatchString(ref) {
				tags = append(tags, ref)
			}
		}
	}
	return tags
}

// THE 0.3.0 ENTRY SAYS WHAT THE BRIEF FOR IT ASKED: capture on your own Android phone with the six things every capture
// text says (the capture seal's own marks), each phone's readiness, the shared-machine check, masking on your machine, and
// the notices — with the command that prints them.
func TestTheCaptureReleaseSaysWhatIsInIt(t *testing.T) {
	var entry string
	for _, s := range changelogSections(t, changelogText(t)) {
		if s.version == "0.3.0" {
			entry = fold(s.body)
		}
	}
	if entry == "" {
		t.Fatal("CHANGELOG.md has no 0.3.0 section — the release that brings capture to a customer's own phone")
	}
	for _, thing := range sixThingsAsReleasedIn030() {
		for _, mark := range thing.marks {
			if !mark.MatchString(entry) {
				t.Errorf("the 0.3.0 entry does not say %q (no %s)", thing.thing, mark)
			}
		}
	}
	for _, says := range []struct{ what, mark string }{
		{"capture is off until a person turns it on", "off until you turn it on"},
		{"it is Android, on macOS or Linux", "Android"},
		{"each phone says whether it is ready", "each phone"},
		{"the check on a machine other people log into", "other people log into"},
		{"masking happens on the customer's machine", "Masked on your machine"},
		{"where the notices are", "THIRD_PARTY_NOTICES.txt"},
		{"the command that prints them", "`notices` command"},
	} {
		if !strings.Contains(entry, says.mark) {
			t.Errorf("the 0.3.0 entry does not say %s (no %q)", says.what, says.mark)
		}
	}
}

// ── THE RELEASE NOTES ARE THE TAG'S SECTION ─────────────────────────────────────────────────────────────────────────

// runNotesWith runs release.yml's own release-notes step for a tag, with the given CHANGELOG.md in the tag's tree (absent
// when changelog is nil), and returns the notes it wrote, its output and its error.
func runNotesWith(t *testing.T, tag string, changelog []byte) (string, string, error) {
	t.Helper()
	needTools(t, "bash", "sed", "grep", "awk", "node")
	work, scratch := t.TempDir(), t.TempDir()
	pkg, err := os.ReadFile(filepath.Join(repoRoot(t), "publish", "npm", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "source", "publish", "npm", "package.json"), pkg, 0o644)
	if changelog != nil {
		writeFile(t, filepath.Join(work, "source", "CHANGELOG.md"), changelog, 0o644)
	}
	out, runErr := runStep(t, releaseNotesStep(t), work, "GITHUB_REF_NAME="+tag, "RUNNER_TEMP="+scratch)
	written, _ := os.ReadFile(filepath.Join(scratch, "release-notes.md"))
	return string(written), out, runErr
}

func TestTheReleaseNotesAreTheTagsOwnChangelogSection(t *testing.T) {
	changelog := changelogText(t)
	sections := changelogSections(t, changelog)
	newest := sections[0]

	notes, out, err := runNotesWith(t, "v"+newest.version, []byte(changelog))
	if err != nil {
		t.Fatalf("the release-notes step refused v%s, whose section is in CHANGELOG.md: %v\n%s", newest.version, err, out)
	}
	if !strings.Contains(notes, strings.TrimSpace(newest.body)) {
		t.Fatalf("the notes for v%s do not carry its CHANGELOG section verbatim:\n%s", newest.version, notes)
	}
	older := strings.TrimSpace(sections[1].body)
	if strings.Contains(notes, older[:min(len(older), 60)]) {
		t.Fatalf("the notes for v%s carry the %s section too", newest.version, sections[1].version)
	}
	for _, want := range []string{"Built from `v" + newest.version + "`", "## What is in " + newest.version, "THIRD_PARTY_NOTICES.txt",
		"./nth-agent notices"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the notes for v%s do not say %q:\n%s", newest.version, want, notes)
		}
	}

	for _, c := range []struct {
		name, tag string
		changelog []byte
	}{
		{"a tag with no section", "v9.9.9", []byte(changelog)},
		{"a tag whose version only prefixes a heading", "v0.3.1", []byte("# Changelog\n\n## 0.3.10\n\n- ten\n")},
		{"a tag whose section is empty", "v0.3.1", []byte("# Changelog\n\n## 0.3.1\n\n## 0.3.0\n\n- zero\n")},
		{"a section that carries a token", "v0.3.1", []byte("# Changelog\n\n## 0.3.1\n\n- pin @{{VERSION}}\n")},
		{"a tag whose tree has no CHANGELOG.md", "v0.3.1", nil},
	} {
		notes, out, err := runNotesWith(t, c.tag, c.changelog)
		if err == nil {
			t.Errorf("%s: the release went ahead with these notes:\n%s", c.name, notes)
			continue
		}
		if !strings.Contains(out, "refusing") {
			t.Errorf("%s: the step failed, but not with its refusal:\n%s", c.name, out)
		}
	}

	// A heading may carry more after a space (a date, say), and the next heading ends the section.
	notes, out, err = runNotesWith(t, "v0.3.1", []byte("# Changelog\n\n## 0.3.1 (a date)\n\n- one\n\n## 0.3.0\n\n- zero\n"))
	if err != nil || !strings.Contains(notes, "- one") || strings.Contains(notes, "- zero") {
		t.Fatalf("a heading with a date after it: want the 0.3.1 section alone, got (%v):\n%s\n%s", err, notes, out)
	}
}

// sixThingsAsReleasedIn030 is the six things as 0.3.0 said them. THE 0.3.0 ENTRY IS HISTORY: it says what that release
// did, and is not rewritten. Its fifth thing was "the CA's key never leaves this machine", which a backup's copy made untrue
// (A_MACHINE_THAT_STAYS_THE_CUSTOMERS); every text this build ships is held to the corrected fifth thing
// (sixThings), and the next release's entry says the correction.
func sixThingsAsReleasedIn030() []struct {
	thing string
	marks []*regexp.Regexp
} {
	out := append(sixThings[:0:0], sixThings...)
	for i := range out {
		if strings.Contains(out[i].thing, "out of backups") {
			out[i].thing = "the CA's key never leaves this machine, and the CA lasts one year"
			out[i].marks = []*regexp.Regexp{regexp.MustCompile(`(?i)key never leaves this machine`), regexp.MustCompile(`(?i)one year`)}
		}
	}
	return out
}
