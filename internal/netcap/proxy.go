// Package netcap is what a network capture runs on this machine: the loopback proxy a test phone's traffic goes
// through during a capture run, the agent's own certificate authority that lets it read HTTPS the phone's app trusts it
// for, the masking that runs on every exchange before anything is written, and the short-lived masked file a capture
// leaves for the farm to pull. It touches no phone and runs no command: internal/capture does that, and asks this
// package for a proxy only when every gate holds.
package netcap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ── THE LOOPBACK PROXY A PHONE'S TRAFFIC GOES THROUGH ───────────────────────────────────────────────────────────
//
// WHAT IT BINDS: 127.0.0.1, on the one port the capture chose and checked (port.go). The phone reaches it over
// `adb reverse`; nothing else on the network can — it never binds a routable address, exactly as the node's mitmdump
// listens with --listen-host 127.0.0.1. It runs only while a capture runs, and Close ends every connection it holds.
//
// WHAT IT DOES WITH AN EXCHANGE: forwards it, untouched, to the host the phone asked for — the phone gets the original
// bytes, compressed bodies included (the upstream leg never asks for or undoes compression of its own) — and hands a
// copy to the capture's Recording, which masks it before anything is written. HTTPS is read only when the phone's
// client accepts the certificate this machine's CA makes for the host; HTTP/1.1 is spoken to the phone, and HTTP/2 to
// a host that offers it.
//
// THE LIMITS (the spike's, carried over, plus the operator's):
//   - a host whose client REFUSES the capture certificate — an app or SDK that pins, a system service with its own trust
//     store — is recorded ONCE ("not readable: …") and then passed through untouched for the rest of the capture: no
//     decryption, and none of the phone's own services broken past that first refused connection;
//   - a body is kept to its first megabyte for the record and streamed to the phone whole;
//   - an upstream that has not answered within UpstreamHeaderTimeout is given up on: the phone gets a 502 and the record
//     says so, in this agent's words;
//   - a WebSocket upgrade is forwarded and recorded; its messages are not;
//   - a capture records at most 500 exchanges (MaxEntries).
//
// NO LIBRARY ERROR TEXT IS EVER RECORDED, PRINTED OR SENT. Go's own errors for an upstream request carry the whole URL —
// query string, tokens and all — so every failure here is reduced to this agent's own sentence, and the standard library's
// logging is pointed at nothing.

const (
	// UpstreamHeaderTimeout is how long an upstream may take to start answering before the phone is told 502. Two minutes,
	// not the spike's fifteen seconds: a phone's long-poll request waits longer than that, and cutting it would change how
	// the app under test behaves during a capture. Nothing a phone's own client still waits for is cut.
	UpstreamHeaderTimeout = 2 * time.Minute

	dialTimeout      = 10 * time.Second
	handshakeTimeout = 10 * time.Second
	readHeaderBound  = 30 * time.Second
	idleBound        = 2 * time.Minute
)

// Options configures a proxy.
type Options struct {
	CA *CA
	// Recording receives every exchange; nil records nothing (the trust check).
	Recording *Recording
	// TrustCheck, when set, makes this the trust check's proxy: only TrustCheck.Host is decrypted, and answered here;
	// everything else passes through untouched and unrecorded.
	TrustCheck *TrustCheck
	// UpstreamRoots replaces the system's roots for the upstream leg. A TEST SEAM: no flag or variable reaches it.
	UpstreamRoots *x509.CertPool
	// UpstreamHeaderTimeout overrides the bound above (a test seam; zero = the constant).
	UpstreamHeaderTimeout time.Duration
	Now                   func() time.Time
	// PeerOwner names the account behind each connection (peer.go); nil = OwnerOfPeer, this system's kernel. A TEST SEAM:
	// no flag or variable reaches it.
	PeerOwner PeerOwner
	// Refused hears each connection closed unread because another account opened it (found) or its account could not be
	// found (!found) — for the console. nil = nobody hears it; the connection is refused all the same.
	Refused func(uid int, found bool)
	// ReachLocal lets the phone's traffic reach this machine and its local network — the customer's
	// --capture-allow-local-network. False, the default, refuses them (destination.go).
	ReachLocal bool
	// refuses replaces the local rule — a TEST SEAM, unexported: no flag or variable reaches it.
	refuses func(net.IP) bool
}

