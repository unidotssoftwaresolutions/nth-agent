package browserstack_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// TheRealBody is what our platform actually sends, captured on 2026-09-01 from Appium.WebDriver 8.2.0 over
// Selenium 4.36 by pointing an AndroidDriver at a listener and printing the bytes.
//
// IT IS firstMatch, NOT alwaysMatch — and that is the whole reason this constant is verbatim rather than
// hand-written. Both the slice prompt and the design note said the platform sends `capabilities.alwaysMatch`;
// it does not. A merge written against alwaysMatch would have produced a body BrowserStack accepts and cannot
// authenticate, and the customer would have been sent to check the one thing that was not wrong.
const TheRealBody = `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2","appium:udid":"PROBE-UDID","appium:newCommandTimeout":90}]}}`

// testApp is a build id in the shape an upload returns. NOT A SECRET — a bs:// id names a build inside an
// account that already belongs to the customer and authenticates nothing on its own.
const testApp = "bs://0123456789abcdef0123456789abcdef01234567"

// THE LEASED SPEC. It arrives from the farm on every real session, so every merge test carries one — a merge
// with no device named is its own refusal and has its own test.
//
// ⚠️ DELIBERATELY NOT "Google Pixel 9 / 15.0". That was the pair the deleted constants held, and while this
// fixture used it NO TEST COULD SEE THE DIFFERENCE between reading the lease and hard-coding the old values —
// injection 5 restored the constants and the whole suite stayed green. A fixture that happens to equal the value
// it is meant to replace is a fixture that proves nothing.
var testSpec = browserstack.DeviceSpec{DeviceName: "Samsung Galaxy S23 Ultra", PlatformVersion: "13.0"}

func creds() browserstack.Credentials {
	return browserstack.Credentials{User: "acme-user", Key: "SUPERSECRETKEY"}
}

func mergeOrFail(t *testing.T, body string) map[string]any {
	t.Helper()
	merged, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(merged, &decoded); err != nil {
		t.Fatalf("merged body is not JSON: %v", err)
	}
	return decoded
}

func capsAt(t *testing.T, root map[string]any, path string) map[string]any {
	t.Helper()
	capabilities, ok := root["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("no capabilities object survived the merge")
	}
	if path == "alwaysMatch" {
		set, ok := capabilities["alwaysMatch"].(map[string]any)
		if !ok {
			t.Fatal("no alwaysMatch")
		}
		return set
	}
	first, ok := capabilities["firstMatch"].([]any)
	if !ok || len(first) == 0 {
		t.Fatal("no firstMatch entries")
	}
	set, ok := first[0].(map[string]any)
	if !ok {
		t.Fatal("firstMatch[0] is not an object")
	}
	return set
}

// ── the shape our platform really sends ────────────────────────────────────────────────────────────────────

func TestTheCredentialsLandInTheBodyOurPlatformActuallySends(t *testing.T) {
	set := capsAt(t, mergeOrFail(t, TheRealBody), "firstMatch")

	options, ok := set["bstack:options"].(map[string]any)
	if !ok {
		t.Fatal("the credentials must ride in the bstack:options vendor block — that is what BrowserStack's W3C documentation shows, and a bare `browserstack.user` is not a valid W3C extension capability at all (no colon), so a strict endpoint may reject the whole session")
	}
	if options["userName"] != "acme-user" || options["accessKey"] != "SUPERSECRETKEY" {
		t.Fatalf("both halves must arrive, got userName=%v", options["userName"])
	}
	if options["idleTimeout"] != float64(browserstack.IdleTimeoutSeconds) {
		t.Fatalf("a session nobody ends bills until it times out; idleTimeout must be set, got %v", options["idleTimeout"])
	}
}

