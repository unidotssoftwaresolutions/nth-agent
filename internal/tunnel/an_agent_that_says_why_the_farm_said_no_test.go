package tunnel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO item 5 — THE TUNNEL'S UPGRADE, REFUSED. The farm counts a workspace's tunnels
// before the upgrade and refuses the eleventh machine's with a 429 and a sentence that says what to do. The agent printed
// the WebSocket library's error ("expected handshake response status code 101 but got 429"), and after a drop nothing at
// all. What the library keeps of the refused answer's body (a kilobyte) is read now, through the same door as a 401's.

// refusingUpgrades answers every upgrade with status and body until accept is set, then accepts and reads.
type refusingUpgrades struct {
	server   *httptest.Server
	status   atomic.Int32
	body     atomic.Value // string
	accept   atomic.Bool
	upgrades atomic.Int32
	conn     atomic.Pointer[websocket.Conn]
}

func newRefusingUpgrades(t *testing.T, status int, body string) *refusingUpgrades {
	f := &refusingUpgrades{}
	f.status.Store(int32(status))
	f.body.Store(body)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.upgrades.Add(1)
		if !f.accept.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(int(f.status.Load()))
			_, _ = io.WriteString(w, f.body.Load().(string))
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.conn.Store(conn)
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func quoted(sentence string) string { return "“" + sentence + "”" }

// THE FARM'S SENTENCE, ONCE, WITH THE STATUS — on a first start, and after a drop.
func TestARefusedUpgradeSaysTheFarmsSentence(t *testing.T) {
	said := farmtest.Read(t)
	f := newRefusingUpgrades(t, http.StatusTooManyRequests, farmtest.Envelope(said.TooManyTunnels))
	var out syncWriter
	liveTunnel(t, f.server.URL, &out, nil)
	waitFor(t, "five refused upgrades", func() bool { return f.upgrades.Load() >= 5 })
	transcript := out.String()
	if strings.Count(transcript, quoted(said.TooManyTunnels)) != 1 || !strings.Contains(transcript, "(429)") {
		t.Fatalf("five upgrades refused with the farm's sentence; the person reads it once, with its status:\n%s", transcript)
	}
	if !strings.Contains(transcript, "not connected yet") || strings.Contains(transcript, "dropped") ||
		strings.Contains(transcript, "expected handshake") {
		t.Fatalf("on a first start the tunnel is not connected yet, in the farm's words, not the library's:\n%s", transcript)
	}

	// THE TUNNEL WAS UP, AND DROPPED, AND THE FARM REFUSES IT NOW: said, once, after the drop's own line.
	f.accept.Store(true)
	waitFor(t, "the tunnel up", func() bool { return f.conn.Load() != nil })
	f.accept.Store(false)
	before := f.upgrades.Load()
	_ = f.conn.Load().CloseNow()
	waitFor(t, "five more refused upgrades", func() bool { return f.upgrades.Load() >= before+5 })
	_, after, _ := strings.Cut(out.String(), "drive tunnel established")
	if strings.Count(after, quoted(said.TooManyTunnels)) != 1 || !strings.Contains(after, "dropped") {
		t.Fatalf("after a drop, the refusal that keeps it down is said once, after the drop:\n%s", out.String())
	}
}

// NO SENTENCE, SAID AS NONE — and the farm's words reach the terminal safe, as themselves, without the token, bounded.
func TestARefusedUpgradeWithNoSentenceSaysSoAndAHostileOneIsMadeSafe(t *testing.T) {
	f := newRefusingUpgrades(t, http.StatusForbidden, "")
	var out syncWriter
	liveTunnel(t, f.server.URL, &out, nil)
	waitFor(t, "a refused upgrade said", func() bool { return strings.Contains(out.String(), "(403)") })
	if !strings.Contains(out.String(), "(403) and gave no reason this agent could read") {
		t.Fatalf("a refusal with no body names its status and that no reason was given:\n%s", out.String())
	}

	hostile := "\x1b[2J\x1b]0;owned\x07Too many %s %v sk-agent-LIVE \x1b[31mtunnels\x1b[0m\r\nestablished"
	f = newRefusingUpgrades(t, http.StatusTooManyRequests, farmtest.Envelope(hostile))
	var hostileOut syncWriter
	liveTunnel(t, f.server.URL, &hostileOut, nil)
	waitFor(t, "a refused upgrade said", func() bool { return strings.Contains(hostileOut.String(), "(429)") })
	for _, r := range hostileOut.String() {
		if (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("a control character (U+%04X) from the farm reached the terminal:\n%q", r, hostileOut.String())
		}
	}
	if !strings.Contains(hostileOut.String(), quoted("Too many %s %v «token» tunnels established")) ||
		strings.Contains(hostileOut.String(), "sk-agent-LIVE") {
		t.Fatalf("the farm's words reach the terminal safe, as themselves, without the token:\n%q", hostileOut.String())
	}

	// What the library keeps of a refused body is a kilobyte: an envelope longer than that does not parse, and is no sentence.
	f = newRefusingUpgrades(t, http.StatusTooManyRequests, farmtest.Envelope(strings.Repeat("x", 2048)))
	var longOut syncWriter
	liveTunnel(t, f.server.URL, &longOut, nil)
	waitFor(t, "a refused upgrade said", func() bool { return strings.Contains(longOut.String(), "(429)") })
	if strings.Contains(longOut.String(), "xxxx") || !strings.Contains(longOut.String(), "gave no reason this agent could read") {
		t.Fatalf("a refused body past what is kept was printed:\n%s", longOut.String())
	}
}

// TODAY'S BACKOFF, AND A 429 NO FASTER: the same farm refusing with a 429 and its sentence, and with a 403 and nothing,
// is dialled no faster than the reconnect's own floor, doubling to its cap, with its jitter. (Since NTH_AGENT_0_4_2 a 429
// doubles on to a minute: a_tunnel_at_the_limit_waits_its_turn_test.go.)
func TestA429DoesNotMakeTheTunnelDialFaster(t *testing.T) {
	said := farmtest.Read(t)
	dials := func(status int, body string) int32 {
		f := newRefusingUpgrades(t, status, body)
		var out syncWriter
		liveTunnel(t, f.server.URL, &out, func(cfg *tunnel.Config) {
			cfg.ReconnectFloor, cfg.ReconnectCap = 50*time.Millisecond, 100*time.Millisecond
		})
		time.Sleep(1500 * time.Millisecond)
		return f.upgrades.Load()
	}
	limited := dials(http.StatusTooManyRequests, farmtest.Envelope(said.TooManyTunnels))
	refused := dials(http.StatusForbidden, "")
	// Floor 50 ms doubling to 100 ms, plus up to 750 ms of jitter: never more than one dial per 50 ms, plus the first.
	if most := int32(1500/50) + 1; limited > most {
		t.Fatalf("a 429 dialled %d times in 1.5 s, past what the backoff allows (%d)", limited, most)
	}
	if limited > refused*2+2 {
		t.Fatalf("a 429 dialled %d times where a 403 dialled %d — a 429 hurried the reconnect", limited, refused)
	}
	t.Logf("1.5 s refused: %d dials at a 429, %d at a 403", limited, refused)
}
