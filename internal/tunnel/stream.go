package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// ── THE LIVE VIEW: a phone's mirror, through the tunnel's stream frames ─────────────────────────────────────────
//
// The farm's rack node's stream family (RelayTunnelClient.StartStream/PumpStreamAsync), ported with the node's bounds: a cap
// on concurrent pumps (MaxConcurrentStreams, 4), 64 KB upstream reads, a pump that never stands between the tunnel and
// its other frames, and a stream cancel that never reaches a write. What the agent adds is in liveview.Offered (which
// upstreams it will open — stricter than the node's, and why) and three ends the node leaves to the farm: the phone
// going away, the mirror going silent (the farm's own 30 s idle bound, StreamIdleTimeoutSeconds), and a slow viewer,
// which waits on the send queue instead of on the network.

// Frame shapes — RelayStreamStart / RelayStreamChunk / RelayStreamEnd / RelayStreamCancel, camelCase.
type streamStartFrame struct {
	Kind    string            `json:"kind"`
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	// LeaseID is the lease a capture pull was made on (A_CAPTURE_PULL_TIED_TO_ITS_LEASE): the farm sets it on capture
	// pulls only, and a pull opens only that lease's capture.
	LeaseID string `json:"leaseId,omitempty"`
}

type streamChunkFrame struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Seq        int64  `json:"seq"`
	BodyBase64 string `json:"bodyBase64"`
}

// streamEndFrame is RelayStreamEnd: the upstream ended (Status), or the stream could not be served (Error).
type streamEndFrame struct {
	Kind   string  `json:"kind"`
	ID     string  `json:"id"`
	Status *int    `json:"status"`
	Error  *string `json:"error"`
}

// MaxConcurrentStreams — the node's cap (Node:Relay:MaxConcurrentStreams, 4), and the farm's own per-node cap.
const MaxConcurrentStreams = 4

// StreamIdle — how long a mirror may send nothing before its stream is ended: the farm's own idle reaper
// (RelayPublishOptions.StreamIdleTimeoutSeconds, 30), so a stream whose cancel never arrives still ends here.
const StreamIdle = 30 * time.Second

// streamChunkBytes — the node's upstream read buffer, which bounds one chunk.
const streamChunkBytes = 64 * 1024

// Why a stream's context ended — read back with context.Cause, so the end frame says what happened.
var (
	errFarmCancelled = errors.New("the farm cancelled the stream")
	errPhoneGone     = errors.New("the phone went away")
	errMirrorIdle    = errors.New("the mirror went silent")
	errTunnelEnded   = errors.New("the tunnel ended")
)

// harRefusal is what a HAR pull's stream-end says back to the farm (which hands it to the consumer — RelayStreamEndpoint
// carries a node's End reason) when this tunnel carries no capture. Every narration in this file is a LITERAL at its
// call, so the transcript census (connect/transcript_test.go), which discovers narration from the source, reads it.
const harRefusal = "no capture: this agent was started without network capture"

// harPathPrefix is the farm's addressing for a network-capture HAR pull on the stream channel — the token the farm's
// LeaseController builds and the C# node's capture controller resolves. Both the raw and the query-encoded form are
// matched (lower-cased): a refusal written against the pretty form alone once passed while the real pull was refused
// under the wrong name.
const (
	harPathPrefix        = "gfhar:"
	harPathPrefixEncoded = "gfhar%3a"
)

func isHarPull(path string) bool {
	lowered := strings.ToLower(path)
	return strings.Contains(lowered, harPathPrefix) || strings.Contains(lowered, harPathPrefixEncoded)
}

