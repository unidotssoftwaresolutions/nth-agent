package connect_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
)

// ── THE SILENCE THAT COST A CUSTOMER THEIR OWN PHONE ───────────────────────────────────────────────────────
//
// Without --appium the agent published no device rows and SAID NOTHING AT ALL: no outcome, no warning, no line in
// the transcript. A person watched their phone appear on the Devices screen, could not pick it for a run, and had
// nothing to read. The report is unchanged, byte for byte; what changed is that the agent now says why it is empty.

func TestConnectWithoutADriverSaysWhyNoDeviceIsOffered(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = driverOutcomes
	cfg.Driver = nil // no --appium: the operator's machine today

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	transcript := out.String()

	for _, want := range []string{
		"devices are not being offered",
		"without --appium",
		"npx nthcase-agent@latest doctor",
		"--appium URL",
		"stay visible on the Devices screen",
	} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the no-driver path must say %q — got:\n%s", want, transcript)
		}
	}

	// SAID ONCE, not every tick: two ticks ran above.
	if n := strings.Count(transcript, "devices are not being offered"); n != 1 {
		t.Fatalf("the reason is stated once per change, got %d times:\n%s", n, transcript)
	}

	// AND THE REPORT IS UNCHANGED. The phones stay present on the wire; only the silence went.
	body := stub.lastRegisterBody.Load().([]byte)
	if strings.Contains(string(body), `"devices":[{`) {
		t.Fatalf("the driver gate is shut — no device rows may be published, got %s", body)
	}
	if !strings.Contains(string(body), `"presentDevices"`) {
		t.Fatalf("the phones must still be reported as PRESENT, got %s", body)
	}
}

// THE TWO SENTENCES THAT PROMISED A BUNDLE. We decided not to bundle a runtime (2026-08-23): we do not put
// software we do not own on hardware we do not own. A promise we have decided not to keep is worse than one never
// made, so it may not survive anywhere in the agent's own words.
func TestNoHelpTextPromisesABundledRuntime(t *testing.T) {
	for _, file := range []string{"../../cmd/nth-agent/main.go", "loop.go"} {
		text := mustReadRel(t, file)
		lower := strings.ToLower(text)
		for _, promise := range []string{"bundled-runtime", "bundled runtime", "removes that prerequisite", "removes the prerequisite"} {
			if strings.Contains(lower, promise) {
				t.Fatalf("%s still promises a bundled runtime (%q) — the prerequisite stays and doctor reports it", file, promise)
			}
		}
		// …and the replacement is really there, so this seal cannot pass by the text simply being deleted.
		if !strings.Contains(text, "doctor") {
			t.Fatalf("%s must point a person at `nth-agent doctor` instead", file)
		}
	}
}

// ── THE FIRST-CONNECT ORDERING SEAL: the tunnel is dialled only after the farm knows the node ───────────────

// ⚠️ THIS SEAL USED TO ASSERT "ONCE PER PROCESS" AND ITS RESULT DEPENDED ON TEST ORDER. The once-ness was a
// package-level `var registeredOnce bool` in loop.go, so under `-count=5` the second run inherited a flag the
// first had set and the seal reddened — 4 times in 5, measured. That is not ordinary flakiness: ANY future test
// in this package that registered first would have disarmed this one silently, with no red anywhere. The guard
// now lives in main (see Config.OnRegistered), connect.Run is repeatable, and this runs it TWICE on purpose.
func TestOnRegisteredFiresOnEverySuccessfulRegistrationAndConnectRunIsRepeatable(t *testing.T) {
	fireCount := func(t *testing.T) (int32, int32) {
		t.Helper()
		stub, server := newStubFarm(t)
		var out strings.Builder
		cfg := config(server, &out, 3) // register + two heartbeats
		cfg.Probes = driverOutcomes
		var fired, registeredWhenFired atomic.Int32
		cfg.OnRegistered = func() {
			fired.Add(1)
			if stub.lastRegisterBody.Load() != nil {
				registeredWhenFired.Add(1) // the farm had the registration when the hook ran
			}
		}
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("run: %v", err)
		}
		return fired.Load(), registeredWhenFired.Load()
	}

	first, firstAfterPost := fireCount(t)
	if first != 1 {
		t.Fatalf("one successful registration must fire the hook once, got %d", first)
	}
	if firstAfterPost != 1 {
		t.Fatal("OnRegistered must run AFTER the register POST — a dial before it addresses a node the farm has never seen")
	}

	// ⚠️ THE SECOND RUN IN THE SAME PROCESS. A package-level flag makes this zero, and the failure below is what
	// four runs in five printed before this slice.
	second, secondAfterPost := fireCount(t)
	if second != 1 {
		t.Fatalf("a SECOND connect.Run in the same process fired the hook %d time(s), not once. connect.Run must "+
			"be repeatable: state that belongs to one run cannot live in a package-level variable, or the "+
			"package's test ORDER decides whether any seal about this hook passes — which is exactly what made "+
			"this test fail 4 times in 5 under -count=5 while passing alone.", second)
	}
	if secondAfterPost != 1 {
		t.Fatal("the ordering property must hold on every run, not only the first")
	}
}

