package nthagent

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// A HISTORY ANYONE CAN READ. A published repository publishes its commits too: each message, each author and committer,
// each tag's message. The tree's census reads files and skips .git, so it never saw them, and six public commits carried a
// link to a private session in their messages. This census reads them with the tree's own rules — no pointer a reader
// cannot follow (pointerRules), and no value of the forbidden list — over the range a push or a pull request adds, which
// CI names in NTH_AGENT_COMMIT_RANGE (.github/workflows/commit-messages.yml). Without a range it skips and says so.
//
// A commit message carries no session trailer at all: CONTRIBUTING.md says so, and this refuses one.

// commitRange is one revision a range is written in: a commit id, a ref, or two joined by "..". Never an option.
var commitRange = regexp.MustCompile(`^[A-Za-z0-9_./^~]+(?:\.\.[A-Za-z0-9_./^~]+)?$`)

// published is one thing the history publishes, with where it is said.
type published struct {
	where, text string
}

// historyIn reads, with git, every commit message, author and committer in the range spec names, and every tag's message
// and tagger.
func historyIn(t *testing.T, spec string) []published {
	t.Helper()
	revisions := strings.Fields(spec)
	for _, r := range revisions {
		if !commitRange.MatchString(r) || strings.HasPrefix(r, "-") {
			t.Fatalf("NTH_AGENT_COMMIT_RANGE holds %q, which is not a commit, a ref or a range of them", r)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"--no-optional-locks"}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
		}
		return string(out)
	}
	var out []published
	log := git(append([]string{"log", "--format=%h%x1f%an <%ae>%x1f%cn <%ce>%x1f%B%x1e"}, revisions...)...)
	for _, record := range strings.Split(log, "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(record, "\n"), "\x1f", 4)
		if len(fields) != 4 {
			continue
		}
		out = append(out, published{"commit " + fields[0] + " (author)", fields[1]}, published{"commit " + fields[0] + " (committer)", fields[2]},
			published{"commit " + fields[0] + " (message)", fields[3]})
	}
	tags := git("for-each-ref", "refs/tags", "--format=%(refname:short)%1f%(taggername) %(taggeremail)%1f%(contents)%1e")
	for _, record := range strings.Split(tags, "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(record, "\n"), "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		out = append(out, published{"tag " + fields[0] + " (tagger)", fields[1]}, published{"tag " + fields[0] + " (message)", fields[2]})
	}
	return out
}

func TestNoCommitMessageCarriesAPointerOrAValue(t *testing.T) {
	spec := strings.TrimSpace(os.Getenv("NTH_AGENT_COMMIT_RANGE"))
	if spec == "" {
		t.Skip("NO RANGE OF COMMITS WAS GIVEN (NTH_AGENT_COMMIT_RANGE), so THIS RUN DID NOT READ ANY COMMIT MESSAGE. CI gives the " +
			"range a push or a pull request adds; to read this clone's whole history, give its last commit: NTH_AGENT_COMMIT_RANGE=HEAD")
	}
	history := historyIn(t, spec)
	commits := 0
	for _, p := range history {
		if strings.HasSuffix(p.where, "(message)") && strings.HasPrefix(p.where, "commit ") {
			commits++
		}
	}
	if commits == 0 {
		t.Fatalf("instrument guard: %q names no commit — a census that read nothing reads green", spec)
	}
	var hits []string
	for _, p := range history {
		hits = append(hits, findPointers(p.where, p.text)...)
		if at := strings.Index(strings.ToLower(p.text), "claude-session:"); at >= 0 {
			hits = append(hits, p.where+":"+strconv.Itoa(lineOf(p.text, at))+" — a session trailer, which no commit message here carries")
		}
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Errorf("%d pointer(s) in what the history publishes (%d commits read) — a commit's message is as public as a file, "+
			"and carries no session trailer:\n  %s", len(hits), commits, strings.Join(hits, "\n  "))
	}
	t.Run("no value of the forbidden list", func(t *testing.T) {
		list := farmtest.TheForbiddenList(t)
		var hits []string
		for _, p := range history {
			hits = append(hits, list.In(p.where, p.text)...)
			hits = append(hits, list.SumsIn(p.where, p.text)...)
		}
		if len(hits) > 0 {
			sort.Strings(hits)
			t.Fatalf("%d value(s) of the forbidden list in what the history publishes:\n  %s", len(hits), strings.Join(hits, "\n  "))
		}
	})
}

// CONTRIBUTING.md says it, so a person who writes a commit here reads it before CI refuses it.
func TestContributingSaysACommitMessageCarriesNoSessionTrailer(t *testing.T) {
	data, err := os.ReadFile("CONTRIBUTING.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{"commit message", "no session trailer", "read by CI with the same rules"} {
		if !strings.Contains(text, want) {
			t.Errorf("CONTRIBUTING.md does not say %q", want)
		}
	}
}