func TestTheDeviceIsASpecAndTheLocalSerialIsRemoved(t *testing.T) {
	set := capsAt(t, mergeOrFail(t, TheRealBody), "firstMatch")

	if set["appium:deviceName"] != testSpec.DeviceName {
		t.Fatalf("BrowserStack picks a device by name, got %v", set["appium:deviceName"])
	}
	if set["appium:platformVersion"] != testSpec.PlatformVersion {
		t.Fatalf("…and by OS version, got %v", set["appium:platformVersion"])
	}
	if _, present := set["appium:udid"]; present {
		t.Fatal("the platform sets appium:udid from the lease because that is how it tells two LOCAL phones apart; BrowserStack has no udid, so asking for one selects nothing — and a local device's serial has no business travelling to a third party")
	}
}

func TestWhatTheBodyAlreadySaidIsKept(t *testing.T) {
	set := capsAt(t, mergeOrFail(t, TheRealBody), "firstMatch")

	if set["platformName"] != "Android" || set["appium:automationName"] != "UiAutomator2" {
		t.Fatal("the merge adds; it must not replace the capability set the platform built")
	}
	if set["appium:newCommandTimeout"] != float64(90) {
		t.Fatal("…including the ones this agent has no opinion about")
	}
}

func TestAnExistingVendorBlockIsExtendedRatherThanReplaced(t *testing.T) {
	body := `{"capabilities":{"firstMatch":[{"platformName":"Android","bstack:options":{"projectName":"Acme","appiumVersion":"2.4.1"}}]}}`
	options, ok := capsAt(t, mergeOrFail(t, body), "firstMatch")["bstack:options"].(map[string]any)
	if !ok {
		t.Fatal("no bstack:options")
	}
	if options["projectName"] != "Acme" || options["appiumVersion"] != "2.4.1" {
		t.Fatal("a caller who already set vendor options must keep them — clobbering the block would silently drop an appiumVersion the customer chose")
	}
	if options["accessKey"] != "SUPERSECRETKEY" {
		t.Fatal("…and still be authenticated")
	}
}

// ── the W3C shapes we do not send today but are legal ──────────────────────────────────────────────────────

// ⚠️ THIS TEST USED TO ASSERT THE BUG. Written in the same round as the belief it was checking, it required
// BOTH alwaysMatch and firstMatch to receive the credentials — and that is precisely the duplicate W3C §7.2
// makes `invalid argument`. It passed for a whole slice while certifying a body a conforming hub must refuse.
// The property it was actually reaching for is the one below: a client that moves to alwaysMatch is still
// authenticated. Which place wins is TestTheVendorBlockIsWrittenInExactlyOnePlace's question.
func TestAnAlwaysMatchOnlyClientIsStillAuthenticated(t *testing.T) {
	body := `{"capabilities":{"alwaysMatch":{"platformName":"Android"}}}`
	options, ok := capsAt(t, mergeOrFail(t, body), "alwaysMatch")["bstack:options"].(map[string]any)
	if !ok || options["accessKey"] != "SUPERSECRETKEY" {
		t.Fatal("a client that sends alwaysMatch instead of firstMatch must still be authenticated — the client " +
			"is free to move between these shapes and a merge that knows only one breaks on an upgrade")
	}
	if capsAt(t, mergeOrFail(t, body), "alwaysMatch")[browserstack.AppCapability] != testApp {
		t.Fatal("…and must still carry the build")
	}
}

// EVERY firstMatch ENTRY, and this is not pedantry. firstMatch entries are ALTERNATIVES — the remote end picks
// one. Merging into a single entry would leave the hub free to choose an unauthenticated sibling, which fails
// as "bad credentials" on a body that contains perfectly good ones.
func TestEveryFirstMatchAlternativeIsAuthenticated(t *testing.T) {
	body := `{"capabilities":{"firstMatch":[{"platformName":"Android"},{"platformName":"iOS"}]}}`
	merged, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	var root map[string]any
	_ = json.Unmarshal(merged, &root)
	entries := root["capabilities"].(map[string]any)["firstMatch"].([]any)
	if len(entries) != 2 {
		t.Fatalf("both alternatives must survive, got %d", len(entries))
	}
	for i, entry := range entries {
		options, ok := entry.(map[string]any)["bstack:options"].(map[string]any)
		if !ok || options["accessKey"] != "SUPERSECRETKEY" {
			t.Fatalf("firstMatch[%d] went up unauthenticated", i)
		}
	}
}

