package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO item 6 — THE GOODBYE A FARM DID NOT TAKE, IN THE FARM'S WORDS. The line used
// to word every answer by the agent's own guess: a 404 was "which an older farm does" even when today's farm said the node
// was not this workspace's. When the farm gave a sentence, the line carries it; when it gave none, the line is today's.

const goodbyeNode = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// goodbyeAnswered is what goodbye says when the farm answers the leave with status and body.
func goodbyeAnswered(t *testing.T, status int, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	var out strings.Builder
	goodbye(&fakeCaptures{}, &farm.Client{BaseURL: server.URL, Token: "sk-agent-GOODBYE", HTTP: server.Client()}, goodbyeNode, &out, leaveBound)
	return out.String()
}

func TestAGoodbyeTheFarmDidNotTakeSaysTheFarmsSentence(t *testing.T) {
	said := farmtest.Read(t)
	for _, c := range []struct {
		status   int
		sentence string
	}{
		// (A 404's sentence is no longer quoted at a stop: NTH_AGENT_0_4_2, TestAGoodbyeTheFarmDidNotKnowSaysSoAndQuotesNoAdvice.)
		{http.StatusUnauthorized, "This agent token has expired. Create a new one in Settings → Agent tokens and start the agent with it."},
		{http.StatusTooManyRequests, said.TooManyDevices},
		{http.StatusServiceUnavailable, said.RegistryBusy},
		{http.StatusBadRequest, "A body is required."},
	} {
		out := goodbyeAnswered(t, c.status, farmtest.Envelope(c.sentence))
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "the farm did not take this machine's goodbye") {
			t.Fatalf("%d: one line says the goodbye was not taken:\n%s", c.status, out)
		}
		if !strings.Contains(out, "“"+c.sentence+"”") {
			t.Fatalf("%d: the farm said why, and the line words it by a guess of its own instead:\n%s", c.status, out)
		}
		if strings.Contains(out, "older farm") || strings.Contains(out, "it was busy") {
			t.Fatalf("%d: the farm's sentence and a guess, side by side:\n%s", c.status, out)
		}
	}
}

// WHEN THE FARM GAVE NO SENTENCE, TODAY'S LINE — word for word. (A 503 with no body at all is a gateway's since NTH_AGENT_0_4_4,
// not the farm's: a_gateways_answer_is_not_the_farms_test.go. The farm's own 503 with no sentence is its envelope saying nothing.)
func TestAGoodbyeWithNoSentenceKeepsTodaysLine(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusNotFound:           "the farm did not take this machine's goodbye (it answered 404, which an older farm does), so it takes the phones off offer within a minute instead\n",
		http.StatusServiceUnavailable: "the farm did not take this machine's goodbye (it was busy), so it takes the phones off offer within a minute instead\n",
		http.StatusUnauthorized:       "the farm did not take this machine's goodbye (it refused this agent's token), so it takes the phones off offer within a minute instead\n",
	} {
		body := ""
		if status == http.StatusServiceUnavailable {
			body = farmtest.EnvelopeSayingNothing
		}
		if got := goodbyeAnswered(t, status, body); got != want {
			t.Fatalf("%d with no body:\n got %q\nwant %q", status, got, want)
		}
	}
}
