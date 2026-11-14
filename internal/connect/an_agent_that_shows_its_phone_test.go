package connect_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
)

// AN_AGENT_THAT_SHOWS_ITS_PHONE — what the connect loop says about the live view and the tunnel: each phone's own
// mirror port on its descriptor, the set the tunnel may stream kept to exactly those rows, the node's word for whether
// it can stream at all, and TunnelDown said from the tunnel's evidence.

// judged is a live-view judge with fixed answers: the Samsung on 35118, the iPhone on 31002, anything else none.
func judged(platform inventory.Platform, udid string) liveview.Availability {
	switch udid {
	case "R5CT30ABCDE":
		return liveview.Availability{Available: true, Port: 35118, Reason: "its mirror is 127.0.0.1:35118"}
	case "00008030-HEALTHY":
		return liveview.Availability{Available: true, Port: 31002, Reason: "its mirror is 127.0.0.1:31002"}
	}
	return liveview.Availability{Reason: "no mirror port was free on this machine"}
}

func drivingConfig(server interface{ Close() }, cfg connect.Config) connect.Config {
	cfg.Probes = driverOutcomes
	cfg.IosTeamID, cfg.IosWdaBundleID = "TEAM123456", "com.example.WebDriverAgentRunner" // so the iPhone is offered too
	cfg.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723",
		Probe:     func(context.Context) error { return nil },
		TunnelUp:  func() bool { return true },
	}
	return cfg
}

