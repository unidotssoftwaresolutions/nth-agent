package farm_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO — the farm's side of it: every answer the farm did not take a call with is read
// the way a 401 is (a little of it, through SafeForTerminal, the token never printed), and its sentence leaves post()
// beside the status. Only a 401 was read before; a 429 left as "the farm answered 429" and nothing else.

const limitNodeID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// answeredBy makes one call against a farm that answers status with body, and returns what came out of post().
func answeredBy(t *testing.T, call string, status int, body string) (farm.CallStatus, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client := &farm.Client{BaseURL: server.URL, Token: secretToken, HTTP: server.Client()}
	switch call {
	case "register":
		_, s, err := client.Register(context.Background(), limitNodeID, "host", farm.Report{})
		return s, err
	case "heartbeat":
		_, s, err := client.Heartbeat(context.Background(), limitNodeID, farm.Report{})
		return s, err
	default:
		return client.Leave(context.Background(), limitNodeID)
	}
}

// EVERY ANSWER CARRIES THE FARM'S SENTENCE, at every call, with the status beside it.
func TestEveryAnswerTheFarmDidNotTakeCarriesItsSentence(t *testing.T) {
	said := farmtest.Read(t)
	for _, c := range []struct {
		status   int
		sentence string
	}{
		{http.StatusTooManyRequests, said.TooManyMachines},
		{http.StatusTooManyRequests, said.TooManyDevices},
		{http.StatusServiceUnavailable, said.HeartbeatTooSoon(1.2)},
		{http.StatusServiceUnavailable, said.RegistryBusy},
		{http.StatusNotFound, said.UnknownNode(limitNodeID)},
		{http.StatusBadRequest, "A registration body is required."},
		{http.StatusRequestEntityTooLarge, "Too large."},
	} {
		for _, call := range []string{"register", "heartbeat", "leave"} {
			status, err := answeredBy(t, call, c.status, farmtest.Envelope(c.sentence))
			if status == farm.CallOK || err == nil {
				t.Fatalf("%s answered %d came out as taken: %v %v", call, c.status, status, err)
			}
			if !strings.Contains(err.Error(), c.sentence) || !strings.Contains(err.Error(), itoa(c.status)) {
				t.Fatalf("%s answered %d with %q, and what came out of post() was %q — the person would never read why",
					call, c.status, c.sentence, err.Error())
			}
			got, reason, ok := farm.WhatTheFarmSaid(err)
			if !ok || got != c.status || reason != c.sentence {
				t.Fatalf("%s answered %d: the status and the sentence must be readable apart, got %d %q %v", call, c.status, got, reason, ok)
			}
		}
	}
}

// A 429 IS ITS OWN STATUS: the farm answered, at a limit. Everything else keeps the status it had.
func TestA429HasItsOwnStatusAndTheRestKeepTheirs(t *testing.T) {
	for _, c := range []struct {
		answer int
		want   farm.CallStatus
	}{
		{http.StatusTooManyRequests, farm.CallTooMany},
		{http.StatusServiceUnavailable, farm.CallBusy},
		{http.StatusNotFound, farm.CallNotFound},
		{http.StatusUnauthorized, farm.CallUnauthorized},
		{http.StatusRequestEntityTooLarge, farm.CallTransport},
		{http.StatusBadRequest, farm.CallTransport},
		{http.StatusBadGateway, farm.CallTransport},
	} {
		for _, call := range []string{"register", "heartbeat", "leave"} {
			if status, _ := answeredBy(t, call, c.answer, farmtest.Envelope("No.")); status != c.want {
				t.Errorf("%s answered %d came out as %v, want %v", call, c.answer, status, c.want)
			}
		}
	}
}

// NO ENVELOPE, NO SENTENCE: nginx's 413, a body that is not the farm's — said as none, with the status. (A 502, 503 or 504
// with no envelope is not the farm's answer at all since NTH_AGENT_0_4_4: a_gateways_answer_is_not_the_farms_test.go. The
// farm's own 502 and 503 with no sentence stay here.)
func TestAnAnswerWithNoSentenceSaysSoAndNamesItsStatus(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"nginx's 413":                 {http.StatusRequestEntityTooLarge, "<html>\r\n<head><title>413 Request Entity Too Large</title></head>\r\n<body>\r\n<center><h1>413 Request Entity Too Large</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"},
		"the farm's 502, no sentence": {http.StatusBadGateway, farmtest.EnvelopeSayingNothing},
		"no message":                  {http.StatusTooManyRequests, `{"success":false,"data":null}`},
		"the farm's 503, no sentence": {http.StatusServiceUnavailable, `{"success":false,"data":null}`},
	} {
		_, err := answeredBy(t, "heartbeat", c.status, c.body)
		if err == nil || !strings.Contains(err.Error(), "gave no reason this agent could read") || !strings.Contains(err.Error(), itoa(c.status)) {
			t.Fatalf("%s: want the status and that no reason was given, got %v", name, err)
		}
		if strings.Contains(err.Error(), "nginx") || strings.Contains(err.Error(), "<") {
			t.Fatalf("%s: a body that is not the farm's envelope was carried as a sentence: %v", name, err)
		}
		if status, reason, ok := farm.WhatTheFarmSaid(err); !ok || status != c.status || reason != "" {
			t.Fatalf("%s: an answer with no sentence still says its status, and no reason: %d %q %v", name, status, reason, ok)
		}
	}
}

// THE SAME DOOR AS A 401: bounded, safe for a terminal, and the token never comes back out.
func TestEveryAnswersSentenceGoesThroughTheOneDoor(t *testing.T) {
	hostile := "\x1b[2J\x1b]0;owned\x07At a limit %s %v " + secretToken + " \x1b[31mnow\x1b[0m\r\nregistered"
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusNotFound, http.StatusBadRequest} {
		_, err := answeredBy(t, "heartbeat", status, envelopeSaying(hostile))
		_, reason, _ := farm.WhatTheFarmSaid(err)
		if reason != "At a limit %s %v «token» now registered" {
			t.Fatalf("%d: the sentence was not made safe the way a 401's is: %q", status, reason)
		}
		if strings.Contains(err.Error(), "VERYSECRET") {
			t.Fatalf("%d: the token came back out of post(): %q", status, err.Error())
		}
	}
	// Bounded: the farm's sentence sits in the first kilobytes; an endless body is read no further than a 401's is.
	long := envelopeSaying(strings.Repeat("x", farm.RefusalBodyLimit))
	if _, err := answeredBy(t, "heartbeat", http.StatusTooManyRequests, long); err == nil || strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("a body past the bound was read as a sentence: %v", err)
	}
}

// THE 401 DOES NOT CHANGE: still CallUnauthorized, still a *RefusedError, still its own words.
func TestThe401IsWhatItWas(t *testing.T) {
	status, err := answeredBy(t, "heartbeat", http.StatusUnauthorized, envelopeSaying(expiredSentence))
	var refused *farm.RefusedError
	if status != farm.CallUnauthorized || !errors.As(err, &refused) || refused.Reason != expiredSentence ||
		err.Error() != "the farm did not accept the credential (401): "+expiredSentence {
		t.Fatalf("the 401 changed: %v %T %v", status, err, err)
	}
}

func itoa(n int) string {
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