// Proxy is one capture's proxy.
type Proxy struct {
	opts     Options
	upstream *http.Transport
	reverse  *httputil.ReverseProxy
	dialer   *net.Dialer

	mu          sync.Mutex
	listener    net.Listener
	server      *http.Server
	conns       map[net.Conn]struct{}
	passThrough map[string]bool
	closed      bool
}

// quietLog is where the standard library's own messages go: nowhere. They name addresses this agent masks.
var quietLog = log.New(io.Discard, "", 0)

// NewProxy builds a proxy; Listen and Serve start it.
func NewProxy(opts Options) *Proxy {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.UpstreamHeaderTimeout <= 0 {
		opts.UpstreamHeaderTimeout = UpstreamHeaderTimeout
	}
	if opts.PeerOwner == nil {
		opts.PeerOwner = OwnerOfPeer
	}
	p := &Proxy{opts: opts, conns: map[net.Conn]struct{}{}, passThrough: map[string]bool{}}
	p.dialer = &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, Control: p.destinationControl}
	p.upstream = &http.Transport{
		// DIRECT, from this machine: never through a proxy of this machine's environment.
		Proxy:       nil,
		DialContext: p.dialer.DialContext,
		// The upstream's certificate is verified as a browser would; a forged one is caught on the way out.
		TLSClientConfig:       &tls.Config{RootCAs: opts.UpstreamRoots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   handshakeTimeout,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true, // the phone's own Accept-Encoding goes up, and the body it gets back is untouched
		ResponseHeaderTimeout: opts.UpstreamHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          64,
		IdleConnTimeout:       30 * time.Second,
	}
	p.reverse = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		Transport:      p.upstream,
		FlushInterval:  -1,
		ModifyResponse: p.sawResponse,
		ErrorHandler:   p.failed,
		ErrorLog:       quietLog,
	}
	return p
}

// Listen binds 127.0.0.1 on port (0 lets the system choose — tests only) and returns the address. Loopback only, by
// construction: the host is not a parameter.
func (p *Proxy) Listen(port uint16) (string, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = listener.Close()
		return "", net.ErrClosed
	}
	p.listener = &trackingListener{Listener: listener, proxy: p}
	p.server = &http.Server{
		Handler:           http.HandlerFunc(p.serveOuter),
		ReadHeaderTimeout: readHeaderBound,
		IdleTimeout:       idleBound,
		ErrorLog:          quietLog,
	}
	return listener.Addr().String(), nil
}

// Serve answers until Close. Call Listen first.
func (p *Proxy) Serve() {
	p.mu.Lock()
	server, listener := p.server, p.listener
	p.mu.Unlock()
	if server == nil {
		return
	}
	_ = server.Serve(listener)
}

// Close stops the proxy and ends every connection it holds — the phone's and the upstreams' alike.
func (p *Proxy) Close() error {
	p.mu.Lock()
	p.closed = true
	server := p.server
	conns := make([]net.Conn, 0, len(p.conns))
	for c := range p.conns {
		conns = append(conns, c)
	}
	p.conns = map[net.Conn]struct{}{}
	p.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	p.upstream.CloseIdleConnections()
	return nil
}

// PassesThrough reports whether the phone's traffic to hostPort is passed through unread for the rest of the capture.
func (p *Proxy) PassesThrough(hostPort string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.passThrough[hostPort]
}

func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.conns[c] = struct{}{}
	return true
}

func (p *Proxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
}

// trackingListener remembers every connection it hands out, so Close can end the hijacked ones too — and hands out only
// connections this account (or the machine's administrator) opened: any other is closed here, before a byte of it is read
// (peer.go says why, and how the kernel is asked).
type trackingListener struct {
	net.Listener
	proxy *Proxy
}