// ── refusing beats forwarding ──────────────────────────────────────────────────────────────────────────────

func TestAShapeWeDoNotRecogniseIsRefusedRatherThanSentUnauthenticated(t *testing.T) {
	for name, body := range map[string]string{
		"legacy JSONWP":    `{"desiredCapabilities":{"platformName":"Android"}}`,
		"no capabilities":  `{"something":"else"}`,
		"not JSON at all":  `<html>proxy error</html>`,
		"empty W3C object": `{"capabilities":{}}`,
	} {
		if _, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec); !errors.Is(err, browserstack.ErrUnrecognisedBody) {
			t.Fatalf("%s: expected a refusal naming the shape, got %v — forwarding it would send the session up WITHOUT credentials and come back as an authentication failure, which reads exactly like the customer mistyping their own key", name, err)
		}
	}
}

// The legacy shape is refused DELIBERATELY, and the reason belongs in a test rather than only in a comment:
// a JSONWP body pairs with the legacy `browserstack.user` spelling, which this agent does not write and has
// never tested against the hub. Shipping an untested credential path is worse than refusing one that our
// measured traffic does not produce.
func TestTheLegacyRefusalIsADecisionAndSaysSo(t *testing.T) {
	_, err := browserstack.Merge([]byte(`{"desiredCapabilities":{}}`), creds(), testApp, testSpec)
	if err == nil || !strings.Contains(err.Error(), "W3C") {
		t.Fatalf("the refusal must name the dialect it expected, got %v", err)
	}
}

func TestMergingWithoutBothHalvesIsRefused(t *testing.T) {
	for _, c := range []browserstack.Credentials{{}, {User: "u"}, {Key: "k"}} {
		if _, err := browserstack.Merge([]byte(TheRealBody), c, testApp, testSpec); err == nil {
			t.Fatalf("half a credential must never be merged: %+v", browserstack.Credentials{User: c.User})
		}
	}
}

func TestConfiguredAndPartialAreDifferentQuestions(t *testing.T) {
	cases := []struct {
		c                   browserstack.Credentials
		configured, partial bool
	}{
		{browserstack.Credentials{}, false, false},
		{browserstack.Credentials{User: "u"}, false, true},
		{browserstack.Credentials{Key: "k"}, false, true},
		{browserstack.Credentials{User: "u", Key: "k"}, true, false},
	}
	for _, c := range cases {
		if c.c.Configured() != c.configured || c.c.Partial() != c.partial {
			t.Fatalf("user=%q key set=%v: configured=%v partial=%v", c.c.User, c.c.Key != "", c.c.Configured(), c.c.Partial())
		}
	}
}

// ── item 0(c): which request is the session ────────────────────────────────────────────────────────────────

func TestOnlySessionCreationIsRecognised(t *testing.T) {
	yes := []struct{ method, path string }{
		{"POST", "/session"},         // what our platform sends, measured
		{"POST", "/wd/hub/session"},  // and what the hub's own base makes of it
		{"POST", "/session/"},        // a trailing slash is the same request
		{"post", "/session"},         // the method is compared case-insensitively
		{"POST", "/session?foo=bar"}, // a query does not change which request this is
	}
	for _, c := range yes {
		if !browserstack.IsSessionCreate(c.method, c.path) {
			t.Fatalf("%s %s is session creation and must be merged", c.method, c.path)
		}
	}

	// THE NEAR MISSES ARE THE POINT. Every in-session Appium call is POST /session/{id}/… — merging into one of
	// those would rewrite a command body mid-session.
	no := []struct{ method, path string }{
		{"GET", "/session"},
		{"DELETE", "/session/abc-123"},
		{"POST", "/session/abc-123/element"},
		{"POST", "/session/abc-123/execute/sync"},
		{"POST", "/session/abc-123/appium/device/lock"},
		{"POST", "/sessions"},
		{"POST", "/status"},
	}
	for _, c := range no {
		if browserstack.IsSessionCreate(c.method, c.path) {
			t.Fatalf("%s %s is NOT session creation and must pass through as bytes", c.method, c.path)
		}
	}
}

