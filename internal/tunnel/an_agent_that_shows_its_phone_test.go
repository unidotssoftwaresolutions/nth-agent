package tunnel_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// AN_AGENT_THAT_SHOWS_ITS_PHONE, part two — the live view: a phone's MJPEG mirror on this machine's loopback, streamed
// through the tunnel's stream frames to a farm, ended in the right words for each way it can end.

func streamStart(id, path string) map[string]any {
	return map[string]any{"kind": "stream-start", "id": id, "method": "GET", "path": path, "headers": map[string]string{}}
}

func ofStream(id, k string) func(map[string]any) bool {
	return func(f map[string]any) bool { return f["id"] == id && f["kind"] == k }
}

// ── END TO END ──────────────────────────────────────────────────────────────────────────────────────────────────

func TestAPhonesMirrorReachesTheFarmWholeAndInOrder(t *testing.T) {
	frame := []byte("--BoundaryString\r\nContent-Type: image/jpeg\r\n\r\n" + strings.Repeat("J", 50*1024) + "\r\n")
	mirror := newFakeMirror(t, frame, time.Millisecond, 300*1024, false)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offering(mirror.port) })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "33333333-0000-0000-0000-000000000001"
	send(t, conn, streamStart(id, mirror.url()))
	var got bytes.Buffer
	var seq float64
	for {
		f := reader.await(t, 5*time.Second, "the mirror's chunks and end", func(f map[string]any) bool { return f["id"] == id })
		if f["kind"] == "stream-end" {
			if f["status"] != float64(200) || f["error"] != nil {
				t.Fatalf("a mirror that ended cleanly ends with its own status and no error: %v", f)
			}
			break
		}
		if f["kind"] != "stream-chunk" || f["seq"] != seq {
			t.Fatalf("chunks are numbered 0,1,2… in order: got %v (want seq %v)", f, seq)
		}
		seq++
		decoded, _ := base64.StdEncoding.DecodeString(f["bodyBase64"].(string))
		if len(decoded) > 64*1024 {
			t.Fatalf("a chunk of %d bytes: the node's read buffer bounds one chunk at 64 KB", len(decoded))
		}
		got.Write(decoded)
	}
	want := bytes.Repeat(frame, 300*1024/len(frame)+1)[:300*1024]
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("the farm reassembled %d bytes; the mirror sent %d, and they differ", got.Len(), len(want))
	}
	if !strings.Contains(out.String(), "the farm opened a live view") {
		t.Fatalf("the first live view is said once:\n%s", out.String())
	}
}

// ── THREE ENDS: THE FARM CANCELS, THE PHONE GOES, THE TUNNEL ENDS ────────────────────────────────────────────────

func TestAFarmCancelEndsThePumpAndSaysNothing(t *testing.T) {
	mirror := newFakeMirror(t, []byte(strings.Repeat("c", 8*1024)), 5*time.Millisecond, 0, false)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offering(mirror.port) })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "33333333-0000-0000-0000-000000000002"
	send(t, conn, streamStart(id, mirror.url()))
	reader.await(t, 5*time.Second, "a first chunk", ofStream(id, "stream-chunk"))
	send(t, conn, map[string]any{"kind": "stream-cancel", "id": id})
	mirror.awaitViewerLeft(t, 2*time.Second, "a stream the farm cancelled")

	// The node's rule: a cancelled stream sends no End — the farm already knows. Chunks queued before the cancel may
	// still land; an End may not.
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case f := <-reader.frames:
			if f["id"] == id && f["kind"] == "stream-end" {
				t.Fatalf("a stream the farm cancelled was answered with an End: %v", f)
			}
		case <-deadline:
			return
		}
	}
}

func TestAPhoneThatGoesAwayEndsItsMirrorAndSaysSo(t *testing.T) {
	mirror := newFakeMirror(t, []byte(strings.Repeat("g", 8*1024)), 5*time.Millisecond, 0, false)
	offered := offering(mirror.port)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offered })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "33333333-0000-0000-0000-000000000003"
	send(t, conn, streamStart(id, mirror.url()))
	reader.await(t, 5*time.Second, "a first chunk", ofStream(id, "stream-chunk"))

	offered.Offer(map[string]uint16{}) // the connect loop's next tick no longer offers it: unplugged
	end := reader.await(t, 5*time.Second, "the End for a phone that went away", ofStream(id, "stream-end"))
	if !strings.Contains(fmt.Sprint(end["error"]), "the phone went away") {
		t.Fatalf("a phone that went away ends its mirror in those words: %v", end)
	}
	mirror.awaitViewerLeft(t, 2*time.Second, "a phone that went away")
}

