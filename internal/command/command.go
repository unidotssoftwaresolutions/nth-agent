// Package command spells every command the agent tells a person to type (NTH_AGENT_0_4_3).
//
// On 2026-10-24 the operator ran `npx nthcase-agent connect …` with no version while 0.4.2 was the latest release, and npx
// ran 0.4.0, a copy it already had. The agent's own advice taught that same bare form, so a person who copied it while
// something was wrong could start an old copy and never know. A command the agent prints now runs this release or a
// newer one, never an older copy, and in the way this agent was started:
//
//   - started through npx (from npx's cache, or a global install npx ran instead of fetching): `npx nthcase-agent@latest <verb>`;
//   - a global install run by its own name: `nth-agent <verb>`, which runs that install;
//   - a project's install: that project's own link to it, `<project>/node_modules/.bin/nth-agent <verb>`;
//   - anything else, or a project whose path a shell would need quoted: the npx form, with @latest.
//
// A SENTENCE READ AWAY FROM THIS MACHINE (relayed to the workspace, to a run, to a phone) never carries a path of this
// machine, so it says a project's install in the npx form (OnThatMachine). It is still this release or a newer one.
//
// main reads how the agent was started once, before it prints anything (Use), so every command reads it here and none
// runs npm to find out.
package command

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// Package is the npm package, and Program the command it installs (publish/npm/package.json declares both).
const (
	Package = "nthcase-agent"
	Program = "nth-agent"
)

// npxLatest asks the registry for the newest release each time: with `@latest`, npx does not run a copy it already has.
// Written whole, so a reader of the program's strings sees the command it prints (command_test.go holds it to Package).
const npxLatest = "npx nthcase-agent@latest"

// ── HOW THIS AGENT WAS STARTED (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 1, moved here from internal/connect/newer.go) ─────
//
// The agent can tell that only from where its own program is (the npm package puts it in <package>/bin/, beside the script
// npx or npm runs) and from what npx leaves in its environment. It runs no npm to find out. Read from npm 10.9.2's own
// source (libnpmexec, @npmcli/run-script):
//
//   - `npx <package>` with no version runs an installed copy when there is one — the project's, or a global install's —
//     before it looks in its own cache, and it fetches the newest into that cache only when there is none. That is how a
//     global install hides every newer release from npx, which was the operator's case on 2026-10-20.
//   - npx starts the command with npm_lifecycle_event=npx in its environment, and the npm shim passes the environment on.
//   - npx's cache is a folder named _npx; a global install is <prefix>/lib/node_modules; a project's is <project>/node_modules
//     beside the project's package.json.

// Started is how this agent was started, as far as it can tell.
type Started int

const (
	// StartedUnknown: a program somewhere else (downloaded, or built), or a place the agent cannot read.
	StartedUnknown Started = iota
	// StartedByNpx: from npx's own cache.
	StartedByNpx
	// StartedFromGlobal: a copy installed with `npm install -g`, run by its own name.
	StartedFromGlobal
	// StartedFromGlobalByNpx: a copy installed with `npm install -g`, which npx ran instead of fetching one.
	StartedFromGlobalByNpx
	// StartedFromProject: a copy installed in a project's node_modules.
	StartedFromProject
)

// Install is how this agent was started, and the project it was installed in when it was installed in one.
type Install struct {
	Started Started
	Project string
}

// HowStarted reads how this agent was started from where its program is (os.Executable, links resolved), the environment
// and whether a file exists. Anything it cannot place is StartedUnknown.
func HowStarted(executable string, getenv func(string) string, isFile func(string) bool) Install {
	parts := strings.Split(filepath.ToSlash(executable), "/")
	n := len(parts)
	// <node_modules>/<package>/bin/<program>
	if n < 5 || parts[n-2] != "bin" || parts[n-4] != "node_modules" {
		return Install{}
	}
	for _, part := range parts[:n-4] {
		if part == "_npx" {
			return Install{Started: StartedByNpx}
		}
	}
	owner := filepath.FromSlash(strings.Join(parts[:n-4], "/"))
	switch {
	case isFile(filepath.Join(owner, "package.json")):
		return Install{Started: StartedFromProject, Project: owner}
	case filepath.Base(owner) == "lib" && getenv("npm_lifecycle_event") == "npx":
		return Install{Started: StartedFromGlobalByNpx}
	case filepath.Base(owner) == "lib":
		return Install{Started: StartedFromGlobal}
	}
	return Install{}
}

// ── THE ONE PLACE A COMMAND IS SPELLED ─────────────────────────────────────────────────────────────────────────

// Line is args as a person at this machine types them for this install: Line("doctor") is `npx nthcase-agent@latest
// doctor` for an agent started through npx. Line("") is the command alone.
func (i Install) Line(args string) string {
	return joined(i.program(true), args)
}

// OnThatMachine is Line for a sentence read away from this machine: the same, but never a path of this machine.
func (i Install) OnThatMachine(args string) string {
	return joined(i.program(false), args)
}

func (i Install) program(here bool) string {
	switch i.Started {
	case StartedFromGlobal:
		return Program
	case StartedFromProject:
		if link, ok := projectLink(i.Project); ok && here {
			return link
		}
	}
	return npxLatest
}

// projectLink is the project's own link to its copy. Only a path a shell reads as it is, and a terminal shows as it is, is
// spelled: a path that would need quoting (a space, a quote, a `$`, a `%`, a control character) could not sit inside the
// sentences that quote a command, and the npx form is used instead.
func projectLink(project string) (string, bool) {
	if !filepath.IsAbs(project) || !utf8.ValidString(project) {
		return "", false
	}
	link := filepath.Join(project, "node_modules", ".bin", Program)
	for _, r := range link {
		if r >= utf8.RuneSelf || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/._-+@,:=", r)) {
			return "", false
		}
	}
	return link, true
}

func joined(program, args string) string {
	if args == "" {
		return program
	}
	return program + " " + args
}

// ── THIS PROCESS ───────────────────────────────────────────────────────────────────────────────────────────────

var current atomic.Pointer[Install]

// Use sets how this process was started. main calls it once, before it prints anything; until then, and in a program
// that never calls it, the agent is StartedUnknown and every command is the npx form with @latest.
func Use(install Install) {
	current.Store(&install)
}

// This is how this process was started (Use).
func This() Install {
	if install := current.Load(); install != nil {
		return *install
	}
	return Install{}
}

// Line is args spelled for this process: This().Line(args).
func Line(args string) string {
	return This().Line(args)
}

// OnThatMachine is args spelled for this process, for a sentence read away from this machine: This().OnThatMachine(args).
func OnThatMachine(args string) string {
	return This().OnThatMachine(args)
}
