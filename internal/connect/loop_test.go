package connect_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

const testToken = "sk-agent-VERYSECRET-0123456789"

// stubFarm is the in-process farm: it records what the agent SENT and answers what the test says.
// No real farm, no network, no sleeps — the loop's timings are parameters.
type stubFarm struct {
	t                *testing.T
	mu               sync.Mutex
	urls             []string
	registerStatus   atomic.Int32 // http status to answer register with
	heartbeatStatus  atomic.Int32
	registers        atomic.Int32
	heartbeats       atomic.Int32
	lastRegisterBody atomic.Value // []byte
	lastHeartbeat    atomic.Value // []byte
	lastToken        atomic.Value // string
	workBody         atomic.Value // string — heartbeat 200 data payload
}

func newStubFarm(t *testing.T) (*stubFarm, *httptest.Server) {
	s := &stubFarm{t: t}
	s.registerStatus.Store(http.StatusOK)
	s.heartbeatStatus.Store(http.StatusOK)
	s.workBody.Store(`{"provisionTasks":[],"commands":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.urls = append(s.urls, r.URL.String())
		s.mu.Unlock()
		s.lastToken.Store(r.Header.Get("X-Agent-Token"))
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			s.registers.Add(1)
			s.lastRegisterBody.Store(body)
			status := int(s.registerStatus.Load())
			w.WriteHeader(status)
			if status == http.StatusOK {
				w.Write([]byte(`{"success":true,"data":{"nodeId":"11111111-2222-3333-4444-555555555555","apiKey":""}}`))
			} else {
				w.Write([]byte(`{"success":false,"message":"refused"}`))
			}
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			s.heartbeats.Add(1)
			s.lastHeartbeat.Store(body)
			status := int(s.heartbeatStatus.Load())
			w.WriteHeader(status)
			if status == http.StatusOK {
				w.Write([]byte(`{"success":true,"data":` + s.workBody.Load().(string) + `}`))
			} else {
				w.Write([]byte(`{"success":false,"message":"refused"}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return s, server
}

func (s *stubFarm) SeenURLs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

func firstRunOutcomes(ctx context.Context) []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.Android, nil, nil),
		inventory.Answered(inventory.IOS,
			[]inventory.Offerable{{ID: "00008030-HEALTHY", Model: "iPhone 15"}},
			[]inventory.Unusable{inventory.NewUnusable("00008030-PENDING", "PairingDialogResponsePending",
				"Unlock the device and tap Trust on the 'Trust This Computer?' dialog, then re-run.")}),
	}
}

func config(server *httptest.Server, out *strings.Builder, maxTicks int) connect.Config {
	return connect.Config{
		Client:         &farm.Client{BaseURL: server.URL, Token: testToken, HTTP: server.Client()},
		NodeID:         "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		HostName:       "omers-macbook",
		Probes:         firstRunOutcomes,
		Interval:       time.Millisecond, // parameters, not sleeps: the seam waits for nothing real
		BackoffBase:    time.Millisecond,
		BackoffCeiling: 2 * time.Millisecond,
		MaxTicks:       maxTicks,
		Sleep:          func(context.Context, time.Duration) {},
		Out:            out,
	}
}

// ── item 3: the mapping — and THE NO-PHANTOM SEAL ────────────────────────────────────────────────────────────

