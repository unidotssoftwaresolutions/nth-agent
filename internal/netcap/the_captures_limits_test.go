package netcap

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// THE CAPTURE'S LIMITS (ANOTHER_ACCOUNT_AND_LIMITS): what a host the phone reaches, or an app on the phone, can make the
// capture keep or spend.

// A body is masked whole and only then cut to its preview. A credential whose value the preview's cut fell inside was
// read as an unfinished string — no closing quote — and kept in part.
func TestACredentialTheCutFallsInsideIsMaskedWhole(t *testing.T) {
	value := strings.Repeat("Z", 40)
	for _, filler := range []int{262103, 262110, 262123} {
		body := `{"pad":"` + strings.Repeat("a", filler) + `","password":"` + value + `"}`
		entry := entryFor(Flow{Method: "GET", URL: "https://api.example/x", Status: 200,
			RespHeaders: []Header{{Name: "Content-Type", Value: "application/json"}},
			RespBody:    []byte(body), RespBodySize: int64(len(body))})
		if text := entry.Response.Content.Text; strings.Contains(text, "ZZ") {
			t.Fatalf("filler %d: part of the password was kept: …%s", filler, text[max(0, len(text)-60):])
		}
	}
}

// A header value longer than any header this agent records is left out whole, never cut first and masked after.
func TestAHeaderValuePastItsBoundIsLeftOutWhole(t *testing.T) {
	long := "Bearer " + strings.Repeat("k", 70<<10)
	entry := entryFor(Flow{Method: "GET", URL: "https://api.example/x", Status: 200,
		ReqHeaders:  []Header{{Name: "X-Session", Value: "sid=" + strings.Repeat("s", 70<<10)}},
		RespHeaders: []Header{{Name: "X-Echo", Value: long}}})
	for _, h := range append(entry.Request.Headers, entry.Response.Headers...) {
		if strings.Contains(h.Value, "kkkk") || strings.Contains(h.Value, "ssss") {
			t.Fatalf("%s kept part of a value past the bound (%d chars)", h.Name, len(h.Value))
		}
		if !strings.Contains(h.Value, "left out") {
			t.Fatalf("%s says why its value is not there: %q", h.Name, h.Value[:min(len(h.Value), 80)])
		}
	}
}

// An upstream that answers with more headers than a browser accepts is not read whole: the phone is told, in words.
func TestAnAnswerWithMoreHeadersThanABrowserAcceptsIsNotRead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 80; i++ {
			w.Header().Set(fmt.Sprintf("X-Pad-%d", i), strings.Repeat("p", 8<<10))
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	rig := newProxyRig(t, upstream, nil)
	response, body := get(t, rig.phone(nil), upstream.URL+"/big")
	if response.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "headers") {
		t.Fatalf("an answer with 640 KiB of headers is refused in words: %d %q", response.StatusCode, body)
	}
}

// An app on the phone can open as many tunnels as it likes, each to a new name, and refuse each one: the capture
// remembers a bounded number of such hosts, and none longer than a host's name can be.
func TestTheHostsACaptureRemembersAreBounded(t *testing.T) {
	rig := newProxyRig(t, nil, func(o *Options) { o.Recording.bound = 8 })
	refuse := func(authority string) {
		conn, err := net.DialTimeout("tcp4", rig.address, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
		status, _ := bufio.NewReader(conn).ReadString('\n')
		if !strings.Contains(status, "200") {
			return
		}
		_, _ = conn.Write([]byte{0x16})
	}
	for i := 0; i < 20; i++ {
		refuse(fmt.Sprintf("h%d.example:443", i))
	}
	refuse(strings.Repeat("x", 2000) + ".example:443")
	time.Sleep(300 * time.Millisecond)
	rig.proxy.mu.Lock()
	remembered := len(rig.proxy.passThrough)
	_, long := rig.proxy.passThrough[strings.Repeat("x", 2000)+".example:443"]
	rig.proxy.mu.Unlock()
	if remembered > 8 || long {
		t.Fatalf("the proxy remembers %d hosts (bound 8), the 2000-character one %v", remembered, long)
	}
	rig.rec.mu.Lock()
	recorded := len(rig.rec.refused)
	rig.rec.mu.Unlock()
	if recorded > 8 {
		t.Fatalf("the record remembers %d refusing hosts (bound 8)", recorded)
	}
}

// Masking reads each pair's value once: a text made of leads inside one long value costs about twice as much when it is
// twice as long, not four times.
func TestMaskingCostsInProportionToTheText(t *testing.T) {
	cost := func(n int) time.Duration {
		text := strings.Repeat("?a=", n)
		started := time.Now()
		_ = maskText(text)
		return time.Since(started)
	}
	cost(1000) // warm
	small, large := cost(5000), cost(20000)
	if large > 8*small+20*time.Millisecond {
		t.Fatalf("masking four times the text took %v against %v: it grows faster than the text", large, small)
	}
}
