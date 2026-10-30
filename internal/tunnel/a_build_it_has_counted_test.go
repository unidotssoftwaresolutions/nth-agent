package tunnel_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A BUILD IT HAS COUNTED, END TO END. Appium on this machine installs only a build this agent has fetched, measured and
// checked, and it gets that build as a file: a session's app, and every install inside a session, reaches Appium as the
// path of a file in the agent's own folder holding exactly the bytes the agent fetched and counted — never the farm's
// address, which Appium would fetch, keep and perhaps unpack itself. A .zip or an .apks never reaches Appium or the farm's
// build route; a build that fails its count, or that the farm answers with a redirect, never reaches Appium. The file goes
// when its session ends — the client's DELETE, the release of a session nobody ended, or the agent stopping — and a crash's
// leftovers go at the next start. The builds are served from memory: nothing here dials the farm's build route, a phone or
// the network.

// aBuild is a small build as a build machine writes one.
func aBuild(t *testing.T, kind string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range map[string]string{"Payload/Lantern.app/Info.plist": "<plist/>", "Payload/Lantern.app/Lantern": "binary",
		"AndroidManifest.xml": "manifest", "classes.dex": "dex " + kind} {
		f, _ := w.Create(name)
		f.Write([]byte(data))
	}
	w.Close()
	return b.Bytes()
}

// buildRoute is the farm's build route, answered from memory: every GET is recorded, and answer decides what it gets.
type buildRoute struct {
	mu     sync.Mutex
	asked  []*http.Request
	answer func(*http.Request) *http.Response
}

func (b *buildRoute) RoundTrip(r *http.Request) (*http.Response, error) {
	b.mu.Lock()
	b.asked = append(b.asked, r)
	answer := b.answer
	b.mu.Unlock()
	response := answer(r)
	response.Request = r
	if response.Header == nil {
		response.Header = http.Header{}
	}
	return response, nil
}

func (b *buildRoute) gets() []*http.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*http.Request(nil), b.asked...)
}

func serves(build []byte) func(*http.Request) *http.Response {
	return func(*http.Request) *http.Response {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(build)), Body: io.NopCloser(bytes.NewReader(build)),
			Header: http.Header{"Content-Type": {"application/octet-stream"}}}
	}
}

// shelfAt is the agent's folder of builds under config.
func shelfAt(config string) string { return filepath.Join(config, "nth-agent", "builds") }

// countedShelf is the shelf a seal's agent fetches through: its config folder, the build route answered from memory, and
// a disk with room to spare.
func countedShelf(config string, route *buildRoute) tunnel.BuildShelf {
	return tunnel.BuildShelf{In: config, Client: &http.Client{Transport: route},
		FreeBytes: func(string) (uint64, error) { return 64 << 30, nil }}
}

// countingTunnel is askingTunnel's agent with its shelf in config and its build route answered by route.
func countingTunnel(t *testing.T, config string, route *buildRoute, change func(*tunnel.Config)) (*farmReader, func(map[string]any), *recorder, *syncWriter, string) {
	t.Helper()
	appium := &recorder{reply: `{"value":{"sessionId":"local-session-1","capabilities":{}}}`}
	server := httptest.NewServer(appium.handler())
	t.Cleanup(server.Close)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
		c.AppiumURL = server.URL
		offeringPhones(c, offeredIPhone, offeredAndroid)
		c.Builds = countedShelf(config, route)
		if change != nil {
			change(c)
		}
	})
	conn := farm.next(t)
	return readFarmSide(conn), func(f map[string]any) { send(t, conn, f) }, appium, &out, mustHost(t, farm.server.URL)
}

// handedOver is the build a request Appium received names, and the request with that build put back as the farm's.
func handedOver(t *testing.T, create bool, body string) (string, map[string]any) {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		t.Fatalf("Appium received a body that is not JSON: %s", body)
	}
	var found string
	var walk func(v any)
	walk = func(v any) {
		switch typed := v.(type) {
		case map[string]any:
			for key, inner := range typed {
				if s, ok := inner.(string); ok && (key == "app" || key == "appium:app" || key == "appPath") {
					found = s
					typed[key] = "BUILD"
				}
				walk(inner)
			}
		case []any:
			for i, inner := range typed {
				if s, ok := inner.(string); ok { // a list of builds (apks) holds nothing else
					found = s
					typed[i] = "BUILD"
				}
				walk(inner)
			}
		}
	}
	walk(top)
	return found, top
}

