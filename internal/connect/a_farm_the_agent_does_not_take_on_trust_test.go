package connect_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
)

// A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST — the node id the farm answers a registration with is printed only in
// the form this agent sent it, and nothing the farm says reaches the console with a terminal control in it.

func farmNamingTheNode(t *testing.T, nodeID string) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/register") {
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"` + nodeID + `","apiKey":""}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"provisionTasks":[],"commands":[]}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTheNodeIDIsPrintedOnlyAsThisAgentSentIt(t *testing.T) {
	const sent = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, c := range []struct {
		name, answered string
		saysOther      bool
	}{
		{"escape sequences in place of the id", `\u001b[2J\u001b]0;owned\u0007\u009b31mpwned\u202e`, true},
		{"another well-formed id", "11111111-2222-3333-4444-555555555555", true},
		{"the id it was sent, in capitals", strings.ToUpper(sent), false},
		{"the id it was sent", sent, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			if err := connect.Run(context.Background(), config(farmNamingTheNode(t, c.answered), &out, 1)); err != nil {
				t.Fatalf("run: %v", err)
			}
			said := out.String()
			if strings.ContainsAny(said, "\x1b\x07\u009b\u202e") {
				t.Fatalf("a terminal control from the farm reached the console: %q", said)
			}
			if !strings.Contains(said, "registered as node "+sent+"\n") {
				t.Fatalf("the agent must name itself by the id it sent:\n%s", said)
			}
			if got := strings.Contains(said, "a different node id than the one this agent sent"); got != c.saysOther {
				t.Fatalf("whether the agent says the farm named another id: got %v, want %v:\n%s", got, c.saysOther, said)
			}
			if strings.Contains(said, "pwned") || strings.Contains(said, "11111111-2222") {
				t.Fatalf("the farm's other id must not be printed:\n%s", said)
			}
		})
	}
}
