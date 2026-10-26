package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// NTH_AGENT_0_4_4 item 1 — THE GOODBYE, WHEN THE FARM'S ADDRESS ANSWERS IN ITS PLACE. A stop while the farm's API restarts
// meets nginx's 502 page at the leave door. The goodbye said "the farm did not take this machine's goodbye (the farm
// answered 502 and gave no reason this agent could read)", and a 503 page was "it was busy". The farm was not reached; the
// line says so. The farm's own answers keep their lines (an_agent_that_says_why_the_farm_said_no_test.go).
func TestAGoodbyeAGatewayAnswersIsTheFarmNotReached(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		for _, body := range []string{farmtest.GatewayPage(status), ""} {
			want := "could not reach the farm to say goodbye (the farm's address answered " + itoa(status) +
				", not the farm), so it takes the phones off offer within a minute instead\n"
			if got := goodbyeAnswered(t, status, body); got != want {
				t.Fatalf("%d (%d bytes of body):\n got %q\nwant %q", status, len(body), got, want)
			}
		}
	}
	// The farm's own 503 and 429, each with its sentence, are the farm's: not taken, in its words.
	said := farmtest.Read(t)
	for status, sentence := range map[int]string{http.StatusServiceUnavailable: said.RegistryBusy, http.StatusTooManyRequests: said.TooManyDevices} {
		got := goodbyeAnswered(t, status, farmtest.Envelope(sentence))
		if !strings.HasPrefix(got, "the farm did not take this machine's goodbye ("+itoa(status)+"). It says: “"+sentence+"”") {
			t.Fatalf("%d with the farm's sentence keeps its line: %q", status, got)
		}
	}
}