func sameJSON(t *testing.T, a string, b map[string]any) bool {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(a), &decoded); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(decoded, any(b))
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestALegitimateBuildIsHandedToAppiumAsTheAgentsOwnFile(t *testing.T) {
	for _, c := range []struct {
		kind, phone, platform string
	}{{"ipa", offeredIPhone, "iOS"}, {"apk", offeredAndroid, "Android"}} {
		t.Run(c.kind, func(t *testing.T) {
			build := aBuild(t, c.kind)
			route := &buildRoute{answer: serves(build)}
			config := t.TempDir()
			reader, send, appium, out, host := countingTunnel(t, config, route, nil)
			address := farmBuild(host, c.kind)
			create := `{"capabilities":{"firstMatch":[{"platformName":"` + c.platform + `","appium:udid":"` + c.phone + `",` +
				`"appium:app":"` + address + `","appium:newCommandTimeout":120,"appium:fullReset":true}]}}`
			send(sessionFrame("c1", create))
			if r := answerOf(t, reader, "c1"); r["status"] != float64(http.StatusOK) {
				t.Fatalf("a legitimate build must start its session: %v %s\n%s", r["status"], decodeBody(t, r), out.String())
			}
			seen := appium.seen()
			if len(seen) != 1 {
				t.Fatalf("Appium must receive the one create: %+v", seen)
			}
			file, rest := handedOver(t, true, seen[0].Body)
			if filepath.Dir(file) != shelfAt(config) || filepath.Ext(file) != "."+c.kind || strings.Contains(seen[0].Body, host) {
				t.Fatalf("Appium must be handed the agent's own file, never the farm's address: %s", seen[0].Body)
			}
			_, farmRest := handedOver(t, true, create)
			if !reflect.DeepEqual(rest, farmRest) {
				t.Fatalf("every other capability must reach Appium as the farm sent it:\n got  %v\n want %v", rest, farmRest)
			}
			if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, build) {
				t.Fatalf("the file Appium is handed must hold exactly the bytes the agent fetched and counted (%v)", err)
			}
			gets := route.gets()
			if len(gets) != 1 || gets[0].URL.String() != address || gets[0].Header.Get("X-Agent-Token") != "" {
				t.Fatalf("the agent fetches the build once, from the farm's address, carrying no token: %+v", gets)
			}

			// An install inside the session, in each route, is handed over the same way.
			for i, install := range []struct{ path, body string }{
				{"/session/local-session-1/execute/sync", `{"script":"mobile: installApp","args":[{"app":"` + address + `"}]}`},
				{"/session/local-session-1/appium/device/install_app", `{"appPath":"` + address + `","options":{"grantPermissions":true}}`},
				{"/session/local-session-1/execute/sync", `{"script":"mobile: installMultipleApks","args":[{"apks":["` + address + `"]}]}`},
			} {
				id := "i" + string(rune('a'+i))
				send(inSession(id, "POST", install.path, install.body))
				if r := answerOf(t, reader, id); r["status"] != float64(http.StatusOK) {
					t.Fatalf("%s: an install of a legitimate build must pass: %v %s", install.path, r["status"], decodeBody(t, r))
				}
				seen := appium.seen()
				last := seen[len(seen)-1]
				installed, rest := handedOver(t, false, last.Body)
				_, farmRest := handedOver(t, false, install.body)
				if filepath.Dir(installed) != shelfAt(config) || strings.Contains(last.Body, host) || !reflect.DeepEqual(rest, farmRest) {
					t.Fatalf("%s: Appium must be handed the agent's own file and nothing else changed: %s", install.path, last.Body)
				}
			}
			if n := len(filesIn(t, shelfAt(config))); n != 4 {
				t.Fatalf("each build is kept while its session lives: %d files", n)
			}

			// The session ends: every file fetched for it goes.
			send(inSession("d1", "DELETE", "/session/local-session-1", ""))
			answerOf(t, reader, "d1")
			if left := filesIn(t, shelfAt(config)); len(left) != 0 {
				t.Fatalf("the session's DELETE must remove every file fetched for it, left %v", left)
			}
		})
	}
}

