package farm_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 1: the farm's newerAgentVersion, read at the door and on every heartbeat ──────
//
// The farm's wire: data.newerAgentVersion, a string, present only when
// this agent is older than the configured release, never null. It is read as a version or not at all, and a field this
// agent cannot use never costs it the call: an older agent keeps working whatever the farm writes there.

func answeringWith(t *testing.T, field string) *farm.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/register") {
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"` + trustNodeID + `","apiKey":""` + field + `},"message":"Node registered."}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"provisionTasks":[],"commands":[],"leasedDeviceIds":[]` + field + `},"message":"Heartbeat accepted."}`))
	}))
	t.Cleanup(server.Close)
	return &farm.Client{BaseURL: server.URL, Token: "dummy-token", HTTP: &http.Client{Timeout: 5 * time.Second}}
}

func TestTheNewerReleaseIsReadAtTheDoorAndOnEveryHeartbeat(t *testing.T) {
	for _, c := range []struct{ field, want string }{
		{`,"newerAgentVersion":"0.4.1"`, "0.4.1"},
		{`,"newerAgentVersion":"v0.4.1"`, "v0.4.1"},
		{`,"newerAgentVersion":" 0.4.1 "`, "0.4.1"},
		{``, ""},
		{`,"newerAgentVersion":null`, ""},
		{`,"newerAgentVersion":""`, ""},
		{`,"newerAgentVersion":"latest"`, ""},
		{`,"newerAgentVersion":"0.4.1-rc.1"`, ""},
		{`,"newerAgentVersion":"0.4"`, ""},
		{`,"newerAgentVersion":"0.4.1.2"`, ""},
		{`,"newerAgentVersion":"\u001b[31m0.4.1"`, ""},
		{`,"newerAgentVersion":"0.4.1\nnow update"`, ""},
		{`,"newerAgentVersion":"` + strings.Repeat("9", 40) + `.0.0"`, ""},
		{`,"newerAgentVersion":41`, ""},
		{`,"newerAgentVersion":{"version":"0.4.1"}`, ""},
	} {
		client := answeringWith(t, c.field)
		registered, status, err := client.Register(context.Background(), trustNodeID, "host", farm.Report{})
		if status != farm.CallOK || registered.NodeID != trustNodeID || registered.NewerAgentVersion != c.want {
			t.Errorf("register answering %s: got %+v %v %v, want %q and the call to stand", c.field, registered, status, err, c.want)
		}
		answer, status, err := client.Heartbeat(context.Background(), trustNodeID, farm.Report{})
		if status != farm.CallOK || answer.NewerAgentVersion != c.want {
			t.Errorf("heartbeat answering %s: got %+v %v %v, want %q and the call to stand", c.field, answer, status, err, c.want)
		}
	}
}

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 2: the goodbye ────────────────────────────────────────────────────────────
//
// POST /v1/nodes/{id}/leave, the heartbeat's credential, no body read (the farm §2). Every 200 means the node is offline
// now. An older farm has no such route and answers 404 with an empty body; any answer but a 2xx is "the farm did not take
// the goodbye", and so is no answer.

func TestTheGoodbyeIsOnePostWithTheHeartbeatsCredential(t *testing.T) {
	var method, path, token, contentLength atomic.Value
	answer := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		path.Store(r.URL.Path)
		token.Store(r.Header.Get("X-Agent-Token"))
		body, _ := io.ReadAll(r.Body)
		contentLength.Store(len(body))
		switch status := int(answer.Load()); status {
		case http.StatusOK:
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"` + trustNodeID + `","outcome":"Left"},"message":"This node has left. Its devices are offline now; register again to bring them back."}`))
		case http.StatusUnauthorized:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"success":false,"data":null,"message":"Invalid or missing node credential."}`))
		default:
			w.WriteHeader(status)
		}
	}))
	t.Cleanup(server.Close)
	client := &farm.Client{BaseURL: server.URL, Token: "dummy-token", HTTP: &http.Client{Timeout: 5 * time.Second}}

	for _, c := range []struct {
		answer int
		want   farm.CallStatus
	}{
		{http.StatusOK, farm.CallOK},
		{http.StatusNotFound, farm.CallNotFound}, // someone else's node, no such node, or a farm older than the door
		{http.StatusUnauthorized, farm.CallUnauthorized},
		{http.StatusInternalServerError, farm.CallTransport},
	} {
		answer.Store(int32(c.answer))
		status, err := client.Leave(context.Background(), trustNodeID)
		if status != c.want || (c.want != farm.CallOK && err == nil) {
			t.Errorf("a %d to the goodbye: got %v %v, want %v", c.answer, status, err, c.want)
		}
		if method.Load() != http.MethodPost || path.Load() != "/v1/nodes/"+trustNodeID+"/leave" ||
			token.Load() != "dummy-token" || contentLength.Load() != 0 {
			t.Fatalf("the goodbye is POST /v1/nodes/{id}/leave with X-Agent-Token and no body: %v %v %v %v bytes",
				method.Load(), path.Load(), token.Load(), contentLength.Load())
		}
	}
}

func TestAFarmThatDoesNotAnswerTheGoodbyeCostsOnlyTheBound(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	client := &farm.Client{BaseURL: server.URL, Token: "dummy-token", HTTP: &http.Client{Timeout: 15 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	status, err := client.Leave(ctx, trustNodeID)
	if status == farm.CallOK || err == nil || time.Since(started) > 2*time.Second {
		t.Fatalf("a goodbye nobody answers ends at the caller's bound: %v %v after %v", status, err, time.Since(started))
	}
}
