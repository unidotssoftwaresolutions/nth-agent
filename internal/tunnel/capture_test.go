package tunnel_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── NETWORK CAPTURE OVER THE TUNNEL: the farm's frames, its handback question, and the pull ──────────────────────

const (
	captureLease = "0f3c2a91-5b7e-4d21-9a3c-6e8f10b2c4d5"
	otherLease   = "7d1e9b44-2c6a-4f80-b1d3-95a7e3c0f218"
)

// fakeCapture is the machine's capture as the tunnel sees it: it records what it is asked and answers what it is told.
type fakeCapture struct {
	mu          sync.Mutex
	calls       []string
	clean       bool
	observation string
	captures    map[string][]byte
	refusal     string
}

func (f *fakeCapture) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeCapture) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeCapture) Start(_ context.Context, lease, serial string) {
	f.record("start " + lease + " " + serial)
}
func (f *fakeCapture) Stop(_ context.Context, lease string) { f.record("stop " + lease) }
func (f *fakeCapture) HandBackForFarm(_ context.Context, udid, lease string) (bool, string) {
	f.record("handback " + udid + " " + lease)
	return f.clean, f.observation
}
func (f *fakeCapture) Pull(lease string) ([]byte, string) {
	f.record("pull " + lease)
	f.mu.Lock()
	defer f.mu.Unlock()
	if data, ok := f.captures[lease]; ok {
		return data, ""
	}
	return nil, f.refusal
}
func (f *fakeCapture) Pulled(lease string) { f.record("pulled " + lease) }

// appiumThatCounts is the local Appium, on a port this test opened: the capture paths must never reach it.
func appiumThatCounts(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func captureTunnel(t *testing.T, capture *fakeCapture) (*farmReader, func(any), *atomic.Int32, *syncWriter) {
	t.Helper()
	appium, hits := appiumThatCounts(t)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appium.URL
		c.Capture = capture
	})
	conn := farm.next(t)
	return readFarmSide(conn), func(frame any) { send(t, conn, frame) }, hits, out
}

func TestCaptureStartAndStopReachTheMachinesCapture(t *testing.T) {
	capture := &fakeCapture{}
	_, sendFrame, hits, _ := captureTunnel(t, capture)
	sendFrame(map[string]any{"kind": "capture-start", "leaseId": captureLease, "serial": "R5CT30ABCDE"})
	sendFrame(map[string]any{"kind": "capture-stop", "leaseId": captureLease})
	waitFor(t, "the start and the stop handed over", func() bool { return len(capture.said()) == 2 })
	got := strings.Join(capture.said(), " | ")
	if !strings.Contains(got, "start "+captureLease+" R5CT30ABCDE") || !strings.Contains(got, "stop "+captureLease) {
		t.Fatalf("got %s", got)
	}
	if hits.Load() != 0 {
		t.Fatal("a capture frame reached Appium")
	}
}

func handbackQuestion(id string, headers map[string]string, body string) map[string]any {
	frame := map[string]any{"id": id, "method": "POST", "path": "/gurufarm-node/android/capture-handback", "headers": headers}
	if body != "" {
		frame["bodyBase64"] = base64.StdEncoding.EncodeToString([]byte(body))
	}
	return frame
}

func responseTo(id string) func(map[string]any) bool {
	return func(f map[string]any) bool { return f["id"] == id && f["kind"] == nil }
}

func bodyOf(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	raw, _ := base64.StdEncoding.DecodeString(fmt.Sprint(frame["bodyBase64"]))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("the answer is not JSON: %s", raw)
	}
	return body
}