// ⚠️ THE RE-REGISTRATION PATH ANSWERS FOR ITSELF NOW. loop.go's CallNotFound branch re-registers with the same
// identity when the farm has forgotten this node; a reader standing there used to have to find an unexported
// flag in another function to learn whether the hook fires again. It does, and this is where that is written.
func TestTheHookFiresAgainWhenTheFarmForgetsThisNodeAndItReRegisters(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 3)
	cfg.Probes = driverOutcomes
	stub.heartbeatStatus.Store(int32(http.StatusNotFound)) // the farm forgot this node
	var fired atomic.Int32
	cfg.OnRegistered = func() { fired.Add(1) }

	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	if fired.Load() < 2 {
		t.Fatalf("the farm answered the heartbeat 404 — it forgot this node — so connect re-registered with the "+
			"same identity, and the hook must fire on that registration too: it is a successful registration, "+
			"which is the only thing this package can honestly report. It fired %d time(s). A caller that must "+
			"not act twice owns that guard (main wraps its closure in a sync.Once, because IT knows the closure "+
			"starts a tunnel goroutine); a caller that SHOULD act again is no longer swallowed in silence by a "+
			"flag 130 lines away.", fired.Load())
	}
}

func TestMainDialsTheTunnelOnlyFromOnRegistered(t *testing.T) {
	// A labelled SOURCE PIN on main.go. ⚠️ ITS PATTERN CHANGED THIS SLICE AND ITS REASON IS REWRITTEN RATHER
	// THAN RE-MATCHED — a pin edited to fit the code it pins stops being a pin.
	//
	// It pinned `onRegistered = func() { go drive.Run(` — the tunnel starts in exactly one place and that place
	// is the OnRegistered closure. That property is unchanged and still pinned. What is NEW is the third clause:
	// the start must sit inside a sync.Once. connect.OnRegistered now fires on EVERY successful registration
	// (including the re-registration the CallNotFound branch performs), because a package that cannot see this
	// closure cannot know that acting twice is wrong. main can, so main holds the guard — and if that guard were
	// ever deleted while the contract stayed as it is, a forgotten-node re-registration would start a second
	// tunnel goroutine for one node. This pin is what notices.
	src := mustReadRel(t, "../../cmd/nth-agent/main.go")
	if strings.Count(src, "go drive.Run(") != 1 {
		t.Fatalf("main must start the tunnel in exactly one place, found %d", strings.Count(src, "go drive.Run("))
	}
	if !regexp.MustCompile(`onRegistered = func\(\) \{ dialOnce\.Do\(func\(\) \{ go drive\.Run\(`).MatchString(src) {
		t.Fatal("the one tunnel start must be the OnRegistered closure, and it must be guarded by the sync.Once " +
			"that replaced internal/connect's package-level flag — connect fires this hook on every successful " +
			"registration, so without the guard a forgotten-node re-registration starts a SECOND tunnel " +
			"goroutine for one node")
	}
	if !regexp.MustCompile(`var dialOnce sync\.Once`).MatchString(src) {
		t.Fatal("the guard must be a sync.Once declared beside the closure it guards — the whole point of moving " +
			"it out of internal/connect was that it belongs with the party that can state its reason")
	}
}

// ── THE FIRST TICK reads as a first tick: "has not connected yet", never "reconnects … back" ──────────────────

func TestTheFirstTickSaysNotConnectedYetNotReconnecting(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 1)
	cfg.Probes = driverOutcomes
	cfg.Driver = &connect.Driver{
		AppiumURL:    "http://127.0.0.1:4723",
		Probe:        func(context.Context) error { return nil },
		TunnelUp:     func() bool { return false },
		TunnelEverUp: func() bool { return false }, // dialled after registration — never up yet
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	_ = stub
	if !strings.Contains(out.String(), "has not connected yet") || strings.Contains(out.String(), "reconnects") {
		t.Fatalf("the first tick must describe a first tick:\n%s", out.String())
	}

	// …and once it HAS been up, an outage is a reconnect.
	out.Reset()
	stub2, server2 := newStubFarm(t)
	_ = stub2
	cfg2 := config(server2, &out, 1)
	cfg2.Probes = driverOutcomes
	cfg2.Driver = &connect.Driver{
		AppiumURL: "http://127.0.0.1:4723", Probe: func(context.Context) error { return nil },
		TunnelUp: func() bool { return false }, TunnelEverUp: func() bool { return true },
	}
	if err := connect.Run(context.Background(), cfg2); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "reconnects with backoff") {
		t.Fatalf("after an establishment, an outage reads as a reconnect:\n%s", out.String())
	}
}

// ── THE SWEEP: every sentence the agent can narrate, discovered from the source, read as a first-run transcript