func descriptors(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var decoded struct {
		Devices []struct {
			Descriptor map[string]any `json:"descriptor"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	out := map[string]map[string]any{}
	for _, d := range decoded.Devices {
		out[d.Descriptor["udid"].(string)] = d.Descriptor
	}
	return out
}

func TestEveryOfferedPhoneCarriesItsOwnMirrorPortAndOnlyThoseAreStreamable(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	mirrors := &liveview.Offered{}
	cfg := drivingConfig(server, config(server, &out, 1))
	cfg.LiveView, cfg.Mirrors = judged, mirrors
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	rows := descriptors(t, stub.lastHeartbeat.Load().([]byte))
	if rows["R5CT30ABCDE"]["mirrorPort"] != float64(35118) || rows["00008030-HEALTHY"]["mirrorPort"] != float64(31002) {
		t.Fatalf("each phone carries the mirror port this machine reserved for it — Android included: %v", rows)
	}
	for port, want := range map[uint16]string{35118: "R5CT30ABCDE", 31002: "00008030-HEALTHY"} {
		if phone, _, ok := mirrors.Mirror(port); !ok || phone != want {
			t.Fatalf("the tunnel may stream %d for %s: got %q %v", port, want, phone, ok)
		}
	}
	if mirrors.Count() != 2 {
		t.Fatalf("exactly the published mirrors are streamable: %d", mirrors.Count())
	}
	var register map[string]any
	json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &register)
	if register["liveViewAvailability"] != "Available" {
		t.Fatalf("a machine with a local Appium and a live view says it can stream: %v", register["liveViewAvailability"])
	}
}

func TestAPhoneWithNoLiveViewCarriesNoPortAndIsNotStreamable(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	mirrors := &liveview.Offered{}
	cfg := drivingConfig(server, config(server, &out, 1))
	cfg.LiveView = func(platform inventory.Platform, udid string) liveview.Availability {
		if udid == "R5CT30ABCDE" {
			return liveview.Availability{Reason: "no mirror port was free on this machine"}
		}
		return judged(platform, udid)
	}
	cfg.Mirrors = mirrors
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	rows := descriptors(t, stub.lastHeartbeat.Load().([]byte))
	if _, carries := rows["R5CT30ABCDE"]["mirrorPort"]; carries {
		t.Fatalf("a phone this machine cannot show must carry NO mirror port — absent is the farm's \"reserved none\": %v", rows["R5CT30ABCDE"])
	}
	if mirrors.Count() != 1 {
		t.Fatalf("only the phone with a mirror is streamable: %d", mirrors.Count())
	}
}

func TestTheNodeSaysItCanStreamOnlyWithALocalAppium(t *testing.T) {
	// A CLOUD-ONLY agent: a tunnel, no local Appium — nothing here starts a phone's screen server.
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.LiveView, cfg.Mirrors = judged, &liveview.Offered{}
	cfg.Driver = &connect.Driver{TunnelUp: func() bool { return true }}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	var register map[string]any
	json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &register)
	if register["liveViewAvailability"] != "Unavailable" {
		t.Fatalf("an agent with no local Appium cannot stream and must say so: %v", register["liveViewAvailability"])
	}
	// (No driver at all keeps yesterday's bytes, "Unavailable" included — TestNoDriverMeansTodaysBehaviourByteForByte.)
}

func TestAPhoneNoLongerOfferedIsWithdrawnTheSameTickAndAClosedGateWithdrawsAll(t *testing.T) {
	stub, server := newStubFarm(t)
	_ = stub
	var out strings.Builder
	mirrors := &liveview.Offered{}
	cfg := drivingConfig(server, config(server, &out, 3))
	cfg.LiveView, cfg.Mirrors = judged, mirrors

	var mu sync.Mutex
	tick := 0
	var samsungGone <-chan struct{}
	appiumUp := true
	cfg.Probes = func(ctx context.Context) []inventory.Outcome {
		mu.Lock()
		defer mu.Unlock()
		tick++
		if tick == 1 {
			return driverOutcomes(ctx)
		}
		if tick == 3 { // registration, heartbeat 1: both phones; heartbeat 2: the Samsung is unplugged
			_, samsungGone, _ = mirrors.Mirror(35118)
		}
		if tick >= 3 {
			return []inventory.Outcome{inventory.Answered(inventory.Android, nil, nil),
				inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030-HEALTHY", Model: "iPhone 15"}}, nil)}
		}
		return driverOutcomes(ctx)
	}
	cfg.Driver.Probe = func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if !appiumUp {
			return errors.New("connection refused")
		}
		return nil
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if samsungGone == nil {
		t.Fatal("setup: the Samsung was never on offer")
	}
	select {
	case <-samsungGone:
	default:
		t.Fatal("a phone unplugged between ticks must be withdrawn on the tick that no longer offers it")
	}
	if _, _, ok := mirrors.Mirror(31002); !ok {
		t.Fatal("the iPhone is still offered and still streamable")
	}

	// THE GATE CLOSING — Appium stops answering — publishes no rows, so nothing is streamable either.
	mu.Lock()
	appiumUp = false
	mu.Unlock()
	cfg.MaxTicks = 1
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if mirrors.Count() != 0 {
		t.Fatalf("a closed driver gate offers nothing, so no mirror may be streamed: %d", mirrors.Count())
	}
}

// ── THE HEARTBEAT AND THE TUNNEL'S EVIDENCE ────────────────────────────────────────────────────────────────────

type recordedEvidence struct {
	mu         sync.Mutex
	answered   int
	turnedAway int // answered, at one of the farm's limits (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO)
	silent     int
	down       bool
	why        string
}

func (e *recordedEvidence) FarmAnswered()     { e.mu.Lock(); e.answered++; e.mu.Unlock() }
func (e *recordedEvidence) FarmDidNotAnswer() { e.mu.Lock(); e.silent++; e.mu.Unlock() }
func (e *recordedEvidence) FarmTurnedAway()   { e.mu.Lock(); e.turnedAway++; e.mu.Unlock() }
func (e *recordedEvidence) DeclareDown() (bool, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.down, e.why
}

func TestTheHeartbeatTellsTheTunnelWhetherTheFarmAnsweredAndSaysWhatItDeclares(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	evidence := &recordedEvidence{down: true, why: "could not connect: expected handshake response status code 101 but got 403"}
	cfg := drivingConfig(server, config(server, &out, 2))
	cfg.Driver.TunnelUp = func() bool { return false } // a tunnel that cannot be opened
	cfg.Driver.Evidence = evidence
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	var beat map[string]any
	json.Unmarshal(stub.lastHeartbeat.Load().([]byte), &beat)
	if beat["reachability"] != farm.ReachabilityTunnelDown {
		t.Fatalf("on the tunnel's evidence the heartbeat says TunnelDown, got %v", beat["reachability"])
	}
	if strings.Count(out.String(), "could not be opened although the farm answers (could not connect") != 1 {
		t.Fatalf("the change is said once, with the tunnel's own reason:\n%s", out.String())
	}
	if evidence.answered != 3 || evidence.silent != 0 {
		t.Fatalf("a registration and two heartbeats answered: answered=%d silent=%d", evidence.answered, evidence.silent)
	}

	// THE TWO ANSWERS THAT ARE NOT EVIDENCE — a 503 (which a gateway in front of an absent farm gives too) and no
	// answer at all — are passed on as such; and once the tunnel's state stops saying down, so does the heartbeat.
	stub.heartbeatStatus.Store(http.StatusServiceUnavailable)
	evidence.mu.Lock()
	evidence.down = false
	evidence.mu.Unlock()
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	json.Unmarshal(stub.lastHeartbeat.Load().([]byte), &beat)
	if beat["reachability"] != farm.ReachabilityTunnelOnly {
		t.Fatalf("no evidence, no TunnelDown: %v", beat["reachability"])
	}
	if evidence.silent == 0 {
		t.Fatal("a 503 is not an answer that says the farm can be reached")
	}
}

// sequencedEvidence says "down" for the first n reports, then not — with the tunnel still not up.
type sequencedEvidence struct {
	mu    sync.Mutex
	asked int
	downs int
}

func (e *sequencedEvidence) FarmAnswered()     {}
func (e *sequencedEvidence) FarmDidNotAnswer() {}
func (e *sequencedEvidence) FarmTurnedAway()   {}
func (e *sequencedEvidence) DeclareDown() (bool, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.asked++
	return e.asked <= e.downs, "could not connect: refused"
}

func TestDownWithdrawnBecauseTheFarmNeverHeardItIsNotCalledUp(t *testing.T) {
	// The node's rule: evidence the farm has not heard is void once a heartbeat fails, so "down" stops being said while
	// the tunnel is still down. The sentence must not claim the tunnel came up.
	_, server := newStubFarm(t)
	var out strings.Builder
	cfg := drivingConfig(server, config(server, &out, 2))
	cfg.Driver.TunnelUp = func() bool { return false }
	cfg.Driver.Evidence = &sequencedEvidence{downs: 2} // registration and the first heartbeat say down; then not
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "the drive tunnel is up") {
		t.Fatalf("a tunnel that is still down was called up:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "so that is no longer evidence about the tunnel") {
		t.Fatalf("withdrawing an unheard down says why:\n%s", out.String())
	}
}