// mirrorUpstream reads a stream's path as an upstream this agent may open: an absolute http(s) URL on this machine's
// loopback — the node's SSRF rule (RelayStreamUpstream), word for word where it can be. It returns the port for the
// second half of the rule, which is liveview.Offered's. A refusal is a sentence; nothing the consumer sent is echoed
// into it except the port number.
func mirrorUpstream(path string) (upstream string, port uint16, refusal string) {
	if !hasURIScheme(path) {
		return "", 0, "this agent streams a phone's mirror and nothing else — a path relative to Appium is not one"
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.Host == "" {
		return "", 0, "malformed absolute stream URL"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", 0, "unsupported stream scheme — a phone's mirror is served over http"
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return "", 0, "absolute stream upstreams must target loopback (127.0.0.1 / localhost / ::1)"
	}
	portText := parsed.Port()
	if portText == "" {
		portText = map[string]string{"http": "80", "https": "443"}[parsed.Scheme]
	}
	number, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || number == 0 {
		return "", 0, "malformed absolute stream URL"
	}
	return parsed.String(), uint16(number), ""
}

// isTheMirror is the farm's own rule for a mirror's address (LiveViewRules.IsTheMirror), compared as a whole string and
// ignoring case: http://127.0.0.1:{port}, http://localhost:{port} or http://[::1]:{port}, with or without a trailing slash.
func isTheMirror(path string, port uint16) bool {
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		address := fmt.Sprintf("http://%s:%d", host, port)
		if strings.EqualFold(path, address) || strings.EqualFold(path, address+"/") {
			return true
		}
	}
	return false
}