// Narrated DISCOVERS narration literals: every string literal passed to narrator.say / driverDown / driverUp, and
// every fmt.Fprint* whose first argument is a narration writer (t.cfg.Out / n.out / cfg.Out).
func Narrated(source string) []string {
	var found []string
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`\bsay\("((?:[^"\\]|\\.)*)"`),
		// sayOnce(key, "format", …) — internal/capture's narration said once per key: the FORMAT is the sentence.
		regexp.MustCompile(`\bsayOnce\([^,]+,\s*"((?:[^"\\]|\\.)*)"`),
		// sayOnChange("format", …) — the loop's answers the farm did not take a call with, said once per change
		// (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO).
		regexp.MustCompile(`\bsayOnChange\("((?:[^"\\]|\\.)*)"`),
		regexp.MustCompile(`driverDown\(\s*"((?:[^"\\]|\\.)*)"`),
		regexp.MustCompile(`driverDown\([^"]*"(?:[^"\\]|\\.)*",\s*\n?\s*"((?:[^"\\]|\\.)*)"`),
		regexp.MustCompile(`Fprint(?:f|ln)?\((?:t\.cfg\.Out|n\.out|cfg\.Out), "((?:[^"\\]|\\.)*)"`),
	} {
		for _, m := range re.FindAllStringSubmatch(source, -1) {
			found = append(found, m[1])
		}
	}
	sort.Strings(found)
	return found
}

func TestEveryNarratedSentenceReadsAsTheStateTheAgentIsIn(t *testing.T) {
	// INSTRUMENT GUARD: the scanner sees a planted sentence in each shape it claims to read.
	planted := "n.say(\"PLANTED alpha %s\")\nnarrator.driverDown(\"PLANTED beta\",\n\t\"PLANTED gamma\")\nfmt.Fprintf(t.cfg.Out, \"PLANTED delta\\n\")\n" +
		"c.sayOnce(\"kept \"+r.Lease, \"PLANTED epsilon %s\", serial)\n" +
		"n.sayOnChange(\"PLANTED zeta (%d)\", status)\n"
	got := strings.Join(Narrated(planted), "|")
	for _, want := range []string{"PLANTED alpha", "PLANTED beta", "PLANTED gamma", "PLANTED delta", "PLANTED epsilon", "PLANTED zeta"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the scanner does not see %q in the shape it is written in: %q", want, got)
		}
	}

	var sentences []string
	// ../tunnel/stream.go joined the population with AN_AGENT_THAT_SHOWS_ITS_PHONE: the live view narrates too.
	// ../capture/controller.go and ../tunnel/capture.go joined with AN_AGENT_THAT_CAN_RECORD_ITS_PHONE: network capture
	// narrates what it does to a phone, and a first run must read as a first run there too.
	// newer.go joined with AN_AGENT_THAT_KNOWS_IT_IS_OLD: the loop says there when a newer agent is out.
	for _, rel := range []string{"loop.go", "newer.go", "../tunnel/tunnel.go", "../tunnel/stream.go", "../tunnel/capture.go", "../capture/controller.go"} {
		found := Narrated(mustReadRel(t, rel))
		if len(found) == 0 {
			t.Fatalf("%s narrates and the sweep found nothing in it — the scanner is blind to its shape", rel)
		}
		sentences = append(sentences, found...)
	}
	if len(sentences) < 10 {
		t.Fatalf("the sweep is blind: %d sentences", len(sentences))
	}

	// THE TRANSCRIPT TEST: a sentence must not describe a history that did not happen on a first run. A
	// sentence is allowed to describe a PAST only if it is guarded by a fact about that past (the tunnel's
	// everConnected / TunnelEverUp, the farm connection's recovered flag). The guarded ones are named here;
	// everything else must read as the present tense of the state it reports.
	historyWords := regexp.MustCompile(`(?i)\b(dropped|is back|restored|reconnects|reconnecting|again)\b`)
	guardedByAFactAboutThePast := map[string]string{
		"the drive tunnel dropped (%v) — reconnecting with backoff; devices stay unoffered until it is back\\n": "tunnel.go: said only when everConnected (the three-states seal)",
		"the drive tunnel is back\\n": "tunnel.go: said only when everConnected (the three-states seal)",
		"nothing to do — the agent reconnects with backoff; devices stay unoffered until the tunnel is back": "loop.go: said only when TunnelEverUp (the first-tick seal)",
		"connection to the farm restored": "loop.go: recovered() runs only after a 'cannot reach the farm' was said (the narrator's own flag)",
		// AN_AGENT_THAT_GUESSES_WHY_IT_WAS_REFUSED: the two refusal sentences left this table — registration's "did not
		// accept … start the agent again" and heartbeat's "no longer accepts — it may have been revoked". Both sites now
		// narrate through narrator.refused, and its remedy ("…then restart the agent") describes no past at all, so there
		// is nothing here for it to be classified as. Keeping it free of history words is deliberate: a remedy that has
		// to be re-classified every time it is reworded is a remedy nobody improves.
	}
	for _, s := range sentences {
		if !historyWords.MatchString(s) {
			continue
		}
		if _, ok := guardedByAFactAboutThePast[s]; !ok {
			t.Fatalf("narration describes a past the agent may not have had, and no fact guards it: %q — guard it, reword it, or classify it here", s)
		}
	}
	for s := range guardedByAFactAboutThePast {
		if !contains(sentences, s) {
			t.Fatalf("STALE classification: %q is no longer narrated", s)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func mustReadRel(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

var _ = json.Marshal
var _ = errors.New
