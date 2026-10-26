package tunnel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// ── A GATEWAY'S ANSWER TO THE UPGRADE IS THE FARM NOT REACHED (NTH_AGENT_0_4_4 item 1) ────────────────────────────
//
// While the farm's API restarts, nginx answers the tunnel's upgrade with its 502 page. Since 0.4.2 the tunnel said "the farm
// refused the drive tunnel (502) and gave no reason this agent could read". Nothing refused it: the farm was not there. Such
// an answer is said as the tunnel not reaching the farm, in its own words for that; the farm's own 503 and 429 keep theirs.

// upgradeAnswer is how the fake answers the next upgrade: a status and a body, or nil to take it.
type upgradeAnswer struct {
	status int
	body   string
}

// gatewayFarm answers every upgrade with its current answer, and counts them.
type gatewayFarm struct {
	server   *httptest.Server
	answer   atomic.Pointer[upgradeAnswer]
	upgrades atomic.Int32
	conn     atomic.Pointer[websocket.Conn]
}

func newGatewayFarm(t *testing.T, first *upgradeAnswer) *gatewayFarm {
	f := &gatewayFarm{}
	f.answer.Store(first)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.upgrades.Add(1)
		if a := f.answer.Load(); a != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(a.status)
			_, _ = io.WriteString(w, a.body)
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

// answers sets the next answer and waits for n upgrades to have had it.
func (f *gatewayFarm) answers(t *testing.T, a *upgradeAnswer, n int32) {
	t.Helper()
	f.answer.Store(a)
	from := f.upgrades.Load()
	waitFor(t, "more upgrades", func() bool { return f.upgrades.Load() >= from+n+1 })
}

func TestAGatewayInFrontOfTheUpgradeIsTheFarmNotReached(t *testing.T) {
	said := farmtest.Read(t)
	f := newGatewayFarm(t, &upgradeAnswer{http.StatusBadGateway, farmtest.GatewayPage(http.StatusBadGateway)})
	var out syncWriter
	liveTunnel(t, f.server.URL, &out, nil)
	waitFor(t, "three upgrades", func() bool { return f.upgrades.Load() >= 3 })
	f.answers(t, &upgradeAnswer{http.StatusGatewayTimeout, farmtest.GatewayPage(http.StatusGatewayTimeout)}, 3)
	f.answers(t, &upgradeAnswer{http.StatusServiceUnavailable, ""}, 3)

	// A FIRST START, the farm not there: one line, with the first status, and the tunnel's own words for it.
	first := out.String()
	const notReached = "the drive tunnel is not connected yet (the farm's address answered 502, not the farm) — retrying with backoff; devices are offered once it connects\n"
	if first != notReached {
		t.Fatalf("nine upgrades a gateway answered in the farm's place: one line, that the farm was not reached, got:\n%s", first)
	}

	// THE FARM IS BACK, and its own answers are its own: a 503 with its sentence, then a 429 with its sentence.
	f.answers(t, &upgradeAnswer{http.StatusServiceUnavailable, farmtest.Envelope(said.RegistryBusy)}, 2)
	f.answers(t, &upgradeAnswer{http.StatusTooManyRequests, farmtest.Envelope(said.TooManyTunnels)}, 2)
	f.answer.Store(nil)
	waitFor(t, "the tunnel up", func() bool { return strings.Contains(out.String(), "drive tunnel established") })
	farms := strings.TrimPrefix(out.String(), first)
	for _, want := range []string{
		"the drive tunnel is not connected yet: the farm refused it (503). It says: " + quoted(said.RegistryBusy),
		"the drive tunnel is not connected yet: the farm refused it (429). It says: " + quoted(said.TooManyTunnels),
	} {
		if strings.Count(farms, want) != 1 {
			t.Fatalf("the farm's own answer keeps 0.4.2's words, once:\n  %s\nin:\n%s", want, out.String())
		}
	}

	// AFTER A DROP, THE FARM RESTARTING: the drop is said, and the gateway's answers after it say nothing more.
	f.answer.Store(&upgradeAnswer{http.StatusBadGateway, farmtest.GatewayPage(http.StatusBadGateway)})
	before := f.upgrades.Load()
	_ = f.conn.Load().CloseNow()
	waitFor(t, "five upgrades after the drop", func() bool { return f.upgrades.Load() >= before+5 })
	f.answer.Store(nil)
	waitFor(t, "the tunnel back", func() bool { return strings.Contains(out.String(), "the drive tunnel is back") })
	_, after, _ := strings.Cut(out.String(), "drive tunnel established")
	if strings.Count(after, "the drive tunnel dropped") != 1 || strings.Contains(after, "refused") || strings.Contains(after, "(502)") {
		t.Fatalf("a drop, then the farm's address answering in its place: the drop alone is said:\n%s", out.String())
	}
	if strings.Contains(out.String(), "nginx") || strings.Contains(out.String(), "Bad Gateway") {
		t.Fatalf("the gateway's page reached the terminal:\n%s", out.String())
	}
}
