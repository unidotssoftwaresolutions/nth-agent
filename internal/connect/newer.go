package connect

import (
	"strconv"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// ── A NEWER AGENT, SAID ONCE (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 1) ─────────────────────────────────────────────
//
// The farm names the newest agent release in its answer to a registration and to every heartbeat, when this agent is older
// (farm.Registration / farm.HeartbeatAnswer). The agent says so once, and again only when the farm names a different
// release. Nothing else changes: an older agent keeps working.
//
// HOW TO UPDATE DEPENDS ON HOW IT WAS STARTED. internal/command reads that, and spells every command the agent prints
// from it (NTH_AGENT_0_4_3); the loop and its tests name it as this package always did.

// Install is how this agent was started (command.HowStarted).
type Install = command.Install

// Started is how this agent was started, as far as it can tell.
type Started = command.Started

const (
	StartedUnknown         = command.StartedUnknown
	StartedByNpx           = command.StartedByNpx
	StartedFromGlobal      = command.StartedFromGlobal
	StartedFromGlobalByNpx = command.StartedFromGlobalByNpx
	StartedFromProject     = command.StartedFromProject
)

// HowStarted is command.HowStarted.
func HowStarted(executable string, getenv func(string) string, isFile func(string) bool) Install {
	return command.HowStarted(executable, getenv, isFile)
}

// newerRelease says, once per release the farm names, that a newer agent is out and how to install it. It says nothing
// when the farm named none, or named one this agent cannot read as newer than its own: a build of its own (`dev`) or an
// agent the farm's setting is behind.
func (n *narrator) newerRelease(own, newer string, install Install) {
	if newer == "" || !olderThan(own, newer) {
		return
	}
	newer = "v" + strings.TrimPrefix(newer, "v")
	if newer == n.newerSaid {
		return
	}
	n.newerSaid = newer
	n.say("this agent is %s, and %s is out. It keeps working as it is.", own, newer)
	switch install.Started {
	case StartedByNpx:
		n.say("  to update, stop it (Ctrl-C) and start it with the flags you use now: %s", install.Line("connect …"))
	case StartedFromGlobal:
		n.say("  to update, stop it (Ctrl-C), run npm install -g nthcase-agent@latest, and start it the way you do now")
	case StartedFromGlobalByNpx:
		n.say("  this is the copy installed on this machine with npm install -g, which is older. Started through npx, an installed copy is used before a newer one is fetched.")
		n.say("  to update, stop it (Ctrl-C), run npm install -g nthcase-agent@latest, and start it the way you do now")
	case StartedFromProject:
		n.say("  to update, stop it (Ctrl-C), run npm install nthcase-agent@latest in %s, and start it the way you do now", install.Project)
	default:
		n.say("  to update, stop it (Ctrl-C) and start it with the flags you use now: %s", install.Line("connect …"))
		n.say("  or, if it was installed with npm install -g, run npm install -g nthcase-agent@latest and start it the way you do now")
	}
}

// olderThan answers whether own orders below newer, both read as major.minor.patch with at most one leading `v`. A version
// that does not read that way is never older: the agent then makes no claim it cannot check.
func olderThan(own, newer string) bool {
	a, okA := releaseNumbers(own)
	b, okB := releaseNumbers(newer)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func releaseNumbers(version string) ([3]int, bool) {
	var numbers [3]int
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return numbers, false
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || part == "" || part[0] == '+' || part[0] == '-' {
			return numbers, false
		}
		numbers[i] = value
	}
	return numbers, true
}
