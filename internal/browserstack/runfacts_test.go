package browserstack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// ── THREE_FACTS_THAT_ONLY_THE_AGENT_CAN_SPELL — the agent half ─────────────────────────────────────────────
//
// The platform states three facts in its own words and cannot act on any of them: which run a session is, which
// way up the device should start, and which country the session should appear to connect from. The last one
// STOPPED THE PRODUCT — the operator's own app answered "This app is not allowed in your country." twice in a
// row, and nothing in the platform can change the country of an IP address.
//
// This is where the two vocabularies meet, and the seals below hold both halves of that: the translation
// happens, and the platform's own words do not survive it.

const (
	theRunID = "7c9f0b12-3d4e-4a5b-8c6d-9e0f1a2b3c4d"
	theApp   = "bs://9f8e7d6c5b4a"
)

func mergedOptions(t *testing.T, body string) map[string]any {
	t.Helper()
	merged, err := browserstack.Merge([]byte(body), browserstack.Credentials{User: "u", Key: "k"}, theApp, theSpec())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var root struct {
		Capabilities struct {
			AlwaysMatch map[string]any   `json:"alwaysMatch"`
			FirstMatch  []map[string]any `json:"firstMatch"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(merged, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	set := root.Capabilities.AlwaysMatch
	if set == nil && len(root.Capabilities.FirstMatch) > 0 {
		set = root.Capabilities.FirstMatch[0]
	}
	options, _ := set["bstack:options"].(map[string]any)
	if options == nil {
		t.Fatalf("no bstack:options in the merged body: %s", merged)
	}
	return options
}

func theSpec() browserstack.DeviceSpec {
	return browserstack.DeviceSpec{DeviceName: "Google Pixel 9", PlatformVersion: "15.0"}
}

// bodyWithFacts is the shape the .NET client really posts — the block at the top level of a firstMatch entry
// (measured 2026-09-02, not assumed).
func bodyWithFacts(facts string) string {
	return `{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",` +
		`"appium:udid":"browserstack:Google Pixel 9|15.0"` + facts + `}]}}`
}

// ── the translation ────────────────────────────────────────────────────────────────────────────────────────

func TestTheThreeFactsAreTranslatedIntoThisVendorsNames(t *testing.T) {
	options := mergedOptions(t, bodyWithFacts(
		`,"qanguru:run":{"runId":"`+theRunID+`","orientation":"LANDSCAPE","country":"SE"}`))

	if options["sessionName"] != theRunID {
		t.Fatalf("the run's identity must become the session's name, got %v", options["sessionName"])
	}
	if options["deviceOrientation"] != "LANDSCAPE" {
		t.Fatalf("which way up must become deviceOrientation, got %v", options["deviceOrientation"])
	}
	// ⚠️ THE ONE THAT STOPPED THE PRODUCT. `geoLocation` is this vendor's IP country, which is what a licence
	// check reads — NOT a GPS fix.
	if options["geoLocation"] != "SE" {
		t.Fatalf("the country must become geoLocation, got %v", options["geoLocation"])
	}
	// …and the ONE name the agent fills from what it already holds.
	if options["buildName"] != theApp {
		t.Fatalf("the build is the id THEY configured on THEIR account — already on that dashboard: %v", options["buildName"])
	}
	// ⚠️ AND NO projectName. See TestNoCapabilityTheAgentWritesCarriesAProductName: this platform has no name yet,
	// and the dashboard's "Untitled Project" is the honest answer until it does.
	if _, present := options["projectName"]; present {
		t.Fatalf("a project name was written: %v", options["projectName"])
	}
}

// ── the removal, which is the fourth clause of the same rule ───────────────────────────────────────────────

func TestThePlatformsOwnBlockNeverREACHESTheHub(t *testing.T) {
	// An extension capability the hub does not recognise is a capability the hub may refuse, and this agent must
	// not turn a session-create that works today into one that can be rejected. The platform's words are for us.
	merged, err := browserstack.Merge([]byte(bodyWithFacts(
		`,"qanguru:run":{"runId":"`+theRunID+`","country":"SE"}`)),
		browserstack.Credentials{User: "u", Key: "k"}, theApp, theSpec())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if strings.Contains(string(merged), "qanguru") {
		t.Fatalf("the platform's own block survived into the outbound body: %s", merged)
	}
	// The instrument guard: the fact still crossed, so this is not passing because nothing happened.
	if !strings.Contains(string(merged), `"geoLocation":"SE"`) {
		t.Fatalf("the country was stripped along with the block: %s", merged)
	}
}

func TestTheBlockIsStrippedFromEVERYSetIncludingOnesWeDoNotWriteInto(t *testing.T) {
	// Unconditional, exactly like the local serial, and for the same shape of reason. A body with an alwaysMatch
	// makes every firstMatch entry a NON-target; a strip that lived inside the conditional write would leave the
	// block in the alternatives — which is precisely how a local serial once reached a third party.
	body := `{"capabilities":{"alwaysMatch":{"platformName":"Android","qanguru:run":{"runId":"` + theRunID + `"}},` +
		`"firstMatch":[{"qanguru:run":{"runId":"other","country":"GB"}},{"appium:automationName":"UiAutomator2"}]}}`

	merged, err := browserstack.Merge([]byte(body), browserstack.Credentials{User: "u", Key: "k"}, theApp, theSpec())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if strings.Contains(string(merged), "qanguru") {
		t.Fatalf("a non-target set kept the platform's block: %s", merged)
	}
}

func TestTheBlockIsREADFromWhicheverSetCarriesItNotOnlyTheTarget(t *testing.T) {
	// The client puts it in a firstMatch entry; a body with an alwaysMatch would carry it there instead. Reading
	// only the write target would have worked for exactly one of those two shapes.
	body := `{"capabilities":{"alwaysMatch":{"platformName":"Android"},` +
		`"firstMatch":[{"qanguru:run":{"runId":"` + theRunID + `","country":"SE"}}]}}`

	options := mergedOptions(t, body)
	if options["sessionName"] != theRunID || options["geoLocation"] != "SE" {
		t.Fatalf("a block in a non-target set must still be read: %v", options)
	}
}

// ── the agent invents nothing ──────────────────────────────────────────────────────────────────────────────

func TestAStatedNothingStaysUntitledAndUnrouted(t *testing.T) {
	// ABSENT MEANS ABSENT. No fabricated run id, no default country, no orientation nobody asked for — a session
	// with no stated identity stays "Untitled" on the dashboard exactly as it does today.
	options := mergedOptions(t, bodyWithFacts(""))

	for _, name := range []string{"sessionName", "deviceOrientation", "geoLocation", "buildName"} {
		if _, present := options[name]; present {
			t.Fatalf("%s was written for a run that stated nothing: %v", name, options[name])
		}
	}
	// …and the credentials and idle timeout are untouched, which is what this body always carried.
	if options["userName"] != "u" || options["idleTimeout"] == nil {
		t.Fatalf("the merge's own keys must be unchanged: %v", options)
	}
}

func TestEachFactIsOptionalOnItsOwn(t *testing.T) {
	only := mergedOptions(t, bodyWithFacts(`,"qanguru:run":{"runId":"`+theRunID+`"}`))
	if only["sessionName"] != theRunID {
		t.Fatalf("the identity crossed: %v", only)
	}
	if _, present := only["geoLocation"]; present {
		t.Fatal("a country nobody stated must not be invented — there is no safe default for where a customer's traffic comes from")
	}
	if _, present := only["deviceOrientation"]; present {
		t.Fatal("an orientation nobody stated must not be invented")
	}
}

// ── the two facts that share a word ────────────────────────────────────────────────────────────────────────

func TestAPointShapedGeolocationIsNotACountryAndCannotBecomeOne(t *testing.T) {
	// ⚠️ THE CONFUSION THE WHOLE SLICE IS BUILT TO PREVENT. `geoLocation` here is an IP COUNTRY — a two-letter
	// code. A GPS fix is a point, it is applied with `mobile: setGeolocation`, and it is a different fact that
	// happens to share a word. A point that reached this key would be a coordinate posted where a country goes.
	options := mergedOptions(t, bodyWithFacts(
		`,"qanguru:run":{"runId":"`+theRunID+`","country":"SE","latitude":59.33,"longitude":18.06}`))

	if options["geoLocation"] != "SE" {
		t.Fatalf("the country is the country: %v", options["geoLocation"])
	}
	for name, value := range options {
		text := strings.TrimSpace(strings.ToLower(toText(value)))
		if strings.Contains(text, "59.33") || strings.Contains(text, "18.06") {
			t.Fatalf("a GPS coordinate reached %s — the platform's own field carries a country for exactly this reason", name)
		}
	}
}

func toText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ── ⚠️ NO CAPABILITY THIS AGENT WRITES MAY CARRY A HARD-CODED PRODUCT NAME ─────────────────────────────────
//
// THE REASON, STATED WHERE THE SEAL LIVES: a name we have not chosen cannot be published on somebody else's
// screen. This platform's own name has not been settled; the name an earlier version of `applyRunFacts` wrote
// into `projectName` belonged to a SEPARATE crowdtesting platform that merely consumes this one. Every
// capability here lands on the customer's BrowserStack dashboard, where it is read by them and by anyone they
// show it to, so a placeholder is not a neutral choice — it is a claim about who we are, made by whoever last
// edited a constant.
//
// What the agent may write is a MEASURED FACT about this run: `sessionName` is the run's own identifier and
// `buildName` is the app id the customer configured on their own account. Neither says anything about us.
//
// When the platform is named, whoever names it decides whether `projectName` belongs here at all — the line is
// on the rename slice's list for exactly that reason, not as a reminder to fill this in.

// productNames are the names in this workspace that must never be hard-coded into a capability. `browserstack`
// is deliberately NOT here: it is the VENDOR's name, it is theirs, and this file's whole job is to speak it.
// The private repositories' names are in that list too, held as sums so this published file does not spell them
// (farmtest.PrivateRepositoryIn): carriesAProductName reads both.
var productNames = []string{"qanguru",
	// The new names too — a leak of either brand is a leak. Never a bare "nth": it is inside "month".
	"nthcase", "nth-agent"}

// carriesAProductName is the product name v carries, or "".
func carriesAProductName(v string) string {
	for _, name := range productNames {
		if strings.Contains(strings.ToLower(v), name) {
			return name
		}
	}
	if what, _ := farmtest.PrivateRepositoryIn(v); what != "" {
		return what
	}
	return ""
}

// stringValues walks every string in a decoded JSON body — keys are ours by construction, values are what we
// chose to send.
func stringValues(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case map[string]any:
		for _, inner := range t {
			stringValues(inner, out)
		}
	case []any:
		for _, inner := range t {
			stringValues(inner, out)
		}
	}
}

func TestNoCapabilityTheAgentWritesCarriesAProductName(t *testing.T) {
	// The inputs are deliberately free of our names, so anything found is something the AGENT put there.
	merged, err := browserstack.Merge(
		[]byte(`{"capabilities":{"firstMatch":[{"platformName":"Android","appium:automationName":"UiAutomator2",`+
			`"qanguru:run":{"runId":"`+theRunID+`","orientation":"PORTRAIT","country":"SE"}}]}}`),
		browserstack.Credentials{User: "u", Key: "k"}, theApp, theSpec())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	var root any
	if err := json.Unmarshal(merged, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var values []string
	stringValues(root, &values)

	// INSTRUMENT GUARD: the walk must actually be finding the values we send, or it would pass on a body full of
	// them. The build id and the run id are both in there and neither is a product name.
	if len(values) < 5 {
		t.Fatalf("the walk found only %d string values — it is reading the wrong thing: %v", len(values), values)
	}
	found := false
	for _, v := range values {
		if v == theApp {
			found = true
		}
	}
	if !found {
		t.Fatalf("instrument guard: the walk did not find the build id it is supposed to be inspecting: %v", values)
	}

	for _, v := range values {
		if name := carriesAProductName(v); name != "" {
			t.Fatalf("a capability carries the hard-coded product name %q (%q). A name we have not chosen "+
				"cannot be published on somebody else's screen — see the comment above this seal.", name, v)
		}
	}
}

func TestTheProductNameSweepWouldCatchOne(t *testing.T) {
	// THE VACUITY DIRECTION. Without this, the seal above passes on a merge that writes a name into every key.
	var values []string
	stringValues(map[string]any{"bstack:options": map[string]any{"projectName": "NthCase"}}, &values)

	caught := false
	for _, v := range values {
		if carriesAProductName(v) != "" {
			caught = true
		}
	}
	if !caught {
		t.Fatal("the sweep cannot see a product name it is handed, so its green means nothing")
	}
}