// ── item 3: three failures, three sentences, no key in any of them ─────────────────────────────────────────

func TestTheThreeFailuresAreDistinguishableAndCarryNoSecret(t *testing.T) {
	all := []browserstack.Failure{
		browserstack.FailureUnreachable, browserstack.FailureRejected, browserstack.FailureNoDevice,
	}
	seen := map[string]bool{}
	for _, f := range all {
		sentence := f.Describe()
		if sentence == "" {
			t.Fatalf("%s has no sentence — a failure a person cannot read is the single 'could not start a session' this slice exists to avoid", f)
		}
		if seen[sentence] {
			t.Fatalf("%s repeats another failure's sentence; one sentence for three causes sends a customer to look at the wrong thing", f)
		}
		seen[sentence] = true
		if strings.Contains(sentence, "SUPERSECRETKEY") || strings.Contains(strings.ToLower(sentence), "accesskey=") {
			t.Fatalf("%s leaks a credential", f)
		}
	}
}

func TestAFailureIsClassifiedFromWhatTheHubSaid(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   browserstack.Failure
	}{
		{"401 is the documented authentication failure", 401, `{"value":{"error":"unknown error"}}`, browserstack.FailureRejected},
		{"403 too", 403, ``, browserstack.FailureRejected},
		{"their words for a busy plan", 500, `{"value":{"message":"All parallel tests are currently in use"}}`, browserstack.FailureNoDevice},
		{"…and for a device it cannot give", 500, `{"value":{"message":"device not available"}}`, browserstack.FailureNoDevice},
		{"a created session is not a failure", 200, `{"value":{"sessionId":"abc"}}`, browserstack.Failure("")},
		{"an unrecognised failure names no cause", 500, `{"value":{"message":"something else entirely"}}`, browserstack.Failure("")},
	}
	for _, c := range cases {
		if got := browserstack.ClassifyResponse(c.status, []byte(c.body)); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A cause we cannot identify is left UNNAMED rather than guessed. The no-device arm keys on BrowserStack's own
// error wording and has not been confirmed on the wire; naming a cause we do not know would be the same defect
// as the single sentence, one step further on.
func TestAnUnknownFailureSaysNothingRatherThanGuessing(t *testing.T) {
	if got := browserstack.ClassifyResponse(500, []byte("gateway timeout")); got != "" {
		t.Fatalf("expected silence, got %q", got)
	}
	if browserstack.Failure("").Describe() != "" {
		t.Fatal("…and silence has no sentence")
	}
}

// ── item 3(a): one place or the other, never both ──────────────────────────────────────────────────────────

// W3C §7.2 REQUIRES THE REMOTE END TO REJECT a capability set where the same extension capability appears in
// alwaysMatch and in a firstMatch entry — the merged set is the union, and a duplicate key makes the whole
// request `invalid argument`. `Merge` used to test the two shapes in independent `if`s, so a body carrying both
// got `bstack:options` written into both and became a body the hub must refuse.
//
// UNREACHABLE FROM MEASURED TRAFFIC (the client sends firstMatch only), which is exactly why it is sealed: a
// bug nothing exercises is a bug nothing will notice when the traffic changes.
func TestTheVendorBlockIsWrittenInExactlyOnePlace(t *testing.T) {
	body := `{"capabilities":{"alwaysMatch":{"platformName":"Android"},"firstMatch":[{"appium:automationName":"UiAutomator2"},{}]}}`
	root := mergeOrFail(t, body)
	capabilities := root["capabilities"].(map[string]any)

	always, hasAlways := capabilities["alwaysMatch"].(map[string]any)["bstack:options"]
	if !hasAlways || always == nil {
		t.Fatal("alwaysMatch is the set that applies to EVERY alternative, so it is the one that carries the credentials")
	}
	for i, entry := range capabilities["firstMatch"].([]any) {
		if _, present := entry.(map[string]any)["bstack:options"]; present {
			t.Fatalf("firstMatch[%d] also carries bstack:options — the same extension capability in alwaysMatch "+
				"AND a firstMatch entry is `invalid argument` under W3C §7.2, so this body would be refused "+
				"outright by a conforming hub", i)
		}
	}
}

// ── item 3(b): the build reaches every capability set ──────────────────────────────────────────────────────

func TestTheBuildReachesEveryAlternativeTheHubMightPick(t *testing.T) {
	body := `{"capabilities":{"firstMatch":[{"platformName":"Android"},{"platformName":"Android","appium:deviceName":"other"}]}}`
	merged, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	var root map[string]any
	_ = json.Unmarshal(merged, &root)
	for i, entry := range root["capabilities"].(map[string]any)["firstMatch"].([]any) {
		if entry.(map[string]any)[browserstack.AppCapability] != testApp {
			t.Fatalf("firstMatch[%d] has no build — a firstMatch entry is an ALTERNATIVE the hub is free to pick, "+
				"and the one it picks must not be the one with nothing to run", i)
		}
	}
}

// ── item 2: a session with credentials and no build is refused before it leaves the machine ────────────────

func TestASessionWithNoBuildIsRefusedRatherThanSent(t *testing.T) {
	_, err := browserstack.Merge([]byte(TheRealBody), creds(), "", testSpec)
	if !errors.Is(err, browserstack.ErrNoApp) {
		t.Fatalf("expected a refusal naming the missing build, got %v", err)
	}
	// THE WHOLE TRUTH, not "app is required": a person who reads this knows the next command to type.
	for _, must := range []string{"--browserstack-app", "NTH_AGENT_BROWSERSTACK_APP", "upload", "bs://"} {
		if !strings.Contains(err.Error(), must) {
			t.Fatalf("the refusal must mention %q — the hub's own message for this failure says \"Platform and "+
				"Browser not valid.\", which names neither the build nor the remedy; got %q", must, err.Error())
		}
	}
}

func TestMissingAppIsASeparateQuestionFromTheOtherTwo(t *testing.T) {
	full := creds()
	cases := []struct {
		name       string
		c          browserstack.Credentials
		app        string
		missingApp bool
	}{
		{"configured, no app", full, "", true},
		{"configured with app", full, testApp, false},
		{"no credentials at all", browserstack.Credentials{}, "", false},
		{"no credentials, app set", browserstack.Credentials{}, testApp, false},
		{"half a credential", browserstack.Credentials{User: "u"}, "", false},
	}
	for _, c := range cases {
		if got := browserstack.MissingApp(c.c, c.app); got != c.missingApp {
			t.Fatalf("%s: MissingApp = %v, want %v", c.name, got, c.missingApp)
		}
	}
	// AND IT MUST NOT FIRE WITHOUT BROWSERSTACK. An agent driving a local Appium needs no build and must be
	// completely unaffected — the refusal is for people who asked for the cloud.
	if browserstack.MissingApp(browserstack.Credentials{}, "") {
		t.Fatal("an agent with no BrowserStack account was told it needs a BrowserStack build")
	}
}

// ── item 4: a 200 is not a verdict ─────────────────────────────────────────────────────────────────────────

// THE REAL ENVELOPE, captured 2026-09-01 from the hub. The transport said 200; the payload said failure. The
// first version of ClassifyResponse returned "" for anything under 400, so the agent stayed silent about a
// session that had not started.
const TheObservedFailure = `{"value":{"error":"Error : Platform and Browser not valid.","message":"Error : Platform and Browser not valid."},"sessionId":"","status":13}`

func TestTheObservedFailureIsNoLongerSilent(t *testing.T) {
	got := browserstack.ClassifyResponse(200, []byte(TheObservedFailure))
	if got != browserstack.FailureCannotStart {
		t.Fatalf("the one failure we have actually seen must be classified, got %q — it arrived as HTTP 200 and "+
			"the old function read the transport instead of the payload", got)
	}
	said := got.Describe()
	// NAMED HONESTLY AND NOT OVER-NAMED: one sentence, two candidates, because the wire cannot tell them apart.
	for _, must := range []string{"build", "device"} {
		if !strings.Contains(said, must) {
			t.Fatalf("the sentence must name both candidates (%q missing): the hub's message has at least two "+
				"causes and picking one is a guess dressed as a finding; got %q", must, said)
		}
	}
}

func TestASuccessIsStillASuccess(t *testing.T) {
	ok := `{"value":{"sessionId":"abc123","capabilities":{"platformName":"android"}}}`
	if got := browserstack.ClassifyResponse(200, []byte(ok)); got != "" {
		t.Fatalf("a session that really started is not a failure, got %q", got)
	}
	// …and a session id makes it a success even if the envelope carries other noise.
	noisy := `{"value":{"sessionId":"abc123"},"status":0,"sessionId":"abc123"}`
	if got := browserstack.ClassifyResponse(200, []byte(noisy)); got != "" {
		t.Fatalf("value.sessionId is the W3C verdict, got %q", got)
	}
}

func TestAFailureWithNoWordsWeKnowIsStillAFailure(t *testing.T) {
	// The general arm: no session id, and the envelope says something went wrong. Silence here is what let the
	// observed failure look like a success.
	for _, body := range []string{
		`{"value":{"error":"some new thing we have never seen"},"sessionId":"","status":13}`,
		`{"value":{"error":"whatever"}}`,
	} {
		if got := browserstack.ClassifyResponse(200, []byte(body)); got != browserstack.FailureCannotStart {
			t.Fatalf("a failed session must not be silent: %s -> %q", body, got)
		}
	}
}

// ── the serial is stripped from every set, no matter which one we write into ───────────────────────────────

// ⚠️ THE SHAPE THAT LEAKED, AND WHY NOTHING CAUGHT IT. TestTheVendorBlockIsWrittenInExactlyOnePlace uses this
// exact body — alwaysMatch present, firstMatch entries alongside — and passes, because its entries happen to
// carry no serial. The `else if` that fixed the duplicate-capability bug also stopped apply() from ever
// visiting a firstMatch entry, and apply() was where the serial was deleted. A LOCAL DEVICE'S SERIAL WENT TO
// BROWSERSTACK, and no test in this package could see it.
func TestTheLocalSerialNeverSurvivesAnywhereInTheBody(t *testing.T) {
	body := `{"capabilities":{"alwaysMatch":{"platformName":"Android"},"firstMatch":[
		{"appium:udid":"LOCAL-SERIAL-1"},{"appium:udid":"LOCAL-SERIAL-2","appium:automationName":"UiAutomator2"}]}}`

	merged, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	if strings.Contains(string(merged), "LOCAL-SERIAL") {
		t.Fatalf("a local device's serial travelled to a third party. The strip is UNCONDITIONAL — it is not "+
			"part of choosing where to write:\n%s", merged)
	}

	// AND EVERY SET, not just the one that happens to be inspected first.
	var root map[string]any
	_ = json.Unmarshal(merged, &root)
	capabilities := root["capabilities"].(map[string]any)
	for i, entry := range capabilities["firstMatch"].([]any) {
		if _, present := entry.(map[string]any)[browserstack.LocalSerialCapability]; present {
			t.Fatalf("firstMatch[%d] kept its serial", i)
		}
	}
	if _, present := capabilities["alwaysMatch"].(map[string]any)[browserstack.LocalSerialCapability]; present {
		t.Fatal("alwaysMatch kept its serial")
	}

	// …and what the body legitimately said is untouched.
	second := capabilities["firstMatch"].([]any)[1].(map[string]any)
	if second["appium:automationName"] != "UiAutomator2" {
		t.Fatal("stripping the serial must not take the client's own capabilities with it")
	}
}

// ── choosing a write target does not by itself prevent the collision ───────────────────────────────────────

// A client that already sends one of OUR keys in an alternative, plus anything at all in alwaysMatch, would end
// up with that key in both sets after the merge — `invalid argument` under W3C §7.2, which is the exact
// rejection the write-target choice exists to prevent. The rule only holds if the other side is cleared.
func TestAKeyWeWriteIsClearedFromEverySetWeDoNotWriteInto(t *testing.T) {
	for _, key := range []string{"bstack:options", "appium:deviceName", "appium:platformVersion", browserstack.AppCapability} {
		body := fmt.Sprintf(`{"capabilities":{"alwaysMatch":{"platformName":"Android"},"firstMatch":[{%q:%q}]}}`,
			key, "whatever-the-client-sent")
		merged, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec)
		if err != nil {
			t.Fatalf("%s: merging: %v", key, err)
		}
		var root map[string]any
		_ = json.Unmarshal(merged, &root)
		capabilities := root["capabilities"].(map[string]any)

		entry := capabilities["firstMatch"].([]any)[0].(map[string]any)
		if _, present := entry[key]; present {
			t.Fatalf("%s survives in a firstMatch alternative while we also wrote it into alwaysMatch — the same "+
				"extension capability in both is `invalid argument` and the hub refuses the whole request", key)
		}
		if _, present := capabilities["alwaysMatch"].(map[string]any)[key]; !present {
			t.Fatalf("%s must still be written into the target set", key)
		}
	}
}