func (l *trackingListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if uid, found := l.proxy.opts.PeerOwner(c); !found || !MayUseTheProxy(uid) {
			_ = c.Close()
			if l.proxy.opts.Refused != nil {
				l.proxy.opts.Refused(uid, found)
			}
			continue
		}
		if !l.proxy.track(c) {
			_ = c.Close()
			return nil, net.ErrClosed
		}
		return &untrackOnClose{Conn: c, proxy: l.proxy}, nil
	}
}

type untrackOnClose struct {
	net.Conn
	proxy *Proxy
	once  sync.Once
}

func (c *untrackOnClose) Close() error {
	c.once.Do(func() { c.proxy.untrack(c.Conn) })
	return c.Conn.Close()
}

// ── the phone's request ───────────────────────────────────────────────────────────────────────────────────────

func (p *Proxy) serveOuter(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.serveConnect(w, r)
		return
	}
	// A PLAIN-HTTP REQUEST arrives in absolute form (GET http://host/path). It needs no certificate, and plain HTTP is
	// readable by whatever it passes through — which the customer is told.
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "this is a network capture proxy, and this request was not one a proxy is sent", http.StatusBadRequest)
		return
	}
	// THE TRUST CHECK'S OWN ADDRESS, IN PLAIN HTTP, is answered here and never forwarded: it is the check's last page
	// (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 4), and the address exists nowhere else.
	if trust := p.opts.TrustCheck; trust != nil && trust.is(r.URL.Hostname()) {
		trust.answerPlain(w, r)
		return
	}
	p.forward(w, r, "http", r.URL.Host)
}

func (p *Proxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	hostPort := r.Host
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host, hostPort = hostPort, net.JoinHostPort(hostPort, "443")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "this capture proxy cannot open a tunnel here", http.StatusInternalServerError)
		return
	}
	raw, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer raw.Close()
	if _, err := raw.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	client := net.Conn(raw)
	if n := buffered.Reader.Buffered(); n > 0 {
		head, _ := buffered.Reader.Peek(n)
		client = &prefixConn{Conn: raw, prefix: append([]byte(nil), head...)}
	}

	if trust := p.opts.TrustCheck; trust != nil && !trust.is(host) {
		p.tunnel(client, hostPort)
		return
	}
	if p.PassesThrough(hostPort) {
		p.tunnel(client, hostPort)
		return
	}
	// NOT TLS AT ALL (a protocol of its own over the tunnel): passed through as it is — there is nothing here to read.
	peeked, isTLS := peekTLS(client)
	if !isTLS {
		p.tunnel(peeked, hostPort)
		return
	}

	tlsConn := tls.Server(peeked, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// The name the phone's client checks the certificate against is the one it sent (SNI); a client reaching an
			// address by IP sends none, and then the address it asked the proxy for is the name.
			name := hello.ServerName
			if name == "" {
				name = host
			}
			return p.opts.CA.leafFor(name)
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	err = tlsConn.HandshakeContext(ctx)
	cancel()
	if err != nil {
		if refusedByClient(err) {
			if trust := p.opts.TrustCheck; trust != nil {
				trust.report(TrustRefused)
				return
			}
			p.mu.Lock()
			p.passThrough[hostPort] = true
			p.mu.Unlock()
			p.opts.Recording.RecordRefusal(hostPort)
		}
		return
	}
	defer tlsConn.Close()
	if trust := p.opts.TrustCheck; trust != nil {
		trust.answer(tlsConn)
		return
	}
	p.serveDecrypted(tlsConn, hostPort)
}

// refusedByClient: the phone's client ended our handshake — an alert of its own (it does not trust the certificate), or
// it hung up in the middle of it.
func refusedByClient(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "remote error" {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)
}

// serveDecrypted serves the requests on one decrypted connection, keep-alive and all, until it closes.
func (p *Proxy) serveDecrypted(conn *tls.Conn, hostPort string) {
	listener := newOneConnListener(conn)
	server := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.forward(w, r, "https", hostPort) }),
		ReadHeaderTimeout: readHeaderBound,
		IdleTimeout:       idleBound,
		ErrorLog:          quietLog,
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				_ = listener.Close()
			}
		},
	}
	_ = server.Serve(listener)
}

