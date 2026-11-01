// Package farmstandin is a farm for the load harness's tests, on 127.0.0.1: the node routes the agent calls (register,
// heartbeat, leave), the drive tunnel's websocket, and one build to download — and, on the far side of each tunnel, a
// runner's requests sent as the farm relays them. It holds what it was told and what crossed it, so a test can say what
// the fleet did.
//
// It is NOT the farm: it leases nothing, keeps no limits, has no relay to a runner of its own and no door in front of it.
// It is a test helper: no program imports it (a seal in the fleet's tests holds that).
package farmstandin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/runnertest"
)

// BuildPath is where the farm serves its one build.
const BuildPath = "/builds/nthcase-demo.apk"

// Device is one device row a node reported.
type Device struct {
	Udid, Model, Manufacturer, AppiumURL string
	MirrorPort                           *int
}

// Node is what the farm knows of one node.
type Node struct {
	ID         string
	HostName   string
	Version    string
	Devices    []Device
	Registers  int
	Heartbeats int
	Leaves     int
}

// live is a node with its tunnel.
type live struct {
	Node
	conn    *websocket.Conn
	writeMu sync.Mutex
	pending map[string]chan []byte
}

// Farm is the stand-in.
type Farm struct {
	Server *httptest.Server
	token  string

	mu      sync.Mutex
	nodes   map[string]*live
	refused int
	bodies  [][]byte // every request body the agent sent, and every frame it answered with
	urls    []string
	headers []string // every header value but X-Agent-Token's
	tunnels int
	build   []byte
	fetches int
}

// New starts a farm that takes token, over TLS when secure (a build is fetched only from an https farm).
func New(token string, secure bool) *Farm {
	f := &Farm{token: token, nodes: map[string]*live{}, build: demoBuild()}
	handler := http.HandlerFunc(f.serve)
	if secure {
		f.Server = httptest.NewTLSServer(handler)
	} else {
		f.Server = httptest.NewServer(handler)
	}
	return f
}

// URL is the farm's address.
func (f *Farm) URL() string { return f.Server.URL }

// Roots trusts the farm's certificate.
func (f *Farm) Roots() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(f.Server.Certificate())
	return pool
}

func (f *Farm) Close() { f.Server.Close() }

func demoBuild() []byte {
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, name := range []string{"AndroidManifest.xml", "classes.dex", "resources.arsc"} {
		entry, _ := w.Create(name)
		entry.Write(bytes.Repeat([]byte(name), 512))
	}
	w.Close()
	return out.Bytes()
}

func (f *Farm) record(r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, body)
	f.urls = append(f.urls, r.URL.String())
	for name, values := range r.Header {
		if !strings.EqualFold(name, "X-Agent-Token") {
			f.headers = append(f.headers, values...)
		}
	}
}