// THE TWO LISTS MUST AGREE. ourKeys() is what gets cleared from non-target sets; apply() is what gets written.
// A fifth write with no entry in the list is a collision waiting for the body that carries it — and this seal
// is the reason that cannot happen quietly.
func TestEveryKeyTheMergeWritesIsAlsoAKeyItClears(t *testing.T) {
	// What a merge into a bare set actually produces IS the write list, discovered rather than typed.
	merged, err := browserstack.Merge([]byte(`{"capabilities":{"firstMatch":[{}]}}`), creds(), testApp, testSpec)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}
	var root map[string]any
	_ = json.Unmarshal(merged, &root)
	written := root["capabilities"].(map[string]any)["firstMatch"].([]any)[0].(map[string]any)

	for key := range written {
		// Prove the key is cleared from a non-target set by putting it in one and merging again.
		body := fmt.Sprintf(`{"capabilities":{"alwaysMatch":{"platformName":"Android"},"firstMatch":[{%q:"x"}]}}`, key)
		out, err := browserstack.Merge([]byte(body), creds(), testApp, testSpec)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		var again map[string]any
		_ = json.Unmarshal(out, &again)
		entry := again["capabilities"].(map[string]any)["firstMatch"].([]any)[0].(map[string]any)
		if _, present := entry[key]; present {
			t.Fatalf("the merge WRITES %q but does not CLEAR it from a non-target set — add it to ourKeys(), or "+
				"the first client that sends it collides with us and the hub refuses the request", key)
		}
	}
}

