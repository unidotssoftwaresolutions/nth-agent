package tunnel_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── A FARM THAT RECORDS THE UPGRADE AND HANDS OVER EACH CONNECTION ─────────────────────────────────────────────────

type liveFarm struct {
	server   *httptest.Server
	conns    chan *websocket.Conn
	upgrades atomic.Int32
	answers  atomic.Value // the X-Relay-Answers-Pings value of the last upgrade
	refuse   atomic.Bool  // answer the upgrade 503 instead of accepting it
}

func newLiveFarm(t *testing.T) *liveFarm {
	f := &liveFarm{conns: make(chan *websocket.Conn, 8)}
	f.answers.Store("")
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tunnel") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.refuse.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.answers.Store(r.Header.Get(tunnel.AnswersPingsHeader))
		f.upgrades.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(-1)
		f.conns <- conn
		<-r.Context().Done()
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *liveFarm) next(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-f.conns:
		t.Cleanup(func() { c.CloseNow() })
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the agent never dialled the tunnel")
		return nil
	}
}

// farmReader reads the farm's side of one tunnel — which is also what answers the agent's pings — and hands every
// frame over. pause stops reading (the farm holding its loop for a slow viewer); resume starts again.
type farmReader struct {
	conn   *websocket.Conn
	frames chan map[string]any
	paused atomic.Bool
	ended  chan struct{}
}

func readFarmSide(conn *websocket.Conn) *farmReader {
	r := &farmReader{conn: conn, frames: make(chan map[string]any, 4096), ended: make(chan struct{})}
	go func() {
		defer close(r.ended)
		for {
			for r.paused.Load() {
				time.Sleep(5 * time.Millisecond)
			}
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var frame map[string]any
			if json.Unmarshal(data, &frame) == nil {
				r.frames <- frame
			}
		}
	}()
	return r
}

// await returns the next frame matching keep, failing after the bound.
func (r *farmReader) await(t *testing.T, bound time.Duration, what string, keep func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(bound)
	for {
		select {
		case frame := <-r.frames:
			if keep(frame) {
				return frame
			}
		case <-deadline:
			t.Fatalf("no %s within %s", what, bound)
			return nil
		}
	}
}

func send(t *testing.T, conn *websocket.Conn, frame any) {
	t.Helper()
	payload, _ := json.Marshal(frame)
	if err := conn.Write(context.Background(), websocket.MessageText, payload); err != nil {
		t.Fatalf("the farm could not send %s: %v", payload, err)
	}
}

func kind(k string) func(map[string]any) bool {
	return func(f map[string]any) bool { return f["kind"] == k }
}

// ── THE AGENT UNDER TEST ─────────────────────────────────────────────────────────────────────────────────────────

func liveTunnel(t *testing.T, farmURL string, out *syncWriter, change func(*tunnel.Config)) *tunnel.Tunnel {
	t.Helper()
	cfg := tunnel.Config{
		FarmURL: farmURL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-LIVE", AppiumURL: "http://127.0.0.1:1",
		ReconnectFloor: 10 * time.Millisecond, ReconnectCap: 20 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out: out,
		// The phones the older seals start sessions on; a seal about which phones are offered sets its own.
		Phones: allowed.Offering(fixturePhones...),
	}
	if change != nil {
		change(&cfg)
	}
	drive := tunnel.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { drive.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the tunnel did not stop when its context ended")
		}
	})
	return drive
}

// ── A TCP GATE BETWEEN THE AGENT AND THE FARM ────────────────────────────────────────────────────────────────────

// gate forwards both ways. silence() makes every connection open NOW stop forwarding in both directions without
// closing — a black-holed path, what a VPN switching on did to the node on 2026-10-02 — while later connections
// pass. It also reads the agent's own WebSocket frames off the wire (headers only), after the upgrade request.
type gate struct {
	listener net.Listener
	target   string
	mu       sync.Mutex
	pipes    []*atomic.Bool
	frames   []wireFrame
}

type wireFrame struct {
	fin    bool
	opcode byte
	length int
}

func newGate(t *testing.T, target string) *gate {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := &gate{listener: listener, target: target}
	go g.accept()
	t.Cleanup(func() { listener.Close() })
	return g
}

func (g *gate) url() string { return "http://" + g.listener.Addr().String() }

