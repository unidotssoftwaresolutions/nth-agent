package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A RELEASE NOBODY CAN MOVE — every action a workflow runs is named by its commit, and every job holds only the
// token it needs.
//
// An action named by a tag (`actions/checkout@v4`) runs whatever commit that tag points at when the job starts, and the
// tag belongs to the action's repository, not to this one. Whoever can move it chooses code that runs here, in jobs that
// build the binaries customers run and that hold this repository's write token or npm's publish token. A commit SHA
// cannot be moved, so each `uses:` names one, with the version it stood for in a comment beside it. Moving to a newer
// version is then an edit somebody reads: a new SHA, a new comment.
//
// The permissions are the other half. A job with no `permissions:` of its own takes the workflow's, and a workflow with
// none takes the repository's default, which can be write to everything. So every job is held here to a list, and a job
// the list does not name is refused until somebody writes down what it may hold.

// jobGrants is the most each job may ask for. A job missing from this list, or a grant above its row, fails the seal.
var jobGrants = map[string]map[string]map[string]string{
	"ci.yml": {
		"build-and-test": {"contents": "read"},
		"cross-compile":  {"contents": "read"},
	},
	"release.yml": {
		"build":          {"contents": "read"},
		"github-release": {"contents": "write"}, // creates the release and uploads its assets
	},
	"publish-npm.yml": {
		// actions: read downloads the archives release.yml's run kept; id-token: write is npm's trusted publishing.
		"publish": {"contents": "read", "actions": "read", "id-token": "write"},
	},
}

// workflowDefault is the most a workflow's own top-level `permissions:` may grant: what a job without a block of its
// own would get.
var workflowDefault = map[string]string{"contents": "read"}

var (
	usesRef        = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinnedRef      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_./-]+)?@[0-9a-f]{40}$`)
	versionComment = regexp.MustCompile(`^\s+# v\d+\.\d+\.\d+$`)
)

var grantLevel = map[string]int{"none": 0, "read": 1, "write": 2}

// grantBlock reads the `key: value` lines under a `permissions:` key at indent, stopping at the first line indented no
// deeper. A scalar on the key's own line (`permissions: write-all`) comes back as the single grant "*".
func grantBlock(lines []string, at int, indent string) map[string]string {
	grants := map[string]string{}
	_, scalar, _ := strings.Cut(lines[at], "permissions:")
	scalar = strings.TrimSpace(stripComment(scalar))
	switch scalar {
	case "":
	case "{}":
		return grants
	default:
		grants["*"] = scalar
		return grants
	}
	for _, line := range lines[at+1:] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, indent+"  ") {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(stripComment(line)), ":")
		if !ok {
			grants[strings.TrimSpace(line)] = "?"
			continue
		}
		grants[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return grants
}

func stripComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return s[:i]
	}
	return s
}

// actionProblems is everything wrong with one workflow file: each `uses:` not pinned to a commit with its version beside
// it, and each job that can ask for more than allowed lets it. It reads the text it is given, so the guard below can
// hand it planted files.
func actionProblems(file, text string, allowed map[string]map[string]string) (problems []string, uses int) {
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := usesRef.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		uses++
		where := file + ":" + itoa(i+1)
		if !pinnedRef.MatchString(m[1]) {
			problems = append(problems, where+" runs "+m[1]+", which is not pinned to a full commit SHA")
		} else if !versionComment.MatchString(m[2]) {
			problems = append(problems, where+" pins "+m[1]+" with no `# vX.Y.Z` beside it saying which version that commit is")
		}
	}

	var top map[string]string
	jobs := map[string]map[string]string{}
	declared := map[string]bool{}
	inJobs := false
	job := ""
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "permissions:"):
			top = grantBlock(lines, i, "")
		case strings.HasPrefix(line, "jobs:"):
			inJobs = true
		case inJobs && regexp.MustCompile(`^  [A-Za-z0-9_-]+:\s*$`).MatchString(line):
			job = strings.TrimSuffix(strings.TrimSpace(line), ":")
			declared[job] = true
		case inJobs && job != "" && strings.HasPrefix(line, "    permissions:"):
			jobs[job] = grantBlock(lines, i, "    ")
		case len(line) > 0 && line[0] != ' ' && line[0] != '#':
			inJobs = false
		}
	}
	if len(declared) == 0 {
		problems = append(problems, file+" has no jobs this seal could read")
	}

	exceeds := func(grants, limit map[string]string) []string {
		var over []string
		for key, value := range grants {
			level, known := grantLevel[value]
			if key == "*" || !known {
				over = append(over, key+": "+value)
				continue
			}
			if level > grantLevel[limit[key]] {
				over = append(over, key+": "+value)
			}
		}
		sort.Strings(over)
		return over
	}
	if top != nil {
		if over := exceeds(top, workflowDefault); len(over) > 0 {
			problems = append(problems, file+"'s own permissions grant "+strings.Join(over, ", ")+
				" to every job without a block of its own; the most it may grant is contents: read")
		}
	}
	for name := range declared {
		limit, listed := allowed[name]
		if !listed {
			problems = append(problems, file+" has a job "+name+" that the permission list does not name")
			continue
		}
		grants, own := jobs[name]
		if !own {
			if top == nil {
				problems = append(problems, file+": job "+name+" declares no permissions and neither does the workflow, "+
					"so it gets the repository's default token")
				continue
			}
			grants = top
		}
		if over := exceeds(grants, limit); len(over) > 0 {
			problems = append(problems, file+": job "+name+" can ask for "+strings.Join(over, ", ")+", more than its list allows")
		}
	}
	for name := range allowed {
		if !declared[name] {
			problems = append(problems, file+" has no job "+name+" any more, and the permission list still names it")
		}
	}
	sort.Strings(problems)
	return problems, uses
}

