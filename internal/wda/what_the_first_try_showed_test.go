package wda_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// WHAT_THE_FIRST_TRY_SHOWED, decision A6. The agent asks what answers on an iPhone's WebDriverAgent port on the phone, and
// takes it for this machine's WebDriverAgent only when it says it is the build this machine declared
// (--ios-wda-bundle-id). WebDriverAgent says so itself: its /status carries value.build.productBundleIdentifier, the
// WDA_PRODUCT_BUNDLE_IDENTIFIER Appium starts it with (appium-webdriveragent 13.2.0, FBSessionCommands.m:269-281, set
// from updatedWDABundleId in lib/xcodebuild.ts:446; the preinstalled launch adds the .xctrunner suffix, webdriveragent.ts:180).
// Every release from 4.10.2 to 13.2.0 carries it (the field predates the changelog's first entry, and no entry removes
// it). Nothing listening is no stranger; anything that answers and does not say it is the declared build is one.

const declared = "com.example.WebDriverAgentRunner"

// statusOf is WebDriverAgent 13.2.0's /status, as FBSessionCommands.m builds it, for a bundle and a session.
func statusOf(bundle, session string) string {
	sessionID := "null"
	if session != "" {
		sessionID = `"` + session + `"`
	}
	build := `"time":"Nov 14 2026 10:00:00","version":"13.2.0"`
	if bundle != "" {
		build += `,"productBundleIdentifier":"` + bundle + `"`
	}
	return `{"value":{"ready":true,"message":"WebDriverAgent is ready to accept commands","state":"success",` +
		`"os":{"name":"iOS","version":"26.6","sdkVersion":"26.0","testmanagerdVersion":65535},"ios":{"ip":null},` +
		`"build":{` + build + `},"device":"iphone"},"sessionId":` + sessionID + `}`
}

func TestWhatAnswersOnTheWebDriverAgentPortIsTakenForItOnlyWhenItSaysItIsTheDeclaredBuild(t *testing.T) {
	answering := func(status int, body string) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return server.URL
	}
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { silent.Close() })
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
		}
	}()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedURL := "http://" + closed.Addr().String()
	closed.Close()

	client := &http.Client{Timeout: 300 * time.Millisecond}
	for _, c := range []struct {
		name    string
		url     string
		want    wda.IdentityKind
		session string
	}{
		{"the declared build, idle", answering(200, statusOf(declared, "")), wda.IdentityDeclared, ""},
		{"the declared build, holding a session", answering(200, statusOf(declared, "6C1E2A9B-0000-4000-8000-00000000AB12")), wda.IdentityDeclared, "6C1E2A9B-0000-4000-8000-00000000AB12"},
		{"the declared build, preinstalled (.xctrunner)", answering(200, statusOf(declared+".xctrunner", "")), wda.IdentityDeclared, ""},
		{"another WebDriverAgent build", answering(200, statusOf("com.facebook.WebDriverAgentRunner", "")), wda.IdentityStranger, ""},
		{"a build whose name only starts like the declared one", answering(200, statusOf(declared+"Evil", "")), wda.IdentityStranger, ""},
		{"WebDriverAgent-shaped with no build", answering(200, `{"value":{"ready":true},"sessionId":null}`), wda.IdentityStranger, ""},
		{"any JSON at all", answering(200, `{}`), wda.IdentityStranger, ""},
		{"not JSON", answering(200, `<html>hello</html>`), wda.IdentityStranger, ""},
		{"an error page", answering(500, statusOf(declared, "")), wda.IdentityStranger, ""},
		{"a listener that never answers", "http://" + silent.Addr().String(), wda.IdentityStranger, ""},
		{"nothing listening", closedURL, wda.IdentityNothing, ""},
	} {
		got := wda.Identify(context.Background(), client, c.url, declared)
		if got.Is != c.want || got.SessionID != c.session {
			t.Errorf("%s: read as %d with session %q, want %d with %q (%+v)", c.name, got.Is, got.SessionID, c.want, c.session, got)
		}
		if got.Is == wda.IdentityStranger && got.Found == "" {
			t.Errorf("%s: a stranger is said in words, and this one was not: %+v", c.name, got)
		}
	}
	if got := wda.Identify(context.Background(), client, answering(200, statusOf(declared, "")), ""); got.Is != wda.IdentityStranger {
		t.Errorf("with no bundle declared nothing is this machine's WebDriverAgent, got %+v", got)
	}
}
