package allowed

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// A BUILD IT HAS COUNTED. Appium on this machine installs only a build this agent has fetched, measured and checked, and it
// gets that build as a file. The list's half of that rule: a build that is a .zip or an .apks (which Appium unpacks on this
// computer, and which no phone this agent drives installs), or anything else that is not an .ipa or an .apk, is refused
// before anything is fetched, in every spelling a session or an install can use; one request names at most one build; and
// the one path on this computer a request may name is the file the agent itself wrote for that request.

const countedFarm = "https://farm.nthcase.com"

// countedBuild is an address the farm's build cache hands out, with the build's extension spelled as given.
func countedBuild(ext string) string {
	return "https://farm.nthcase.com/app-cache/4f9c0a8b7e6d5c4b3a29181706f5e4d3c2b1a09f8e7d6c5b4a3928170605f4e3." + ext +
		"?grant=1761480000.AbCdEf0123456789"
}

// everySpelling is every place a request this list lets through can name a build: a session's app in each set and spelling,
// and each install route Appium serves. FORMAT holds the address.
var everySpelling = map[string]struct {
	create             bool
	method, path, body string
}{
	"create, appium:app in alwaysMatch": {true, "POST", "/session", `{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:udid":"R5CT64M346B","appium:app":"FORMAT"}}}`},
	"create, app in firstMatch":         {true, "POST", "/session", `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:udid":"R5CT64M346B","app":"FORMAT"}]}}`},
	"create, app in appium:options":     {true, "POST", "/session", `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:options":{"udid":"00008140-0000C0FFEE0000A1","app":"FORMAT"}}}}`},
	"create, appium:app in appium:options": {true, "POST", "/session",
		`{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:options":{"udid":"00008140-0000C0FFEE0000A1","appium:app":"FORMAT"}}}}`},
	"create, the older desiredCapabilities": {true, "POST", "/session", `{"desiredCapabilities":{"platformName":"Android","udid":"R5CT64M346B","app":"FORMAT"}}`},
	"install_app":                           {false, "POST", "/session/s1/appium/device/install_app", `{"appPath":"FORMAT"}`},
	"install_app, its route in capitals":    {false, "POST", "/session/s1/APPIUM/DEVICE/INSTALL_APP", `{"appPath":"FORMAT"}`},
	"mobile: installApp (iOS, app)":         {false, "POST", "/session/s1/execute/sync", `{"script":"mobile: installApp","args":[{"app":"FORMAT"}]}`},
	"mobile: installApp (Android, appPath)": {false, "POST", "/session/s1/execute/sync", `{"script":"mobile: installApp","args":[{"appPath":"FORMAT","grantPermissions":true}]}`},
	"mobile: installApp, loosely spelled":   {false, "POST", "/session/s1/execute", `{"script":"  MOBILE:InstallApp ","args":[{"app":"FORMAT"}]}`},
	"mobile: installMultipleApks":           {false, "POST", "/session/s1/execute/sync", `{"script":"mobile: installMultipleApks","args":[{"apks":["FORMAT"]}]}`},
	"mobile: installApp, async":             {false, "POST", "/session/s1/execute/async", `{"script":"mobile: installApp","args":[{"app":"FORMAT"}]}`},
}

func judge(create bool, method, path, body string) string {
	if create {
		return Create([]byte(body), countedFarm, Offering("R5CT64M346B", "00008140-0000C0FFEE0000A1"))
	}
	return InSession(method, path, []byte(body), countedFarm)
}

func TestAZipOrAnApksIsRefusedInEverySpelling(t *testing.T) {
	for _, ext := range []string{"zip", "ZIP", "Zip", "apks", "APKS"} {
		for name, c := range everySpelling {
			for _, address := range []string{
				countedBuild(ext),
				// the address as the worker mints it, with the grant in the path
				"https://farm.nthcase.com/app-cache/1761480000.AbCdEf0123456789/4f9c0a8b7e6d5c4b." + ext,
				// a query that ends like an .ipa does not make the build one: the path says what it is
				"https://farm.nthcase.com/app-cache/4f9c0a8b7e6d5c4b." + ext + "?grant=1.x&name=app.ipa",
			} {
				body := strings.ReplaceAll(c.body, "FORMAT", address)
				got := judge(c.create, c.method, c.path, body)
				want := "which is a ." + strings.ToLower(ext)
				if !strings.Contains(got, want) || !strings.Contains(got, "nothing was sent to Appium") {
					t.Errorf("%s, %s: must be refused, saying %q, got %q", name, address, want, got)
				}
				if !strings.Contains(got, "an iPhone takes an .ipa, an Android phone an .apk") {
					t.Errorf("%s, %s: the refusal must say what a phone this agent drives installs: %q", name, address, got)
				}
			}
		}
	}
}

