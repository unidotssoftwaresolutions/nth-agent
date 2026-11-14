package farm_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// ── A GATEWAY'S ANSWER IS NOT THE FARM'S (NTH_AGENT_0_4_4 item 1) ───────────────────────────────────────────────
//
// When the farm's API restarts, nginx in front of it answers 502 (or 504, or 503) with its own page. Since 0.4.2 the agent
// read every non-2xx as the farm's answer, so the person read "the farm did not take this machine's heartbeat (502)" and
// "the farm refused the drive tunnel (502)" about a farm that was not there. The farm answers every refusal in its
// envelope (ApiResponse: `success`, always written, beside `message`), so a 502, 503 or 504 without it is the farm's
// address answering in the farm's place: the farm was not reached. The status (CallTransport, CallBusy) does not change.

// gatewayBodies are what a farm's address answers with when the farm is not behind it: nginx's page, nothing, and JSON
// that is not the farm's envelope.
func gatewayBodies(status int) map[string]string {
	return map[string]string{
		"nginx's page":          farmtest.GatewayPage(status),
		"no body":               "",
		"JSON of another shape": `{"error":"upstream unavailable"}`,
		"success not a boolean": `{"success":"false","message":"Bad gateway"}`,
		"a JSON null":           "null",
	}
}

func TestAGatewaysAnswerIsTheFarmNotReached(t *testing.T) {
	for status, want := range map[int]farm.CallStatus{
		http.StatusBadGateway:         farm.CallTransport,
		http.StatusServiceUnavailable: farm.CallBusy,
		http.StatusGatewayTimeout:     farm.CallTransport,
	} {
		for name, body := range gatewayBodies(status) {
			for _, call := range []string{"register", "heartbeat", "leave"} {
				got, err := answeredBy(t, call, status, body)
				if got != want {
					t.Errorf("%s, %d, %s: came out as %v — the backoff must not change (%v)", call, status, name, got, want)
				}
				if s, reason, ok := farm.WhatTheFarmSaid(err); ok {
					t.Errorf("%s, %d, %s: read as the farm's answer (%d %q) — the farm was not reached", call, status, name, s, reason)
				}
				if err == nil || err.Error() != fmt.Sprintf("the farm's address answered %d, not the farm", status) {
					t.Errorf("%s, %d, %s: the error says who answered: %v", call, status, name, err)
				}
			}
		}
	}
}

// THE FARM'S OWN 502, 503 OR 504 IS THE FARM'S ANSWER: in its envelope, with its sentence or without one.
func TestTheFarmsOwnGatewayStatusIsStillTheFarmsAnswer(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		for body, sentence := range map[string]string{
			farmtest.Envelope("This machine's previous heartbeat is still being applied."): "This machine's previous heartbeat is still being applied.",
			farmtest.EnvelopeSayingNothing: "",
			`{"success":false}`:            "",
		} {
			for _, call := range []string{"register", "heartbeat", "leave"} {
				_, err := answeredBy(t, call, status, body)
				if got, reason, ok := farm.WhatTheFarmSaid(err); !ok || got != status || reason != sentence {
					t.Errorf("%s, %d in the farm's envelope (%s): want the farm's answer %q, got %d %q %v", call, status, body, sentence, got, reason, ok)
				}
			}
		}
	}
}

// EVERY OTHER STATUS IS THE FARM'S, whatever its body: a 429, nginx's 413, a 500 page, a 400, a 404.
func TestEveryOtherStatusIsStillTheFarmsAnswerWhateverItsBody(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusRequestEntityTooLarge, http.StatusInternalServerError,
		http.StatusBadRequest, http.StatusNotFound} {
		for _, body := range []string{farmtest.GatewayPage(status), ""} {
			_, err := answeredBy(t, "heartbeat", status, body)
			if got, reason, ok := farm.WhatTheFarmSaid(err); !ok || got != status || reason != "" {
				t.Errorf("%d with no envelope: want the farm's answer with no reason, got %d %q %v", status, got, reason, ok)
			}
		}
	}
}
