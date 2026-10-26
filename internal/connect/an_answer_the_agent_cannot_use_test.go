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

// ── A 2xx THE AGENT CANNOT USE (NTH_AGENT_0_4_3 item 5) ────────────────────────────────────────────────────────────
//
// The farm took the call and answered 200, and the agent could not use the answer: it could not be read, it was larger
// than the agent reads, it was not the farm's envelope, or it had another shape. Up to 0.4.2 the person read "cannot reach
// the farm" about a farm that had answered. The line now says the farm answered, with its status, why the answer could not
// be used, and that the agent retries — on today's backoff, once per answer (it changes nothing else).

// unusableFarm answers each registration with the next of its answers, then takes it, and takes every heartbeat.
type unusableFarm struct {
	server    *httptest.Server
	answers   []func(w http.ResponseWriter)
	registers atomic.Int32
}

func newUnusableFarm(t *testing.T, answers ...func(w http.ResponseWriter)) *unusableFarm {
	f := &unusableFarm{answers: answers}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			n := int(f.registers.Add(1))
			if n <= len(f.answers) {
				f.answers[n-1](w)
				return
			}
			_, _ = io.WriteString(w, `{"success":true,"data":{"nodeId":"`+limitNode+`"}}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			_, _ = io.WriteString(w, `{"success":true,"data":{"provisionTasks":[],"commands":[]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func ok(body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// cutShort promises a body and closes the connection partway through it.
func cutShort(w http.ResponseWriter) {
	conn, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 400\r\n\r\n{\"success\":tr")
	_ = buffered.Flush()
	_ = conn.Close()
}

func TestA2xxTheAgentCannotUseIsAnAnswerNotAFarmGone(t *testing.T) {
	f := newUnusableFarm(t,
		ok("<html>a page</html>"),
		ok(`{"success":true,"data":`+strings.Repeat(" ", farm.AnswerBodyLimit)+`{"nodeId":"`+limitNode+`"}}`),
		ok(`{"success":true,"data":{"nodeId":5}}`),
		ok(`{"success":true,"data":{"nodeId":5}}`), // the same answer again is not said again
		cutShort,
	)
	var out strings.Builder
	w := &waits{}
	cfg := connect.Config{
		Client:   &farm.Client{BaseURL: f.server.URL, Token: testToken, HTTP: f.server.Client()},
		NodeID:   limitNode,
		HostName: "omers-macbook",
		Probes:   func(context.Context) []inventory.Outcome { return nil },
		Interval: time.Second, BackoffBase: time.Millisecond, BackoffCeiling: 8 * time.Millisecond,
		MaxTicks: 1,
		Sleep:    w.sleep,
		Out:      &out,
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("an answer the agent could not use stopped it: %v\n%s", err, out.String())
	}
	transcript := out.String()
	if strings.Contains(transcript, "cannot reach the farm") || strings.Contains(transcript, "connection to the farm restored") {
		t.Fatalf("the farm answered every call, and the person was told it could not be reached:\n%s", transcript)
	}
	const retry = " — retrying with backoff"
	for _, want := range []string{
		"the farm answered this machine's registration (200), and this agent could not use the answer: it was not readable (invalid character '<' looking for beginning of value)" + retry,
		"the farm answered this machine's registration (200), and this agent could not use the answer: it was larger than the 1048576 bytes this agent reads" + retry,
		"the farm answered this machine's registration (200), and this agent could not use the answer: it had an unexpected shape (json: cannot unmarshal number into Go struct field registerData.nodeId of type string)" + retry,
		"the farm answered this machine's registration (200), and this agent could not use the answer: it could not be read (unexpected EOF)" + retry,
		"the farm took this machine's registration",
	} {
		if strings.Count(transcript, want+"\n") != 1 {
			t.Errorf("want once:\n  %s\nin:\n%s", want, transcript)
		}
	}
	if got := f.registers.Load(); got != 6 {
		t.Fatalf("the agent asks again until the farm's answer can be used: %d registrations", got)
	}
	// TODAY'S BACKOFF: one wait per unusable answer, doubling from the base to the ceiling with up to half again of jitter,
	// then the tick.
	backs := w.all()
	if len(backs) != 6 || backs[5] != time.Second {
		t.Fatalf("one backoff per answer the agent could not use, then the tick, got %v", backs)
	}
	for i, d := range backs[:5] {
		floor := min(time.Millisecond<<i, 8*time.Millisecond)
		if d < floor || d > floor*3/2 {
			t.Fatalf("wait %d was %v: an answer the agent cannot use backs off as before (%v…%v): %v", i+1, d, floor, floor*3/2, backs)
		}
	}
}