func TestABuildThatIsNeitherAnIpaNorAnApkIsRefused(t *testing.T) {
	for _, address := range []string{
		"https://farm.nthcase.com/app-cache/4f9c0a8b.aab?grant=1.x",
		"https://farm.nthcase.com/app-cache/4f9c0a8b.bin?grant=1.x",
		"https://farm.nthcase.com/app-cache/4f9c0a8b?grant=1.x",
		"https://farm.nthcase.com/app-cache/4f9c0a8b.ipa/?grant=1.x",
		"https://farm.nthcase.com/app-cache/4f9c0a8b.tar.gz",
		"https://farm.nthcase.com/app-cache/4f9c0a8b.app",
	} {
		for name, c := range everySpelling {
			got := judge(c.create, c.method, c.path, strings.ReplaceAll(c.body, "FORMAT", address))
			if !strings.Contains(got, "neither an .ipa nor an .apk") || !strings.Contains(got, "nothing was sent to Appium") {
				t.Errorf("%s, %s: must be refused as neither kind, got %q", name, address, got)
			}
		}
	}
}

// The list still lets an .ipa or an .apk from the farm through, in whatever letters its extension is spelled: the fetch,
// the count and the hand-over come after it.
func TestAnIpaOrAnApkFromTheFarmStillPassesTheList(t *testing.T) {
	for _, ext := range []string{"ipa", "IPA", "apk", "Apk"} {
		for name, c := range everySpelling {
			if got := judge(c.create, c.method, c.path, strings.ReplaceAll(c.body, "FORMAT", countedBuild(ext))); got != "" {
				t.Errorf("%s, .%s: must pass the list, got %q", name, ext, got)
			}
		}
		if kind, refusal := BuildKind(countedBuild(ext)); refusal != "" || kind != "."+strings.ToLower(ext) {
			t.Errorf(".%s: BuildKind says %q, %q", ext, kind, refusal)
		}
	}
}

func TestOneRequestNamesAtMostOneBuild(t *testing.T) {
	two := []struct {
		name               string
		create             bool
		method, path, body string
	}{
		{"two sets, two builds", true, "POST", "/session", `{"capabilities":{"alwaysMatch":{"platformName":"Android"},"firstMatch":[` +
			`{"appium:udid":"R5CT64M346B","appium:app":"` + countedBuild("apk") + `"},` +
			`{"appium:udid":"R5CT64M346B","appium:app":"` + strings.Replace(countedBuild("apk"), "4f9c", "5f9c", 1) + `"}]}}`},
		{"two split apks", false, "POST", "/session/s1/execute/sync", `{"script":"mobile: installMultipleApks","args":[{"apks":["` +
			countedBuild("apk") + `","` + strings.Replace(countedBuild("apk"), "4f9c", "5f9c", 1) + `"]}]}`},
		{"two installs in one call", false, "POST", "/session/s1/execute/sync", `{"script":"mobile: installApp","args":[{"app":"` +
			countedBuild("ipa") + `"},{"app":"` + strings.Replace(countedBuild("ipa"), "4f9c", "5f9c", 1) + `"}]}`},
	}
	for _, c := range two {
		if got := judge(c.create, c.method, c.path, c.body); !strings.Contains(got, "more than one build") {
			t.Errorf("%s: must be refused, got %q", c.name, got)
		}
	}
	// The same build named in every set is one build.
	same := `{"capabilities":{"alwaysMatch":{"platformName":"Android"},"firstMatch":[` +
		`{"appium:udid":"R5CT64M346B","appium:app":"` + countedBuild("apk") + `"},{"appium:udid":"R5CT64M346B","app":"` + countedBuild("apk") + `"}]}}`
	if got := judge(true, "POST", "/session", same); got != "" {
		t.Errorf("one build named in two sets must pass, got %q", got)
	}
	if got := BuildsNamed(true, "POST", "/session", []byte(same)); !reflect.DeepEqual(got, []string{countedBuild("apk")}) {
		t.Errorf("BuildsNamed must name the one build once, got %q", got)
	}
}

