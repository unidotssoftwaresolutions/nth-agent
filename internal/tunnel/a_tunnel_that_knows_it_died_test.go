package tunnel_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// AN_AGENT_THAT_SHOWS_ITS_PHONE, part one — the tunnel as the node now has it (A_TUNNEL_THAT_KNOWS_IT_DIED's "Not
// covered: nth-agent"): requests off the read loop, a ping with an answer due, the header, the farm's `alive`, and
// fragments. Every one of these runs over real sockets: the library's behaviour is the subject.

// ── RULE 1 · A REQUEST NEVER HOLDS THE READ LOOP ──────────────────────────────────────────────────────────────────

func TestALongAppiumCommandLeavesThePingsAnsweredAndTheAnswersInOrder(t *testing.T) {
	// The farm pings with a 300 ms answer bound while one Appium command takes 1.5 s. Before this slice the agent
	// forwarded on its read loop, so nothing read the socket for those 1.5 s and every ping went unanswered — the
	// reason the farm could not ping it at all.
	var arrived atomic.Int32
	appium := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Add(1)
		// A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST: a frame that names no session reaches Appium only as GET /status, so the
		// slow and the fast command are told apart by their query (they were GET /slow and GET /fast).
		if r.URL.Query().Get("cmd") == "slow" {
			time.Sleep(1500 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.URL.Path))
	}))
	defer appium.Close()

	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = appium.URL })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	send(t, conn, map[string]any{"id": "11111111-0000-0000-0000-000000000001", "method": "GET", "path": "/status?cmd=slow", "headers": map[string]string{}})
	send(t, conn, map[string]any{"id": "11111111-0000-0000-0000-000000000002", "method": "GET", "path": "/status?cmd=fast", "headers": map[string]string{}})

	// While the slow command runs, the farm pings every 100 ms and every ping must come back within 300 ms.
	unanswered := 0
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		if err := conn.Ping(ctx); err != nil {
			unanswered++
		}
		cancel()
		time.Sleep(100 * time.Millisecond)
	}
	if unanswered > 0 {
		t.Fatalf("%d of 10 farm pings went unanswered while an Appium command ran — the read loop is waiting on Appium", unanswered)
	}

	// ONE WORKER, IN ORDER — the node's arrangement: the fast answer comes after the slow one, never around it.
	first := reader.await(t, 5*time.Second, "the first answer", func(f map[string]any) bool { return f["status"] != nil })
	second := reader.await(t, 5*time.Second, "the second answer", func(f map[string]any) bool { return f["status"] != nil })
	if first["id"] != "11111111-0000-0000-0000-000000000001" || second["id"] != "11111111-0000-0000-0000-000000000002" {
		t.Fatalf("the answers came back out of order: %v then %v", first["id"], second["id"])
	}
}

// ── RULE 2 · A SILENT TUNNEL IS FOUND DEAD WITHIN THE BOUND, AND RECONNECTED ─────────────────────────────────────

func TestASilentFarmIsFoundDeadWithinTheBoundAndTheTunnelComesBack(t *testing.T) {
	farm := newLiveFarm(t)
	path := newGate(t, farm.server.Listener.Addr().String())
	var out syncWriter
	const interval, within = 100 * time.Millisecond, 300 * time.Millisecond
	drive := liveTunnel(t, path.url(), &out, func(c *tunnel.Config) { c.PingInterval, c.AnswerWithin = interval, within })
	readFarmSide(farm.next(t))
	waitFor(t, "connected", drive.Connected)

	// Healthy for a while first: the agent's pings are answered and nothing is called dead.
	time.Sleep(4 * within)
	if strings.Contains(out.String(), "found dead") {
		t.Fatalf("a farm that answers every ping was called dead:\n%s", out.String())
	}

	path.silence()
	silencedAt := time.Now()
	waitFor(t, "found dead", func() bool { return strings.Contains(out.String(), "found dead") })
	took := time.Since(silencedAt)
	// The node's bound is PingInterval + AnswerWithin; one more interval of ticker slack, and scheduling.
	if bound := interval + within + interval + 500*time.Millisecond; took > bound {
		t.Fatalf("a silent farm was found dead after %s; the bound is %s", took, bound)
	}
	if !strings.Contains(out.String(), "did not answer this agent's ping within 300ms") {
		t.Fatalf("the drop must say how the tunnel ended:\n%s", out.String())
	}
	// …and the tunnel is dialled again AT ONCE — a new upgrade reaches the farm through a path that works.
	readFarmSide(farm.next(t))
	waitFor(t, "back", func() bool { return strings.Contains(out.String(), "the drive tunnel is back") })
	if farm.upgrades.Load() < 2 {
		t.Fatalf("no second upgrade after the tunnel was found dead: %d", farm.upgrades.Load())
	}
}

func TestTheUpgradeSaysThisEndAnswersPings(t *testing.T) {
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, nil)
	farm.next(t)
	if got := farm.answers.Load().(string); got != "1" {
		t.Fatalf("the upgrade must carry X-Relay-Answers-Pings: 1 so the farm pings this end too, got %q", got)
	}
}