func TestATunnelThatEndsEndsEveryPump(t *testing.T) {
	mirror := newFakeMirror(t, []byte(strings.Repeat("e", 8*1024)), 5*time.Millisecond, 0, false)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offering(mirror.port) })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "33333333-0000-0000-0000-000000000004"
	send(t, conn, streamStart(id, mirror.url()))
	reader.await(t, 5*time.Second, "a first chunk", ofStream(id, "stream-chunk"))
	conn.CloseNow()
	mirror.awaitViewerLeft(t, 2*time.Second, "a tunnel that ended")
}

// ── A SLOW VIEWER IS HELD ON THE QUEUE, NEVER BY THE TUNNEL ──────────────────────────────────────────────────────

func TestASlowViewerHoldsAQuarterMegabyteAndNotTheTunnel(t *testing.T) {
	// The farm stops reading for 6 s — its own bound for one viewer that takes nothing is 10 s, and coder/websocket
	// gives a pong FIVE to get out — while it pings the agent and the phone's mirror produces 16 MB/s. Measured before
	// the send queue existed: the agent's own reader died at 6 s with "failed to write control frame opPong". Here the
	// tunnel must live through it, the mirror must be held to what the queue and the kernel take, and the stream must
	// carry on when the farm reads again.
	mirror := newFakeMirror(t, []byte(strings.Repeat("s", 64*1024)), 4*time.Millisecond, 0, false)
	farm := newLiveFarm(t)
	var out syncWriter
	drive := liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offering(mirror.port) })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "33333333-0000-0000-0000-000000000005"
	send(t, conn, streamStart(id, mirror.url()))
	reader.await(t, 5*time.Second, "a first chunk", ofStream(id, "stream-chunk"))
	time.Sleep(300 * time.Millisecond)

	reader.paused.Store(true)
	before := mirror.wrote.Load()
	var pings atomic.Int32
	for i := 0; i < 12; i++ { // the farm keeps pinging while it is not reading
		time.Sleep(500 * time.Millisecond)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if conn.Ping(ctx) == nil {
				pings.Add(1)
			}
		}()
	}
	duringTheStall := mirror.wrote.Load() - before
	reader.paused.Store(false)

	reader.await(t, 5*time.Second, "chunks once the farm reads again", ofStream(id, "stream-chunk"))
	waitFor(t, "the farm's pings answered", func() bool { return pings.Load() == 12 })
	if farm.upgrades.Load() != 1 || !drive.Connected() || strings.Contains(out.String(), "dropped") {
		t.Fatalf("a slow viewer took the tunnel down (upgrades=%d):\n%s", farm.upgrades.Load(), out.String())
	}
	// 6 s of a 16 MB/s mirror is 96 MB. What may wait is the queue's quarter megabyte, one chunk, and what the kernel
	// buffers on loopback between three sockets; the mirror itself is held.
	if duringTheStall > 24<<20 {
		t.Fatalf("the mirror wrote %d bytes while the farm read nothing — the slow viewer is buffered here, not held", duringTheStall)
	}
	t.Logf("the mirror wrote %d bytes during a 6 s stall (a 16 MB/s mirror unheld would be ~96 MB)", duringTheStall)
}

// ── WHAT IS REFUSED, AND IN WHOSE WORDS ──────────────────────────────────────────────────────────────────────────

