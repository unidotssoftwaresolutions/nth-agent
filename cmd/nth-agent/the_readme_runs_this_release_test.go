package main

import (
	"regexp"
	"strings"
	"testing"
)

// ── THE README TEACHES COMMANDS THAT RUN THE NEWEST RELEASE (NTH_AGENT_0_4_3 item 4) ──────────────────────────────
//
// At v0.4.2 the README taught 29 lines of `npx {{PACKAGE}} <verb>` with no version, and said twice that npx fetches the
// latest published version by default. It does not: with no version npx runs a copy it already has, however old it is
// (internal/command, read from npm's own source), which is what the operator met on 2026-10-24. Every command the README
// teaches carries @latest now, or @{{VERSION}} where the line is about pinning, and the page says what npx does with each.

// npxOfThePackage is every `npx {{PACKAGE}}` in the README, with what follows the name (a version, or nothing). The name may
// break across a line, as prose wraps it.
var npxOfThePackage = regexp.MustCompile(`npx\s+\{\{PACKAGE\}\}(@\S*)?`)

func TestEveryCommandTheReadmeTeachesNamesItsVersion(t *testing.T) {
	readme := readmeText(t)
	commands := npxOfThePackage.FindAllStringSubmatch(readme, -1)
	if len(commands) < 29 {
		t.Fatalf("instrument guard: read %d npx commands in the README — it taught 29 at v0.4.2", len(commands))
	}
	var bare []string
	pinned := 0
	for _, m := range commands {
		switch m[1] {
		case "@latest":
		case "@{{VERSION}}":
			pinned++
		default:
			bare = append(bare, strings.ReplaceAll(m[0], "\n", " "))
		}
	}
	if len(bare) > 0 {
		t.Fatalf("the README teaches %d command(s) with no version, and npx then runs whatever copy it already has, however "+
			"old: write `npx {{PACKAGE}}@latest`, or `@{{VERSION}}` where the line is about pinning:\n  %s",
			len(bare), strings.Join(bare, "\n  "))
	}
	if pinned == 0 {
		t.Fatal("the README no longer shows the pinned form, `npx {{PACKAGE}}@{{VERSION}}`")
	}
}

func TestTheReadmeSaysWhatNpxDoesWithEachForm(t *testing.T) {
	readme := readmeText(t)
	_, getting, found := strings.Cut(readme, "## Getting it\n")
	if !found {
		t.Fatal("instrument guard: the README has no `## Getting it` section")
	}
	getting, _, _ = strings.Cut(getting, "\n## ")
	// Prose wraps; the claims are read with every run of spaces and line breaks as one space.
	flat := strings.Join(strings.Fields(getting), " ")
	for _, untrue := range []string{"fetches the latest published version by default", "`npx` does, by default"} {
		if strings.Contains(flat, untrue) {
			t.Errorf("the README says %q, and npx with no version runs a copy it already has", untrue)
		}
	}
	for _, claim := range []string{
		// with no version
		"With no version after the name, `npx` can run a copy that is already on the machine — one it fetched earlier, or one installed with `npm i -g` — however old it is.",
		// with @latest
		"With `@latest` after the name, as every command on this page has it, `npx` asks the registry for the newest release each time.",
		// with a version
		"`npx {{PACKAGE}}@{{VERSION}} …` runs exactly that release, every time, on every machine, until you change the pin.",
	} {
		if !strings.Contains(flat, claim) {
			t.Errorf("the README's `## Getting it` must say:\n  %s", claim)
		}
	}
}