// The scenario that matters: one healthy phone, one untrusted phone, one platform answered-empty.
// The register body must carry NO device rows (a healthy phone with no driver endpoint would be a
// device with a fabricated Appium URL — the phantom), a COUNT on the answered outcome, the unusable
// device verbatim, and the token on the header.
func TestTheRegisterBodyCarriesTheTruthAndNoPhantoms(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	if err := connect.Run(context.Background(), config(server, &out, 1)); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := stub.lastToken.Load().(string); got != testToken {
		t.Fatalf("the credential rides X-Agent-Token, got %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body); err != nil {
		t.Fatalf("register body: %v", err)
	}

	// THE NO-PHANTOM SEAL: devices is present (the farm requires the field) and EMPTY — no
	// DeviceRegistration, no invented URL, ever.
	devices, present := body["devices"].([]any)
	if !present || len(devices) != 0 {
		t.Fatalf("devices must be [] — an offerable device would be a phantom with a fabricated URL: %v", body["devices"])
	}
	if body["nodeId"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("the stable identity must ride the body, got %v", body["nodeId"])
	}

	outcomes := body["platformOutcomes"].([]any)
	if len(outcomes) != 2 {
		t.Fatalf("one outcome per platform, got %d", len(outcomes))
	}
	ios := outcomes[1].(map[string]any)
	if ios["status"] != "Answered" || ios["offerableCount"] != float64(1) {
		t.Fatalf("the healthy phone travels as a COUNT on its Answered outcome, got %v", ios)
	}
	android := outcomes[0].(map[string]any)
	if android["status"] != "Answered" || android["offerableCount"] != float64(0) {
		t.Fatalf("android answered with zero, got %v", android)
	}

	unusable := body["unusableDevices"].([]any)
	entry := unusable[0].(map[string]any)
	if entry["serial"] != "00008030-PENDING" || entry["state"] != "pairingdialogresponsepending" {
		t.Fatalf("the unusable device travels verbatim, got %v", entry)
	}
	if !strings.Contains(entry["remedy"].(string), "Trust") {
		t.Fatalf("…with its remedy, got %v", entry["remedy"])
	}
}

// ── THE POPULATION SEAL: one platform's failure must not null the whole unusable list ───────────────────────

func TestOnePlatformsFailureDoesNotNullTheUnusableList(t *testing.T) {
	report := farm.BuildReport([]inventory.Outcome{
		inventory.Answered(inventory.Android, nil, nil), // answered: NOTHING attached — a real answer
		inventory.CouldNotAsk(inventory.IOS, "usbmuxd is not running", "start it"),
	})
	if report.Unusable == nil {
		t.Fatal("android ESTABLISHED its answer (empty) — the list must be [], not null; nulling it rebuilds the coarse one-voice lie")
	}
	if len(*report.Unusable) != 0 {
		t.Fatalf("android established an EMPTY answer, got %v", *report.Unusable)
	}

	// …and when NO platform established anything, null is the truth.
	nothing := farm.BuildReport([]inventory.Outcome{
		inventory.CouldNotAsk(inventory.Android, "adb gone", "install it"),
		inventory.CouldNotAsk(inventory.IOS, "usbmuxd gone", "start it"),
	})
	if nothing.Unusable != nil {
		t.Fatalf("no platform established anything — the list must be null, got %v", *nothing.Unusable)
	}
}

// ── item 4: four answers, four behaviours ────────────────────────────────────────────────────────────────────

// THE TERMINAL-AUTH SEAL: a 401 stops the loop — no retry, no re-register, non-nil error — and tells
// the user what to do.
func TestA401IsTerminal(t *testing.T) {
	stub, server := newStubFarm(t)
	stub.registerStatus.Store(http.StatusUnauthorized)
	var out strings.Builder

	err := connect.Run(context.Background(), config(server, &out, 10))
	if err == nil {
		t.Fatal("a refused credential must surface as an error (the command exits non-zero)")
	}
	if got := stub.registers.Load(); got != 1 {
		t.Fatalf("a 401 must not be retried — hammering an auth failure is the incident; got %d attempts", got)
	}
	if !strings.Contains(out.String(), "NTH_AGENT_TOKEN") {
		t.Fatalf("the user must be told what to do, got %q", out.String())
	}
}

// THE RE-REGISTER SEAL: a heartbeat 404 means the row is gone — re-register with the SAME id, continue.
func TestA404ReRegistersWithTheSameIdentity(t *testing.T) {
	stub, server := newStubFarm(t)
	stub.heartbeatStatus.Store(http.StatusNotFound)
	var out strings.Builder

	go func() { // flip the farm back once the re-register lands, so the loop can finish its ticks
		for stub.registers.Load() < 2 {
			time.Sleep(time.Millisecond)
		}
		stub.heartbeatStatus.Store(http.StatusOK)
	}()
	if err := connect.Run(context.Background(), config(server, &out, 3)); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := stub.registers.Load(); got < 2 {
		t.Fatalf("the 404 sentence says re-register; got %d registrations", got)
	}
	var body map[string]any
	json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body)
	if body["nodeId"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("…with the SAME identity (GF2 makes that safe), got %v", body["nodeId"])
	}
}

