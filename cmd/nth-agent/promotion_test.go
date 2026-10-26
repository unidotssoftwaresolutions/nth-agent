package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A RELEASE NOBODY CAN MOVE — npm gets the bytes release.yml built, and nothing else.
//
// publish-npm.yml used to take whatever the tag's release held when somebody pressed the button: each archive picked by
// its file name, its binary unpacked into the package, then two questions put to the linux binary (its version and its
// notices) that any replacement can answer. The two macOS binaries were never looked at. Anything that could write the
// release's assets chose what every `npx` user then ran.
//
// Now the archives on the release must be, byte for byte, the ones the successful run of release.yml at the tag's commit
// kept as its own artifacts, which an edit of the release does not reach. Every archive, the macOS ones included, is
// compared before anything is unpacked, run or published. This seal runs the publish's steps from the fetch up to the
// notices, in order, against a stub `gh` and a stub `git`, and swaps one archive at a time.

// stepScript is a step's `run: |` script, dedented as the runner gets it. Unlike workflowStep it allows an `env:` block
// between the step's name and its script.
func stepScript(t *testing.T, workflow, name string) string {
	t.Helper()
	lines := strings.Split(workflowText(t, workflow), "\n")
	start := -1
	for i, line := range lines {
		if line == "      - name: "+name {
			start = i
		}
	}
	if start < 0 {
		t.Fatalf("%s has no step %q", workflow, name)
	}
	i := start + 1
	for i < len(lines) && lines[i] != "        run: |" && strings.HasPrefix(lines[i], "        ") {
		i++
	}
	if i >= len(lines) || lines[i] != "        run: |" {
		t.Fatalf("%s's step %q has no `run: |` block", workflow, name)
	}
	var script []string
	for _, line := range lines[i+1:] {
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

// stepsFrom is the name of every step from first up to, not including, before, in the order the job runs them.
func stepsFrom(t *testing.T, workflow, first, before string) []string {
	t.Helper()
	var names []string
	inside := false
	for _, line := range strings.Split(workflowText(t, workflow), "\n") {
		name, ok := strings.CutPrefix(line, "      - name: ")
		if !ok {
			continue
		}
		if name == first {
			inside = true
		}
		if name == before {
			break
		}
		if inside {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Fatalf("instrument guard: no steps between %q and %q in %s", first, before, workflow)
	}
	return names
}

func agentArchive(t *testing.T, binary string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body string
		mode int64
	}{{"nth-agent", binary, 0o755}, {"README.md", "readme\n", 0o644}, {"THIRD_PARTY_NOTICES.txt", "notices\n", 0o644},
		{"LICENSE", "licence\n", 0o644}, {"NOTICE", "notice\n", 0o644}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const stubGh = `#!/bin/sh
echo "$*" >> "$GH_LOG"
dir=""; prev=""
for a in "$@"; do
  if [ "$prev" = "--dir" ]; then dir="$a"; fi
  prev="$a"
done
case "$1 $2" in
  "release download")
    [ "$3" = "$WANT_TAG" ] || { echo "release not found" >&2; exit 1; }
    mkdir -p "$dir"
    for f in "$FIXTURE_RELEASE"/*.tar.gz "$FIXTURE_RELEASE"/THIRD_PARTY_NOTICES.txt; do
      [ -f "$f" ] && cp "$f" "$dir"/
    done
    exit 0 ;;
  "run download")
    [ "$3" = "4242" ] && [ -z "$ARTIFACTS_GONE" ] || { echo "no valid artifacts found to download" >&2; exit 1; }
    mkdir -p "$dir"
    cp -R "$FIXTURE_BUILT"/. "$dir"/
    exit 0 ;;
esac
if [ "$1" = "api" ]; then
  case "$2" in
    *"/actions/workflows/release.yml/runs?"*"head_sha=$WANT_COMMIT"*) ;;
    *) exit 0 ;;
  esac
  case "$*" in
    *"\"$WANT_TAG\""*) [ -z "$NO_RUN" ] && echo 4242 ;;
  esac
  exit 0
fi
echo "stub gh: unexpected $*" >&2
exit 3
`

func TestThePublishShipsOnlyTheArchivesReleaseYmlBuilt(t *testing.T) {
	needTools(t, "bash", "tar", "cmp", "find")
	const tag, commit, repo = "v0.3.0", "0123456789abcdef0123456789abcdef01234567", "fixture/nth-agent"
	targets := []string{"darwin_arm64", "darwin_amd64", "linux_amd64"}
	names := stepsFrom(t, "publish-npm.yml", "Fetch the release built by release.yml", "Put the notices and the changelog in the package")
	var scripts []string
	for _, name := range names {
		scripts = append(scripts, expand(t, stepScript(t, "publish-npm.yml", name), map[string]string{
			"inputs.tag": tag, "github.repository": repo,
		}))
	}

	type fixture struct {
		swap, extra, missing string // the target whose archive the release swaps, carries beyond the build, or lacks
		noRun, artifactsGone bool
	}
	publish := func(f fixture) (out string, err error, bins map[string][]byte, ghLog string) {
		work, release, built, stub := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		for _, target := range targets {
			archive := "nth-agent_" + tag + "_" + target + ".tar.gz"
			genuine := agentArchive(t, "built by release.yml for "+target+"\n")
			artifact := filepath.Join(built, strings.ReplaceAll(target, "_", "-"))
			writeFile(t, filepath.Join(artifact, archive), genuine, 0o644)
			writeFile(t, filepath.Join(artifact, archive+".sha256"), []byte("whatever  "+archive+"\n"), 0o644)
			switch target {
			case f.missing:
			case f.swap:
				writeFile(t, filepath.Join(release, archive), agentArchive(t, "NOT built by release.yml\n"), 0o644)
			default:
				writeFile(t, filepath.Join(release, archive), genuine, 0o644)
			}
		}
		if f.extra != "" {
			writeFile(t, filepath.Join(release, "nth-agent_"+tag+"_"+f.extra+".tar.gz"), agentArchive(t, "extra\n"), 0o644)
		}
		writeFile(t, filepath.Join(release, "THIRD_PARTY_NOTICES.txt"), []byte("notices\n"), 0o644)
		writeFile(t, filepath.Join(stub, "gh"), []byte(stubGh), 0o755)
		writeFile(t, filepath.Join(stub, "git"), []byte("#!/bin/sh\n[ \"$*\" = \"rev-parse HEAD\" ] && echo "+commit+" && exit 0\nexit 3\n"), 0o755)
		writeFile(t, filepath.Join(work, "publish", "npm", "package.json"), []byte(`{"name":"x","version":"0.0.0"}`), 0o644)

		logFile := filepath.Join(t.TempDir(), "gh.log")
		env := []string{"PATH=" + stub + string(os.PathListSeparator) + os.Getenv("PATH"), "GH_LOG=" + logFile,
			"FIXTURE_RELEASE=" + release, "FIXTURE_BUILT=" + built, "WANT_TAG=" + tag, "WANT_COMMIT=" + commit, "GH_TOKEN=DUMMY"}
		if f.noRun {
			env = append(env, "NO_RUN=1")
		}
		if f.artifactsGone {
			env = append(env, "ARTIFACTS_GONE=1")
		}
		var all strings.Builder
		for i, script := range scripts {
			out, err = runStep(t, script, work, env...)
			all.WriteString("── " + names[i] + "\n" + out)
			if err != nil {
				break
			}
		}
		bins = map[string][]byte{}
		entries, _ := os.ReadDir(filepath.Join(work, "publish", "npm", "bin"))
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(work, "publish", "npm", "bin", e.Name()))
			bins[e.Name()] = data
		}
		logged, _ := os.ReadFile(logFile)
		return all.String(), err, bins, string(logged)
	}

	out, err, bins, _ := publish(fixture{})
	if err != nil {
		t.Fatalf("the publish refused a release whose archives are the ones release.yml built: %v\n%s", err, out)
	}
	for _, target := range targets {
		bin := "nth-agent-" + strings.ReplaceAll(target, "_", "-")
		if string(bins[bin]) != "built by release.yml for "+target+"\n" {
			t.Fatalf("the package's %s is not the binary release.yml built (%q):\n%s", bin, bins[bin], out)
		}
	}

	refused := func(what string, f fixture) {
		t.Helper()
		out, err, bins, ghLog := publish(f)
		if err == nil || !strings.Contains(out, "refusing") {
			t.Errorf("%s was not refused (%v):\n%s\ngh was asked:\n%s", what, err, out, ghLog)
			return
		}
		if len(bins) != 0 {
			t.Errorf("%s was refused only after %d binaries were unpacked into the package — the check must come first:\n%s", what, len(bins), out)
		}
	}
	for _, target := range targets {
		refused("a release whose "+target+" archive is not the one release.yml built", fixture{swap: target})
	}
	refused("a release with an archive release.yml never built", fixture{extra: "linux_arm64"})
	refused("a release missing an archive release.yml built", fixture{missing: "darwin_amd64"})
	refused("a tag whose commit no successful release.yml run built", fixture{noRun: true})
	refused("a run whose artifacts can no longer be downloaded", fixture{artifactsGone: true})

	// The binding is to release.yml at the tag's own commit, and the job may read that run's artifacts.
	workflow := workflowText(t, "publish-npm.yml")
	if !regexp.MustCompile(`actions/workflows/release\.yml/runs\?[^"]*head_sha=\$COMMIT`).MatchString(workflow) {
		t.Error("the publish does not look for the run of release.yml that built the tag's commit")
	}
	if !strings.Contains(jobPermissions(t, workflow), "actions: read") {
		t.Error("the publish job cannot read the release run's artifacts without `actions: read`")
	}
}
