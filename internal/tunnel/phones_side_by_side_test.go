package tunnel_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// PHONES SIDE BY SIDE — one machine's phones no longer wait for each other. A tunnel is one machine, and every Appium
// command of every phone on it went through one worker, in arrival order: a 20-second session create on one phone held
// the next tap of every other phone on that machine for 20 seconds. Each session now has its own lane, in arrival order
// within it; different sessions' lanes run at once; status reads have a lane of their own; and session creates take
// turns in theirs, beside every session's lane.

const (
	createA = `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"00008140-0000C0FFEE0000A1"}]}}`
	createB = `{"capabilities":{"firstMatch":[{"platformName":"iOS","appium:automationName":"XCUITest","appium:udid":"00008030-HEALTHY"}]}}`
)

// twoPhones is one machine's Appium with two phones behind it: a create names its phone, a session is "sess-A" or
// "sess-B", and any request can be held at a named gate until the test lets it go. It records each session's commands
// in the order they reached it.
type twoPhones struct {
	mu      sync.Mutex
	gates   map[string]chan struct{}
	entered chan string
	order   map[string][]int
	creates []string
}

func newTwoPhones(t *testing.T) (*twoPhones, string) {
	p := &twoPhones{gates: map[string]chan struct{}{}, entered: make(chan string, 64), order: map[string][]int{}}
	server := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(server.Close)
	t.Cleanup(p.releaseAll) // before the server closes: a held handler would keep Close waiting
	return p, server.URL
}

// hold makes the next requests named gate wait until release(gate).
func (p *twoPhones) hold(gate string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gates[gate] = make(chan struct{})
}

func (p *twoPhones) release(gate string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g, ok := p.gates[gate]; ok {
		close(g)
		delete(p.gates, gate)
	}
}

func (p *twoPhones) releaseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, g := range p.gates {
		close(g)
		delete(p.gates, name)
	}
}

func (p *twoPhones) wait(gate string) {
	p.mu.Lock()
	g, ok := p.gates[gate]
	p.mu.Unlock()
	p.entered <- gate
	if ok {
		<-g
	}
}

