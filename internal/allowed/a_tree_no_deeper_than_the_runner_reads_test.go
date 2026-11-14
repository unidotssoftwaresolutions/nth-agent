package allowed

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// A LEASE CANNOT RAISE APPIUM'S TREE DEPTH PAST WHAT THE RUNNER READS. The runner refuses a page source nested deeper than
// 128 levels, and a source holds two levels of its own around the app's tree (the document's root, and the app on iOS),
// so the deepest snapshotMaxDepth whose source the runner still reads is 126. Appium's own defaults are 70 on Android and
// 50 on iOS. A settings call asking for more — or for a depth in any other shape — is REFUSED, whole, in the words every
// in-session refusal uses; nothing of it reaches Appium. Every other setting passes as it did.

const settingsPath = "/session/local-session-1/appium/settings"

func TestADeeperTreeThanTheRunnerReadsIsRefused(t *testing.T) {
	for name, c := range map[string]struct{ path, body string }{
		"one past the bound":                   {settingsPath, `{"settings":{"snapshotMaxDepth":127}}`},
		"the runner's own bound":               {settingsPath, `{"settings":{"snapshotMaxDepth":128}}`},
		"far past it":                          {settingsPath, `{"settings":{"snapshotMaxDepth":100000}}`},
		"beside a setting that passes":         {settingsPath, `{"settings":{"waitForIdleTimeout":0,"snapshotMaxDepth":500}}`},
		"as text, which iOS reads as a number": {settingsPath, `{"settings":{"snapshotMaxDepth":"500"}}`},
		"as a fraction":                        {settingsPath, `{"settings":{"snapshotMaxDepth":126.5}}`},
		"with an exponent":                     {settingsPath, `{"settings":{"snapshotMaxDepth":1e3}}`},
		"none":                                 {settingsPath, `{"settings":{"snapshotMaxDepth":0}}`},
		"below none":                           {settingsPath, `{"settings":{"snapshotMaxDepth":-1}}`},
		"null":                                 {settingsPath, `{"settings":{"snapshotMaxDepth":null}}`},
		"a list":                               {settingsPath, `{"settings":{"snapshotMaxDepth":[500]}}`},
		"named twice, the last one deep":       {settingsPath, `{"settings":{"snapshotMaxDepth":50,"snapshotMaxDepth":500}}`},
		"the name escaped":                     {settingsPath, `{"settings":{"snapshotMax\u0044epth":500}}`},
		"the name in other letters":            {settingsPath, `{"settings":{"SnapshotMaxDepth":500}}`},
		"settings given twice, last deep":      {settingsPath, `{"settings":{"snapshotMaxDepth":50},"settings":{"snapshotMaxDepth":500}}`},
		"under the farm's prefix":              {"/wd/hub" + settingsPath, `{"settings":{"snapshotMaxDepth":500}}`},
		"the route in capitals":                {"/session/local-session-1/APPIUM/Settings", `{"settings":{"snapshotMaxDepth":500}}`},
		"a trailing slash and a query":         {settingsPath + "/?x=1", `{"settings":{"snapshotMaxDepth":500}}`},
		"a body that is not JSON":              {settingsPath, `{"settings":`},
		"settings that are not an object":      {settingsPath, `{"settings":"snapshotMaxDepth=500"}`},
	} {
		refusal := InSession("POST", c.path, []byte(c.body), "https://farm.example")
		if refusal == "" {
			t.Errorf("%s: %s must be refused, and passed", name, c.body)
			continue
		}
		if !strings.Contains(refusal, "nothing was sent to Appium") || !strings.HasPrefix(refusal, "the farm asked the Appium on this machine ") {
			t.Errorf("%s: the refusal is said as every in-session refusal is, got %q", name, refusal)
		}
	}
}

func TestATreeTheRunnerReadsAndEveryOtherSettingStillPass(t *testing.T) {
	for name, c := range map[string]struct{ method, path, body string }{
		"another setting":              {"POST", settingsPath, `{"settings":{"waitForIdleTimeout":0}}`},
		"several, none a depth":        {"POST", settingsPath, `{"settings":{"waitForIdleTimeout":0,"snapshotMaxChildren":200,"ignoreUnimportantViews":true}}`},
		"the recorder's shallow read":  {"POST", settingsPath, `{"settings":{"snapshotMaxDepth":15}}`},
		"Appium's iOS default":         {"POST", settingsPath, `{"settings":{"snapshotMaxDepth":50}}`},
		"Appium's Android default":     {"POST", settingsPath, `{"settings":{"snapshotMaxDepth":70}}`},
		"the deepest the runner reads": {"POST", settingsPath, `{"settings":{"snapshotMaxDepth":126}}`},
		"the shallowest":               {"POST", settingsPath, `{"settings":{"snapshotMaxDepth":1}}`},
		"reading the settings":         {"GET", settingsPath, ``},
	} {
		if refusal := InSession(c.method, c.path, []byte(c.body), "https://farm.example"); refusal != "" {
			t.Errorf("%s: %s %s must pass as before, refused: %s", name, c.method, c.body, refusal)
		}
	}
}

// APPIUM ROUTES WITHOUT REGARD TO CASE (its server is an Express app, whose routing is case-insensitive unless told
// otherwise, and Appium does not tell it), so a route spelled in other letters is the same route, and is judged as one.
func TestARouteSpelledInOtherLettersIsJudgedAsTheRouteItIs(t *testing.T) {
	const s = "/session/local-session-1"
	for name, c := range map[string]struct{ path, body string }{
		"execute_driver":         {s + "/Appium/Execute_Driver", `{"script":"return 1","type":"webdriverio"}`},
		"mobile: shell":          {s + "/Execute/Sync", `{"script":"mobile: shell","args":[{"command":"id"}]}`},
		"the screen recording":   {s + "/appium/START_RECORDING_SCREEN", `{"options":{}}`},
		"Appium's own log":       {s + "/SE/Log", `{"type":"server"}`},
		"a local file installed": {s + "/appium/device/Install_App", `{"appPath":"/Users/ada/app.apk"}`},
	} {
		if refusal := InSession("POST", c.path, []byte(c.body), "https://farm.example"); refusal == "" {
			t.Errorf("%s: %s reaches the same Appium route as its lower-case spelling, and passed", name, c.path)
		}
	}
}

// THE BOUND IS THE RUNNER'S, read from its source when it is beside this repository: its page-source nesting bound less the
// two levels a source holds around the app's tree, which the runner's own note on that bound counts.
func TestTheDepthBoundIsTheRunnersLessTheDocumentsOwnTwo(t *testing.T) {
	if MaxTreeDepth != 126 {
		t.Fatalf("the bound is 126, written out by hand; it is %d", MaxTreeDepth)
	}
	data, err := os.ReadFile(farmtest.ServerSource(t, farmtest.RunnerReadingBounds))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	m := regexp.MustCompile(`public const int MaxScreenNesting = (\d+);`).FindStringSubmatch(source)
	if m == nil || m[1] != "128" {
		t.Fatalf("the runner's nesting bound is no longer 128: %v", m)
	}
	if !strings.Contains(source, "with the document's own two") {
		t.Fatal("the runner's note no longer counts the document's own two levels, which this bound subtracts")
	}
}
