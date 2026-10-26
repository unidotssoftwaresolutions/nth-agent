package tunnel_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// THE FARM IS wss://, and since this slice TLS runs ON TOP OF the send queue (sendqueue.go): the handshake, the
// upgrade and every record go through it. Every other seal in this package speaks plain ws:// to a loopback farm, so
// this is the one that would see a queue that broke TLS. The certificate is httptest's own, made in memory — no key
// material enters the tree (the farm's NoCredentialEntersTheTreeTests walks this repository for that).
func TestTheTunnelWorksOverTLSThroughTheSendQueue(t *testing.T) {
	body := strings.Repeat("t", 100*1024)
	appium := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer appium.Close()

	conns := make(chan *websocket.Conn, 2)
	farm := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tunnel") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(-1)
		conns <- conn
		<-r.Context().Done()
	}))
	defer farm.Close()
	trusted := x509.NewCertPool()
	trusted.AddCert(farm.Certificate())

	var out syncWriter
	drive := liveTunnel(t, farm.URL, &out, func(c *tunnel.Config) {
		c.AppiumURL = appium.URL
		c.TLSClientConfig = &tls.Config{RootCAs: trusted}
	})
	var conn *websocket.Conn
	select {
	case conn = <-conns:
	case <-time.After(5 * time.Second):
		t.Fatalf("no tunnel over TLS:\n%s", out.String())
	}
	defer conn.CloseNow()
	reader := readFarmSide(conn)
	waitFor(t, "connected over TLS", drive.Connected)
	if !strings.HasPrefix(farm.URL, "https://") {
		t.Fatalf("the farm under test must be TLS, is %s", farm.URL)
	}

	// A session-less frame reaches Appium only as GET /status (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST); it was GET /source.
	send(t, conn, map[string]any{"id": "77777777-0000-0000-0000-000000000001", "method": "GET", "path": "/status?source", "headers": map[string]string{}})
	answer := reader.await(t, 5*time.Second, "the answer over TLS", func(f map[string]any) bool { return f["status"] != nil })
	decoded, _ := base64.StdEncoding.DecodeString(answer["bodyBase64"].(string))
	if string(decoded) != body {
		t.Fatal("a fragmented answer must arrive whole through TLS on the send queue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("the farm's ping over TLS went unanswered: %v", err)
	}
}