// THE CONTENTION SEAL: a 503 waits for the next tick and does NOT re-register — anything more would
// undo the farm's own contention fix.
func TestA503WaitsAndNeverReRegisters(t *testing.T) {
	stub, server := newStubFarm(t)
	stub.heartbeatStatus.Store(http.StatusServiceUnavailable)
	var out strings.Builder

	if err := connect.Run(context.Background(), config(server, &out, 3)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := stub.registers.Load(); got != 1 {
		t.Fatalf("a 503 must not trigger a re-register, got %d registrations", got)
	}
	if got := stub.heartbeats.Load(); got < 2 {
		t.Fatalf("the loop keeps ticking through 503s, got %d heartbeats", got)
	}
	// AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO: this stub's 503 carries the farm's envelope, saying "refused", and that is
	// what is narrated now — once, as the message here always said and never checked. A 503 with no sentence still says
	// "the farm is busy" (TestA503SaysTheFarmsSentenceOrTodaysLine).
	if strings.Count(out.String(), "(503). It says: “refused”") != 1 {
		t.Fatalf("the busy state is narrated once, got %q", out.String())
	}
}

// Work the agent cannot perform is PRINTED, never silently discarded.
func TestAssignedWorkIsPrintedNotDiscarded(t *testing.T) {
	stub, server := newStubFarm(t)
	stub.workBody.Store(`{"provisionTasks":[{"provisioningId":"x"}],"commands":[]}`)
	var out strings.Builder

	if err := connect.Run(context.Background(), config(server, &out, 1)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "cannot perform") {
		t.Fatalf("assigned work must be printed as a fact, got %q", out.String())
	}
}

// ── item 5: narration, not a ticker ─────────────────────────────────────────────────────────────────────────

func TestTheLoopNarratesChangesNotTicks(t *testing.T) {
	_, server := newStubFarm(t)
	var out strings.Builder

	if err := connect.Run(context.Background(), config(server, &out, 5)); err != nil {
		t.Fatalf("run: %v", err)
	}
	// The platform situation is printed ONCE (nothing changed across 5 ticks), not per heartbeat.
	// (Counted on one distinctive line — an earlier draft counted a phrase BOTH platform lines share
	// and read one narration pass as two.)
	if got := strings.Count(out.String(), "ios: 1 usable"); got != 1 {
		t.Fatalf("an unchanged state prints once, got %d prints in %q", got, out.String())
	}
}

// ── item 1: THE NEVER-WRITTEN SEAL ──────────────────────────────────────────────────────────────────────────

// No output of any scenario — success, auth failure, busy, transport failure — contains the token.
func TestTheTokenNeverAppearsInAnyOutput(t *testing.T) {
	scenarios := map[string]func(*stubFarm){
		"success":   func(s *stubFarm) {},
		"auth":      func(s *stubFarm) { s.registerStatus.Store(http.StatusUnauthorized) },
		"busy":      func(s *stubFarm) { s.heartbeatStatus.Store(http.StatusServiceUnavailable) },
		"not found": func(s *stubFarm) { s.registerStatus.Store(http.StatusNotFound) },
	}
	for name, arrange := range scenarios {
		stub, server := newStubFarm(t)
		arrange(stub)
		var out strings.Builder
		_ = connect.Run(context.Background(), config(server, &out, 2))
		if strings.Contains(out.String(), testToken) || strings.Contains(out.String(), "VERYSECRET") {
			t.Fatalf("%s: the token reached the output: %q", name, out.String())
		}
	}

	// …and a transport error that carries the URL still carries no token (it rides a header).
	var out strings.Builder
	cfg := config(&httptest.Server{URL: "http://127.0.0.1:1"}, &out, 1)
	cfg.Client.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
	cfg.MaxTicks = 1
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = connect.Run(ctx, cfg)
	if strings.Contains(out.String(), "VERYSECRET") {
		t.Fatalf("transport error narration leaked the token: %q", out.String())
	}
}

// ── the transport seals (A_CREDENTIAL_IS_ONLY_AS_PRIVATE_AS_ITS_TRANSPORT) ──────────────────────────────────

// THE FAIL-BEFORE-THE-FIRST-REQUEST SEAL: a plaintext farm URL is refused before ANY request — the
// stub fails the test if it is ever touched, so "checked at startup" is proven, not assumed.
func TestAPlaintextFarmUrlIsRefusedBeforeAnyRequest(t *testing.T) {
	touched := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was made before the transport was validated — the token crossed the wire")
	}))
	defer touched.Close()

	var out strings.Builder
	cfg := config(touched, &out, 3)
	cfg.Client.BaseURL = "http://farm.example" // plaintext, non-loopback — never contacted

	// THE TERMINAL-TRANSPORT GUARD (AN_ASSERTION_THAT_BUYS_A_READ item 0): if the loop ever treats the
	// unfixable transport as retryable weather, Run never returns — this deadline turns that spin into
	// a loud red instead of a package-timeout hang.
	done := make(chan error, 1)
	go func() { done <- connect.Run(context.Background(), cfg) }()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop retried an unfixable transport — plaintext is terminal, not weather")
	}
	if err == nil {
		t.Fatal("a plaintext non-loopback farm URL must be terminal")
	}
	if !strings.Contains(out.String(), "cleartext") || !strings.Contains(out.String(), "https://") {
		t.Fatalf("the refusal names what is wrong and what to do, got %q", out.String())
	}
}

