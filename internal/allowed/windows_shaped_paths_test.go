package allowed

import (
	"strings"
	"testing"
)

// AN AGENT THAT PASSES ON WINDOWS. After the hand-over a request names the agent's own file, and on Windows that file is
// C:\Users\…\builds\<name>.apk. Read as a URL it is the scheme "c" with no path, so its kind was "" and every session that
// installed a build was refused there ("which is neither an .ipa nor an .apk"): the first windows-latest run's 502s, every
// one. An address is read as a URL only when it is shaped like one the farm may send; the agent's own file is a path of
// this computer. These shapes are text on every system, so the seal is red on a Mac at f77a50a too.

// windowsOwnFiles are the agent's own file as Windows names it, and the kind each is.
var windowsOwnFiles = map[string]string{
	`C:\Users\runneradmin\AppData\Local\nth-agent\builds\2eef4373f40a3cfafe96afce5d9bee95.apk`:                ".apk",
	`C:\Users\RUNNER~1\AppData\Local\Temp\TestX123\001\nth-agent\builds\0123456789abcdef0123456789abcdef.IPA`: ".ipa",
	`\\server\share\nth-agent\builds\0123456789abcdef0123456789abcdef.apk`:                                    ".apk",
	`C:0123456789abcdef0123456789abcdef.apk`:                                                                  ".apk",
	// …and a path of any system whose folder a URL would cut: a # starts a fragment, a ? a query.
	"/Users/ada/Library/Application Support/proj #2/nth-agent/builds/0123456789abcdef0123456789abcdef.apk": ".apk",
	"/home/ada/what?/nth-agent/builds/0123456789abcdef0123456789abcdef.ipa":                                ".ipa",
}

func TestTheAgentsOwnFileIsReadAsAPathOfThisComputer(t *testing.T) {
	for file, want := range windowsOwnFiles {
		if kind, refusal := BuildKind(file); kind != want || refusal != "" {
			t.Errorf("%s: BuildKind says %q, %q; it is an %s", file, kind, refusal, want)
		}
		if got := judgeBuild(file, countedFarm, func(p string) bool { return p == file }); got != "" {
			t.Errorf("%s: the agent's own file must pass, got %q", file, got)
		}
	}
}

// The create path, and every install inside a session, after the hand-over: each place a build is named, with the agent's
// own file as Windows names it.
func TestTheHandedOverRequestPassesWithAWindowsPathInEverySpelling(t *testing.T) {
	phones := Offering("R5CT64M346B", "00008140-0000C0FFEE0000A1")
	for file := range windowsOwnFiles {
		own := func(p string) bool { return p == file }
		for name, c := range everySpelling {
			farmBody := strings.ReplaceAll(c.body, "FORMAT", countedBuild("apk"))
			body, err := HandOver(c.create, c.path, []byte(farmBody), map[string]string{countedBuild("apk"): file})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got := ""
			if c.create {
				got = CreateHandedOver(body, countedFarm, phones, own)
			} else {
				got = InSessionHandedOver(c.method, c.path, body, countedFarm, own)
			}
			if got != "" {
				t.Errorf("%s, %s: the handed-over request must pass, got %q", name, file, got)
			}
		}
	}
}

// Read as a path, a Windows path is still held to the two kinds, and a path the agent did not write is still refused.
func TestAWindowsPathIsStillHeldToTheRule(t *testing.T) {
	const mine = `C:\Users\ada\AppData\Local\nth-agent\builds\0123456789abcdef0123456789abcdef.apk`
	own := func(p string) bool { return p == mine }
	for _, c := range []struct{ address, says string }{
		{strings.TrimSuffix(mine, ".apk") + ".zip", "which is a .zip"},
		{strings.TrimSuffix(mine, ".apk") + ".APKS", "which is a .apks"},
		{strings.TrimSuffix(mine, ".apk"), "neither an .ipa nor an .apk"},
		{mine + ".part", "neither an .ipa nor an .apk"},
	} {
		if got := judgeBuild(c.address, countedFarm, func(string) bool { return true }); !strings.Contains(got, c.says) {
			t.Errorf("%s: must be refused saying %q, got %q", c.address, c.says, got)
		}
	}
	for _, other := range []string{`C:\Users\ada\Downloads\app.apk`, `\\server\share\app.apk`, `C:app.apk`, mine + ".part"} {
		if got := judgeBuild(other, countedFarm, own); !strings.Contains(got, "not an https address on the farm's own host") {
			t.Errorf("%s: a path the agent did not write is refused, got %q", other, got)
		}
	}
	if got := judgeBuild(mine, countedFarm, nil); got == "" {
		t.Error("with nothing the agent wrote for this request, its own folder's Windows path is refused")
	}
}

// AN ADDRESS SHAPED LIKE THE FARM'S IS NOT THE FARM'S: an https address whose path ends in .apk, on any other host, is
// refused — read as a URL, as before, and judged by its host.
func TestAnHttpsBuildOnAnotherHostIsStillRefused(t *testing.T) {
	const elsewhere = "https://builds.example.com/app-cache/4f9c0a8b.apk?grant=1.x"
	if kind, refusal := BuildKind(elsewhere); kind != ".apk" || refusal != "" {
		t.Fatalf("instrument guard: %s is an .apk by its path, so only its host can refuse it; got %q, %q", elsewhere, kind, refusal)
	}
	for _, address := range []string{
		elsewhere,
		"https://farm.nthcase.com.example.com/app-cache/4f9c0a8b.apk",
		"https://farm.nthcase.com:8443/app-cache/4f9c0a8b.apk",
		"https://ada@farm.nthcase.com/app-cache/4f9c0a8b.apk",
		"http://farm.nthcase.com/app-cache/4f9c0a8b.apk",
	} {
		for name, c := range everySpelling {
			got := judge(c.create, c.method, c.path, strings.ReplaceAll(c.body, "FORMAT", address))
			if !strings.Contains(got, "not an https address on the farm's own host") {
				t.Errorf("%s, %s: must be refused for its host, got %q", name, address, got)
			}
		}
		if got := judgeBuild(address, countedFarm, func(p string) bool { return false }); !strings.Contains(got, "farm's own host") {
			t.Errorf("%s: after the hand-over too, got %q", address, got)
		}
	}
}
