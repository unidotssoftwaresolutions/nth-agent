package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/state"
)

// WHAT_THE_FIRST_TRY_SHOWED — what the operator's first run of the released agent on his own Mac found.

// theFarmOnOffer is the one farm a customer connects to.
const theFarmOnOffer = "https://farm.nthcase.com"

// A CUSTOMER HAS ONE FARM, so connect does not refuse to start without its address. --farm and NTH_AGENT_FARM still win,
// in that order, and keep every rule they had: the default passes the same check a typed address does, and a typed
// address the check refuses is still the one used, so it is still refused.
func TestConnectWithNoFarmNamedConnectsToTheFarm(t *testing.T) {
	if got := theFarm("", ""); got != theFarmOnOffer {
		t.Fatalf("with neither --farm nor NTH_AGENT_FARM, connect must connect to %s, got %q", theFarmOnOffer, got)
	}
	if got := farm.DefaultBaseURL; got != theFarmOnOffer {
		t.Fatalf("the default farm is spelled once, in internal/farm, got %q", got)
	}
	if err := farm.ValidateBaseURL(theFarm("", "")); err != nil {
		t.Fatalf("the default farm must pass the check every farm address passes: %v", err)
	}
	if got := theFarm("https://farm.example", "http://127.0.0.1:5080"); got != "https://farm.example" {
		t.Fatalf("--farm wins over NTH_AGENT_FARM, got %q", got)
	}
	if got := theFarm("", "http://127.0.0.1:5080"); got != "http://127.0.0.1:5080" {
		t.Fatalf("NTH_AGENT_FARM wins over the default, got %q", got)
	}
	if got := theFarm("http://farm.example", ""); got != "http://farm.example" || farm.ValidateBaseURL(got) == nil {
		t.Fatalf("a plain-http farm typed by hand is still the one used, and still refused, got %q", got)
	}
}

// WITH NO TOKEN, connect asks for the token, and only for it: the farm is no longer something a customer must name.
func TestConnectWithNoTokenAsksOnlyForTheToken(t *testing.T) {
	t.Setenv("NTH_AGENT_TOKEN", "")
	t.Setenv("NTH_AGENT_FARM", "")
	var stdout, stderr strings.Builder
	code := run([]string{"connect", "--no-color"}, strings.NewReader(""), &stdout, &stderr)
	said := stderr.String()
	if code != exitMistake || !strings.Contains(said, "NTH_AGENT_TOKEN") {
		t.Fatalf("connect with no token must stop and name the token (exit %d):\n%s", code, said)
	}
	if strings.Contains(said, "farm URL") || strings.Contains(said, "--farm") || strings.Contains(said, "NTH_AGENT_FARM") {
		t.Fatalf("connect with no token must not ask for a farm, which it has:\n%s", said)
	}
}

// EVERY LINE THAT TELLS A CUSTOMER ABOUT --farm says it is optional and which farm is used without it; no example
// command names a farm, and the line upload prints to go on with connect names none either.
func TestEveryLineAboutTheFarmSaysItIsOptional(t *testing.T) {
	entry := ""
	lines := strings.Split(usage(), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--farm URL") {
			entry = line
			for _, next := range lines[i+1:] {
				if strings.HasPrefix(strings.TrimSpace(next), "--") {
					break
				}
				entry += " " + strings.TrimSpace(next)
			}
		}
	}
	if !strings.Contains(entry, "optional") || !strings.Contains(entry, theFarmOnOffer) {
		t.Fatalf("the usage's --farm entry must say it is optional and name %s:\n%s", theFarmOnOffer, entry)
	}
	readme, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	row, prose := "", false
	for _, line := range strings.Split(string(readme), "\n") {
		if strings.HasPrefix(line, "| `--farm URL`") {
			row = line
		}
		if strings.Contains(line, "`--farm` is optional") && strings.Contains(line, theFarmOnOffer) {
			prose = true
		}
		if strings.Contains(line, "connect --farm") {
			t.Fatalf("a README example still names a farm, which a customer need not do:\n%s", line)
		}
	}
	if !strings.Contains(row, "optional") || !strings.Contains(row, theFarmOnOffer) {
		t.Fatalf("the README's --farm row must say it is optional and name %s:\n%s", theFarmOnOffer, row)
	}
	if !prose {
		t.Fatalf("the README's connect section must say `--farm` is optional and name %s", theFarmOnOffer)
	}
	upload, err := os.ReadFile("uploadcmd.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(upload), "connect --farm") {
		t.Fatal("the line upload prints to go on with connect still names a farm")
	}
}

// THE TWO PLACES THAT NAME THE STATE ON THIS MACHINE ARE WIRED: connect hands the loop the node-id file its refusal names,
// and doctor is told the folder it says. Read from main.go's own composite literals, as the missing-build seal reads it.
func TestConnectAndDoctorAreToldWhereTheStateIs(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"NodeIDFile: state.NodeIDFile(configDir)", "StateFolder: stateFolder()"} {
		if !strings.Contains(strings.Join(strings.Fields(string(source)), " "), must) {
			t.Fatalf("main.go must wire %q", must)
		}
	}
	dir, err := state.ConfigDir()
	if err != nil {
		t.Skip("no config folder on this machine")
	}
	if got := stateFolder(); got != state.Folder(dir) {
		t.Fatalf("doctor's folder must be the agent's own (%q), got %q", state.Folder(dir), got)
	}
}

// THE WEBDRIVERAGENT ON THE PHONE IS ASKED ABOUT FOR REAL (decision A6): connect is handed the question, and it is asked
// through a forward to the phone, of the build this machine declared.
func TestConnectAsksWhatAnswersOnEachIphonesWebDriverAgentPort(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(source)), " ")
	if !strings.Contains(text, "WdaIdentity: identifyWda(wdaClient, iosWdaBundleID),") {
		t.Fatal("main.go must hand connect the question, asked of the build this machine declared")
	}
	asker, err := os.ReadFile("thefarm.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(asker)), " "),
		"ios.WithWdaPort(udid, port, func(baseURL string) error { id = wda.Identify(ctx, client, baseURL, declaredBundle)") {
		t.Fatal("the question must be asked through a forward to the phone, of the declared build")
	}
}