func (p *twoPhones) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/status":
		p.wait("status")
		_, _ = w.Write([]byte(`{"value":{"ready":true}}`))
	case r.Method == http.MethodPost && r.URL.Path == "/session":
		phone := "A"
		if strings.Contains(string(raw), "HEALTHY") {
			phone = "B"
		}
		p.mu.Lock()
		p.creates = append(p.creates, phone)
		p.mu.Unlock()
		p.wait("create " + phone)
		_, _ = fmt.Fprintf(w, `{"value":{"sessionId":"sess-%s","capabilities":{}}}`, phone)
	case strings.HasPrefix(r.URL.Path, "/session/"):
		id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/session/"), "/")
		if n, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil {
			p.mu.Lock()
			p.order[id] = append(p.order[id], n)
			p.mu.Unlock()
		}
		p.wait(id)
		_, _ = w.Write([]byte(`{"value":null}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *twoPhones) seen(id string) []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.order[id]...)
}

// answers keeps every answer the farm side reads, by id, in the order they came — farmReader.await drops whatever it is
// not waiting for, and these seals wait for answers in an order the agent no longer promises.
type answers struct {
	mu     sync.Mutex
	byID   map[string]map[string]any
	order  []string
	update chan struct{}
}

func collect(reader *farmReader) *answers {
	a := &answers{byID: map[string]map[string]any{}, update: make(chan struct{}, 1)}
	go func() {
		for {
			select {
			case f := <-reader.frames:
				id, _ := f["id"].(string)
				if f["kind"] != nil || id == "" {
					continue
				}
				a.mu.Lock()
				a.byID[id] = f
				a.order = append(a.order, id)
				a.mu.Unlock()
				select {
				case a.update <- struct{}{}:
				default:
				}
			case <-reader.ended:
				return
			}
		}
	}()
	return a
}

// within waits up to bound for the answer to id, and says whether it came.
func (a *answers) within(id string, bound time.Duration) (map[string]any, bool) {
	deadline := time.After(bound)
	for {
		a.mu.Lock()
		f, ok := a.byID[id]
		a.mu.Unlock()
		if ok {
			return f, true
		}
		select {
		case <-a.update:
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			return nil, false
		}
	}
}

func (a *answers) arrived() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.order...)
}

// sideBySide is one machine with two phones, connected, with phone A's session made and a collector on the farm's side.
func sideBySide(t *testing.T) (*twoPhones, *tunnel.Tunnel, func(any), *answers) {
	t.Helper()
	phones, appiumURL := newTwoPhones(t)
	farm := newLiveFarm(t)
	var out syncWriter
	drive := liveTunnel(t, farm.server.URL, &out, func(c *tunnel.Config) { c.AppiumURL = appiumURL })
	conn := farm.next(t)
	got := collect(readFarmSide(conn))
	sendOne := func(f any) { send(t, conn, f) }
	sendOne(requestWithBody("51515151-0000-0000-0000-00000000000a", "POST", "/session", createA))
	if f, ok := got.within("51515151-0000-0000-0000-00000000000a", 5*time.Second); !ok || statusOf(f) != http.StatusOK {
		t.Fatalf("phone A's session must start: %v\n%s", f, out.String())
	}
	<-phones.entered // phone A's create
	return phones, drive, sendOne, got
}

// THE RULE THIS ROUND ADDS. A create on phone B that takes its time — an install — holds nothing of phone A's.
func TestASlowCreateOnOnePhoneDoesNotHoldAnotherPhonesCommands(t *testing.T) {
	phones, _, sendOne, got := sideBySide(t)
	phones.hold("create B")
	sendOne(requestWithBody("52525252-0000-0000-0000-00000000000b", "POST", "/session", createB))
	if gate := <-phones.entered; gate != "create B" {
		t.Fatalf("phone B's create should have reached Appium, not %q", gate)
	}
	sendOne(frame("52525252-0000-0000-0000-000000000001", "GET", "/session/sess-A/source?n=1"))
	if _, ok := got.within("52525252-0000-0000-0000-000000000001", 2*time.Second); !ok {
		t.Fatal("phone A's command waited behind phone B's session create — one machine's phones must not wait for each other")
	}
	phones.release("create B")
	if f, ok := got.within("52525252-0000-0000-0000-00000000000b", 5*time.Second); !ok || statusOf(f) != http.StatusOK {
		t.Fatalf("phone B's create must still be answered once it ends: %v", f)
	}
}

func TestAPhonesSlowCommandDoesNotHoldAnotherPhonesCreate(t *testing.T) {
	phones, _, sendOne, got := sideBySide(t)
	phones.hold("sess-A")
	sendOne(frame("53535353-0000-0000-0000-000000000001", "GET", "/session/sess-A/source?n=1"))
	<-phones.entered
	sendOne(requestWithBody("53535353-0000-0000-0000-00000000000b", "POST", "/session", createB))
	if _, ok := got.within("53535353-0000-0000-0000-00000000000b", 2*time.Second); !ok {
		t.Fatal("phone B's session create waited behind phone A's command")
	}
	phones.release("sess-A")
	if _, ok := got.within("53535353-0000-0000-0000-000000000001", 5*time.Second); !ok {
		t.Fatal("phone A's held command must still be answered")
	}
}

func TestTwoPhonesCommandsRunSideBySideAndAStatusReadWaitsForNeither(t *testing.T) {
	phones, _, sendOne, got := sideBySide(t)
	sendOne(requestWithBody("54545454-0000-0000-0000-00000000000b", "POST", "/session", createB))
	if _, ok := got.within("54545454-0000-0000-0000-00000000000b", 5*time.Second); !ok {
		t.Fatal("phone B's session must start")
	}
	<-phones.entered
	phones.hold("sess-A")
	sendOne(frame("54545454-0000-0000-0000-000000000001", "GET", "/session/sess-A/source?n=1"))
	<-phones.entered
	sendOne(frame("54545454-0000-0000-0000-000000000002", "GET", "/session/sess-B/source?n=1"))
	sendOne(frame("54545454-0000-0000-0000-000000000003", "GET", "/status"))
	if _, ok := got.within("54545454-0000-0000-0000-000000000002", 2*time.Second); !ok {
		t.Fatal("phone B's command waited behind phone A's")
	}
	if _, ok := got.within("54545454-0000-0000-0000-000000000003", 2*time.Second); !ok {
		t.Fatal("a status read waited behind a session's command")
	}
	phones.release("sess-A")
	if _, ok := got.within("54545454-0000-0000-0000-000000000001", 5*time.Second); !ok {
		t.Fatal("phone A's held command must still be answered")
	}
}

// ONE SESSION'S COMMANDS STAY IN THEIR ORDER: sent all at once, without waiting for an answer, they reach Appium one at a
// time, in the order the farm sent them, and are answered in that order.
func TestOneSessionsCommandsStayInTheirOrder(t *testing.T) {
	phones, _, sendOne, got := sideBySide(t)
	go func() {
		for range phones.entered {
		}
	}()
	const n = 40
	var ids []string
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("55555555-0000-0000-0000-%012d", i)
		ids = append(ids, id)
		path := "/session/sess-A/source?n=" + strconv.Itoa(i)
		if i%2 == 0 {
			path = "/session/sess-A/element/e1/text?n=" + strconv.Itoa(i)
		}
		sendOne(frame(id, "GET", path))
	}
	if _, ok := got.within(ids[n-1], 5*time.Second); !ok {
		t.Fatal("the last command was never answered")
	}
	seen := phones.seen("sess-A")
	for i, v := range seen {
		if v != i+1 {
			t.Fatalf("one session's commands reached Appium out of the order they were sent: %v", seen)
		}
	}
	var answered []string
	for _, id := range got.arrived() {
		if strings.HasPrefix(id, "55555555-") {
			answered = append(answered, id)
		}
	}
	if strings.Join(answered, ",") != strings.Join(ids, ",") {
		t.Fatalf("one session's answers came back out of order: %v", answered)
	}
}

// CREATES STAY ONE AT A TIME ON ONE MACHINE (the safer way: the report says what would have to be true to overlap them).
func TestSessionCreatesOnOneMachineStillTakeTurns(t *testing.T) {
	phones, _, sendOne, got := sideBySide(t)
	phones.hold("create B")
	sendOne(requestWithBody("56565656-0000-0000-0000-00000000000b", "POST", "/session", createB))
	<-phones.entered
	sendOne(requestWithBody("56565656-0000-0000-0000-00000000000a", "POST", "/session", createA))
	select {
	case gate := <-phones.entered:
		t.Fatalf("a second create (%s) reached Appium while the first was still running", gate)
	case <-time.After(300 * time.Millisecond):
	}
	phones.release("create B")
	for _, id := range []string{"56565656-0000-0000-0000-00000000000b", "56565656-0000-0000-0000-00000000000a"} {
		if _, ok := got.within(id, 5*time.Second); !ok {
			t.Fatalf("create %s was never answered", id)
		}
	}
	phones.mu.Lock()
	creates := strings.Join(phones.creates, "")
	phones.mu.Unlock()
	if creates != "ABA" {
		t.Fatalf("the creates reached Appium as %q; they must arrive in the order sent, one at a time", creates)
	}
}
