package tunnel_test

import (
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// AN AGENT THAT PASSES ON WINDOWS, END TO END. The first windows-latest run refused every session that installed a build:
// the agent fetched and counted the build, handed Appium its own file — C:\Users\…\builds\<name>.apk — and then judged
// that file as a URL, which is the scheme "c" with no path, so no kind. This seal makes the agent's own folder a C:\ path on
// every system: on Windows its temp folder is one; elsewhere a folder of the test's own is named C:\… (a name, not a path,
// there), so the hand-over writes C:\…/nth-agent/builds/<name>.apk and the very same reading is exercised on a Mac.
func TestASessionWhoseBuildIsAWindowsPathStarts(t *testing.T) {
	config := t.TempDir()
	if runtime.GOOS != "windows" {
		t.Chdir(config)
		config = `C:\Users\runneradmin\AppData\Local`
	}
	for _, c := range []struct{ kind, phone, platform string }{{"apk", offeredAndroid, "Android"}, {"ipa", offeredIPhone, "iOS"}} {
		t.Run(c.kind, func(t *testing.T) {
			route := &buildRoute{answer: serves(aBuild(t, c.kind))}
			reader, send, appium, out, host := countingTunnel(t, config, route, nil)
			create := `{"capabilities":{"alwaysMatch":{"platformName":"` + c.platform + `","appium:udid":"` + c.phone + `",` +
				`"appium:app":"` + farmBuild(host, c.kind) + `"}}}`
			send(sessionFrame("c1", create))
			if r := answerOf(t, reader, "c1"); r["status"] != float64(http.StatusOK) {
				t.Fatalf("a build handed over as a C:\\ path must start its session: %v %s\n%s", r["status"], decodeBody(t, r), out.String())
			}
			seen := appium.seen()
			if len(seen) != 1 {
				t.Fatalf("Appium must receive the one create: %+v", seen)
			}
			file, _ := handedOver(t, true, seen[0].Body)
			// The shape that was refused: a drive letter and a colon, which a URL reads as its scheme.
			if len(file) < 3 || file[1] != ':' || file[2] != '\\' || filepath.Dir(file) != shelfAt(config) || !strings.HasSuffix(file, "."+c.kind) {
				t.Fatalf("instrument guard: Appium must be handed the agent's own file, a drive path under %s, got %q", shelfAt(config), file)
			}
		})
	}
}