func TestTheFarmsHandbackQuestionIsAnsweredHereAndNeverForwarded(t *testing.T) {
	capture := &fakeCapture{clean: false, observation: "the phone was not reachable over adb (adb exit 1)"}
	reader, sendFrame, hits, _ := captureTunnel(t, capture)
	farmAsks := map[string]string{"X-GuruFarm-Asks": "phone-state", "Content-Type": "application/json"}
	question := fmt.Sprintf(`{"udid":"R5CT30ABCDE","leaseId":%q}`, captureLease)

	// THE FARM'S QUESTION: answered from the phone, in the node's shape.
	sendFrame(handbackQuestion("11111111-0000-0000-0000-000000000001", farmAsks, question))
	answer := reader.await(t, 5*time.Second, "the handback answer", responseTo("11111111-0000-0000-0000-000000000001"))
	body := bodyOf(t, answer)
	if answer["status"] != float64(200) || body["clean"] != false || body["observation"] != capture.observation {
		t.Fatalf("got %v %v", answer, body)
	}

	// WITHOUT THE FARM'S MARK: 404, and nothing about any phone.
	sendFrame(handbackQuestion("11111111-0000-0000-0000-000000000002", map[string]string{}, question))
	answer = reader.await(t, 5*time.Second, "the refusal", responseTo("11111111-0000-0000-0000-000000000002"))
	if answer["status"] != float64(404) || strings.Contains(fmt.Sprint(bodyOf(t, answer)), "R5CT30ABCDE") {
		t.Fatalf("got %v", answer)
	}

	// A QUESTION WITH NO PHONE OR NO LEASE IN IT: 400.
	for i, malformed := range []string{"", `{"leaseId":"` + captureLease + `"}`, `{"udid":"R5CT30ABCDE"}`, `{"udid":"R5CT30ABCDE","leaseId":"x"}`, `not json`} {
		id := fmt.Sprintf("11111111-0000-0000-0000-00000000010%d", i)
		sendFrame(handbackQuestion(id, farmAsks, malformed))
		if answer := reader.await(t, 5*time.Second, "the 400", responseTo(id)); answer["status"] != float64(400) {
			t.Fatalf("%q: got %v", malformed, answer)
		}
	}

	if calls := capture.said(); len(calls) != 1 || calls[0] != "handback R5CT30ABCDE "+captureLease {
		t.Fatalf("only the farm's well-formed question reaches the phone: %q", calls)
	}
	if hits.Load() != 0 {
		t.Fatal("the handback path was forwarded to Appium")
	}
}

func pullStart(id, path, lease string) map[string]any {
	frame := streamStart(id, path)
	if lease != "" {
		frame["leaseId"] = lease
	}
	return frame
}

func TestAPullIsServedForItsOwnLeaseAndTheFileGoesOnceItHasLeft(t *testing.T) {
	har := []byte(`{"log":{"version":"1.2","entries":[` + strings.Repeat(`{"comment":"x"},`, 9000) + `{"comment":"last"}]}}`)
	capture := &fakeCapture{captures: map[string][]byte{captureLease: har}, refusal: "no capture: it was pulled once and deleted from the machine it was captured on"}
	reader, sendFrame, hits, _ := captureTunnel(t, capture)

	const id = "22222222-0000-0000-0000-000000000001"
	sendFrame(pullStart(id, "gfhar:"+captureLease, captureLease))
	var got []byte
	var seq float64
	for {
		frame := reader.await(t, 10*time.Second, "the pull's frames", func(f map[string]any) bool { return f["id"] == id })
		if frame["kind"] == "stream-end" {
			if frame["status"] != float64(200) || frame["error"] != nil {
				t.Fatalf("the pull must end 200: %v", frame)
			}
			break
		}
		if frame["seq"] != seq {
			t.Fatalf("chunk %v arrived as %v", seq, frame["seq"])
		}
		seq++
		chunk, _ := base64.StdEncoding.DecodeString(fmt.Sprint(frame["bodyBase64"]))
		got = append(got, chunk...)
	}
	if string(got) != string(har) || seq < 2 {
		t.Fatalf("the capture must arrive whole, in order (%d chunks, %d of %d bytes)", int(seq), len(got), len(har))
	}
	waitFor(t, "the file deleted after the last chunk left", func() bool {
		calls := capture.said()
		return len(calls) == 2 && calls[1] == "pulled "+captureLease
	})
	if hits.Load() != 0 {
		t.Fatal("a pull reached Appium")
	}
}