func TestEveryActionIsPinnedAndEveryJobHoldsOnlyItsList(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	files, uses := 0, 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !(strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			continue
		}
		files++
		allowed, listed := jobGrants[name]
		if !listed {
			problems = append(problems, name+" is a workflow the permission list does not name")
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		found, n := actionProblems(name, string(data), allowed)
		problems = append(problems, found...)
		uses += n
	}
	for name := range jobGrants {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			problems = append(problems, "the permission list names "+name+", and there is no such workflow")
		}
	}
	if files < 3 || uses < 10 {
		t.Fatalf("instrument guard: read %d workflows and %d `uses:` lines — the seal is not reading the real files", files, uses)
	}
	if len(problems) > 0 {
		t.Fatalf("the workflows run code someone else can change, or hold more than they need:\n  %s", strings.Join(problems, "\n  "))
	}
}

// THE GUARD: the reading above, on planted files whose answers are known.
func TestThePinAndPermissionReadingFindsWhatIsPlanted(t *testing.T) {
	sha := strings.Repeat("a", 40)
	allowed := map[string]map[string]string{"build": {"contents": "read"}, "ship": {"contents": "write"}}
	clean := "permissions:\n  contents: read\n\njobs:\n  build:\n    runs-on: x\n    steps:\n" +
		"      - uses: actions/checkout@" + sha + " # v4.4.0\n" +
		"  ship:\n    permissions:\n      contents: write # to upload\n    steps:\n" +
		"      # - uses: actions/checkout@v4 is a comment and runs nothing\n" +
		"      - name: x\n        uses: softprops/action-gh-release@" + sha + " # v2.6.2\n"
	if problems, uses := actionProblems("clean.yml", clean, allowed); len(problems) != 0 || uses != 2 {
		t.Fatalf("a clean file read as %d uses and problems %v", uses, problems)
	}

	for _, c := range []struct{ name, text, want string }{
		{"a tag", strings.Replace(clean, "checkout@"+sha, "checkout@v4", 1), "not pinned to a full commit SHA"},
		{"a short SHA", strings.Replace(clean, "checkout@"+sha, "checkout@"+sha[:12], 1), "not pinned to a full commit SHA"},
		{"a branch", strings.Replace(clean, "checkout@"+sha, "checkout@main", 1), "not pinned to a full commit SHA"},
		{"a pin with no version", strings.Replace(clean, sha+" # v4.4.0", sha, 1), "no `# vX.Y.Z`"},
		{"a build job inheriting write", strings.Replace(clean, "permissions:\n  contents: read\n", "permissions:\n  contents: write\n", 1), "can ask for contents: write"},
		{"write-all", strings.Replace(clean, "permissions:\n  contents: read\n", "permissions: write-all\n", 1), "*: write-all"},
		{"an id-token where none is listed", strings.Replace(clean, "      contents: write # to upload\n", "      contents: write # to upload\n      id-token: write\n", 1), "id-token: write"},
		{"no permissions anywhere", strings.Replace(clean, "permissions:\n  contents: read\n", "", 1), "repository's default token"},
		{"a job nobody listed", clean + "  other:\n    steps: []\n", "does not name"},
	} {
		problems, _ := actionProblems("planted.yml", c.text, allowed)
		if !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%s was not refused with %q: %v", c.name, c.want, problems)
		}
	}
}
