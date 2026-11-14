package tunnel_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ── WHAT THIS AGENT HOLDS AT ONCE, ACROSS EVERY TUNNEL IT HAS HAD (ANOTHER_ACCOUNT_AND_LIMITS) ───────────────────────
//
// The request queue bounded what waits on one tunnel. These seals hold the rest of what the farm end of a tunnel can
// make the agent hold or start: handback questions, refusals, the send queue, answers on their way out, a hub answer
// the key scrub made longer, a create the farm has already given up on, and forwards that outlive their tunnel. Each
// limit is scaled down through its Config seam so a seal can meet it; the numbers themselves are in limits.go.

// heldCapture is a machine's capture whose handback answers wait until the test lets them go, counting how many run at
// once.
type heldCapture struct {
	running, peak atomic.Int32
	let           chan struct{}
}

func (h *heldCapture) Start(context.Context, string, string) {}
func (h *heldCapture) Stop(context.Context, string)          {}
func (h *heldCapture) HandBackForFarm(ctx context.Context, udid, lease string) (bool, string) {
	n := h.running.Add(1)
	defer h.running.Add(-1)
	for {
		p := h.peak.Load()
		if n <= p || h.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-h.let:
	case <-ctx.Done():
	}
	return true, "handed back"
}
func (h *heldCapture) Pull(string) ([]byte, string) { return nil, "no capture" }
func (h *heldCapture) Pulled(string)                {}

// Every marked handback question started its own goroutine, and, for the phone, adb processes, with no limit on how
// many ran at once; the request queue never counted them.
func TestHandbackQuestionsAreAnsweredAFewAtATime(t *testing.T) {
	capture := &heldCapture{let: make(chan struct{})}
	appium, _ := appiumThatCounts(t)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appium.URL
		c.Capture = capture
		offeringPhones(c, append([]string{"R5CT30ABCDE"}, fixturePhones...)...)
	})
	conn := farm.next(t)
	got := collect(readFarmSide(conn))
	defer close(capture.let)
	asks := map[string]string{"X-GuruFarm-Asks": "phone-state", "Content-Type": "application/json"}
	question := fmt.Sprintf(`{"udid":"R5CT30ABCDE","leaseId":%q}`, captureLease)
	const questions = 3 * tunnel.MaxHandbacks
	for i := 0; i < questions; i++ {
		send(t, conn, handbackQuestion(fmt.Sprintf("33333333-0000-0000-0000-%012d", i), asks, question))
	}
	waitFor(t, "the questions reach the capture", func() bool { return capture.running.Load() >= tunnel.MaxHandbacks })
	time.Sleep(300 * time.Millisecond)
	if peak := capture.peak.Load(); peak > tunnel.MaxHandbacks {
		t.Fatalf("%d handback questions were being answered at once; at most %d may be", peak, tunnel.MaxHandbacks)
	}
	refused := 0
	for i := 0; i < questions; i++ {
		if f, ok := got.within(fmt.Sprintf("33333333-0000-0000-0000-%012d", i), 20*time.Millisecond); ok {
			if statusOf(f) != http.StatusServiceUnavailable || !strings.Contains(answerBody(t, f), "handback questions, its most") {
				t.Fatalf("a question past the limit is answered with a 503 in words, got %d %q", statusOf(f), answerBody(t, f))
			}
			refused++
		}
	}
	if refused != questions-tunnel.MaxHandbacks {
		t.Fatalf("%d questions were answered at once with a 503; want the %d past the limit", refused, questions-tunnel.MaxHandbacks)
	}
	if !strings.Contains(out.String(), "capture handback questions at once") {
		t.Errorf("the terminal must say the limit was met:\n%s", out.String())
	}
	// A question is a phone and a lease: one of a megabyte is answered 400, and never holds its frame in a slot.
	huge := fmt.Sprintf(`{"udid":"R5CT30ABCDE","leaseId":%q,"pad":%q}`, captureLease, strings.Repeat("p", 1<<20))
	send(t, conn, handbackQuestion("33333333-0000-0000-0001-000000000000", asks, huge))
	if f, ok := got.within("33333333-0000-0000-0001-000000000000", 5*time.Second); !ok || statusOf(f) != http.StatusBadRequest {
		t.Fatalf("a handback question of a megabyte is answered 400: %v", f)
	}
}

