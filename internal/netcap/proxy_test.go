package netcap

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// proxyRig is one capture's proxy on loopback, its CA, its record, and an upstream it may reach.
type proxyRig struct {
	t        *testing.T
	ca       *CA
	dir      string
	rec      *Recording
	proxy    *Proxy
	address  string
	upstream *httptest.Server
	roots    *x509.CertPool // the upstream's own root — what a client that bypasses the capture trusts
}

func newProxyRig(t *testing.T, upstream *httptest.Server, opts func(*Options)) *proxyRig {
	t.Helper()
	ca, _ := newTestCA(t)
	dir := filepath.Join(t.TempDir(), "files")
	rec, err := NewRecording(dir, lease1, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if upstream != nil && upstream.Certificate() != nil {
		roots.AddCert(upstream.Certificate())
	}
	// THE UPSTREAMS HERE ARE THIS MACHINE'S OWN TEST SERVERS, on loopback, so the rig opens what
	// --capture-allow-local-network opens; destination_test.go holds the rule with it shut.
	o := Options{CA: ca, Recording: rec, UpstreamRoots: roots, ReachLocal: true}
	if opts != nil {
		opts(&o)
	}
	p := NewProxy(o)
	address, err := p.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	go p.Serve()
	r := &proxyRig{t: t, ca: ca, dir: dir, rec: rec, proxy: p, address: address, upstream: upstream, roots: roots}
	t.Cleanup(func() {
		_ = p.Close()
		rec.Close()
	})
	return r
}

// phone is an HTTP client that goes through the capture proxy and trusts what it is given — the capture CA for a phone
// set up for capture, something else for a client that pins.
func (r *proxyRig) phone(trust *x509.CertPool) *http.Client {
	proxyURL, _ := url.Parse("http://" + r.address)
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:              http.ProxyURL(proxyURL),
			TLSClientConfig:    &tls.Config{RootCAs: trust},
			DisableCompression: true, // read the bytes exactly as they arrive
			DisableKeepAlives:  true,
		},
	}
}

func (r *proxyRig) trustsCapture() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(r.ca.Certificate())
	return pool
}

func (r *proxyRig) entries() []map[string]any {
	r.t.Helper()
	data, err := r.rec.Snapshot()
	if err != nil {
		r.t.Fatal(err)
	}
	entries, _ := readHAR(r.t, data)
	return entries
}

// waitEntries waits for the record to hold n entries: an exchange is recorded when its response has been handed to the
// phone, a moment after the phone may already have read it.
func (r *proxyRig) waitEntries(n int) []map[string]any {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries := r.entries()
		if len(entries) >= n || time.Now().After(deadline) {
			return entries
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func eventually(t *testing.T, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

func get(t *testing.T, client *http.Client, target string) (*http.Response, []byte) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, body
}

func gzipBytes(data []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write(data)
	_ = w.Close()
	return b.Bytes()
}

// THE PHONE GETS THE ORIGINAL BYTES; THE RECORD GETS THE DECODED, MASKED TEXT.
func TestTheProxyForwardsUntouchedAndRecordsDecodedAndMasked(t *testing.T) {
	plain := []byte(`{"user":{"name":"keepPHONENAME"},"accessToken":"PXHIDDEN"}`)
	compressed := gzipBytes(plain)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" {
			http.Error(w, "the phone's own Accept-Encoding must arrive as it sent it", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Set-Cookie", "sid=COOKIEHIDDEN; Path=/")
		_, _ = w.Write(compressed)
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)

	request, _ := http.NewRequest(http.MethodGet, upstream.URL+"/v1/me?token=QPHIDDEN&page=2", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Authorization", "Bearer AUTHPHIDDEN")
	response, err := rig.phone(rig.trustsCapture()).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Equal(got, compressed) {
		t.Fatalf("the phone must get the upstream's bytes untouched (%d, %d bytes vs %d)", response.StatusCode, len(got), len(compressed))
	}

	entries := rig.waitEntries(1)
	if len(entries) != 1 {
		t.Fatalf("one exchange, one entry: %d", len(entries))
	}
	encoded, _ := json.Marshal(entries[0])
	for _, gone := range []string{"PXHIDDEN", "COOKIEHIDDEN", "QPHIDDEN", "AUTHPHIDDEN"} {
		if strings.Contains(string(encoded), gone) {
			t.Fatalf("%s reached the record: %s", gone, encoded)
		}
	}
	content := entries[0]["response"].(map[string]any)["content"].(map[string]any)
	if !strings.Contains(content["text"].(string), "keepPHONENAME") || content["size"].(float64) != float64(len(compressed)) {
		t.Fatalf("the record holds the decoded text and the size on the wire: %v", content)
	}
	if v := entries[0]["response"].(map[string]any)["httpVersion"]; v != "HTTP/2.0" {
		t.Fatalf("a host that speaks HTTP/2 is spoken to in it, and the record says so: %v", v)
	}
	if u := entries[0]["request"].(map[string]any)["url"].(string); !strings.Contains(u, "page=2") || !strings.HasPrefix(u, "https://127.0.0.1:") {
		t.Fatalf("the address as mitmproxy writes it, masked: %s", u)
	}
}

func TestPlainHTTPIsForwardedAndRecorded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "plain hello")
	}))
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)
	response, body := get(t, rig.phone(nil), upstream.URL+"/hello")
	if response.StatusCode != 200 || string(body) != "plain hello" {
		t.Fatalf("plain HTTP is forwarded: %d %q", response.StatusCode, body)
	}
	entries := rig.waitEntries(1)
	if len(entries) != 1 || !strings.HasPrefix(entries[0]["request"].(map[string]any)["url"].(string), "http://127.0.0.1:") {
		t.Fatalf("and recorded: %v", entries)
	}
}