// THE LOCAL-DEVELOPMENT EXCEPTION SEAL: loopback http works end to end (every other test in this file
// rides it — this one pins it on the full loop explicitly).
func TestLoopbackHttpIsAllowedEndToEnd(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	if err := connect.Run(context.Background(), config(server, &out, 1)); err != nil {
		t.Fatalf("loopback http must work — the token never leaves the machine: %v", err)
	}
	if stub.registers.Load() != 1 {
		t.Fatal("the loopback farm must actually have been reached")
	}
}

// THE URL HALF OF THE NEVER-WRITTEN SEAL (widened this slice: the output half could not see a token
// smuggled into a query parameter — the stub now records every request URL).
func TestTheTokenNeverRidesTheUrl(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	if err := connect.Run(context.Background(), config(server, &out, 2)); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, url := range stub.SeenURLs() {
		if strings.Contains(url, testToken) || strings.Contains(url, "VERYSECRET") {
			t.Fatalf("the token rode the URL — a query string lands in access logs on every hop: %q", url)
		}
	}
	if len(stub.SeenURLs()) == 0 {
		t.Fatal("instrument guard: no URLs recorded — the widened seal is not observing")
	}
}

// ── A ROW FOR A PHONE WE WILL NEVER TOUCH: cloud rows need the tunnel, and nothing else ─────────────────────

func cloudSpecs() []farm.CloudDeviceSpec {
	return []farm.CloudDeviceSpec{
		{DeviceName: "Google Pixel 9", PlatformVersion: "15.0", Wire: "browserstack:Google Pixel 9|15.0",
			Platform: farm.WireAndroid},
	}
}

