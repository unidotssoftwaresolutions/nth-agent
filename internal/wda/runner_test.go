package wda_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// WHAT_WE_LEAVE_RUNNING_ON_YOUR_PHONE item 1 — the probe, over the envelope WebDriverAgent actually sends.
//
// The bodies below are WDA's own shape, read off the source that produces them:
// FBSessionCommands.m:269 builds the `value` object and FBResponsePayload.m:94 sets
// `response["sessionId"] = [FBSession activeSession].identifier ?: NSNull.null`. THE LIVE STATES COULD NOT ALL BE
// OBSERVED: on 2026-08-24 neither of the operator's iPhones had anything listening on the device's WDA port
// (a forward answered "connection reset by peer"), so `not-listening` is measured and `idle` / `in-use` are driven
// here against the exact envelope instead. That limit is in the report; it is not papered over.

func serving(t *testing.T, status int, body string) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &http.Client{Timeout: 2 * time.Second}
}

const idleBody = `{"value":{"ready":true,"message":"WebDriverAgent is ready to accept commands","state":"success",` +
	`"os":{"name":"iOS","version":"26.6"},"build":{"productBundleIdentifier":"com.example.WebDriverAgentRunner"}},` +
	`"sessionId":null}`

const inUseBody = `{"value":{"ready":true,"message":"WebDriverAgent is ready to accept commands","state":"success"},` +
	`"sessionId":"C0FFEE00-1111-2222-3333-444455556666"}`

func TestAnIdleRunnerSaysSoAboutItself(t *testing.T) {
	server, client := serving(t, http.StatusOK, idleBody)
	got := wda.ReadStatus(context.Background(), client, server.URL)

	if got.State != wda.RunnerIdle {
		t.Fatalf("a null sessionId is WebDriverAgent saying nothing is driving it, got %v (%s)", got.State, got.Reason)
	}
}

func TestARunnerWithASessionIsInUseAndNamesIt(t *testing.T) {
	server, client := serving(t, http.StatusOK, inUseBody)
	got := wda.ReadStatus(context.Background(), client, server.URL)

	if got.State != wda.RunnerInUse {
		t.Fatalf("a session id means something is driving it, got %v", got.State)
	}
	if got.SessionID != "C0FFEE00-1111-2222-3333-444455556666" {
		t.Fatalf("the session it named must survive the read, got %q", got.SessionID)
	}
}

// A MISSING FIELD AND AN EXPLICIT NULL ARE THE SAME ANSWER; NEITHER IS AN EMPTY STRING. An older WDA that omits
// the key must not read as a session whose id happens to be blank.
func TestAMissingSessionIdIsIdleAndNotAnEmptySession(t *testing.T) {
	server, client := serving(t, http.StatusOK, `{"value":{"ready":true}}`)
	if got := wda.ReadStatus(context.Background(), client, server.URL); got.State != wda.RunnerIdle {
		t.Fatalf("an absent sessionId is idle, got %v", got.State)
	}
	server2, client2 := serving(t, http.StatusOK, `{"value":{"ready":true},"sessionId":""}`)
	if got := wda.ReadStatus(context.Background(), client2, server2.URL); got.State != wda.RunnerIdle {
		t.Fatalf("an empty sessionId is idle, got %v", got.State)
	}
}

// NOTHING LISTENING IS A REAL ANSWER, AND IT IS NOT "I COULD NOT TELL". A closed port is the state a phone is in
// after we have already cleaned up — measured live today as "connection reset by peer" over a usbmuxd forward.
func TestAClosedPortIsNotListeningRatherThanUnknown(t *testing.T) {
	server, client := serving(t, http.StatusOK, idleBody)
	url := server.URL
	server.Close() // the port is now closed, which is exactly the case this is about

	got := wda.ReadStatus(context.Background(), client, url)
	if got.State != wda.RunnerNotListening {
		t.Fatalf("a port nobody answers means there is nothing to stop, got %v", got.State)
	}
}

// AN ANSWER NOBODY COULD READ IS UNKNOWN — never idle, because the decision treats idle as permission to stop.
func TestAnUnreadableAnswerIsUnknownAndNeverIdle(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
	}{
		{"not json", http.StatusOK, "<html>who are you</html>"},
		{"an error status", http.StatusInternalServerError, idleBody},
		{"an empty body", http.StatusOK, ""},
	} {
		server, client := serving(t, c.status, c.body)
		got := wda.ReadStatus(context.Background(), client, server.URL)
		if got.State != wda.RunnerUnknown {
			t.Fatalf("%s must be unknown, got %v", c.name, got.State)
		}
		if got.Reason == "" {
			t.Fatalf("%s: an unknown must say why", c.name)
		}
	}
}

// THE STOP IS A REQUEST TO WDA, NOT A KILL — and a connection that drops while it is shutting itself down is the
// success case as often as the failure case, so it is never reported as a failure.
func TestShutdownAsksWdaAndDoesNotClaimMoreThanItDid(t *testing.T) {
	var asked string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := wda.Shutdown(context.Background(), &http.Client{Timeout: time.Second}, server.URL); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if asked != "/wda/shutdown" {
		t.Fatalf("it must ask WebDriverAgent's own shutdown route, got %q", asked)
	}

	server.Close()
	if err := wda.Shutdown(context.Background(), &http.Client{Timeout: time.Second}, server.URL); err != nil {
		t.Fatalf("a dropped connection during its own shutdown is not a failure to report: %v", err)
	}
}