// A LARGE BODY REACHES THE PHONE WHOLE; the record keeps its preview and its whole size.
func TestALargeBodyIsStreamedWholeAndRecordedAsAPreview(t *testing.T) {
	large := bytes.Repeat([]byte("0123456789abcdef"), 200*1024) // 3.2 MB
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(large)
	}))
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)
	_, body := get(t, rig.phone(rig.trustsCapture()), upstream.URL+"/large")
	if !bytes.Equal(body, large) {
		t.Fatalf("the phone gets every byte: %d of %d", len(body), len(large))
	}
	content := rig.waitEntries(1)[0]["response"].(map[string]any)["content"].(map[string]any)
	if content["size"].(float64) != float64(len(large)) || len(content["text"].(string)) != runnerBodyCap+1 {
		t.Fatalf("the record keeps a preview just past the runner's bound and the whole size: size %v, text %d", content["size"], len(content["text"].(string)))
	}
}

// AN UPSTREAM THAT NEVER ANSWERS: the phone is told 502 within the bound, and the record says why in the agent's words.
func TestAnUpstreamThatNeverAnswersIsGivenUpOn(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)
	rig := newProxyRig(t, upstream, func(o *Options) { o.UpstreamHeaderTimeout = 300 * time.Millisecond })
	started := time.Now()
	response, body := get(t, rig.phone(rig.trustsCapture()), upstream.URL+"/slow?token=SLOWHIDDEN")
	if response.StatusCode != http.StatusBadGateway || time.Since(started) > 5*time.Second {
		t.Fatalf("a 502 within the bound: %d after %s", response.StatusCode, time.Since(started))
	}
	if strings.Contains(string(body), "SLOWHIDDEN") || strings.Contains(string(body), "127.0.0.1") {
		t.Fatalf("the agent's sentence, never the library's error text with the address in it: %q", body)
	}
	entry := rig.waitEntries(1)[0]
	if e := entry["response"].(map[string]any)["_error"]; e != "the host did not answer in time, so the capture proxy gave up on it" {
		t.Fatalf("the record says why: %v", e)
	}
}

// A HOST WHOSE CLIENT REFUSES THE CAPTURE CERTIFICATE is recorded once and passed through untouched for the rest of the
// capture — no decryption, and the phone's own service works from the next connection on.
func TestARefusingHostIsRecordedOnceAndThenPassedThrough(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "the real host")
	}))
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)
	pinning := rig.phone(rig.roots) // trusts only the host's own certificate, as a pinning SDK does

	if _, err := pinning.Get(upstream.URL + "/first"); err == nil {
		t.Fatal("the first connection meets the capture certificate, and a client that does not trust it refuses it")
	}
	hostPort := strings.TrimPrefix(upstream.URL, "https://")
	if !eventually(t, func() bool { return rig.proxy.PassesThrough(hostPort) }) {
		t.Fatal("the refusing host is passed through for the rest of the capture")
	}
	for i := 0; i < 2; i++ {
		response, body := get(t, pinning, upstream.URL+"/again")
		if response.StatusCode != 200 || string(body) != "the real host" {
			t.Fatalf("passed through, the phone's own service works: %d %q", response.StatusCode, body)
		}
	}
	entries := rig.waitEntries(1)
	if len(entries) != 1 {
		t.Fatalf("recorded ONCE, and nothing passed through is recorded: %d entries", len(entries))
	}
	if entries[0]["response"].(map[string]any)["_error"] != NotReadableRefused {
		t.Fatalf("the one entry says why: %v", entries[0])
	}
}

// A WEBSOCKET UPGRADE is forwarded and recorded; its messages cross untouched and unrecorded.
func TestAWebSocketUpgradeIsSplicedAndRecorded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = buffered.Flush()
		line, _ := buffered.ReadString('\n')
		_, _ = conn.Write([]byte("echo:" + line))
	}))
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)
	request, _ := http.NewRequest(http.MethodGet, upstream.URL+"/socket", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	client := rig.phone(nil)
	client.Timeout = 0 // a timeout wraps the body, and an upgraded body must stay the connection it became
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("the upgrade is forwarded: %d", response.StatusCode)
	}
	stream := response.Body.(io.ReadWriteCloser)
	defer stream.Close()
	_, _ = stream.Write([]byte("MESSAGEHIDDEN\n"))
	reply, _ := bufio.NewReader(stream).ReadString('\n')
	if reply != "echo:MESSAGEHIDDEN\n" {
		t.Fatalf("bytes cross both ways: %q", reply)
	}
	entries := rig.waitEntries(1)
	encoded, _ := json.Marshal(entries)
	if len(entries) != 1 || entries[0]["_resourceType"] != "websocket" || strings.Contains(string(encoded), "MESSAGEHIDDEN") {
		t.Fatalf("the upgrade is recorded and its messages are not: %s", encoded)
	}
}

