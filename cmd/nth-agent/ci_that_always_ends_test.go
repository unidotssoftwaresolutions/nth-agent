package main

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// CI THAT ALWAYS ENDS — every job ends on its own, a hang is named by the step it is in, and no script is written with
// text a person typed.
//
// A job with no `timeout-minutes` runs until GitHub's default of six hours. On 2026-10-30 a CI run in a sibling repository
// printed its results and then sat 22 minutes in a step that never returned, and no job here had a timeout either. So every
// job says how long it may take, at most longestJob, and the build and test steps of ci.yml say it again for themselves, so
// the log names the step that stopped.
//
// The runner pastes `${{ inputs.tag }}` into a script's TEXT before bash reads it, so a dispatch input of `x"; curl …; "`
// would run — in publish-npm.yml, beside `id-token: write`. Each input reaches a script through `env:` and is quoted there.
//
// This repository runs no dotnet, so the sibling seals' build-server row has nothing to read here.

// longestJob is the longest any job here may run; nothing here comes near it, and it still ends a hang four hours early.
const longestJob = 120

// untrustedInScript is a value somebody outside the workflow chooses: a dispatch input, a ref or branch name, the event.
var untrustedInScript = regexp.MustCompile(`\$\{\{[^}]*\b(?:inputs\.|github\.(?:ref_name|head_ref|event\b))[^}]*\}\}`)

// buildsOrTests is a ci.yml step that compiles or tests: each carries a timeout of its own. ci.yml's scripts are one command
// each, so a `go build` anywhere in one is that step's command (the cross-compile's comes after `GOOS=${{ matrix.goos }}`).
var buildsOrTests = regexp.MustCompile(`(?:^|\s)go\s+(?:build|vet|test)\s`)

// jobKey is a job's id line, at two spaces under `jobs:`.
var jobKey = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)

type workflowJob struct {
	file, id string
	timeout  string // the job's own `timeout-minutes`, "" when it has none
	steps    []workflowStepText
}

type workflowStepText struct {
	name, timeout, script string
}

// readJobs reads each job of a workflow: its id, its own timeout, and each step's name, timeout and `run:` script. The
// workflows here keep GitHub's layout — jobs at two spaces, a job's keys at four, steps at six, a step's keys at eight — and a
// file that does not is refused by the instrument guards below rather than read wrong.
func readJobs(file, text string) []workflowJob {
	var jobs []workflowJob
	lines := strings.Split(text, "\n")
	inJobs := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if m := jobKey.FindStringSubmatch(line); m != nil {
			jobs = append(jobs, workflowJob{file: file, id: m[1]})
			continue
		}
		if len(jobs) == 0 {
			continue
		}
		job := &jobs[len(jobs)-1]
		if v, ok := strings.CutPrefix(line, "    timeout-minutes:"); ok {
			job.timeout = strings.TrimSpace(stripComment(v))
			continue
		}
		if strings.HasPrefix(line, "      - ") {
			job.steps = append(job.steps, workflowStepText{})
			line = "        " + line[len("      - "):] // the step's first key, at a key's indent
		}
		if len(job.steps) == 0 || !strings.HasPrefix(line, "        ") || strings.HasPrefix(line, "         ") {
			continue
		}
		step := &job.steps[len(job.steps)-1]
		key, value, _ := strings.Cut(strings.TrimPrefix(line, "        "), ":")
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			step.name = value
		case "timeout-minutes":
			step.timeout = strings.TrimSpace(stripComment(value))
		case "run":
			if value != "|" && value != ">" {
				step.script = value
				continue
			}
			var block []string
			for i+1 < len(lines) && (strings.TrimSpace(lines[i+1]) == "" || strings.HasPrefix(lines[i+1], "          ")) {
				i++
				block = append(block, strings.TrimPrefix(lines[i], "          "))
			}
			step.script = strings.Join(block, "\n")
		}
	}
	return jobs
}

func timeoutProblem(where, raw string, ceiling int) string {
	if raw == "" {
		return where + " has no timeout-minutes"
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil {
		return where + ": timeout-minutes " + strconv.Quote(raw) + " is not a number of minutes"
	}
	if minutes < 1 || minutes > ceiling {
		return where + ": timeout-minutes " + raw + " is not between 1 and " + strconv.Itoa(ceiling)
	}
	return ""
}

func jobTimeoutProblems(jobs []workflowJob) []string {
	var problems []string
	for _, job := range jobs {
		if p := timeoutProblem(job.file+" job '"+job.id+"'", job.timeout, longestJob); p != "" {
			problems = append(problems, p)
		}
	}
	return problems
}

func stepTimeoutProblems(jobs []workflowJob) (problems []string, count int) {
	for _, job := range jobs {
		jobMinutes, _ := strconv.Atoi(job.timeout)
		for _, step := range job.steps {
			if !buildsOrTests.MatchString(step.script) {
				continue
			}
			count++
			where := job.file + " job '" + job.id + "' step '" + stepLabel(step.name, step.script) + "'"
			ceiling := longestJob
			if jobMinutes > 1 {
				ceiling = jobMinutes - 1 // a step's own timeout is shorter than its job's, or the job's would always speak first
			}
			if p := timeoutProblem(where, step.timeout, ceiling); p != "" {
				problems = append(problems, p)
			}
		}
	}
	return problems, count
}

func scriptProblems(jobs []workflowJob) []string {
	var problems []string
	for _, job := range jobs {
		for _, step := range job.steps {
			for _, expr := range untrustedInScript.FindAllString(step.script, -1) {
				problems = append(problems, job.file+" job '"+job.id+"' step '"+stepLabel(step.name, step.script)+"' puts "+expr+" into its script")
			}
		}
	}
	return problems
}

func stepLabel(a, b string) string {
	if a != "" {
		return a
	}
	first, _, _ := strings.Cut(b, "\n")
	return first
}

func allWorkflowJobs(t *testing.T) []workflowJob {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("instrument guard: no workflows found (%v)", err)
	}
	sort.Strings(files)
	var jobs []workflowJob
	for _, f := range files {
		jobs = append(jobs, readJobs(filepath.Base(f), workflowText(t, filepath.Base(f)))...)
	}
	return jobs
}

