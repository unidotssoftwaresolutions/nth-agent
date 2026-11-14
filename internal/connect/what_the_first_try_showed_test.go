package connect_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// WHAT_THE_FIRST_TRY_SHOWED. A machine that had connected to another workspace before kept that workspace's node id, and the
// farm refused it: "this id is already registered at the farm, but not to the workspace of this agent's token". The person
// at the machine could not tell what to do, nor where the id was. The sentence now names the file by its full path on this
// machine, says to move it aside rather than delete it, and what happens then.
func TestARefusedIdentityNamesItsFileAndSaysToMoveItAside(t *testing.T) {
	const file = "/Users/ada/Library/Application Support/nth-agent/node-id"
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.register.Store(&answer{status: http.StatusNotFound, body: farmtest.Envelope(said.UnknownNode(limitNode))})
	var out strings.Builder
	cfg := limitConfig(f, &out, &waits{}, 0, 0, 0, 2)
	cfg.NodeIDFile = file
	if err := connect.Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "identity refused") {
		t.Fatalf("a registration refused with a 404 must stop the loop as an identity refused, got %v", err)
	}
	text := strings.Join(strings.Fields(out.String()), " ")
	for _, must := range []string{
		"the id in " + file + " is already registered at the farm, but not to the workspace of this agent's token",
		"move " + file + " aside",
		"do not delete it",
		"registers this machine anew under your token's workspace",
	} {
		if !strings.Contains(text, must) {
			t.Fatalf("the refused identity must say %q:\n%s", must, out.String())
		}
	}
	stop, move := strings.Index(text, "stop every npx nthcase-agent connect running on this machine"), strings.Index(text, "move "+file)
	if stop < 0 || stop > move {
		t.Fatalf("the advice says to stop the running connect before moving the identity:\n%s", out.String())
	}
	if strings.Contains(text, "remove the node-id file") {
		t.Fatalf("the advice must not say to remove the identity:\n%s", out.String())
	}
}

// WITH NO FILE KNOWN (a loop started by a test or the load fleet), the sentence still says to move the identity aside, by
// the folder it is kept in.
func TestARefusedIdentityWithNoFileKnownStillSaysToMoveItAside(t *testing.T) {
	said := farmtest.Read(t)
	f := newLimitFarm(t)
	f.register.Store(&answer{status: http.StatusNotFound, body: farmtest.Envelope(said.UnknownNode(limitNode))})
	var out strings.Builder
	_ = connect.Run(context.Background(), limitConfig(f, &out, &waits{}, 0, 0, 0, 2))
	text := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(text, "move the node-id file in the agent's config folder aside") || !strings.Contains(text, "do not delete it") {
		t.Fatalf("the refused identity must still say to move the file aside:\n%s", out.String())
	}
}