func TestTheProxyListensOnLoopbackOnlyAndCloseEndsItsConnections(t *testing.T) {
	gate := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		<-gate
	}))
	defer upstream.Close()
	defer close(gate)
	rig := newProxyRig(t, upstream, nil)
	if host, _, _ := net.SplitHostPort(rig.address); host != "127.0.0.1" {
		t.Fatalf("the proxy binds loopback and nothing else: %s", rig.address)
	}
	response, err := rig.phone(rig.trustsCapture()).Get(upstream.URL + "/hang")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(response.Body); done <- err }()
	time.Sleep(100 * time.Millisecond)
	_ = rig.proxy.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close must end the connections the proxy holds, a phone's in the middle of a response included")
	}
	if _, err := net.DialTimeout("tcp", rig.address, time.Second); err == nil {
		t.Fatal("a closed proxy listens on nothing")
	}
}

// NOTHING RAW IS EVER WRITTEN: after a capture full of secrets in every place, every file the capture path wrote is read,
// and none holds any of them.
func TestNothingRawIsEverWritten(t *testing.T) {
	secrets := []string{"RAWQUERYHIDDEN", "RAWAUTHHIDDEN", "RAWCOOKIEHIDDEN", "RAWBODYHIDDEN", "RAWGZIPHIDDEN", "RAWSETCOOKIEHIDDEN", "RAWFORMHIDDEN"}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Set-Cookie", "s=RAWSETCOOKIEHIDDEN")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzipBytes([]byte(`{"refresh_token":"RAWGZIPHIDDEN"}`)))
	}))
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)
	client := rig.phone(rig.trustsCapture())
	request, _ := http.NewRequest(http.MethodPost, upstream.URL+"/x?access_token=RAWQUERYHIDDEN",
		strings.NewReader(`{"password":"RAWBODYHIDDEN"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Basic UkFXQVVUSEhJRERFTjpwdw==RAWAUTHHIDDEN")
	request.Header.Set("Cookie", "c=RAWCOOKIEHIDDEN")
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
	}
	form, _ := http.NewRequest(http.MethodPost, upstream.URL+"/form", strings.NewReader("user=a&password=RAWFORMHIDDEN"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if response, err := client.Do(form); err == nil {
		response.Body.Close()
	}
	if n := len(rig.waitEntries(2)); n != 2 {
		t.Fatalf("both exchanges are in the record, masked: %d", n)
	}
	_ = rig.proxy.Close()
	rig.rec.Close()

	files := 0
	_ = filepath.WalkDir(filepath.Dir(rig.dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s holds %s raw", path, secret)
			}
		}
		return nil
	})
	if files != 1 {
		t.Fatalf("the capture path writes one file, the masked capture; found %d", files)
	}
}

// THE TRUST CHECK: the phone's browser trusts the certificate, refuses it, or never arrives — and nothing else it sends
// meanwhile is decrypted or recorded.
func TestTheTrustCheckTellsTrustedFromRefusedFromNoAnswer(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "elsewhere")
	}))
	defer upstream.Close()

	run := func(trustCapture bool) TrustOutcome {
		check := NewTrustCheck()
		rig := newProxyRig(t, upstream, func(o *Options) { o.Recording = nil; o.TrustCheck = check })
		trust := rig.roots
		if trustCapture {
			trust = rig.trustsCapture()
		}
		response, err := rig.phone(trust).Get("https://" + TrustCheckHost + "/")
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if !strings.Contains(string(body), "trusts the capture certificate") {
				t.Fatalf("the check's own page answers the browser: %q", body)
			}
		}
		// Everything else passes through untouched, and nothing is decrypted: a client trusting the host's own
		// certificate reaches it through the check's proxy.
		other, body := get(t, rig.phone(rig.roots), upstream.URL+"/")
		if other.StatusCode != 200 || string(body) != "elsewhere" {
			t.Fatalf("the phone's other traffic passes through during the check: %d %q", other.StatusCode, body)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return check.Wait(ctx)
	}
	if got := run(true); got != TrustTrusted {
		t.Fatalf("a browser that trusts the CA: %v", got)
	}
	if got := run(false); got != TrustRefused {
		t.Fatalf("a browser that does not: %v", got)
	}
	check := NewTrustCheck()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := check.Wait(ctx); got != TrustNoAnswer {
		t.Fatalf("nothing arrived: %v", got)
	}
}

var _ = errors.New