// tunnel splices the phone's connection to hostPort, both ways, untouched and unread.
func (p *Proxy) tunnel(client net.Conn, hostPort string) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	upstream, err := p.dialer.DialContext(ctx, "tcp", hostPort)
	cancel()
	if err != nil {
		// A TUNNEL TO THIS MACHINE OR ITS NETWORK is refused like any request (destination.go), and recorded once per host:
		// a spliced tunnel has nothing else in the capture to say it was ever asked for.
		if errors.As(err, &errLocalDestination{}) {
			p.opts.Recording.RecordLocalRefusal(hostPort)
		}
		return
	}
	if !p.track(upstream) {
		_ = upstream.Close()
		return
	}
	defer func() {
		p.untrack(upstream)
		_ = upstream.Close()
	}()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

// ── one exchange, forwarded and recorded ─────────────────────────────────────────────────────────────────────

type flowKey struct{}

// exchange is one request's state while it is forwarded: the flow being built, the kept copies of its bodies, and when
// each part happened.
type exchange struct {
	proxy    *Proxy
	scheme   string
	hostPort string
	flow     Flow
	request  *kept
	response *kept
	started  time.Time
	sent     time.Time
	answered time.Time
	once     sync.Once
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, scheme, hostPort string) {
	x := &exchange{proxy: p, scheme: scheme, hostPort: hostPort, started: p.opts.Now()}
	x.flow = Flow{
		Started:    x.started,
		Method:     r.Method,
		URL:        prettyURL(scheme, hostPort, r.URL),
		ReqProto:   r.Proto,
		ReqHeaders: append([]Header{{Name: "Host", Value: r.Host}}, headersOf(r.Header)...),
	}
	if r.Body != nil && r.Body != http.NoBody {
		x.request = &kept{}
		r.Body = &teeBody{ReadCloser: r.Body, sink: x.request, atEOF: func() { x.sent = p.opts.Now() }}
	}
	defer x.finish() // also when the copy to the phone panics with http.ErrAbortHandler (a phone that hung up)
	p.reverse.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), flowKey{}, x)))
}

func exchangeOf(ctx context.Context) *exchange {
	x, _ := ctx.Value(flowKey{}).(*exchange)
	return x
}

func (p *Proxy) rewrite(pr *httputil.ProxyRequest) {
	x := exchangeOf(pr.In.Context())
	if x == nil {
		return
	}
	pr.Out.URL.Scheme = x.scheme
	pr.Out.URL.Host = x.hostPort
	pr.Out.Host = pr.In.Host
}

// sawResponse records the response's head and keeps a copy of its body as it streams to the phone.
func (p *Proxy) sawResponse(response *http.Response) error {
	x := exchangeOf(response.Request.Context())
	if x == nil {
		return nil
	}
	x.answered = p.opts.Now()
	x.flow.Status = response.StatusCode
	x.flow.Reason = strings.TrimSpace(strings.TrimPrefix(response.Status, strconv.Itoa(response.StatusCode)))
	x.flow.RespProto = response.Proto
	x.flow.RespHeaders = headersOf(response.Header)
	if response.StatusCode == http.StatusSwitchingProtocols {
		// A WEBSOCKET OPENED: the upgrade is the record, and the connection it becomes is spliced, not read.
		x.flow.WebSocket = true
		x.finish()
		return nil
	}
	x.response = &kept{}
	response.Body = &teeBody{ReadCloser: response.Body, sink: x.response}
	return nil
}

