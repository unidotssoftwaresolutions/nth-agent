package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── THE SEVEN RESTART LINES, FOR EVERY WAY THE AGENT CAN BE STARTED (NTH_AGENT_0_4_3 item 2) ────────────────────────
//
// These are the lines a person copies while something is wrong: each tells them to start connect again. Each keeps its
// flags and its words; only the command's spelling follows how this agent was started. The spelling each kind must get is
// written out here, not read from internal/command, and each kind is read by command.HowStarted from where its program
// would be.

// startedAs is one way the agent can be started: where its program is, what npx left in its environment, which files
// exist, and the command a person at this machine, and one reading on another screen, must be told to type.
type startedAs struct {
	name       string
	executable string
	env        map[string]string
	files      []string
	want       command.Started
	here       string
	there      string
}

var everyWayToStart = []startedAs{
	{name: "npx", executable: "/Users/ada/.npm/_npx/0a1b2c3d/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64",
		env: map[string]string{"npm_lifecycle_event": "npx"}, want: command.StartedByNpx,
		here: "npx nthcase-agent@latest", there: "npx nthcase-agent@latest"},
	{name: "global", executable: "/opt/homebrew/lib/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64",
		want: command.StartedFromGlobal, here: "nth-agent", there: "nth-agent"},
	{name: "global run by npx", executable: "/usr/local/lib/node_modules/nthcase-agent/bin/nth-agent-darwin-amd64",
		env: map[string]string{"npm_lifecycle_event": "npx"}, want: command.StartedFromGlobalByNpx,
		here: "npx nthcase-agent@latest", there: "npx nthcase-agent@latest"},
	{name: "project", executable: "/home/ada/bench/node_modules/nthcase-agent/bin/nth-agent-linux-amd64",
		files: []string{"/home/ada/bench/package.json"}, want: command.StartedFromProject,
		here: "/home/ada/bench/node_modules/.bin/nth-agent", there: "npx nthcase-agent@latest"},
	{name: "project in a folder with a space", executable: "/Users/ada/my bench/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64",
		files: []string{"/Users/ada/my bench/package.json"}, want: command.StartedFromProject,
		here: "npx nthcase-agent@latest", there: "npx nthcase-agent@latest"}, // a path a shell would need quoted is not spelled
	{name: "unknown", executable: "/Users/ada/Downloads/nth-agent", want: command.StartedUnknown,
		here: "npx nthcase-agent@latest", there: "npx nthcase-agent@latest"},
}

// startAs makes this process read as started that way, until the test ends.
func startAs(t *testing.T, way startedAs) {
	t.Helper()
	files := map[string]bool{}
	for _, f := range way.files {
		files[filepath.FromSlash(f)] = true
	}
	install := command.HowStarted(filepath.FromSlash(way.executable), func(key string) string { return way.env[key] },
		func(path string) bool { return files[path] })
	if install.Started != way.want {
		t.Fatalf("instrument: %s reads as %v, not %v", way.executable, install.Started, way.want)
	}
	before := command.This()
	command.Use(install)
	t.Cleanup(func() { command.Use(before) })
}

func TestEveryRestartLineRunsThisReleaseHoweverTheAgentWasStarted(t *testing.T) {
	const bare = "npx nthcase-agent"
	uploaded := func() string {
		var b strings.Builder
		printUploaded(&b, browserstack.App{URL: "bs://f1e2d3c4"})
		return b.String()
	}
	lines := []struct {
		where  string
		before string // the line at v0.4.2
		there  bool   // read away from this machine
		now    func() string
	}{
		{"connect, an Appium that answered from another machine (internal/connect/loop.go)",
			"run Appium on this machine and restart with its local address: npx nthcase-agent connect --appium http://127.0.0.1:4723",
			false, func() string { return connect.AppiumNotHereRemedy(command.This()) }},
		{"connect, phones left out (internal/connect/loop.go)",
			"to offer these too, restart with: npx nthcase-agent connect … --offer-uncabled-devices",
			false, func() string { return connect.OfferUncabledRemedy(command.This()) }},
		{"a capture that refused a local address, in the capture and on the phone (internal/netcap/destination.go)",
			"refused: the capture proxy does not connect to this machine or its local network — to let a capture reach a local " +
				"backend, restart with npx nthcase-agent connect … --capture-allow-local-network",
			true, netcap.RefusedLocal},
		{"doctor, phones left out (internal/doctor/checks.go)",
			"connect it by USB cable, or to offer these too: npx nthcase-agent connect … --offer-uncabled-devices",
			false, doctor.UncabledRemedy},
		{"doctor, no Appium given (internal/doctor/checks.go)",
			"start Appium and pass its URL: npx nthcase-agent connect --appium http://127.0.0.1:4723 — this command does not guess where it might be",
			false, doctor.NoAppiumRemedy},
		{"doctor, an Appium that answered from another machine (internal/doctor/checks.go)",
			"run Appium on this machine and pass its local address: npx nthcase-agent connect --appium http://127.0.0.1:4723",
			false, doctor.AppiumNotHereRemedy},
		{"upload (cmd/nth-agent/uploadcmd.go)",
			"uploaded: bs://f1e2d3c4\n\nUse it with connect:\n  npx nthcase-agent connect --farm https://... --browserstack-app bs://f1e2d3c4\n" +
				"or export it once:\n  export NTH_AGENT_BROWSERSTACK_APP=bs://f1e2d3c4\n\nBrowserStack deletes uploaded builds after 30 days, so this id stops working then.\n",
			false, uploaded},
	}
	for _, way := range everyWayToStart {
		t.Run(way.name, func(t *testing.T) {
			startAs(t, way)
			for _, line := range lines {
				if strings.Count(line.before, bare) != 1 {
					t.Fatalf("instrument: %s does not hold the command once", line.where)
				}
				spelled := way.here
				if line.there {
					spelled = way.there
				}
				want := strings.Replace(line.before, bare, spelled, 1)
				if got := line.now(); got != want {
					t.Errorf("%s, started as %s:\n got %q\nwant %q", line.where, way.name, got, want)
				}
			}
		})
	}
}
