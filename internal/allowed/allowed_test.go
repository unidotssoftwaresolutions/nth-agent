package allowed

import (
	"strings"
	"testing"
)

// Every entry on the allow-list says why the farm may send it: a capability is added there on purpose, with its reason.
func TestEveryCapabilityOnTheListSaysWhy(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Capabilities {
		if strings.HasPrefix(c.Name, "appium:") {
			t.Errorf("%s: an entry is named without the appium: prefix, which both spellings are read against", c.Name)
		}
		if seen[c.Name] {
			t.Errorf("%s is on the list twice", c.Name)
		}
		seen[c.Name] = true
		if len(c.Why) < 20 {
			t.Errorf("%s carries no reason worth the name: %q", c.Name, c.Why)
		}
		if c.kind == 0 {
			t.Errorf("%s says nothing about the value it may carry", c.Name)
		}
	}
	if len(Capabilities) < 30 {
		t.Fatalf("instrument guard: %d entries — the runner's creates alone use more", len(Capabilities))
	}
}

// What names a program, a path or an address on this computer can never be put on the list, whoever asks, and the refusal
// says what it would have done. Named here by hand, so taking one off the never-list is a test going red too.
func TestNothingThatNamesThisComputersProgramsPathsOrAddressesCanBeListed(t *testing.T) {
	for _, name := range []string{"chromedriverExecutable", "chromedriverExecutableDir", "webDriverAgentUrl", "derivedDataPath",
		"prebuiltWDAPath", "keychainPath", "keychainPassword"} {
		if _, listed := byName[name]; listed {
			t.Errorf("%s is on the allow-list", name)
		}
		if _, never := neverPassed[strings.ToLower(name)]; !never {
			t.Errorf("%s is not on the never-list, so its refusal does not say what it would have done", name)
		}
	}
}

// A build address is the farm's when it is https, on the farm's own host and port, and names no user.
func TestABuildIsTheFarmsOnlyOnItsHostOverHttps(t *testing.T) {
	const farm = "https://farm.nthcase.com"
	for address, want := range map[string]bool{
		"https://farm.nthcase.com/app-cache/abc.apk?grant=1.x":   true,
		"https://FARM.nthcase.com:443/app-cache/abc.ipa":         true,
		"http://farm.nthcase.com/app-cache/abc.apk":              false,
		"https://farm.nthcase.com:8443/app-cache/abc.apk":        false,
		"https://farm.nthcase.com.example.com/app-cache/abc.apk": false,
		"https://user@farm.nthcase.com/app-cache/abc.apk":        false,
		"https://builds.example.com/abc.apk":                     false,
		"/Users/ada/app.apk":                                     false,
		"file:///Users/ada/app.apk":                              false,
		"farm.nthcase.com/app-cache/abc.apk":                     false,
		"":                                                       false,
	} {
		if got := OnTheFarm(address, farm); got != want {
			t.Errorf("OnTheFarm(%q) = %v, want %v", address, got, want)
		}
	}
}