func TestAPullIsRefusedInWordsForAnotherLeaseNoLeaseOrABadToken(t *testing.T) {
	capture := &fakeCapture{captures: map[string][]byte{captureLease: []byte(`{"log":{}}`)}}
	reader, sendFrame, _, out := captureTunnel(t, capture)
	for i, c := range []struct {
		path, lease, says string
	}{
		{"gfhar:" + captureLease, otherLease, "this pull named another lease's"},
		{"gfhar:" + captureLease, "", "this pull did not say which lease it was made on"},
		{"gfhar:not-a-lease", "not-a-lease", "does not name one the way the farm hands it out"},
		{"gfhar:" + captureLease + "/../x", captureLease, "does not name one the way the farm hands it out"},
	} {
		id := fmt.Sprintf("33333333-0000-0000-0000-00000000000%d", i)
		sendFrame(pullStart(id, c.path, c.lease))
		end := reader.await(t, 5*time.Second, "the refusal", ofStream(id, "stream-end"))
		if end["status"] != float64(404) || !strings.Contains(fmt.Sprint(end["error"]), c.says) {
			t.Fatalf("%s for %q: got %v", c.path, c.lease, end)
		}
	}
	if calls := capture.said(); len(calls) != 0 {
		t.Fatalf("a refused pull read nothing: %q", calls)
	}
	if strings.Count(out.String(), "a lease opens only its own capture") != 1 {
		t.Fatalf("the console says it once:\n%s", out.String())
	}
}

func TestAPullWithNothingToSendSaysWhyInTheMachinesWords(t *testing.T) {
	const why = "no capture: phone R5CT30ABCDE has not been set up for capture on the machine it is plugged into (on that machine: nth-agent capture setup R5CT30ABCDE)"
	capture := &fakeCapture{refusal: why}
	reader, sendFrame, _, _ := captureTunnel(t, capture)
	const id = "44444444-0000-0000-0000-000000000001"
	sendFrame(pullStart(id, "gfhar:"+captureLease, captureLease))
	end := reader.await(t, 5*time.Second, "the answer", ofStream(id, "stream-end"))
	if end["status"] != float64(404) || end["error"] != why {
		t.Fatalf("the run must be told the machine's own sentence: %v", end)
	}
	if calls := capture.said(); len(calls) != 1 || calls[0] != "pull "+captureLease {
		t.Fatalf("nothing was sent, so nothing is deleted: %q", calls)
	}
}

// A PULL THE TUNNEL DROPS IN THE MIDDLE KEEPS ITS FILE: the farm stops reading, the connection goes, and the capture is
// still there for its hour — deleted only once every byte has left this machine.
func TestAPullTheTunnelDropsKeepsItsFile(t *testing.T) {
	har := bytes.Repeat([]byte("x"), 16<<20) // more than the kernel buffers between the two ends can hold
	capture := &fakeCapture{captures: map[string][]byte{captureLease: har}}
	appium, _ := appiumThatCounts(t)
	farm := newLiveFarm(t)
	liveTunnel(t, farm.server.URL, &syncWriter{}, func(c *tunnel.Config) {
		c.AppiumURL = appium.URL
		c.Capture = capture
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)
	reader.paused.Store(true) // the farm stops reading
	send(t, conn, pullStart("55555555-0000-0000-0000-000000000001", "gfhar:"+captureLease, captureLease))
	waitFor(t, "the pull begun", func() bool { return len(capture.said()) >= 1 })
	time.Sleep(300 * time.Millisecond) // the pump fills what the connection will take
	conn.CloseNow()
	time.Sleep(time.Second) // time enough to (wrongly) count it sent
	for _, call := range capture.said() {
		if strings.HasPrefix(call, "pulled") {
			t.Fatalf("a pull the tunnel dropped deleted its file: %q", capture.said())
		}
	}
}