func (f *Farm) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	f.record(r, body)
	if r.URL.Path == BuildPath && r.Method == http.MethodGet {
		f.mu.Lock()
		f.fetches++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Write(f.build)
		return
	}
	if r.Header.Get("X-Agent-Token") != f.token {
		f.mu.Lock()
		f.refused++
		f.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"success":false,"message":"unknown agent token"}`))
		return
	}
	ok := func(data any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/nodes/register":
		var report struct {
			NodeID       string  `json:"nodeId"`
			HostName     string  `json:"hostName"`
			AgentVersion *string `json:"agentVersion"`
			Devices      []struct {
				Descriptor struct {
					Udid, Model, Manufacturer, AppiumURL string
					MirrorPort                           *int
				} `json:"descriptor"`
			} `json:"devices"`
		}
		json.Unmarshal(body, &report)
		f.mu.Lock()
		n := f.node(report.NodeID)
		n.Registers++
		n.HostName = report.HostName
		if report.AgentVersion != nil {
			n.Version = *report.AgentVersion
		}
		n.Devices = nil
		for _, d := range report.Devices {
			n.Devices = append(n.Devices, Device{d.Descriptor.Udid, d.Descriptor.Model, d.Descriptor.Manufacturer, d.Descriptor.AppiumURL, d.Descriptor.MirrorPort})
		}
		f.mu.Unlock()
		ok(map[string]any{"nodeId": report.NodeID})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/heartbeat"):
		var report struct {
			Devices []struct {
				Descriptor struct {
					Udid, Model, Manufacturer, AppiumURL string
					MirrorPort                           *int
				} `json:"descriptor"`
			} `json:"devices"`
		}
		json.Unmarshal(body, &report)
		f.mu.Lock()
		n := f.node(strings.TrimSuffix(path, "/heartbeat"))
		n.Heartbeats++
		n.Devices = nil
		for _, d := range report.Devices {
			n.Devices = append(n.Devices, Device{d.Descriptor.Udid, d.Descriptor.Model, d.Descriptor.Manufacturer, d.Descriptor.AppiumURL, d.Descriptor.MirrorPort})
		}
		f.mu.Unlock()
		ok(map[string]any{"provisionTasks": []any{}, "commands": []any{}})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/leave"):
		f.mu.Lock()
		n := f.node(strings.TrimSuffix(path, "/leave"))
		n.Leaves++
		n.Devices = nil
		f.mu.Unlock()
		ok(nil)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/tunnel"):
		f.tunnel(w, r, strings.TrimSuffix(path, "/tunnel"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// node is the node with this id, made when first heard. The caller holds f.mu.
func (f *Farm) node(id string) *live {
	n := f.nodes[id]
	if n == nil {
		n = &live{Node: Node{ID: id}, pending: map[string]chan []byte{}}
		f.nodes[id] = n
	}
	return n
}

func (f *Farm) tunnel(w http.ResponseWriter, r *http.Request, id string) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(-1)
	f.mu.Lock()
	n := f.node(id)
	n.conn = conn
	f.tunnels++
	f.mu.Unlock()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			f.mu.Lock()
			if n.conn == conn {
				n.conn = nil
			}
			f.mu.Unlock()
			return
		}
		f.mu.Lock()
		f.bodies = append(f.bodies, data)
		var head struct {
			ID string `json:"id"`
		}
		json.Unmarshal(data, &head)
		waiting := n.pending[head.ID]
		delete(n.pending, head.ID)
		f.mu.Unlock()
		if waiting != nil {
			waiting <- data
		}
	}
}

// Nodes is a copy of what the farm knows, by node id.
func (f *Farm) Nodes() map[string]Node {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]Node{}
	for id, n := range f.nodes {
		out[id] = Node{ID: n.ID, HostName: n.HostName, Version: n.Version, Devices: append([]Device(nil), n.Devices...),
			Registers: n.Registers, Heartbeats: n.Heartbeats, Leaves: n.Leaves}
	}
	return out
}

// Refused is how many calls came with a token the farm does not take.
func (f *Farm) Refused() int { f.mu.Lock(); defer f.mu.Unlock(); return f.refused }

// Fetches is how many times the build was downloaded.
func (f *Farm) Fetches() int { f.mu.Lock(); defer f.mu.Unlock(); return f.fetches }

// Crossed is everything that crossed the farm but the token's own header: request URLs, other headers, bodies and the
// frames the agents answered with — where a token that leaked would be found.
func (f *Farm) Crossed() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all bytes.Buffer
	for _, u := range f.urls {
		all.WriteString(u + "\n")
	}
	for _, h := range f.headers {
		all.WriteString(h + "\n")
	}
	for _, b := range f.bodies {
		all.Write(b)
		all.WriteString("\n")
		if decoded := decodedBodies(b); decoded != nil {
			all.Write(decoded)
			all.WriteString("\n")
		}
	}
	return all.Bytes()
}

func decodedBodies(frame []byte) []byte {
	var f struct {
		BodyBase64 *string `json:"bodyBase64"`
	}
	if json.Unmarshal(frame, &f) != nil || f.BodyBase64 == nil {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(*f.BodyBase64)
	if err != nil {
		return nil
	}
	return raw
}

// WaitFor waits until want holds of the nodes, or the bound passes.
func (f *Farm) WaitFor(bound time.Duration, want func(map[string]Node, int) bool) bool {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		tunnels := 0
		for _, n := range f.nodes {
			if n.conn != nil {
				tunnels++
			}
		}
		f.mu.Unlock()
		if want(f.Nodes(), tunnels) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Send sends one frame down a node's tunnel and waits for the frame that answers it (by id); a frame kind that answers
// with no frame of that id (a stream refused in words is answered as stream-end) is waited for up to the bound.
func (f *Farm) Send(nodeID string, frame map[string]any, bound time.Duration) ([]byte, error) {
	f.mu.Lock()
	n := f.nodes[nodeID]
	var conn *websocket.Conn
	if n != nil {
		conn = n.conn
	}
	if conn == nil {
		f.mu.Unlock()
		return nil, fmt.Errorf("node %s has no tunnel", nodeID)
	}
	id, _ := frame["id"].(string)
	if id == "" {
		id = newID()
		frame["id"] = id
	}
	answer := make(chan []byte, 1)
	n.pending[id] = answer
	f.mu.Unlock()
	payload, _ := json.Marshal(frame)
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	n.writeMu.Lock()
	err := conn.Write(ctx, websocket.MessageText, payload)
	n.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case data := <-answer:
		return data, nil
	case <-ctx.Done():
		return nil, errors.New("no answer within the bound")
	}
}

// SendInOrder sends frames down a node's tunnel one after another, without waiting for an answer in between — as a relay
// carrying several requests at once does — and then waits for every answer, by id, until the bound. The answers come back
// in the order of frames, whatever order they arrived in.
func (f *Farm) SendInOrder(nodeID string, frames []map[string]any, bound time.Duration) ([][]byte, error) {
	f.mu.Lock()
	n := f.nodes[nodeID]
	var conn *websocket.Conn
	if n != nil {
		conn = n.conn
	}
	if conn == nil {
		f.mu.Unlock()
		return nil, fmt.Errorf("node %s has no tunnel", nodeID)
	}
	waits := make([]chan []byte, len(frames))
	for i, frame := range frames {
		id, _ := frame["id"].(string)
		if id == "" {
			id = newID()
			frame["id"] = id
		}
		waits[i] = make(chan []byte, 1)
		n.pending[id] = waits[i]
	}
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	n.writeMu.Lock()
	for _, frame := range frames {
		payload, _ := json.Marshal(frame)
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			n.writeMu.Unlock()
			return nil, err
		}
	}
	n.writeMu.Unlock()
	answers := make([][]byte, len(frames))
	for i := range frames {
		select {
		case answers[i] = <-waits[i]:
		case <-ctx.Done():
			return nil, fmt.Errorf("no answer to frame %d of %d within the bound", i+1, len(frames))
		}
	}
	return answers, nil
}

// Runner is a runner's Appium client on one node's tunnel: each request is a frame, as the farm relays it.
func (f *Farm) Runner(nodeID string) runnertest.Doer {
	return func(method, path string, body []byte) (int, []byte, error) {
		frame := map[string]any{"method": method, "path": path, "headers": map[string]string{"Content-Type": "application/json"}}
		if len(body) > 0 {
			encoded := base64.StdEncoding.EncodeToString(body)
			frame["bodyBase64"] = encoded
		} else {
			frame["bodyBase64"] = nil
		}
		data, err := f.Send(nodeID, frame, 60*time.Second)
		if err != nil {
			return 0, nil, err
		}
		var answer struct {
			Status     int     `json:"status"`
			BodyBase64 *string `json:"bodyBase64"`
		}
		if err := json.Unmarshal(data, &answer); err != nil {
			return 0, nil, err
		}
		var raw []byte
		if answer.BodyBase64 != nil {
			raw, _ = base64.StdEncoding.DecodeString(*answer.BodyBase64)
		}
		return answer.Status, raw, nil
	}
}