func TestAZipOrAnApksNeverReachesAppiumOrTheFarmsBuildRoute(t *testing.T) {
	for _, ext := range []string{"zip", "apks", "ZIP"} {
		for name, frame := range map[string]func(host string) map[string]any{
			"a session's app": func(host string) map[string]any {
				return sessionFrame("f1", `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"`+offeredIPhone+`","appium:app":"`+farmBuild(host, ext)+`"}]}}`)
			},
			"a session's app in appium:options": func(host string) map[string]any {
				return sessionFrame("f1", `{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:options":{"udid":"`+offeredAndroid+`","app":"`+farmBuild(host, ext)+`"}}}}`)
			},
			"the older desiredCapabilities": func(host string) map[string]any {
				return sessionFrame("f1", `{"desiredCapabilities":{"platformName":"Android","udid":"`+offeredAndroid+`","app":"`+farmBuild(host, ext)+`"}}`)
			},
		} {
			t.Run(ext+", "+name, func(t *testing.T) {
				route := &buildRoute{answer: serves(aBuild(t, ext))}
				config := t.TempDir()
				reader, send, appium, out, host := countingTunnel(t, config, route, nil)
				send(frame(host))
				refused(t, name, answerOf(t, reader, "f1"), appium, out, "which is a ."+strings.ToLower(ext), "an iPhone takes an .ipa")
				if len(route.gets()) != 0 || len(filesIn(t, shelfAt(config))) != 0 {
					t.Fatalf("nothing may be fetched for a .%s", ext)
				}
			})
		}
	}
}

func TestABuildThatFailsItsCountOrComesByRedirectNeverReachesAppium(t *testing.T) {
	for name, c := range map[string]struct {
		answer func(*http.Request) *http.Response
		says   string
	}{
		"bytes that are not a build": {serves([]byte(strings.Repeat("not a zip ", 64))), "it is not a zip archive"},
		"a redirect off the farm's host": {func(*http.Request) *http.Response {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://builds.example.com/app.ipa"}}, Body: io.NopCloser(strings.NewReader(""))}
		}, "does not follow"},
		"an answer that says it is a zip": {func(*http.Request) *http.Response {
			r := serves(aBuild(t, "ipa"))(nil)
			r.Header.Set("Content-Type", "application/zip")
			return r
		}, "the farm's answer says it is a zip archive"},
		"a closed address": {func(*http.Request) *http.Response {
			return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader("This build address's window has closed."))}
		}, "HTTP 410"},
	} {
		t.Run(name, func(t *testing.T) {
			route := &buildRoute{answer: c.answer}
			config := t.TempDir()
			reader, send, appium, out, host := countingTunnel(t, config, route, nil)
			send(sessionFrame("f1", `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"`+offeredIPhone+`","appium:app":"`+farmBuild(host, "ipa")+`"}]}}`))
			refused(t, name, answerOf(t, reader, "f1"), appium, out, "this agent did not hand it on", c.says, "nothing was sent to Appium")
			if gets := route.gets(); len(gets) != 1 {
				t.Fatalf("one request, to the farm's address: %d", len(gets))
			}
			if left := filesIn(t, shelfAt(config)); len(left) != 0 {
				t.Fatalf("nothing may be left behind, left %v", left)
			}
		})
	}
}

// A create that Appium refuses leaves no file: no session holds it.
func TestACreateAppiumRefusesLeavesNoFile(t *testing.T) {
	route := &buildRoute{answer: serves(aBuild(t, "apk"))}
	config := t.TempDir()
	reader, send, appium, _, host := countingTunnel(t, config, route, nil)
	appium.mu.Lock()
	appium.status, appium.reply = http.StatusInternalServerError, `{"value":{"error":"session not created","message":"no"}}`
	appium.mu.Unlock()
	send(sessionFrame("f1", `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:udid":"`+offeredAndroid+`","appium:app":"`+farmBuild(host, "apk")+`"}]}}`))
	if r := answerOf(t, reader, "f1"); r["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("Appium's own answer is relayed: %v", r["status"])
	}
	if left := filesIn(t, shelfAt(config)); len(left) != 0 {
		t.Fatalf("a create that started no session leaves no file, left %v", left)
	}
}