// THE ONE EXCEPTION TO "NEVER A PATH ON THIS MACHINE", AND HOW NARROW IT IS. After the hand-over the body names a path, and it
// is judged again: the path passes only when it is the file the agent wrote for this request (own says so). Any other path,
// a path with no own at all, or the address the farm sent, is judged as before.
func TestTheAgentsOwnFileIsTheOnlyPathThatPasses(t *testing.T) {
	const mine = "/var/folders/zz/zyxwvutsrqponmlkjihgfedcb0000gn/T/nth-agent/builds/0123456789abcdef0123456789abcdef.apk"
	const other = "/Users/ada/Downloads/app.apk"
	own := func(path string) bool { return path == mine }
	phones := Offering("R5CT64M346B", "00008140-0000C0FFEE0000A1")
	for name, c := range everySpelling {
		at := func(p string) []byte { return []byte(strings.ReplaceAll(c.body, "FORMAT", p)) }
		judged := func(p string, own func(string) bool) string {
			if c.create {
				return CreateHandedOver(at(p), countedFarm, phones, own)
			}
			return InSessionHandedOver(c.method, c.path, at(p), countedFarm, own)
		}
		if got := judged(mine, own); got != "" {
			t.Errorf("%s: the agent's own file must pass, got %q", name, got)
		}
		if got := judged(other, own); got == "" {
			t.Errorf("%s: another path on this computer must still be refused", name)
		}
		if got := judged(mine, nil); got == "" {
			t.Errorf("%s: with nothing the agent wrote for this request, even its folder's path is refused", name)
		}
		if got := judged(mine+".part", own); got == "" {
			t.Errorf("%s: a path beside the agent's file must be refused", name)
		}
		if got := judged(strings.Replace(mine, ".apk", ".zip", 1), func(string) bool { return true }); !strings.Contains(got, "which is a .zip") {
			t.Errorf("%s: even an own path is held to the kinds a phone installs, got %q", name, got)
		}
	}
}

// The hand-over rewrites every place the build is named to the agent's file, and leaves every other value as it was.
func TestHandingOverRewritesEveryPlaceTheBuildIsNamed(t *testing.T) {
	const mine = "/var/folders/zz/zyxwvutsrqponmlkjihgfedcb0000gn/T/nth-agent/builds/0123456789abcdef0123456789abcdef.ipa"
	for name, c := range everySpelling {
		farmBody := strings.ReplaceAll(c.body, "FORMAT", countedBuild("ipa"))
		named := BuildsNamed(c.create, c.method, c.path, []byte(farmBody))
		if !reflect.DeepEqual(named, []string{countedBuild("ipa")}) {
			t.Errorf("%s: BuildsNamed must find the build, got %q", name, named)
			continue
		}
		got, err := HandOver(c.create, c.path, []byte(farmBody), map[string]string{countedBuild("ipa"): mine})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if bytes.Contains(got, []byte("farm.nthcase.com")) {
			t.Errorf("%s: the farm's address is still in the body Appium would get: %s", name, got)
		}
		want := strings.ReplaceAll(c.body, "FORMAT", mine)
		var a, b any
		if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("%s: the body must be the farm's with the agent's file in the build's place:\n got  %s\n want %s", name, got, want)
		}
	}
	// A request that names no build is not touched by the hand-over: no build, nothing named.
	for _, body := range []string{`{"capabilities":{"firstMatch":[{"platformName":"Android","appium:udid":"R5CT64M346B"}]}}`} {
		if named := BuildsNamed(true, "POST", "/session", []byte(body)); len(named) != 0 {
			t.Errorf("a create naming no build named %q", named)
		}
	}
	if named := BuildsNamed(false, "GET", "/session/s1/source", nil); len(named) != 0 {
		t.Errorf("a page-source read named %q", named)
	}
}
