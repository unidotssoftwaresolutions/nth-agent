package netcap

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── A CAPTURE KEEPS TO THE INTERNET (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 2) ─────────────────────────
//
// While capture is on, the phone's traffic leaves through this machine. Without a rule it reached this machine's own
// services and its local network too — places the phone cannot reach from its own Wi-Fi — and the answer was recorded.
// The proxy now refuses them, judged on the address it actually dials; a refused request is in the capture with a reason
// that names the flag, and the phone is told the same.

// counting is a loopback upstream that counts what reaches it.
func counting(t *testing.T, network, address string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "a service on the customer's own machine")
	}))
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Skipf("cannot listen on %s here: %v", address, err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server, &hits
}

func shutRig(t *testing.T) *proxyRig {
	return newProxyRig(t, nil, func(o *Options) { o.ReachLocal = false })
}

func errorOf(entry map[string]any) string {
	response, _ := entry["response"].(map[string]any)
	text, _ := response["_error"].(string)
	return text
}

func TestThePlainHttpRequestToThisMachineIsRefusedAndRecorded(t *testing.T) {
	for _, c := range []struct{ name, network, listen, host string }{
		{"IPv4 loopback", "tcp4", "127.0.0.1:0", "127.0.0.1"},
		{"a name that resolves to loopback", "tcp4", "127.0.0.1:0", "localhost"},
		{"IPv6 loopback", "tcp6", "[::1]:0", "[::1]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			upstream, hits := counting(t, c.network, c.listen)
			rig := shutRig(t)
			_, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())

			response, err := rig.phone(nil).Get("http://" + c.host + ":" + port + "/admin")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("a request for this machine's own service must be refused, got %d %q", response.StatusCode, body)
			}
			if hits.Load() != 0 {
				t.Fatal("the request reached a service on the customer's own machine")
			}
			if !strings.Contains(string(body), RefusedLocal()) {
				t.Fatalf("the phone is told why, in the capture's words: %q", body)
			}
			entries := rig.waitEntries(1)
			if len(entries) != 1 || errorOf(entries[0]) != RefusedLocal() {
				t.Fatalf("the refused request is in the capture, with the reason that names the flag: %v", entries)
			}
			if url, _ := entries[0]["request"].(map[string]any)["url"].(string); !strings.Contains(url, "/admin") {
				t.Fatalf("the entry is the request the phone made: %v", entries[0]["request"])
			}
		})
	}
}

func TestAConnectToThisMachineIsRefusedWhetherDecryptedOrPassedThrough(t *testing.T) {
	// DECRYPTED: the phone trusts the capture certificate, so the request inside the tunnel is read — and refused there.
	tlsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an HTTPS request reached a service on the customer's own machine")
	}))
	t.Cleanup(tlsUpstream.Close)
	rig := newProxyRig(t, tlsUpstream, func(o *Options) { o.ReachLocal = false }) // trusts the upstream's own certificate
	response, err := rig.phone(rig.trustsCapture()).Get(tlsUpstream.URL + "/secret")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("an HTTPS request for this machine's own service must be refused, got %d", response.StatusCode)
	}

	// PASSED THROUGH: a tunnel that is not TLS at all is spliced, unread — and the splice is refused.
	raw, hits := counting(t, "tcp4", "127.0.0.1:0")
	_ = raw
	conn, err := net.DialTimeout("tcp4", rig.address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	target := raw.Listener.Addr().String()
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	reader := bufio.NewReader(conn)
	if line, _ := reader.ReadString('\n'); !strings.Contains(line, "200") {
		t.Fatalf("the tunnel is answered as any other: %q", line)
	}
	_, _ = reader.ReadString('\n') // the blank line
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", target)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if rest, _ := io.ReadAll(reader); len(rest) != 0 {
		t.Fatalf("a spliced tunnel to this machine carried an answer: %q", rest)
	}
	if hits.Load() != 0 {
		t.Fatal("a spliced tunnel reached a service on the customer's own machine")
	}

	entries := rig.waitEntries(2)
	var sawDecrypted, sawSplice bool
	for _, e := range entries {
		request := e["request"].(map[string]any)
		switch {
		case request["method"] == "GET" && strings.Contains(request["url"].(string), "/secret") && errorOf(e) == RefusedLocal():
			sawDecrypted = true
		case request["method"] == "CONNECT" && strings.Contains(request["url"].(string), target) && errorOf(e) == RefusedLocal():
			sawSplice = true
		}
	}
	if !sawDecrypted || !sawSplice {
		t.Fatalf("both refusals are in the capture, each with the reason: %v", entries)
	}
}