// hasURIScheme is true when the string starts with an RFC 3986 "<scheme>://" — the node's HasUriScheme, so a bare path
// is never read as a URL.
func hasURIScheme(s string) bool {
	delimiter := strings.Index(s, "://")
	if delimiter <= 0 {
		return false
	}
	for i := 0; i < delimiter; i++ {
		c := s[i]
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 && !letter {
			return false
		}
		if i > 0 && !letter && !(c >= '0' && c <= '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// loopbackStreamClient opens a mirror and NOTHING ELSE: no proxy, no redirect followed (a redirect would be a second
// upstream nobody checked), and a dialer that refuses any address that is not loopback AFTER name resolution — so even
// "localhost" that resolves somewhere else is not dialled — and any screen server that runs as another account on this
// machine (internal/localconn; A_MACHINE_THAT_STAYS_THE_CUSTOMERS): its frames would reach the workspace as this
// phone's screen. No overall timeout: a mirror is long-lived; a dead one is ended by the idle bound, a slow one to open
// by ResponseHeaderTimeout.
func loopbackStreamClient(owner localconn.Owner) *http.Client {
	dialer := localconn.Dialer{What: "a phone's live view", Timeout: 5 * time.Second, Owner: owner}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			DisableKeepAlives:     true,
			DisableCompression:    true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// startStream answers a stream-start: refuse it in its own words, or start a pump. Never blocks the read loop.
func (l *link) startStream(data []byte) {
	var start streamStartFrame
	if err := json.Unmarshal(data, &start); err != nil || start.ID == "" {
		return
	}
	// A HAR PULL rides the same frame and is a different capability: served from this machine's capture, or refused BY
	// NAME — never as a live view.
	if isHarPull(start.Path) {
		l.startPull(start)
		return
	}
	upstream, port, refusal := mirrorUpstream(start.Path)
	var phone string
	var gone <-chan struct{}
	if refusal == "" {
		var offered bool
		if l.t.cfg.Mirrors != nil {
			phone, gone, offered = l.t.cfg.Mirrors.Mirror(port)
		}
		if !offered {
			refusal = fmt.Sprintf("port %d on this machine is not a phone's mirror this agent reserved — it streams "+
				"only the mirror ports it declared for the phones it offers", port)
		} else if !isTheMirror(start.Path, port) {
			// THE ADDRESS RESERVED, AND ONLY IT: the port was checked free, and is declared, at 127.0.0.1 — so a
			// path, a query, a user name, https, or another loopback address names something nobody checked.
			refusal = fmt.Sprintf("this agent opens a phone's mirror only at http://127.0.0.1:%d/ (written also as localhost "+
				"or [::1]), and this stream asked for it with something more or other, so it was not opened", port)
		}
		// …and it is opened at exactly that address, whichever of the farm's spellings asked for it.
		upstream = fmt.Sprintf("http://127.0.0.1:%d/", port)
	}
	if refusal != "" {
		if l.t.saidNotAMirror.CompareAndSwap(false, true) {
			l.t.say("the farm asked for a live view this agent will not open — it streams a phone's mirror only from the loopback port it reserved for a phone it offers, and nothing else on this machine\n")
		}
		l.sendEnd(start.ID, nil, refusal)
		return
	}

	l.streamsMu.Lock()
	if len(l.streams) >= l.t.maxStreams() {
		l.streamsMu.Unlock()
		l.sendEnd(start.ID, nil, fmt.Sprintf("this agent is already streaming %d live views, its cap", l.t.maxStreams()))
		return
	}
	if _, duplicate := l.streams[start.ID]; duplicate {
		l.streamsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancelCause(l.life)
	l.streams[start.ID] = cancel
	l.streamsMu.Unlock()

	if l.t.saidStreaming.CompareAndSwap(false, true) {
		l.t.say("the farm opened a live view — this agent streams a phone's mirror from the port it reserved for that phone\n")
	}
	go l.pump(ctx, cancel, start, upstream, port, phone, gone)
}

// startPull answers a capture pull: refused in the farm's words when it names no lease or another lease's, else served
// by a pump of its own — never on the read loop.
func (l *link) startPull(start streamStartFrame) {
	notFound := http.StatusNotFound
	if l.t.cfg.Capture == nil {
		if l.t.saidNoHar.CompareAndSwap(false, true) {
			l.t.say("the farm asked for a network-capture HAR pull, and this agent was started without network capture\n")
		}
		l.sendEnd(start.ID, &notFound, harRefusal)
		return
	}
	lease, refusal := pullLease(start.Path, start.LeaseID)
	if refusal != "" {
		if l.t.saidBadPull.CompareAndSwap(false, true) {
			l.t.say("the farm asked for a network-capture HAR pull this agent will not open — a lease opens only its own capture\n")
		}
		l.sendEnd(start.ID, &notFound, refusal)
		return
	}
	// A PULL IS A STREAM, held to the node's cap of streams like a live view — and told in its own words.
	l.streamsMu.Lock()
	if len(l.streams) >= l.t.maxStreams() {
		l.streamsMu.Unlock()
		l.sendEnd(start.ID, nil, fmt.Sprintf("this agent is already sending %d streams, its cap, so the capture was not sent now", l.t.maxStreams()))
		return
	}
	if _, duplicate := l.streams[start.ID]; duplicate {
		l.streamsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancelCause(l.life)
	l.streams[start.ID] = cancel
	l.streamsMu.Unlock()
	go l.pumpPull(ctx, cancel, start.ID, lease)
}

// pumpPull sends one lease's masked capture as ordered chunks, then its end, and DELETES THE FILE only once every byte has
// left this process for the network — a pull the tunnel dropped in the middle of keeps its file for its hour.
func (l *link) pumpPull(ctx context.Context, cancel context.CancelCauseFunc, id, lease string) {
	defer func() {
		cancel(nil)
		l.streamsMu.Lock()
		delete(l.streams, id)
		l.streamsMu.Unlock()
	}()
	data, refusal := l.t.cfg.Capture.Pull(lease)
	if refusal != "" {
		notFound := http.StatusNotFound
		l.sendEnd(id, &notFound, refusal)
		return
	}
	var seq int64
	for offset := 0; offset < len(data); offset += streamChunkBytes {
		if err := l.queue.WaitBelow(ctx, l.t.pumpHighWater()); err != nil {
			return
		}
		chunk := streamChunkFrame{Kind: "stream-chunk", ID: id, Seq: seq,
			BodyBase64: base64.StdEncoding.EncodeToString(data[offset:min(offset+streamChunkBytes, len(data))])}
		seq++
		if err := l.send(chunk); err != nil {
			return
		}
	}
	ok := http.StatusOK
	l.sendEnd(id, &ok, "")
	if err := l.queue.WaitBelow(ctx, 1); err != nil || !l.queue.Open() || l.life.Err() != nil {
		return
	}
	l.t.cfg.Capture.Pulled(lease)
}

// cancelStream answers a stream-cancel — idempotent for an id that is unknown or already finished.
func (l *link) cancelStream(id string) {
	l.streamsMu.Lock()
	cancel, ok := l.streams[id]
	l.streamsMu.Unlock()
	if ok {
		cancel(errFarmCancelled)
	}
}

// pump opens one phone's mirror and sends it back as ordered chunks until it ends, then says how — or, for a cancel or
// a tunnel that ended, says nothing (the node's rule: the farm already knows, or nobody is there to tell).
func (l *link) pump(ctx context.Context, cancel context.CancelCauseFunc, start streamStartFrame, upstream string, port uint16,
	phone string, gone <-chan struct{}) {
	defer func() {
		cancel(nil)
		l.streamsMu.Lock()
		delete(l.streams, start.ID)
		l.streamsMu.Unlock()
	}()
	// THE PHONE GOING AWAY ends its mirror: the connect loop withdrew it from what is offered (it was unplugged, or is
	// no longer offered for any reason a lease would also end on).
	go func() {
		select {
		case <-gone:
			cancel(errPhoneGone)
		case <-ctx.Done():
		}
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		l.endFor(ctx, start.ID, nil, "malformed absolute stream URL")
		return
	}
	if accept := start.Headers["Accept"]; accept != "" {
		request.Header.Set("Accept", accept) // the one header worth carrying to an MJPEG server
	}
	response, err := l.t.streamClient().Do(request)
	if err != nil {
		l.endFor(ctx, start.ID, nil, fmt.Sprintf("the phone's mirror at 127.0.0.1:%d could not be opened: %v", port, err))
		return
	}
	defer response.Body.Close()
	status := response.StatusCode

	idleFor := l.t.streamIdle()
	idle := time.AfterFunc(idleFor, func() { cancel(errMirrorIdle) })
	defer idle.Stop()
	buffer := make([]byte, streamChunkBytes)
	var seq int64
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			// A SLOW VIEWER WAITS HERE, on the send queue, holding nothing — and the idle bound is off while it does,
			// because a viewer slower than the phone is not a phone that went silent.
			idle.Stop()
			if err := l.queue.WaitBelow(ctx, l.t.pumpHighWater()); err != nil {
				l.endFor(ctx, start.ID, nil, "")
				return
			}
			chunk := streamChunkFrame{Kind: "stream-chunk", ID: start.ID, Seq: seq, BodyBase64: base64.StdEncoding.EncodeToString(buffer[:n])}
			seq++
			if err := l.send(chunk); err != nil {
				return // the tunnel failed under it; the tunnel's end ends every pump
			}
			idle.Reset(idleFor)
		}
		if readErr == io.EOF {
			l.endFor(ctx, start.ID, &status, "")
			return
		}
		if readErr != nil {
			l.endFor(ctx, start.ID, nil, fmt.Sprintf("the phone's mirror at 127.0.0.1:%d stopped: %v", port, readErr))
			return
		}
	}
}

// endFor sends the stream's end for whatever ended it: a cancel or the tunnel's end say nothing, the phone going away
// and the mirror going silent say so, and otherwise the upstream's own status or failure is carried.
func (l *link) endFor(ctx context.Context, id string, status *int, failure string) {
	switch context.Cause(ctx) {
	case errFarmCancelled, errTunnelEnded:
		return
	case errPhoneGone:
		l.sendEnd(id, nil, "the phone went away — this agent no longer offers it, so its live view ended")
		return
	case errMirrorIdle:
		l.sendEnd(id, nil, fmt.Sprintf("the phone's mirror sent nothing for %s, so its live view ended", l.t.streamIdle()))
		return
	}
	if ctx.Err() != nil && failure == "" && status == nil {
		return
	}
	l.sendEnd(id, status, failure)
}

// sendEnd sends a stream-end, never waiting for room — it is a control frame — and never on a tunnel that has ended.
func (l *link) sendEnd(id string, status *int, failure string) {
	if l.life.Err() != nil {
		return
	}
	end := streamEndFrame{Kind: "stream-end", ID: id, Status: status}
	if failure != "" {
		end.Error = &failure
	}
	_ = l.send(end)
}
