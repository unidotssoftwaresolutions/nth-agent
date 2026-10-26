package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// NTH_AGENT_0_4_2 item 2 — THE GOODBYE'S 404 SAYS WHAT IS TRUE. Today's farm answers the leave of a node it does not know
// for this workspace with "Unknown node '…'. Re-register.", and the line quoted it at a stop, where nothing will register.
// At a stop no farm sentence that tells the person to do something is quoted: a 404 with a sentence says plainly that the
// farm did not know this node. A 404 with none is an older farm, which has no leave door, and keeps its line.
func TestAGoodbyeTheFarmDidNotKnowSaysSoAndQuotesNoAdvice(t *testing.T) {
	said := farmtest.Read(t)
	out := goodbyeAnswered(t, http.StatusNotFound, farmtest.Envelope(said.UnknownNode(goodbyeNode)))
	if want := "the farm did not know this node (404), so it took no goodbye — this machine's phones go off offer within a minute\n"; out != want {
		t.Fatalf("a goodbye the farm answered 404 with its sentence:\n got %q\nwant %q", out, want)
	}
	if strings.Contains(out, "Re-register") || strings.Contains(out, "“") {
		t.Fatalf("at a stop the line quoted the farm's advice:\n%s", out)
	}
	if got, want := goodbyeAnswered(t, http.StatusNotFound, ""), "the farm did not take this machine's goodbye (it answered 404, which an older farm does), so it takes the phones off offer within a minute instead\n"; got != want {
		t.Fatalf("a 404 with no sentence is an older farm's:\n got %q\nwant %q", got, want)
	}
}