// THE WHOLE POINT OF THE SLICE. A customer whose devices are all on BrowserStack has no local phone and no local
// Appium — the product did not work for exactly the customer it was built for. This agent has neither, and must
// still publish a leasable row.
func TestACloudOnlyAgentPublishesItsRowsWithNoLocalAppium(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Probes = func(context.Context) []inventory.Outcome { return nil } // nothing local to see
	cfg.CloudDevices = cloudSpecs()
	// A driver with NO AppiumURL: main builds exactly this when --appium is absent and cloud specs are present.
	// Its probe would dial "" — this machine's own loopback — so the loop must never call it.
	cfg.Driver = &connect.Driver{
		AppiumURL: "",
		Probe: func(context.Context) error {
			t.Fatal("a cloud-only agent has no local Appium and must never be asked to probe one — a probe " +
				"against \"\" dials this machine's own loopback, which is the wrong-device defect the farm's " +
				"blank-address rule exists to prevent")
			return nil
		},
		TunnelUp:     func() bool { return true },
		TunnelEverUp: func() bool { return true },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body); err != nil {
		t.Fatalf("register body: %v", err)
	}
	devices, _ := body["devices"].([]any)
	if len(devices) != 1 {
		t.Fatalf("a cloud-only agent must publish its configured rows, got %d", len(devices))
	}
	descriptor := devices[0].(map[string]any)["descriptor"].(map[string]any)
	if descriptor["cloudSpec"] != "browserstack:Google Pixel 9|15.0" {
		t.Fatalf("the row must carry its spec: %v", descriptor)
	}
	if descriptor["kind"] != "Physical" {
		t.Fatalf("a cloud row is physical hardware, got %v", descriptor["kind"])
	}
	if body["reachability"] != "TunnelOnly" {
		t.Fatalf("a cloud row is reached only through this agent, exactly like a local one: %v", body["reachability"])
	}
}

// THE TUNNEL IS THE ONE PRECONDITION THEY SHARE. A consumer reaches a cloud device through this agent, so a row
// published while the tunnel is down would be the phantom the driver gate exists to prevent.
func TestNoCloudRowIsPublishedWhileTheTunnelIsDown(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Probes = func(context.Context) []inventory.Outcome { return nil }
	cfg.CloudDevices = cloudSpecs()
	cfg.Driver = &connect.Driver{
		AppiumURL:    "",
		Probe:        func(context.Context) error { return nil },
		TunnelUp:     func() bool { return false },
		TunnelEverUp: func() bool { return false },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body)
	if devices, _ := body["devices"].([]any); len(devices) != 0 {
		t.Fatalf("a row nobody can reach is the phantom this programme keeps deleting, got %d", len(devices))
	}
}

// A MIXED AGENT: one machine, one local phone, one cloud spec. BOTH kinds of row, neither path weakened.
func TestAMixedAgentPublishesBothKindsOfRow(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	// AN ANDROID PHONE ON THE DESK: no signing identity is needed to offer one, so this test is about the two
	// kinds of row and not about iOS provisioning.
	cfg.Probes = func(context.Context) []inventory.Outcome {
		return []inventory.Outcome{inventory.Answered(inventory.Android,
			[]inventory.Offerable{{ID: "LOCAL-SERIAL-1", Model: "Pixel 7"}}, nil)}
	}
	cfg.CloudDevices = cloudSpecs()
	cfg.Driver = &connect.Driver{
		AppiumURL:    "http://127.0.0.1:4723",
		Probe:        func(context.Context) error { return nil },
		TunnelUp:     func() bool { return true },
		TunnelEverUp: func() bool { return true },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal(stub.lastRegisterBody.Load().([]byte), &body)
	devices, _ := body["devices"].([]any)

	local, cloud := 0, 0
	for _, d := range devices {
		descriptor := d.(map[string]any)["descriptor"].(map[string]any)
		if _, isCloud := descriptor["cloudSpec"]; isCloud {
			cloud++
			if descriptor["appiumUrl"] == "http://127.0.0.1:4723" {
				t.Fatal("a cloud row must not borrow the local Appium's address — nothing forwards a cloud " +
					"session there, and a real address on a row nobody drives locally is the phantom's shape")
			}
			continue
		}
		local++
		if descriptor["appiumUrl"] != "http://127.0.0.1:4723" {
			t.Fatalf("the local row's address must be untouched, got %v", descriptor["appiumUrl"])
		}
	}
	if local == 0 || cloud != 1 {
		t.Fatalf("both kinds must be published: %d local, %d cloud (of %d rows)", local, cloud, len(devices))
	}
}