// failed is an upstream that never answered (or failed to): the phone is told 502, and the record says why in this
// agent's words.
func (p *Proxy) failed(w http.ResponseWriter, r *http.Request, err error) {
	if x := exchangeOf(r.Context()); x != nil {
		x.flow.Err = describeUpstreamFailure(err)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = io.WriteString(w, "network capture proxy: "+describeUpstreamFailure(err)+"\n")
}

// finish records the exchange, once.
func (x *exchange) finish() {
	x.once.Do(func() {
		now := x.proxy.opts.Now()
		if x.request != nil {
			x.flow.ReqBody, x.flow.ReqBodySize = x.request.contents()
		}
		if x.response != nil {
			x.flow.RespBody, x.flow.RespBodySize = x.response.contents()
		}
		sent := x.sent
		if sent.IsZero() {
			sent = x.started
		}
		x.flow.SendMs = millis(x.started, sent)
		if !x.answered.IsZero() {
			x.flow.WaitMs = millis(sent, x.answered)
			x.flow.ReceiveMs = millis(x.answered, now)
		}
		x.proxy.opts.Recording.Record(x.flow)
	})
}

// describeUpstreamFailure is THIS AGENT'S SENTENCE for a failed upstream request — never the error's own text, which
// for a Go request names the whole URL.
func describeUpstreamFailure(err error) string {
	if errors.As(err, &errLocalDestination{}) {
		return RefusedLocal()
	}
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.Canceled):
		return "the phone ended the request before it was answered"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		return "the host did not answer in time, so the capture proxy gave up on it"
	case errors.As(err, &dnsErr):
		return "the host's name did not resolve from this machine"
	case errors.As(err, &certErr):
		return "the host's own certificate did not verify, so the capture proxy did not send the request on"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "the host refused the connection"
	default:
		return "the request to the host failed before it was answered"
	}
}

func millis(from, to time.Time) float64 {
	if to.Before(from) {
		return 0
	}
	return float64(to.Sub(from).Microseconds()) / 1000
}

// prettyURL is the address as mitmproxy writes it: the scheme, the host — without its port when it is the scheme's own —
// and the path and query as the phone sent them.
func prettyURL(scheme, hostPort string, u *url.URL) string {
	host := hostPort
	if h, port, err := net.SplitHostPort(hostPort); err == nil {
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			host = h
			if strings.Contains(h, ":") {
				host = "[" + h + "]"
			}
		}
	}
	return scheme + "://" + host + u.RequestURI()
}

// headersOf flattens headers into lines: names sorted, each name's values in the order they came. (Go keeps headers in a
// map, so the wire order is gone; the runner reads headers into a map too, and order is not part of what it reads.)
func headersOf(h http.Header) []Header {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Header
	for _, name := range names {
		for _, value := range h[name] {
			out = append(out, Header{Name: name, Value: value})
		}
	}
	return out
}

// ── small pieces ───────────────────────────────────────────────────────────────────────────────────────────────

// kept is a body's first keptBytes and its whole size — the record's copy, never the phone's.
type kept struct {
	mu    sync.Mutex
	buf   []byte
	total int64
}

func (k *kept) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.total += int64(len(p))
	if room := keptBytes - len(k.buf); room > 0 {
		k.buf = append(k.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (k *kept) contents() ([]byte, int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.buf, k.total
}

// teeBody copies what is read through it into the record's sink.
type teeBody struct {
	io.ReadCloser
	sink  *kept
	atEOF func()
	once  sync.Once
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		_, _ = t.sink.Write(p[:n])
	}
	if err == io.EOF && t.atEOF != nil {
		t.once.Do(t.atEOF)
	}
	return n, err
}

// prefixConn is a connection whose first bytes were already read off it.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(b []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(b, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}

// peekTLS reads the connection's first byte — exactly one: a buffered reader would take more than it hands back — and
// says whether it opens a TLS handshake (a handshake record, 0x16). The returned connection still holds that byte.
func peekTLS(c net.Conn) (net.Conn, bool) {
	_ = c.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var first [1]byte
	n, err := io.ReadFull(c, first[:])
	_ = c.SetReadDeadline(time.Time{})
	if n == 0 || err != nil {
		return c, false
	}
	return &prefixConn{Conn: c, prefix: first[:1]}, first[0] == 0x16
}

// oneConnListener hands one connection to an http.Server and then waits until it is closed.
type oneConnListener struct {
	conn      net.Conn
	handed    sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func newOneConnListener(conn net.Conn) *oneConnListener {
	return &oneConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.handed.Do(func() { c = l.conn })
	if c != nil {
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