func TestOnlyAPhonesDeclaredMirrorOnLoopbackIsOpened(t *testing.T) {
	mirror := newFakeMirror(t, []byte("x"), time.Second, 0, true)
	farm := newLiveFarm(t)
	var out syncWriter
	offered := &liveview.Offered{}
	offered.Offer(map[string]uint16{"R5CT30ABCDE": mirror.port, "00008030-HEALTHY": 31002})
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offered })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	notMirror := "is not a phone's mirror this agent reserved"
	for i, c := range []struct{ path, says string }{
		// THE NODE'S SSRF RULE: an absolute upstream must be loopback.
		{"http://10.0.0.1:" + fmt.Sprint(mirror.port) + "/", "must target loopback"},
		{"http://farm.example:30001/", "must target loopback"},
		{"ftp://127.0.0.1:30001/", "unsupported stream scheme"},
		// THE AGENT'S OWN: a path relative to Appium is not a phone's mirror…
		{"/session/abc/screenshot", "phone's mirror and nothing else"},
		// …and neither is a loopback port no phone was given — the customer's own services live there.
		{"http://127.0.0.1:5432/", "port 5432 on this machine " + notMirror},
		{"http://localhost:9100/", "port 9100 on this machine " + notMirror},
		// A HAR pull on a tunnel that carries no capture is refused BY NAME, never read as a mirror. (Before
		// AN_AGENT_THAT_CAN_RECORD_ITS_PHONE every agent said "does not carry network capture yet"; the agent wires its
		// capture into every tunnel now, so only a tunnel built without it — this one — says this.)
		{"gfhar:e35c96a8-d229-4f77-910f-9d90077ea88e", "started without network capture"},
	} {
		id := fmt.Sprintf("44444444-0000-0000-0000-%012d", i)
		send(t, conn, streamStart(id, c.path))
		end := reader.await(t, 5*time.Second, "the refusal of "+c.path, ofStream(id, "stream-end"))
		if !strings.Contains(fmt.Sprint(end["error"]), c.says) {
			t.Fatalf("%s: the refusal must say %q, got %v", c.path, c.says, end["error"])
		}
	}
	if mirror.viewers.Load() != 0 {
		t.Fatal("a refused stream opened the mirror anyway")
	}
	if strings.Count(out.String(), "will not open") != 1 || strings.Count(out.String(), "network-capture HAR pull") != 1 {
		t.Fatalf("each refusal family is narrated once:\n%s", out.String())
	}

	// The declared one, on the same machine, under its localhost name: opened.
	const id = "44444444-0000-0000-0000-999999999999"
	send(t, conn, streamStart(id, "http://localhost:"+fmt.Sprint(mirror.port)+"/"))
	waitFor(t, "the declared mirror opened", func() bool { return mirror.viewers.Load() == 1 })
}

func TestTheNodesCapOfFourStreams(t *testing.T) {
	mirror := newFakeMirror(t, []byte("x"), time.Second, 0, true)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.Mirrors = offering(mirror.port) })
	conn := farm.next(t)
	reader := readFarmSide(conn)

	for i := 0; i < tunnel.MaxConcurrentStreams; i++ {
		send(t, conn, streamStart(fmt.Sprintf("55555555-0000-0000-0000-%012d", i), mirror.url()))
	}
	waitFor(t, "four viewers", func() bool { return mirror.viewers.Load() == tunnel.MaxConcurrentStreams })
	const fifth = "55555555-0000-0000-0000-999999999999"
	send(t, conn, streamStart(fifth, mirror.url()))
	end := reader.await(t, 5*time.Second, "the fifth stream's refusal", ofStream(fifth, "stream-end"))
	if !strings.Contains(fmt.Sprint(end["error"]), "its cap") {
		t.Fatalf("a stream over the node's cap of 4 is refused in words: %v", end)
	}
	if mirror.viewers.Load() != tunnel.MaxConcurrentStreams {
		t.Fatalf("the fifth stream opened a fifth viewer: %d", mirror.viewers.Load())
	}
}

func TestAMirrorThatGoesSilentEndsAtTheFarmsIdleBound(t *testing.T) {
	mirror := newFakeMirror(t, nil, time.Second, 0, true)
	farm := newLiveFarm(t)
	var out syncWriter
	liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) {
		c.Mirrors = offering(mirror.port)
		c.StreamIdle = 300 * time.Millisecond
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)

	const id = "66666666-0000-0000-0000-000000000001"
	send(t, conn, streamStart(id, mirror.url()))
	end := reader.await(t, 5*time.Second, "the End of a silent mirror", ofStream(id, "stream-end"))
	if !strings.Contains(fmt.Sprint(end["error"]), "sent nothing for 300ms") {
		t.Fatalf("a mirror that sends nothing ends at the idle bound, saying so: %v", end)
	}
	mirror.awaitViewerLeft(t, 2*time.Second, "a silent mirror")
}
