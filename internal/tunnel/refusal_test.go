package tunnel_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── A REFUSAL NAMES WHAT WAS ASKED ──────────────────────────────────────────────────────────────────────────
//
// Two capabilities ride the stream-start frame: a live-view stream and a network-capture HAR pull, which the
// farm addresses as `gfhar:<leaseId>` in the frame's Path (the farm's LeaseController builds that URL). Both
// were refused as "live-view streaming", and the runner recorded that wording as the cause of a capture failure.

func refuseWithPath(t *testing.T, path string) (message, narration string) {
	t.Helper()
	stub, server := newStubFarmTunnel(t)
	// Built here (not via startTunnel) so the test owns the narration writer — the refusal is two sentences,
	// one on the wire and one on the console, and both must name the same capability.
	var out syncWriter
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: server.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-T",
		AppiumURL: "http://127.0.0.1:1", ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out: &out,
	})
	ctx0, cancel0 := context.WithCancel(context.Background())
	t.Cleanup(cancel0)
	go drive.Run(ctx0)
	conn := stub.waitConn(t)
	sendFrame(t, conn, map[string]any{"kind": "stream-start", "id": "11111111-2222-3333-4444-555555555555", "method": "GET", "path": path})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("no stream-end for %q: %v", path, err)
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame["kind"] != "stream-end" {
		t.Fatalf("the refusal rides the contract's own vocabulary, got %v", frame["kind"])
	}
	return frame["error"].(string), out.String()
}

func TestAHarPullIsRefusedAsNetworkCaptureNotLiveView(t *testing.T) {
	// The path the farm builds for a HAR pull: /relay/{leaseId}/stream?path=gfhar%3A{leaseId}&contentType=...
	message, narration := refuseWithPath(t, "/relay/e35c96a8-d229-4f77-910f-9d90077ea88e/stream?path=gfhar%3Ae35c96a8-d229-4f77-910f-9d90077ea88e&contentType=application/json")

	if !strings.Contains(message, "network capture") {
		t.Fatalf("a HAR pull must be refused in the vocabulary of what was asked, got %q", message)
	}
	if strings.Contains(message, "live-view") {
		t.Fatalf("the wrong family's name is a false claim about what went wrong: %q", message)
	}
	if !strings.Contains(narration, "network-capture HAR pull") {
		t.Fatalf("the narration names it too, got %q", narration)
	}
}

// Was TestALiveViewStreamKeepsItsOwnSentence: every live view was refused as "this agent does not carry live-view
// streaming yet". Reversed by AN_AGENT_THAT_SHOWS_ITS_PHONE — the live view is carried — so what this now holds is the
// half of that seal that survives: a live view this agent will NOT open is refused in the live view's own words, never
// the other family's. The path is the one the platform actually sends when a machine declared no mirror port for an
// iPhone (the runner's configured fallback, 9100), and this agent declared none here.
func TestALiveViewOfAPortNoPhoneWasGivenIsRefusedAsSuch(t *testing.T) {
	message, narration := refuseWithPath(t, "http://127.0.0.1:9100")

	if !strings.Contains(message, "port 9100 on this machine is not a phone's mirror this agent reserved") {
		t.Fatalf("a live view of a port no phone was given must say exactly that, got %q", message)
	}
	if strings.Contains(message, "network capture") {
		t.Fatalf("and never the other family's: %q", message)
	}
	if !strings.Contains(narration, "streams a phone's mirror only from the loopback port it reserved") {
		t.Fatalf("the narration names the rule too, got %q", narration)
	}
}

var _ = websocket.StatusNormalClosure