// TheObservedRejection is the second envelope this account has produced, captured 2026-09-01 with a corrupted
// access key against the real username. HTTP 401, no session started, no device-minute spent.
//
// RECORDED BYTES. This fixture contacts nothing.
const TheObservedRejection = `{"value":{"error":"Invalid username or password","message":"Invalid username or password"},"sessionId":"","status":13}`

func TestTheObservedRejectionIsClassifiedAsOne(t *testing.T) {
	if got := browserstack.ClassifyResponse(401, []byte(TheObservedRejection)); got != browserstack.FailureRejected {
		t.Fatalf("the rejected-credentials envelope must be named as one, got %q", got)
	}
}

// ⚠️ THE TEST THAT IS ACTUALLY WORTH HAVING, and the reason is that the live measurement did not test what it
// was for. The rejection arrived with HTTP 401, so ClassifyResponse's status arm returned before a single
// keyword was examined — the keyword arm was never reached by the real request.
//
// THIS HUB HAS ALREADY ANSWERED 200 TO A FAILURE ONCE. If a rejection ever arrives that way, the words are all
// there is, and nothing else in this package proves they carry it alone.
func TestTheSameRejectedBytesAreStillARejectionWithoutThe401(t *testing.T) {
	if got := browserstack.ClassifyResponse(200, []byte(TheObservedRejection)); got != browserstack.FailureRejected {
		t.Fatalf("the same bytes with HTTP 200 classified as %q — the status arm was doing all the work, so the "+
			"keyword arm has still never run, and a rejection delivered over 200 would be reported as the wrong "+
			"cause or as nothing at all", got)
	}
}

