package connect_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// ── NTH_AGENT_0_4_2 item 2: THE 404 LINES SAY WHAT IS TRUE ──────────────────────────────────────────────────────────
//
// A heartbeat's 404 was always "the farm forgot this node (it may have been purged)", and a registration's always "it may
// belong to another workspace": guesses, where the farm says which it was (heartbeat) or there is one cause (register).

const reRegistering = " — re-registering with the same identity"

// HEARTBEAT: the farm's sentence when it gave one, then what the agent does; today's line when it gave none.
func TestAHeartbeat404SaysTheFarmsSentenceThenRegistersAgain(t *testing.T) {
	said := farmtest.Read(t)
	for _, c := range []struct {
		name, body, want string
	}{
		{"the node said it was leaving", farmtest.Envelope(said.LeftHeartbeat),
			"the farm did not apply this heartbeat (404). It says: " + quoted(said.LeftHeartbeat) + reRegistering},
		{"the node is unknown", farmtest.Envelope(said.UnknownNode(limitNode)),
			"the farm did not apply this heartbeat (404). It says: " + quoted(said.UnknownNode(limitNode)) + reRegistering},
		{"no sentence", "",
			"the farm forgot this node (it may have been purged) — re-registering with the same identity"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newLimitFarm(t)
			f.heartbeat.Store(&answer{status: http.StatusNotFound, body: c.body})
			var out strings.Builder
			if err := connect.Run(context.Background(), limitConfig(f, &out, &waits{}, 0, 0, 0, 2)); err != nil {
				t.Fatalf("a heartbeat's 404 ended the loop: %v", err)
			}
			lines := afterRegistration(out.String())
			if len(lines) == 0 || lines[0] != c.want {
				t.Fatalf("after a heartbeat's 404 (%s):\n got %q\nwant %q\n%s", c.name, first(lines), c.want, out.String())
			}
			if f.registers.Load() < 2 {
				t.Fatalf("a heartbeat's 404 did not register this node again (%d registrations)", f.registers.Load())
			}
		})
	}
}

// REGISTER: the farm refuses a registration with a 404 for ONE cause — the id is a node row whose owner is not the
// caller's workspace (RegisterNodeAsync's guarded write, and the ownership filter's NodeIdIsFreeOrOwnedByAsync: an unborn
// id is how every node is born). So the line says that, without "may", and keeps the agent's own steps. The farm's
// sentence there ("Re-register.") is the wrong advice for a registration it just refused, and is not quoted.
func TestARegister404SaysTheOneCauseAndKeepsTheAgentsOwnSteps(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.register.Store(&answer{status: http.StatusNotFound, body: farmtest.Envelope(said.UnknownNode(limitNode))})
	var out strings.Builder
	err := connect.Run(context.Background(), limitConfig(f, &out, &waits{}, 0, 0, 0, 2))
	if err == nil || !strings.Contains(err.Error(), "identity refused") {
		t.Fatalf("a registration refused with a 404 must stop the loop as an identity refused, got %v", err)
	}
	want := "the farm refused this node's identity: this id is already registered at the farm, but not to the workspace of " +
		"this agent's token. First stop every npx nthcase-agent connect running on this machine; then remove the node-id " +
		"file in the agent's config folder to mint a fresh identity, or contact your operator.\n"
	if !strings.HasSuffix(out.String(), want) || strings.Count(out.String(), "refused this node's identity") != 1 {
		t.Fatalf("the register 404's line:\n got %q\nwant it to end %q", out.String(), want)
	}
	if strings.Contains(out.String(), "Re-register") || strings.Contains(out.String(), " may ") {
		t.Fatalf("the register 404 quoted the farm's advice, or guessed:\n%s", out.String())
	}
}

func first(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}