// The release that ends a session nobody ended removes its file too: the lease is over.
func TestTheReleaseOfASessionNobodyEndedRemovesItsFile(t *testing.T) {
	route := &buildRoute{answer: serves(aBuild(t, "ipa"))}
	config := t.TempDir()
	reader, send, appium, out, host := countingTunnel(t, config, route, func(c *tunnel.Config) { c.ReleaseMargin = 30 * time.Millisecond })
	send(sessionFrame("f1", `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"`+offeredIPhone+`","appium:app":"`+farmBuild(host, "ipa")+`","appium:newCommandTimeout":0.05}]}}`))
	if r := answerOf(t, reader, "f1"); r["status"] != float64(http.StatusOK) {
		t.Fatalf("the session must start: %v %s", r["status"], decodeBody(t, r))
	}
	if len(filesIn(t, shelfAt(config))) != 1 {
		t.Fatal("the session's build must be kept while it lives")
	}
	waitUntil(t, "the release ends the silent session", func() bool { return len(deletesOf(appium)) == 1 })
	waitUntil(t, "the release removes the session's file", func() bool { return len(filesIn(t, shelfAt(config))) == 0 })
	if !strings.Contains(out.String(), "this agent ended it") {
		t.Fatalf("the release says what it did:\n%s", out.String())
	}
}

// The agent's start sweeps what a crash left; its stop removes every file it holds.
func TestTheAgentsStartSweepsAndItsStopRemovesEveryFile(t *testing.T) {
	config := t.TempDir()
	if err := os.MkdirAll(shelfAt(config), 0o700); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(shelfAt(config), "0123456789abcdef0123456789abcdef.ipa")
	os.WriteFile(leftover, []byte("a crash left me"), 0o600)
	route := &buildRoute{answer: serves(aBuild(t, "ipa"))}
	appium := &recorder{reply: `{"value":{"sessionId":"local-session-1","capabilities":{}}}`}
	server := httptest.NewServer(appium.handler())
	t.Cleanup(server.Close)
	farm := newLiveFarm(t)
	var out syncWriter
	drive := tunnel.New(tunnel.Config{FarmURL: farm.server.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-LIVE",
		AppiumURL: server.URL, Out: &out, ReconnectFloor: 10 * time.Millisecond, ReconnectCap: 20 * time.Millisecond,
		Builds: countedShelf(config, route), Phones: allowed.Offering(offeredIPhone)})
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatal("the agent's start must remove the build a crash left behind")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { drive.Run(ctx); close(done) }()
	conn := farm.next(t)
	reader := readFarmSide(conn)
	send(t, conn, sessionFrame("f1", `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"`+offeredIPhone+`","appium:app":"`+farmBuild(mustHost(t, farm.server.URL), "ipa")+`"}]}}`))
	if r := answerOf(t, reader, "f1"); r["status"] != float64(http.StatusOK) {
		t.Fatalf("the session must start: %v %s\n%s", r["status"], decodeBody(t, r), out.String())
	}
	if len(filesIn(t, shelfAt(config))) != 1 {
		t.Fatal("the session's build must be kept while it lives")
	}
	cancel()
	<-done
	if left := filesIn(t, shelfAt(config)); len(left) != 0 {
		t.Fatalf("the agent's stop must remove every build it holds, left %v", left)
	}
}

// An agent with no folder for builds refuses every build in words, and every session without one still starts.
func TestAnAgentWithNoFolderForBuildsRefusesEveryBuildInWords(t *testing.T) {
	route := &buildRoute{answer: serves(aBuild(t, "ipa"))}
	reader, send, appium, out, host := countingTunnel(t, t.TempDir(), route, func(c *tunnel.Config) { c.Builds = tunnel.BuildShelf{} })
	send(sessionFrame("f1", `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"`+offeredIPhone+`","appium:app":"`+farmBuild(host, "ipa")+`"}]}}`))
	refused(t, "a build with no folder", answerOf(t, reader, "f1"), appium, out, "this agent did not hand it on", "no folder of its own")
	send(sessionFrame("f2", `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:udid":"`+offeredIPhone+`"}]}}`))
	if r := answerOf(t, reader, "f2"); r["status"] != float64(http.StatusOK) {
		t.Fatalf("a session with no build still starts: %v", r["status"])
	}
	if len(route.gets()) != 0 {
		t.Fatal("nothing may be fetched")
	}
}