// A REDIRECT IS A NEW REQUEST from the phone, and it is judged like the first. The proxy never follows one itself.
func TestARedirectToARefusedAddressIsRefusedOnTheWayThere(t *testing.T) {
	inside, hits := counting(t, "tcp6", "[::1]:0")
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inside.URL+"/metadata", http.StatusFound)
	}))
	t.Cleanup(outside.Close)
	// The rule as the test draws it: 127.0.0.1 stands for the internet, ::1 for this machine.
	rig := newProxyRig(t, nil, func(o *Options) {
		o.ReachLocal = false
		o.refuses = func(ip net.IP) bool { return ip.Equal(net.IPv6loopback) }
	})
	response, err := rig.phone(nil).Get(outside.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || hits.Load() != 0 {
		t.Fatalf("the redirect's target must be refused: status %d, reached %d", response.StatusCode, hits.Load())
	}
	entries := rig.waitEntries(2)
	if len(entries) != 2 || entries[0]["response"].(map[string]any)["status"] != float64(302) || errorOf(entries[1]) != RefusedLocal() {
		t.Fatalf("the capture holds the redirect and the refusal after it: %v", entries)
	}
}

// THE FLAG OPENS IT, and only the flag.
func TestTheFlagLetsACaptureReachThisMachine(t *testing.T) {
	upstream, hits := counting(t, "tcp4", "127.0.0.1:0")
	rig := newProxyRig(t, nil, func(o *Options) { o.ReachLocal = true })
	response, err := rig.phone(nil).Get(upstream.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("with --capture-allow-local-network the local backend is reached: %d, %d", response.StatusCode, hits.Load())
	}
}

func TestEveryLocalRangeIsRefusedAndTheInternetIsNot(t *testing.T) {
	for address, local := range map[string]bool{
		"127.0.0.1": true, "127.255.0.9": true, "::1": true, "::ffff:127.0.0.1": true,
		"0.0.0.0": true, "0.1.2.3": true, "::": true,
		"169.254.169.254": true, "fe80::1": true, "fd00:ec2::254": true,
		"10.0.0.1": true, "172.16.0.1": true, "172.31.255.255": true, "192.168.1.20": true, "fc00::1": true, "fec0::1": true,
		"100.64.0.1": true, "100.127.255.254": true, "100.100.100.200": true,
		"::ffff:10.0.0.1": true, "64:ff9b::a00:1": true, "64:ff9b::7f00:1": true,
		"8.8.8.8": false, "1.1.1.1": false, "172.32.0.1": false, "100.128.0.1": false, "192.169.0.1": false,
		"2001:4860:4860::8888": false, "64:ff9b::808:808": false, "::ffff:8.8.8.8": false,
	} {
		if got := isLocalDestination(net.ParseIP(address)); got != local {
			t.Errorf("%s: refused %v, want %v", address, got, local)
		}
	}
}

func TestTheReasonNamesTheFlag(t *testing.T) {
	if !strings.Contains(RefusedLocal(), "--capture-allow-local-network") || !strings.Contains(RefusedLocal(), "npx nthcase-agent@latest connect") {
		t.Fatalf("a refused request says how to open it: %q", RefusedLocal())
	}
}
