package farm_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST — the farm's address and the farm's answers.

const trustNodeID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

// ── a plain-http farm address is exact loopback, and never goes through a proxy ───────────────────────────────

// proxyChildEnv names the farm address a child process dials; set only by TestAPlainHTTPFarmIsNeverSentThroughAProxy.
const proxyChildEnv = "NTH_AGENT_TEST_PROXY_CHILD_FARM"

// TestProxyChild is not a test on its own: it is the child process the proxy test starts, because Go reads the proxy
// variables once per process. It registers, then dials the tunnel, against the address it was given.
func TestProxyChild(t *testing.T) {
	farmURL := os.Getenv(proxyChildEnv)
	if farmURL == "" {
		t.Skip("run only as the child of TestAPlainHTTPFarmIsNeverSentThroughAProxy")
	}
	client := &farm.Client{BaseURL: farmURL, Token: "dummy-token", HTTP: &http.Client{Timeout: 2 * time.Second}}
	_, status, err := client.Register(context.Background(), trustNodeID, "host", farm.Report{})
	fmt.Printf("register=%d err=%v\n", status, err)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	tunnel.New(tunnel.Config{FarmURL: farmURL, NodeID: trustNodeID, Token: "dummy-token", Out: io.Discard,
		Sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}}).Run(ctx)
}

type recordingProxy struct {
	mu   sync.Mutex
	seen []string
}

func (p *recordingProxy) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.seen = append(p.seen, r.Method+" "+r.URL.String()+" token="+r.Header.Get("X-Agent-Token"))
	p.mu.Unlock()
	w.WriteHeader(http.StatusBadGateway)
}

