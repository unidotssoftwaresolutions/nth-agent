package nthagent

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// AN APPIUM ONLY THIS COMPUTER CAN REACH. Every check this agent makes on what the farm asks of the customer's Appium is a
// check on what passes through the agent. Appium itself asks a client for nothing, so an Appium that listens on every address
// of the computer can be driven by anything on the network that reaches it, past every one of those checks. Appium listens
// on one address when it is started with `--address`, so every command a customer reads that starts Appium says
// `appium --address 127.0.0.1`: the README (and the npm page cut from it), doctor's and connect's remedies, the help text.

// startsAppium is a sentence telling the reader to start Appium.
var startsAppium = regexp.MustCompile("(?i)\\b(?:start|restart|run)\\s+(?:your\\s+|the\\s+)?(?:appium\\b|`appium`)")

// appiumCommand is a command line that starts Appium's server: `appium` with flags. `appium driver …`, `npm i -g appium` and
// a bare name in a list are not one.
var appiumCommand = regexp.MustCompile("(?:^|[\\s(`'\"])appium((?:\\s+--[a-z][a-z-]*(?:[ =][^\\s`'\")]+)?)+)")

const onLoopback = "appium --address 127.0.0.1"

// notAStart are the sentences the census reads and lets be, each for its reason: one that says WHERE to start Appium (the
// terminal the environment was set in), and one that says which flags never to start it with.
var notAStart = regexp.MustCompile("(?i)^(?:start|restart|run)\\s+appium\\s+from\\b|^start\\s+appium\\s+with\\s+`--relaxed")

// notAnInstruction is what stands before a "run Appium" that tells nobody to do it: "Node.js to run Appium", "an Appium you run".
var notAnInstruction = regexp.MustCompile(`(?i)\b(?:to|you)\s+$`)

func sentenceFrom(text string, at int) string {
	end := len(text)
	for _, stop := range []string{". ", ".\n", "\n\n"} {
		if i := strings.Index(text[at:], stop); i >= 0 && at+i < end {
			end = at + i
		}
	}
	return text[at:end]
}

func appiumStartProblems(path, text string) []string {
	var hits []string
	for _, at := range startsAppium.FindAllStringIndex(text, -1) {
		sentence := sentenceFrom(text, at[0])
		if notAStart.MatchString(sentence) || notAnInstruction.MatchString(text[:at[0]]) {
			continue
		}
		if !strings.Contains(sentence, onLoopback) {
			hits = append(hits, path+":"+strconv.Itoa(lineOf(text, at[0]))+" — says to start Appium without `"+onLoopback+"`: "+
				strings.Join(strings.Fields(sentence), " "))
		}
	}
	for _, m := range appiumCommand.FindAllStringSubmatchIndex(text, -1) {
		flags := text[m[2]:m[3]]
		if strings.Contains(flags, "--version") || strings.Contains(flags, "--help") {
			continue
		}
		if !strings.Contains(flags, "--address 127.0.0.1") {
			hits = append(hits, path+":"+strconv.Itoa(lineOf(text, m[0]))+" — an Appium command without --address 127.0.0.1: appium"+flags)
		}
	}
	return hits
}

func TestEveryCommandThatStartsAppiumKeepsItOnThisComputer(t *testing.T) {
	files := publishedTree(t)
	var hits []string
	read := 0
	for _, f := range customerText(t, files) {
		// The CHANGELOG's earlier sections say what earlier releases did, in their words; it is history, not an instruction.
		if f.path == "CHANGELOG.md" {
			continue
		}
		text := f.text
		if f.path == "README.md" {
			text, _, _ = strings.Cut(text, "<!-- NOT-FOR-THE-REGISTRY -->")
		}
		read++
		hits = append(hits, appiumStartProblems(f.path, text)...)
	}
	if read < 50 {
		t.Fatalf("instrument guard: %d files of customer text read", read)
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("%d place(s) where a customer is told to start Appium so that it may listen on every address of the computer:\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// THE GUARD: the census on planted lines whose answers are known.
func TestTheAppiumCensusFindsAStartWithoutTheAddress(t *testing.T) {
	for _, line := range []string{"Start `appium` in that terminal.", "start Appium there (appium), then re-run",
		"start Appium at info: appium --log-level info", "run Appium on this machine and restart", "appium --port 4723 --log-level info"} {
		if len(appiumStartProblems("planted", line)) == 0 {
			t.Errorf("the census let through %q", line)
		}
	}
	for _, line := range []string{"Start Appium in that terminal: `appium --address 127.0.0.1`.", "npm i -g appium and appium driver install x",
		"open a new terminal and start Appium from it", "`adb`, `appium`, `node`", "run `appium --version`",
		"Node.js to run Appium", "only to an Appium you run the Appium endpoint",
		"start Appium at info: appium --address 127.0.0.1 --log-level info"} {
		if hits := appiumStartProblems("planted", line); len(hits) != 0 {
			t.Errorf("the census refused %q: %v", line, hits)
		}
	}
}
