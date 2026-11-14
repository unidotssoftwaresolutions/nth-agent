package connect_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── A STOP IS NOT AN OUTAGE (NTH_AGENT_0_4_4 item 2) ────────────────────────────────────────────────────────────
//
// Ctrl-C cancels the call in flight. Up to 0.4.3 the agent then said "cannot reach the farm (Post "…": context canceled) —
// retrying with backoff" while it was stopping: the farm was there, and nothing was going to retry. A registration or a
// heartbeat that fails because the agent is stopping says nothing now. These seals time the stop into the call: the farm
// holds the call open until the agent lets go of it, and the test stops the agent while it waits.

// holdingFarm holds the first call to path open until the agent lets go of it, and says when that call arrived. Every
// other call it takes.
func holdingFarm(t *testing.T, path string) (*httptest.Server, <-chan struct{}) {
	arrived := make(chan struct{}, 1)
	var held atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, path) && held.CompareAndSwap(false, true) {
			// The body read whole, so the server notices when the agent hangs up.
			_, _ = io.Copy(io.Discard, r.Body)
			arrived <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-time.After(30 * time.Second):
			}
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			_, _ = io.WriteString(w, `{"success":true,"data":{"nodeId":"`+limitNode+`"}}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			_, _ = io.WriteString(w, `{"success":true,"data":{"provisionTasks":[],"commands":[]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, arrived
}

func TestAStopDuringACallSaysNothingAboutTheFarm(t *testing.T) {
	for _, call := range []string{"/register", "/heartbeat"} {
		server, arrived := holdingFarm(t, call)
		var out syncBuilder
		evidence := &recordedEvidence{}
		cfg := connect.Config{
			Client:   &farm.Client{BaseURL: server.URL, Token: testToken, HTTP: server.Client()},
			NodeID:   limitNode,
			HostName: "omers-macbook",
			Probes:   func(context.Context) []inventory.Outcome { return nil },
			Interval: time.Hour, BackoffBase: time.Hour, BackoffCeiling: time.Hour,
			Sleep: func(ctx context.Context, d time.Duration) {
				select {
				case <-ctx.Done():
				case <-time.After(d):
				}
			},
			Out:    &out,
			Driver: &connect.Driver{Probe: func(context.Context) error { return nil }, TunnelUp: func() bool { return true }, Evidence: evidence},
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- connect.Run(ctx, cfg) }()
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatalf("%s: the call never reached the farm:\n%s", call, out.String())
		}
		before := out.String()
		cancel() // Ctrl-C, while the farm holds the call
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: a stop is not an error: %v", call, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the agent did not stop", call)
		}
		if after := strings.TrimPrefix(out.String(), before); after != "" {
			t.Fatalf("%s: the agent was stopping, and said:\n%s", call, after)
		}
		if strings.Contains(before, "cannot reach the farm") {
			t.Fatalf("%s: the farm answered every call before the stop:\n%s", call, before)
		}
	}
}