func (p *recordingProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func TestAPlainHTTPFarmIsNeverSentThroughAProxy(t *testing.T) {
	proxy := &recordingProxy{}
	proxyServer := httptest.NewServer(http.HandlerFunc(proxy.handler))
	t.Cleanup(proxyServer.Close)
	var farmMu sync.Mutex
	var farmSaw []string
	farmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		farmMu.Lock()
		farmSaw = append(farmSaw, r.URL.Path)
		farmMu.Unlock()
		if r.URL.Path != "/v1/nodes/register" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"` + trustNodeID + `","apiKey":""},"message":"Node registered.","errors":null}`))
	}))
	t.Cleanup(farmServer.Close)
	port := farmServer.URL[strings.LastIndex(farmServer.URL, ":")+1:]

	for _, c := range []struct {
		host    string
		allowed bool
	}{
		{"LOCALHOST", false}, {"Localhost", false}, {"LocalHost", false},
		{"localhost", true}, {"127.0.0.1", true},
	} {
		t.Run(c.host, func(t *testing.T) {
			before := len(proxy.requests())
			farmMu.Lock()
			farmBefore := len(farmSaw)
			farmMu.Unlock()
			child := exec.Command(os.Args[0], "-test.run=^TestProxyChild$", "-test.count=1")
			child.Env = append(os.Environ(), proxyChildEnv+"=http://"+c.host+":"+port,
				"HTTP_PROXY="+proxyServer.URL, "http_proxy="+proxyServer.URL, "HTTPS_PROXY=", "https_proxy=",
				"NO_PROXY=", "no_proxy=", "REQUEST_METHOD=")
			output, err := child.CombinedOutput()
			if err != nil {
				t.Fatalf("the child failed: %v\n%s", err, output)
			}
			if seen := proxy.requests()[before:]; len(seen) != 0 {
				t.Fatalf("http://%s: the farm's channels went through the proxy, token and all: %v", c.host, seen)
			}
			farmMu.Lock()
			reached := len(farmSaw) > farmBefore
			farmMu.Unlock()
			registered := strings.Contains(string(output), fmt.Sprintf("register=%d ", farm.CallOK))
			if c.allowed != (reached && registered) {
				t.Fatalf("http://%s: allowed=%v, but reached the farm=%v and registered=%v\n%s", c.host, c.allowed, reached, registered, output)
			}
			if !c.allowed && !strings.Contains(string(output), fmt.Sprintf("register=%d ", farm.CallUnsafeTransport)) {
				t.Fatalf("http://%s must be refused before anything is sent:\n%s", c.host, output)
			}
		})
	}
}

func TestOnlyExactLoopbackIsAPlainHTTPFarm(t *testing.T) {
	for _, raw := range []string{"http://LOCALHOST:8080", "http://Localhost", "http://localhost.:8080", "http://LOCALHOST."} {
		err := farm.ValidateBaseURL(raw)
		if err == nil || !strings.Contains(err.Error(), "http://localhost") {
			t.Fatalf("%q must be refused, naming the spelling that works: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "https://FARM.example"} {
		if err := farm.ValidateBaseURL(raw); err != nil {
			t.Fatalf("%q must still be allowed: %v", raw, err)
		}
	}
}

// ── a register or heartbeat answer is read up to a bound ───────────────────────────────────────────────────────

// endlessList answers 200 with a heartbeat whose provisionTasks list never ends — written as it goes, so neither end of
// this test holds it — up to max bytes, and counts what it wrote.
func endlessList(max int64, gzipped bool) (http.HandlerFunc, *int64) {
	var wrote int64
	return func(w http.ResponseWriter, r *http.Request) {
		var body io.Writer = w
		w.Header().Set("Content-Type", "application/json")
		if gzipped {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			defer zw.Close()
			body = zw
		}
		write := func(s string) bool {
			if wrote+int64(len(s)) > max {
				return false
			}
			n, err := io.WriteString(body, s)
			wrote += int64(n)
			return err == nil
		}
		if !write(`{"success":true,"data":{"provisionTasks":[{}`) {
			return
		}
		piece := strings.Repeat(`,{}`, 1024)
		for write(piece) {
		}
		write(`]}}`)
	}, &wrote
}

func TestAFarmAnswerIsReadUpToABound(t *testing.T) {
	for _, c := range []struct {
		name    string
		gzipped bool
	}{{"as sent", false}, {"gzip, which the transport opens on its own", true}} {
		t.Run(c.name, func(t *testing.T) {
			handler, _ := endlessList(farm.AnswerBodyLimit+64*1024, c.gzipped)
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			client := &farm.Client{BaseURL: server.URL, Token: "dummy-token", HTTP: &http.Client{Timeout: 10 * time.Second}}
			work, status, err := client.Heartbeat(context.Background(), trustNodeID, farm.Report{})
			if status != farm.CallTransport || err == nil || !strings.Contains(err.Error(), "larger than") {
				t.Fatalf("an answer past %d bytes must be refused in words, got %v %v (%d tasks)", farm.AnswerBodyLimit, status, err, work.ProvisionTasks)
			}
		})
	}
}

// ── today's farm: its real register and heartbeat answers still pass ─────────────────────────────────────────────────

func TestTodaysFarmAnswersStillPass(t *testing.T) {
	leased := make([]string, 200) // NodeReportLimits.MaxDevices, every one leased
	for i := range leased {
		leased[i] = fmt.Sprintf("R5CT30%05d", i)
	}
	tasks := make([]map[string]any, 100)
	for i := range tasks {
		tasks[i] = map[string]any{"provisioningId": "c3d4e5f6-0000-4000-8000-000000000000", "platform": 1, "osVersion": "14",
			"formFactor": "Phone", "deviceProfile": "pixel_7", "cores": 4, "memoryMB": 4096}
	}
	heartbeat, _ := json.Marshal(map[string]any{"success": true, "message": "Heartbeat accepted.", "errors": nil,
		"data": map[string]any{"provisionTasks": tasks, "commands": []any{map[string]any{"commandId": "x", "kind": "reboot"}}, "leasedDeviceIds": leased}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.URL.Path == "/v1/nodes/register" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"` + trustNodeID + `","apiKey":""},"message":"Node registered.","errors":null}`))
			return
		}
		_, _ = w.Write(heartbeat)
	}))
	t.Cleanup(server.Close)
	client := &farm.Client{BaseURL: server.URL, Token: "dummy-token", HTTP: &http.Client{Timeout: 5 * time.Second}}
	if id, status, err := client.Register(context.Background(), trustNodeID, "host", farm.Report{}); status != farm.CallOK || id.NodeID != trustNodeID {
		t.Fatalf("today's register answer must pass: %q %v %v", id, status, err)
	}
	work, status, err := client.Heartbeat(context.Background(), trustNodeID, farm.Report{})
	if status != farm.CallOK || work.ProvisionTasks != 100 || work.Commands != 1 {
		t.Fatalf("today's heartbeat answer must pass: %+v %v %v", work, status, err)
	}
	if len(heartbeat) > farm.AnswerBodyLimit/16 {
		t.Fatalf("the bound must stand far above today's largest answer (%d bytes)", len(heartbeat))
	}
}