func TestEveryJobInEveryWorkflowEndsOnItsOwn(t *testing.T) {
	jobs := allWorkflowJobs(t)
	if len(jobs) < 5 {
		t.Fatalf("instrument guard: %d jobs read from the three workflows, expected 5", len(jobs))
	}
	if problems := jobTimeoutProblems(jobs); len(problems) > 0 {
		t.Errorf("%d job(s) may run until GitHub's 360-minute default; each says how long it may take, at most %d:\n%s",
			len(problems), longestJob, strings.Join(problems, "\n"))
	}
}

func TestEveryBuildAndTestStepOfCiYmlEndsOnItsOwn(t *testing.T) {
	problems, count := stepTimeoutProblems(readJobs("ci.yml", workflowText(t, "ci.yml")))
	if count < 4 {
		t.Fatalf("instrument guard: %d build or test steps read from ci.yml, expected go build, go vet, go test and the cross-compile", count)
	}
	if len(problems) > 0 {
		t.Errorf("%d build or test step(s) of ci.yml with no timeout of their own; with one, the log names the step that stopped:\n%s",
			len(problems), strings.Join(problems, "\n"))
	}
}

func TestNoScriptHoldsAnInputARefNameOrTheEvent(t *testing.T) {
	jobs := allWorkflowJobs(t)
	scripts := 0
	for _, job := range jobs {
		for _, step := range job.steps {
			if step.script != "" {
				scripts++
			}
		}
	}
	if scripts < 15 {
		t.Fatalf("instrument guard: %d scripts read from the workflows", scripts)
	}
	if problems := scriptProblems(jobs); len(problems) > 0 {
		t.Errorf("%d expression(s) a person or an event chooses are pasted into a script's text before bash reads it; each reaches "+
			"the script through env: and is quoted there:\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

func TestTheEndingReadingRefusesWhatIsPlanted(t *testing.T) {
	const clean = `name: x
jobs:
  build-and-test:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    env:
      TAG: ${{ inputs.tag }}
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
      - run: go build ./...
        timeout-minutes: 10
      - name: Test
        timeout-minutes: 20
        run: |
          set -euo pipefail
          go test ./... -race
          echo "$TAG"
`
	read := func(text string) []string {
		jobs := readJobs("ci.yml", text)
		steps, _ := stepTimeoutProblems(jobs)
		return append(append(jobTimeoutProblems(jobs), steps...), scriptProblems(jobs)...)
	}
	if got := read(clean); len(got) > 0 {
		t.Fatalf("the clean text was refused — an input in a job's env: and a quoted $TAG are fine:\n%s", strings.Join(got, "\n"))
	}
	var missed []string
	for _, plant := range []struct{ why, text string }{
		{"a job with no timeout", strings.Replace(clean, "    timeout-minutes: 30\n", "", 1)},
		{"a job timeout longer than the longest", strings.Replace(clean, "timeout-minutes: 30", "timeout-minutes: 360", 1)},
		{"a job timeout that is not a number", strings.Replace(clean, "timeout-minutes: 30", "timeout-minutes: ${{ inputs.minutes }}", 1)},
		{"a build step with no timeout of its own", strings.Replace(clean, "        timeout-minutes: 10\n", "", 1)},
		{"a test step whose timeout is not shorter than the job's", strings.Replace(clean, "timeout-minutes: 20", "timeout-minutes: 30", 1)},
		{"an input pasted into a script", strings.Replace(clean, `echo "$TAG"`, `echo "${{ inputs.tag }}"`, 1)},
		{"an input pasted into a one-line script", strings.Replace(clean, "run: go build ./...", "run: go build -ldflags=-X=main.v=${{ inputs.tag }} ./...", 1)},
		{"the ref name pasted into a script", strings.Replace(clean, `echo "$TAG"`, `echo "${{ github.ref_name }}"`, 1)},
	} {
		if len(read(plant.text)) == 0 {
			missed = append(missed, plant.why)
		}
	}
	if len(missed) > 0 {
		t.Errorf("%d plant(s) the reading let through:\n%s", len(missed), strings.Join(missed, "\n"))
	}
}

// jobEnv is the environment a workflow's first job gives every step — its `env:` block at four spaces — with each expression
// filled the way the runner would for one case, as `expand` fills a script. A seal that runs a step's script hands it these,
// because a value that reaches the script through `env:` is no longer in the script's text.
func jobEnv(t *testing.T, workflow string, values map[string]string) []string {
	t.Helper()
	var env []string
	lines := strings.Split(workflowText(t, workflow), "\n")
	for i, line := range lines {
		if line != "    env:" {
			continue
		}
		for _, entry := range lines[i+1:] {
			if !strings.HasPrefix(entry, "      ") || strings.HasPrefix(strings.TrimSpace(entry), "#") {
				break
			}
			key, value, _ := strings.Cut(strings.TrimSpace(entry), ":")
			env = append(env, key+"="+expand(t, strings.TrimSpace(stripComment(value)), values))
		}
		break
	}
	if len(env) == 0 {
		t.Fatalf("instrument guard: %s's job has no `env:` block for this seal to read", workflow)
	}
	return env
}