// AND THE FIELD THAT DISTINGUISHES NOTHING, asserted so it cannot quietly become a signal. Both envelopes carry
// "status":13 and an empty top-level sessionId; only the HTTP status and the words ever differ.
func TestTheLegacyStatusIsIdenticalAcrossEveryObservedFailure(t *testing.T) {
	for name, envelope := range map[string]string{
		"rejected credentials": TheObservedRejection,
		"cannot start":         TheObservedFailure,
	} {
		var decoded struct {
			SessionID string `json:"sessionId"`
			Status    *int   `json:"status"`
		}
		if err := json.Unmarshal([]byte(envelope), &decoded); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if decoded.Status == nil || *decoded.Status != 13 {
			t.Fatalf("%s: the recorded envelope no longer carries status 13 — if these ever differ, that IS a "+
				"discriminator and the classifier should be using it", name)
		}
		if decoded.SessionID != "" {
			t.Fatalf("%s: the recorded envelope grew a top-level session id", name)
		}
	}
	// The two DO differ in the only two places anything has ever varied.
	if browserstack.ClassifyResponse(401, []byte(TheObservedRejection)) ==
		browserstack.ClassifyResponse(200, []byte(TheObservedFailure)) {
		t.Fatal("the two observed failures must not classify the same — status and words are the only things " +
			"that have ever told them apart")
	}
}