func (g *gate) accept() {
	for {
		agentSide, err := g.listener.Accept()
		if err != nil {
			return
		}
		farmSide, err := net.Dial("tcp4", g.target)
		if err != nil {
			agentSide.Close()
			continue
		}
		silent := &atomic.Bool{}
		g.mu.Lock()
		g.pipes = append(g.pipes, silent)
		g.mu.Unlock()
		parse := &frameParser{record: g.record}
		go pipe(farmSide, agentSide, silent, parse.feed)
		go pipe(agentSide, farmSide, silent, nil)
	}
}

func (g *gate) silence() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.pipes {
		s.Store(true)
	}
}

func (g *gate) record(f wireFrame) {
	g.mu.Lock()
	g.frames = append(g.frames, f)
	g.mu.Unlock()
}

func (g *gate) seen() []wireFrame {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]wireFrame(nil), g.frames...)
}

func pipe(dst, src net.Conn, silent *atomic.Bool, observe func([]byte)) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			for silent.Load() {
				time.Sleep(20 * time.Millisecond) // black hole: hold the bytes, close nothing
			}
			if observe != nil {
				observe(append([]byte(nil), buffer[:n]...))
			}
			if _, werr := dst.Write(buffer[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			dst.Close()
			return
		}
	}
}

// frameParser reads RFC 6455 frame HEADERS out of the agent→farm byte stream: the HTTP upgrade first, then frames,
// each masked (a client's). Control frames are recorded too; the payload itself is skipped.
type frameParser struct {
	buffer   bytes.Buffer
	upgraded bool
	record   func(wireFrame)
}

func (p *frameParser) feed(chunk []byte) {
	p.buffer.Write(chunk)
	if !p.upgraded {
		at := bytes.Index(p.buffer.Bytes(), []byte("\r\n\r\n"))
		if at < 0 {
			return
		}
		p.buffer.Next(at + 4)
		p.upgraded = true
	}
	for {
		data := p.buffer.Bytes()
		if len(data) < 2 {
			return
		}
		fin, opcode, masked := data[0]&0x80 != 0, data[0]&0x0f, data[1]&0x80 != 0
		length, header := int(data[1]&0x7f), 2
		switch length {
		case 126:
			if len(data) < 4 {
				return
			}
			length, header = int(binary.BigEndian.Uint16(data[2:4])), 4
		case 127:
			if len(data) < 10 {
				return
			}
			length, header = int(binary.BigEndian.Uint64(data[2:10])), 10
		}
		if masked {
			header += 4
		}
		if len(data) < header+length {
			return
		}
		p.buffer.Next(header + length)
		p.record(wireFrame{fin: fin, opcode: opcode, length: length})
	}
}

// ── A PHONE'S MIRROR ON THIS MACHINE ─────────────────────────────────────────────────────────────────────────────

// fakeMirror is an MJPEG server on loopback: it writes `frame` every `every` until its viewer goes, or `total` bytes
// and then ends, or nothing at all after the headers (`silent`). It counts what it wrote BEFORE writing it (a fake
// server counts before it writes — a client that stops early must still read the right tally).
type fakeMirror struct {
	server  *httptest.Server
	port    uint16
	wrote   atomic.Int64
	viewers atomic.Int32
	left    chan struct{} // one value per viewer that went away
}

func newFakeMirror(t *testing.T, frame []byte, every time.Duration, total int, silent bool) *fakeMirror {
	m := &fakeMirror{left: make(chan struct{}, 16)}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.viewers.Add(1)
		defer func() { m.viewers.Add(-1); m.left <- struct{}{} }()
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=--BoundaryString")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if silent {
			<-r.Context().Done()
			return
		}
		sent := 0
		for total <= 0 || sent < total {
			piece := frame
			if total > 0 && sent+len(piece) > total {
				piece = piece[:total-sent]
			}
			m.wrote.Add(int64(len(piece)))
			if _, err := w.Write(piece); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			sent += len(piece)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(every):
			}
		}
	}))
	t.Cleanup(m.server.Close)
	port, _ := strconv.Atoi(m.server.URL[strings.LastIndex(m.server.URL, ":")+1:])
	m.port = uint16(port)
	return m
}

func (m *fakeMirror) url() string { return "http://127.0.0.1:" + strconv.Itoa(int(m.port)) + "/" }

func (m *fakeMirror) awaitViewerLeft(t *testing.T, bound time.Duration, why string) {
	t.Helper()
	select {
	case <-m.left:
	case <-time.After(bound):
		t.Fatalf("%s: the mirror's viewer was still connected after %s", why, bound)
	}
}

func offering(port uint16) *liveview.Offered {
	offered := &liveview.Offered{}
	offered.Offer(map[string]uint16{"R5CT30ABCDE": port})
	return offered
}

var _ = io.EOF
