package netcap

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── DOES THE PHONE'S BROWSER TRUST THE CAPTURE CERTIFICATE? ─────────────────────────────────────────────────────
//
// `capture setup` points the phone's proxy at this machine for a moment and opens one address in the phone's browser:
// TrustCheckHost, a name that cannot exist on the internet (.invalid, RFC 2606). The proxy the check runs decrypts that one
// name and answers it itself; everything else the phone sends meanwhile passes through untouched and unrecorded.
//
// WHAT IT PROVES: the phone's browser — Chrome, Samsung Internet — trusts the capture certificate, so the certificate is
// installed and trusted on the phone. WHAT IT DOES NOT PROVE: that the app under test trusts it. An app built for release,
// or one that pins or ships its own trust store, does not read user certificates, whatever the browser does.

// TrustCheckHost is the address the trust check opens in the phone's browser.
const TrustCheckHost = "trust-check.nthcase.invalid"

// TrustCheckOverAddress is THE CHECK'S LAST PAGE (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 4): once the check has its outcome, whatever it
// is, `capture setup` opens this in the phone's browser, in the tab the check used, before it puts the phone's proxy back. Plain
// HTTP, so no certificate is asked for — a browser that refused the capture certificate has nothing to warn about here — and
// answered by the check's own proxy, never sent anywhere: the address cannot exist on the internet. It replaces the check's page,
// or the browser's "Your connection is not private" warning for it, with one that says the check is over.
const TrustCheckOverAddress = "http://" + TrustCheckHost + trustCheckOverPath

const trustCheckOverPath = "/over"

// overPage is what the check's last page says: that it is over, and that it is the check's own. Nothing about trust — it is
// shown whatever the check found.
const overPage = "<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\">" +
	"<title>NthCase capture check</title><h1>The capture check is over.</h1>" +
	"<p>This page is the check's own. The terminal on your computer says what it found. You can close this tab.</p>"

// TrustOutcome is what the trust check saw.
type TrustOutcome int

const (
	// TrustNoAnswer: nothing reached the check — the browser did not open, the phone was locked, or its proxy setting did
	// not take.
	TrustNoAnswer TrustOutcome = iota
	// TrustTrusted: the phone's browser completed the handshake with the capture certificate.
	TrustTrusted
	// TrustRefused: the phone's browser refused the capture certificate.
	TrustRefused
)

// TrustCheck is one check's sentinel and its outcome. The first outcome reported is the one that stands.
type TrustCheck struct {
	host     string
	once     sync.Once
	outcome  chan TrustOutcome
	overOnce sync.Once
	over     chan struct{}
}

// NewTrustCheck makes a check for TrustCheckHost.
func NewTrustCheck() *TrustCheck {
	return &TrustCheck{host: TrustCheckHost, outcome: make(chan TrustOutcome, 1), over: make(chan struct{})}
}

func (t *TrustCheck) is(host string) bool {
	return strings.EqualFold(strings.TrimSuffix(host, "."), t.host)
}

func (t *TrustCheck) report(outcome TrustOutcome) {
	t.once.Do(func() { t.outcome <- outcome })
}

// answer is the check's own page, served on the decrypted connection: the handshake already succeeded, so the browser
// trusts the certificate; the page tells the person holding the phone so too — or, asked for the check's last page (a browser
// that reaches it over HTTPS), says the check is over.
func (t *TrustCheck) answer(conn *tls.Conn) {
	t.report(TrustTrusted)
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(request.Body, 64<<10))
	page := "<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\">" +
		"<title>NthCase capture check</title><h1>This phone's browser trusts the capture certificate.</h1>" +
		"<p>You can close this tab and go back to the terminal.</p>"
	if request.URL.Path == trustCheckOverPath {
		page = overPage
	}
	response := "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nCache-Control: no-store\r\n" +
		"Connection: close\r\nContent-Length: " + strconv.Itoa(len(page)) + "\r\n\r\n" + page
	if _, err := io.WriteString(conn, response); err == nil && request.URL.Path == trustCheckOverPath {
		t.overOnce.Do(func() { close(t.over) })
	}
}

// answerPlain answers a plain-HTTP request for the check's own address — the check's last page, opened by `capture setup` once
// the check has its outcome. It decides nothing about trust: no certificate was asked for. Any other path on the address gets
// the same page and is not the last page being shown.
func (t *TrustCheck) answerPlain(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 64<<10))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Length", strconv.Itoa(len(overPage)))
	if _, err := io.WriteString(w, overPage); err == nil && r.URL.Path == trustCheckOverPath {
		t.overOnce.Do(func() { close(t.over) })
	}
}

// WaitOver reports whether the phone's browser was served the check's last page before ctx ended.
func (t *TrustCheck) WaitOver(ctx context.Context) bool {
	select {
	case <-t.over:
		return true
	case <-ctx.Done():
		return false
	}
}

// Wait returns the check's outcome, or TrustNoAnswer when ctx ends first.
func (t *TrustCheck) Wait(ctx context.Context) TrustOutcome {
	select {
	case outcome := <-t.outcome:
		return outcome
	case <-ctx.Done():
		return TrustNoAnswer
	}
}
