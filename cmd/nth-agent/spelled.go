package main

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// ── EVERY COMMAND THIS PROGRAM PRINTS IS SPELLED FOR HOW IT WAS STARTED (NTH_AGENT_0_4_3) ────────────────────────────

// HOW THIS AGENT WAS STARTED is read once, before anything is printed — a mistake on the command line prints the usage —
// and every command the agent prints is spelled from it (internal/command, NTH_AGENT_0_4_3).
func init() {
	command.Use(command.HowStarted(ownProgram(), os.Getenv, isFile))
}

// usage is the usage text with each command spelled for this agent (command.Line). {{PAD}} is as wide as the command, so a
// line under a table row stays under its column.
func usage() string {
	program := command.Line("")
	return strings.NewReplacer("{{RUN}}", program, "{{PAD}}", strings.Repeat(" ", utf8.RuneCountInString(program))).Replace(usageTemplate)
}

// noticesLine is --version's second line: where the third-party notices are. THE FIRST LINE STAYS `nth-agent <version>` AND
// NOTHING ELSE — publish-npm.yml compares that line's last word with the package's version, and reads no other line.
func noticesLine() string {
	return "third-party notices: " + command.Line("notices") + " (also THIRD_PARTY_NOTICES.txt, shipped with this program)"
}

// versionAnswer is what --version prints for a build stamped v.
func versionAnswer(v string) string { return "nth-agent " + v + "\n" + noticesLine() + "\n" }