// A frame the full request queue refused was answered from a goroutine that held the whole frame, body and all,
// until the farm read again — with no count and no byte bound.
func TestARefusalHoldsNoFrameAndOnlyAFewWait(t *testing.T) {
	big := strings.Repeat("A", 16<<20)
	appium := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"` + big + `"}`))
	}))
	t.Cleanup(appium.Close)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appium.URL
		c.MaxQueuedRequests = 1
		c.MaxPendingRefusals = 8
	})
	conn := farm.next(t)
	// THE FARM READS NOTHING: the 16 MiB answer to the first status read stays in the send queue, above
	// ResponseHighWater.
	send(t, conn, frame("34343434-0000-0000-0000-000000000001", "GET", "/status"))
	send(t, conn, frame("34343434-0000-0000-0000-000000000002", "GET", "/status"))
	send(t, conn, frame("34343434-0000-0000-0000-000000000003", "GET", "/status"))
	time.Sleep(500 * time.Millisecond)

	body := strings.Repeat("B", 256<<10)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutines := runtime.NumGoroutine()
	const refused = 200
	for i := 0; i < refused; i++ {
		send(t, conn, requestWithBody(fmt.Sprintf("34343434-0000-0000-0001-%012d", i), "GET", "/status", body))
	}
	waitFor(t, "the refusals are said", func() bool { return strings.Contains(out.String(), "more requests than this agent keeps waiting") })
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if grew := runtime.NumGoroutine() - goroutines; grew > 40 {
		t.Errorf("%d refused frames left %d more goroutines waiting; at most the 8 pending refusals may wait", refused, grew)
	}
	if grew := int64(after.HeapInuse) - int64(before.HeapInuse); grew > 16<<20 {
		t.Errorf("%d refused frames of 256 KiB each grew the heap by %d bytes while they waited: a refusal must hold no frame", refused, grew)
	}
	if !strings.Contains(out.String(), "refusals are already waiting to be sent") {
		t.Errorf("the terminal must say refusals past the limit go unanswered:\n%s", out.String())
	}
}

// The ids. The agent echoed a stream's or a request's id in its answer whatever its length, and a stream's end
// never waits for room: an id of a megabyte was a megabyte queued for every frame that carried it.
func TestAFrameIDLongerThanTheFarmMintsIsNeverEchoed(t *testing.T) {
	appium, _ := appiumThatCounts(t)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) { c.AppiumURL = appium.URL })
	conn := farm.next(t)
	reader := readFarmSide(conn)
	long := strings.Repeat("x", 1<<20)
	send(t, conn, map[string]any{"kind": "stream-start", "id": long, "method": "GET", "path": "", "headers": map[string]string{}})
	send(t, conn, frame(long, "GET", "/status"))
	send(t, conn, frame("35353535-0000-0000-0000-000000000001", "GET", "/status"))
	// Every frame the farm reads is looked at, until the answer to the ordinary frame and a little after it.
	answered := false
	deadline := time.After(5 * time.Second)
	for settle := (<-chan time.Time)(nil); ; {
		select {
		case f := <-reader.frames:
			if id, _ := f["id"].(string); len(id) > tunnel.MaxFrameIDBytes {
				t.Fatalf("an id of %d bytes was echoed back to the farm (kind %v)", len(id), f["kind"])
			} else if id == "35353535-0000-0000-0000-000000000001" && !answered {
				answered, settle = true, time.After(300*time.Millisecond)
			}
		case <-settle:
			if !strings.Contains(out.String(), "longer than the 128 this agent echoes") {
				t.Errorf("the terminal must say such a frame is skipped:\n%s", out.String())
			}
			return
		case <-deadline:
			t.Fatal("the ordinary frame was never answered")
		}
	}
}

// sessionsThatAnswer is one machine's Appium with two phones: a create names its phone ("sess-A" or "sess-B"); a
// session's command answers with size bytes, and phone A's holds its last byte until let is closed.
func sessionsThatAnswer(t *testing.T, size int, let chan struct{}) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 4096)
		n, _ := r.Body.Read(raw)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			phone := "A"
			if strings.Contains(string(raw[:n]), "HEALTHY") {
				phone = "B"
			}
			_, _ = fmt.Fprintf(w, `{"value":{"sessionId":"sess-%s","capabilities":{}}}`, phone)
		case strings.HasPrefix(r.URL.Path, "/session/sess-A/"):
			_, _ = w.Write([]byte(strings.Repeat("a", size-1)))
			w.(http.Flusher).Flush()
			select {
			case <-let:
			case <-r.Context().Done():
			}
			_, _ = w.Write([]byte("a"))
		case strings.HasPrefix(r.URL.Path, "/session/sess-B/"):
			_, _ = w.Write([]byte(strings.Repeat("b", size)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// Each lane held its whole answer, read and encoded, while it waited for room, and nothing bounded how many
// bytes every lane together held.
func TestAnswersOnTheirWayToTheFarmHoldABoundedNumberOfBytes(t *testing.T) {
	let := make(chan struct{})
	appiumURL := sessionsThatAnswer(t, 600, let)
	t.Cleanup(func() {
		select {
		case <-let:
		default:
			close(let)
		}
	})
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appiumURL
		c.MaxAnswerBytesInFlight = 1000
	})
	conn := farm.next(t)
	got := collect(readFarmSide(conn))
	for id, body := range map[string]string{"36363636-0000-0000-0000-00000000000a": createA, "36363636-0000-0000-0000-00000000000b": createB} {
		send(t, conn, requestWithBody(id, "POST", "/session", body))
		if f, ok := got.within(id, 5*time.Second); !ok || statusOf(f) != http.StatusOK {
			t.Fatalf("the session must start: %v\n%s", f, out.String())
		}
	}
	send(t, conn, frame("36363636-0000-0000-0000-000000000001", "GET", "/session/sess-A/source"))
	time.Sleep(300 * time.Millisecond) // phone A's 599 bytes are read, and held
	send(t, conn, frame("36363636-0000-0000-0000-000000000002", "GET", "/session/sess-B/source"))
	f, ok := got.within("36363636-0000-0000-0000-000000000002", 5*time.Second)
	if !ok {
		t.Fatal("phone B's answer never came")
	}
	if statusOf(f) != http.StatusBadGateway || !strings.Contains(answerBody(t, f), "already holding") {
		t.Fatalf("with 599 bytes held and a limit of 1000, phone B's 600 must not be relayed: got %d %q", statusOf(f), answerBody(t, f))
	}
	close(let)
	if f, ok := got.within("36363636-0000-0000-0000-000000000001", 5*time.Second); !ok || statusOf(f) != http.StatusOK {
		t.Fatalf("phone A's answer, within the limit, is relayed: %v", f)
	}
	// …and what an answer held is given back once it is sent: the next one fits again.
	send(t, conn, frame("36363636-0000-0000-0000-000000000003", "GET", "/session/sess-B/source"))
	if f, ok := got.within("36363636-0000-0000-0000-000000000003", 5*time.Second); !ok || statusOf(f) != http.StatusOK {
		t.Fatalf("once the answers held are sent, another fits: %v", f)
	}
	if !strings.Contains(out.String(), "answers on their way to the farm already hold 1000 bytes here") {
		t.Errorf("the terminal must say the limit was met:\n%s", out.String())
	}
}

// A hub answer was measured before the key scrub, which replaces a short user name with a longer mask: the frame
// sent could be larger than one tunnel message carries, and the farm would end the tunnel for every lease on it.
func TestAHubAnswerTheScrubMadeLongerIsMeasuredAgain(t *testing.T) {
	const limit = 8 << 10
	user := `{"user":"ab"},`
	payload := `{"value":{"sessionId":"hub-1","capabilities":{},"users":[` + strings.Repeat(user, (limit-200)/len(user)) + `{}]}}`
	if len(payload) > limit {
		t.Fatalf("the hub's answer must fit before the scrub: %d", len(payload))
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(hub.Close)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.BrowserStack = browserstack.Credentials{User: "ab", Key: testKey}
		c.BrowserStackHub, c.BrowserStackApp = hub.URL, testApp
		c.MaxAnswerBytes = limit
	})
	conn := farm.next(t)
	reader := readFarmSide(conn)
	send(t, conn, sessionFrame("37373737-0000-0000-0000-000000000001", cloudCreate))
	answer := reader.await(t, 5*time.Second, "the create's answer", responseTo("37373737-0000-0000-0000-000000000001"))
	encoded, _ := answer["bodyBase64"].(string)
	relayed, _ := base64.StdEncoding.DecodeString(encoded)
	if len(relayed) > limit {
		t.Fatalf("a hub answer of %d bytes after the scrub was relayed, past the %d one tunnel message carries (status %d)",
			len(relayed), limit, statusOf(answer))
	}
	if statusOf(answer) != http.StatusBadGateway || !strings.Contains(string(relayed), "with your access key replaced") {
		t.Fatalf("it is answered with a 502 in words: %d %q", statusOf(answer), relayed)
	}
}

// A create waited in the machine's one create lane however long it took its turn, and was sent on after the farm
// had already failed it: the session it started belonged to no lease.
func TestACreateTheFarmHasGivenUpOnIsNotSentOn(t *testing.T) {
	phones, appiumURL := newTwoPhones(t)
	phones.hold("create A")
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appiumURL
		c.CreateDeadline = 300 * time.Millisecond
	})
	conn := farm.next(t)
	collect(readFarmSide(conn))
	send(t, conn, requestWithBody("38383838-0000-0000-0000-00000000000a", "POST", "/session", createA))
	<-phones.entered
	send(t, conn, requestWithBody("38383838-0000-0000-0000-00000000000b", "POST", "/session", createB))
	time.Sleep(700 * time.Millisecond)
	phones.release("create A")
	time.Sleep(500 * time.Millisecond)
	phones.mu.Lock()
	creates := strings.Join(phones.creates, "")
	phones.mu.Unlock()
	if creates != "A" {
		t.Fatalf("a create that waited past the farm's deadline reached Appium: creates %q", creates)
	}
	if !strings.Contains(out.String(), "the farm had already given up on it") {
		t.Errorf("the terminal must say why it was not sent on:\n%s", out.String())
	}
}

// reconnecting is one machine whose farm can drop the tunnel and take the next one.
func reconnecting(t *testing.T, change func(*tunnel.Config)) (*twoPhones, *liveFarm, *syncWriter) {
	phones, appiumURL := newTwoPhones(t)
	farm := newLiveFarm(t)
	out := &syncWriter{}
	liveTunnel(t, farm.server.URL, out, func(c *tunnel.Config) {
		c.AppiumURL = appiumURL
		if change != nil {
			change(c)
		}
	})
	return phones, farm, out
}

// Creates. One create at a time per machine was a lane per tunnel: a create still running from a tunnel that
// ended did not stop the next tunnel's.
func TestCreatesStayOneAtATimeAcrossReconnects(t *testing.T) {
	phones, farm, out := reconnecting(t, nil)
	phones.hold("create A")
	first := farm.next(t)
	send(t, first, requestWithBody("39393939-0000-0000-0000-00000000000a", "POST", "/session", createA))
	if gate := <-phones.entered; gate != "create A" {
		t.Fatalf("phone A's create should reach Appium, not %q", gate)
	}
	first.CloseNow()
	second := farm.next(t)
	got := collect(readFarmSide(second))
	send(t, second, requestWithBody("39393939-0000-0000-0000-00000000000b", "POST", "/session", createB))
	time.Sleep(500 * time.Millisecond)
	phones.mu.Lock()
	creates := strings.Join(phones.creates, "")
	phones.mu.Unlock()
	if creates != "A" {
		t.Fatalf("a create from the next tunnel reached Appium while one from the tunnel before was still running: %q\n%s", creates, out.String())
	}
	phones.release("create A")
	if f, ok := got.within("39393939-0000-0000-0000-00000000000b", 5*time.Second); !ok || statusOf(f) != http.StatusOK {
		t.Fatalf("phone B's create runs once phone A's has ended: %v", f)
	}
}

// Forwards. Every bound on forwards in flight was the tunnel's own, built afresh at each connect.
func TestForwardsInFlightAreBoundedAcrossReconnects(t *testing.T) {
	phones, farm, out := reconnecting(t, func(c *tunnel.Config) { c.MaxForwards = 1 })
	phones.hold("status")
	first := farm.next(t)
	send(t, first, frame("39393939-0000-0000-0000-000000000001", "GET", "/status"))
	<-phones.entered
	first.CloseNow()
	second := farm.next(t)
	got := collect(readFarmSide(second))
	send(t, second, frame("39393939-0000-0000-0000-000000000002", "GET", "/status"))
	f, ok := got.within("39393939-0000-0000-0000-000000000002", 3*time.Second)
	if !ok {
		t.Fatalf("the second status read waited behind the first tunnel's: with one forward in flight it is answered at once\n%s", out.String())
	}
	if statusOf(f) != http.StatusServiceUnavailable || !strings.Contains(answerBody(t, f), "its most") {
		t.Fatalf("it is answered with a 503 in words: %d %q", statusOf(f), answerBody(t, f))
	}
	select {
	case gate := <-phones.entered:
		t.Fatalf("the second status read reached Appium (%s) while the first tunnel's was still in flight", gate)
	default:
	}
	phones.release("status")
}

var _ sync.Mutex