// ── the merge writes WHAT WAS LEASED, and this is the seal injection 5 proved was missing ──────────────────
//
// The two hard-coded constants this slice deleted are gone from the source, but "gone from the source" is a
// claim about text. The property is that TWO DIFFERENT LEASES PRODUCE TWO DIFFERENT BODIES — which no single
// fixture can show, however carefully its values are chosen, because one value cannot distinguish a lookup from
// a constant that happens to equal it.
func TestTwoDifferentLeasesProduceTwoDifferentSessions(t *testing.T) {
	first := browserstack.DeviceSpec{DeviceName: "Google Pixel 9", PlatformVersion: "15.0"}
	second := browserstack.DeviceSpec{DeviceName: "Samsung Galaxy S23 Ultra", PlatformVersion: "13.0"}

	read := func(spec browserstack.DeviceSpec) (string, string) {
		merged, err := browserstack.Merge([]byte(TheRealBody), creds(), testApp, spec)
		if err != nil {
			t.Fatalf("merging %v: %v", spec, err)
		}
		var root map[string]any
		if err := json.Unmarshal(merged, &root); err != nil {
			t.Fatalf("merged body is not JSON: %v", err)
		}
		set := root["capabilities"].(map[string]any)["firstMatch"].([]any)[0].(map[string]any)
		name, _ := set["appium:deviceName"].(string)
		version, _ := set["appium:platformVersion"].(string)
		return name, version
	}

	firstName, firstVersion := read(first)
	secondName, secondVersion := read(second)

	if firstName != first.DeviceName || firstVersion != first.PlatformVersion {
		t.Fatalf("the first lease's device did not reach the body: got %q/%q", firstName, firstVersion)
	}
	if secondName != second.DeviceName || secondVersion != second.PlatformVersion {
		t.Fatalf("the second lease's device did not reach the body: got %q/%q", secondName, secondVersion)
	}
	if firstName == secondName || firstVersion == secondVersion {
		t.Fatalf("both leases produced the same device (%q/%q) — the merge is not reading the lease at all, "+
			"which is exactly what a hard-coded pair looks like from the outside", firstName, firstVersion)
	}
}