func TestTheFarmsAliveFrameIsHearingFromTheFarm(t *testing.T) {
	// A farm whose pongs never come back — it is not reading — and that says `alive` every 50 ms. Arriving data is
	// hearing from the farm (as it is for .NET's keep-alive, measured), so the tunnel is NOT dead while it talks; once
	// it stops, it is, within the bound.
	farm := newLiveFarm(t)
	var out syncWriter
	const interval, within = 100 * time.Millisecond, 300 * time.Millisecond
	drive := liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.PingInterval, c.AnswerWithin = interval, within })
	conn := farm.next(t) // NO reader on the farm's side: the agent's pings are never answered
	waitFor(t, "connected", drive.Connected)

	stop := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(stop) {
		send(t, conn, map[string]any{"kind": "alive"})
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(out.String(), "found dead") || !drive.Connected() {
		t.Fatalf("a farm saying alive every 50 ms was called dead because its pongs did not come back:\n%s", out.String())
	}

	quietAt := time.Now()
	waitFor(t, "found dead once the farm went quiet", func() bool { return strings.Contains(out.String(), "found dead") })
	if took := time.Since(quietAt); took > interval+within+interval+500*time.Millisecond {
		t.Fatalf("a farm that went quiet was found dead only after %s", took)
	}
}

// ── LARGE FRAMES GO AS FRAGMENTS ─────────────────────────────────────────────────────────────────────────────────

func TestALargeFrameCrossesAsThirtyTwoKilobyteFragments(t *testing.T) {
	body := strings.Repeat("p", 100*1024) // a page source's size, give or take
	appium := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Has("source") { // a session-less frame reaches Appium only as GET /status (it was GET /source)
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"value":{"ready":true}}`))
	}))
	defer appium.Close()

	farm := newLiveFarm(t)
	path := newGate(t, farm.server.Listener.Addr().String())
	var out syncWriter
	liveTunnel(t, path.url(), &out, func(c *tunnel.Config) { c.AppiumURL = appium.URL })
	reader := readFarmSide(farm.next(t))

	small := map[string]any{"id": "22222222-0000-0000-0000-000000000001", "method": "GET", "path": "/status", "headers": map[string]string{}}
	large := map[string]any{"id": "22222222-0000-0000-0000-000000000002", "method": "GET", "path": "/status?source", "headers": map[string]string{}}
	farmConn := reader.conn
	send(t, farmConn, small)
	reader.await(t, 5*time.Second, "the small answer", func(f map[string]any) bool { return f["id"] == small["id"] })
	send(t, farmConn, large)
	answer := reader.await(t, 5*time.Second, "the large answer", func(f map[string]any) bool { return f["id"] == large["id"] })
	decoded, _ := base64.StdEncoding.DecodeString(answer["bodyBase64"].(string))
	if string(decoded) != body {
		t.Fatal("the large answer must arrive whole")
	}

	// Read the frames off the wire: data frames only (opcode 1 text, 0 continuation), in order.
	var messages [][]wireFrame
	var current []wireFrame
	for _, f := range path.seen() {
		if f.opcode != 0 && f.opcode != 1 {
			continue // pings and pongs interleave; RFC 6455 allows it
		}
		current = append(current, f)
		if f.fin {
			messages = append(messages, current)
			current = nil
		}
	}
	if len(messages) < 2 {
		t.Fatalf("expected the two answers on the wire, saw %d messages", len(messages))
	}
	smallFrames, largeFrames := messages[len(messages)-2], messages[len(messages)-1]
	if len(smallFrames) != 1 || smallFrames[0].opcode != 1 {
		t.Fatalf("a frame under 32 KB goes as one frame, saw %+v", smallFrames)
	}
	total, data := 0, 0
	for i, f := range largeFrames {
		total += f.length
		if f.length > 0 {
			data++
		}
		if f.length > tunnel.FragmentBytes {
			t.Fatalf("fragment %d is %d bytes, over the node's %d", i, f.length, tunnel.FragmentBytes)
		}
		if (i == 0) != (f.opcode == 1) {
			t.Fatalf("fragment %d has opcode %d: the first is text, the rest continuations", i, f.opcode)
		}
	}
	if want := (total + tunnel.FragmentBytes - 1) / tunnel.FragmentBytes; data != want || data < 2 {
		t.Fatalf("a %d-byte frame went as %d data fragments, not %d of at most %d", total, data, want, tunnel.FragmentBytes)
	}
	// coder/websocket's Writer cannot know which write is the last, so its Close ends the message with an EMPTY final
	// continuation — RFC 6455 allows it; the node's .NET sets the end bit on the last data fragment instead.
	if last := largeFrames[len(largeFrames)-1]; !last.fin || last.length != 0 || len(largeFrames) != data+1 {
		t.Fatalf("the message ends with one empty final continuation: %+v", largeFrames)
	}
}

func TestAFarmStillSendingOneLongMessageIsTalking(t *testing.T) {
	// The farm never answers a ping here (it is not reading) and sends no `alive`; it is sending ONE request whose body
	// takes 1.5 s to arrive, far past the 300 ms bound. Arriving bytes are the farm talking, piece by piece — timed per
	// whole message, this tunnel was called dead 400 ms in while the farm was still mid-sentence (the review's finding).
	farm := newLiveFarm(t)
	var out syncWriter
	const interval, within = 100 * time.Millisecond, 300 * time.Millisecond
	drive := liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.PingInterval, c.AnswerWithin = interval, within })
	conn := farm.next(t)
	waitFor(t, "connected", drive.Connected)

	writer, err := conn.Writer(context.Background(), websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	writer.Write([]byte(`{"id":"88888888-0000-0000-0000-000000000001","method":"POST","path":"/status","headers":{},"bodyBase64":"`))
	for i := 0; i < 30; i++ {
		writer.Write(bytes.Repeat([]byte("QUJD"), 4096))
		time.Sleep(50 * time.Millisecond)
	}
	writer.Write([]byte(`"}`))
	writer.Close()
	if strings.Contains(out.String(), "found dead") || farm.upgrades.Load() != 1 {
		t.Fatalf("a farm still sending a message was called dead:\n%s", out.String())
	}
}
