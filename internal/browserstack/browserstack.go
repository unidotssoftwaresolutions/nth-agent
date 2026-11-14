// Package browserstack carries the one thing the walking skeleton has to prove: that the tunnel's forward()
// can create a REAL BrowserStack session with credentials the platform never holds.
//
// THE PROPERTY THIS EXISTS TO PRESERVE. forward() builds its outbound request itself and lets only
// Content-Type cross from the platform's frame. The platform sends a method, a path, a body and a content
// type; it cannot inject a header and it will never hold a key. Everything here runs on the agent's side of
// that line, and the credential is read from this machine's own flags or environment — so there is no string
// containing a secret for the farm to store or log. That is the product, not a precaution.
package browserstack

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// HubURL is BrowserStack's plain App Automate hub. MEASURED FROM THEIR DOCUMENTATION, 2026-09-01: the
// credentials ride in the capabilities, not in the URL. The user:key@host basic-auth form has historically
// worked but is not what they document today, and building on an undocumented behaviour they could remove is
// not worth the saving (the design measured it so).
const HubURL = "https://hub-cloud.browserstack.com/wd/hub"

// THE HARD-CODED PAIR IS GONE, AND THE LEASE DECIDES.
//
// It used to be two constants with a docblock saying they were a placeholder "until the registry slice decides".
// This is that slice: the customer names the devices they want offered, the farm holds a row per spec, and the
// merge below writes WHATEVER WAS LEASED. A constant here would now be a second opinion about a question the
// lease already answers — and the two would disagree the first time a customer offered more than one device.
//
// The spec arrives the way every other per-device fact already does: the platform puts the leased device's
// identifier into `appium:udid`, which for a cloud row is a spec rather than a serial. No new channel, no farm
// contract change — see MergeSpecFrom.

// AppCapability is WHERE THE BUILD GOES in the raw W3C body this agent sends.
//
// MEASURED 2026-09-01, against the real hub. `appium:app` at the TOP LEVEL of the capability set — beside
// appium:deviceName and appium:platformVersion — created a session from an uploaded build on
// Google Pixel 9 / 15.0, which was then driven and ended.
//
// NO A/B PROBE WAS NEEDED, and that is worth recording because it explains why only one placement was ever
// tried. The failure this constant was written to fix — "Platform and Browser not valid." — had two candidate
// causes, and the device was eliminated first, for free, from the account's own device list. That left the
// build as the only remaining cause, so the run that proved THE PRODUCT also proved THE PLACEMENT: a session
// that starts is a session whose `app` was read.
//
// The reasoning that suggested it still holds and is why the measurement was expected to come out this way:
// `app` is not a standard W3C capability, and an extension capability with no prefix is not valid W3C at all,
// so it belongs in the same namespace as every other Appium capability the client already sends. The
// alternative — `app` inside bstack:options — was never sent.
const AppCapability = "appium:app"

// IdleTimeoutSeconds is what this agent ASKS BrowserStack for. ⚠️ IT IS NOT WHAT ENDS AN ABANDONED SESSION,
// and the sentence that used to stand here said it was.
//
// THE CORRECTED CLAIM, MEASURED 2026-09-03 (field test item 4, the vendor's own session log as the record). A
// cloud session whose worker was SIGKILLed mid-run was torn down 121.6 s after our last answered command, with
// `[BROWSERSTACK_IDLE_TIMEOUT] … (default: 90 secs)`. Three things follow, none of them the old comment:
//
//   - it did not fire at 60 s, so this value did not govern;
//   - the vendor's message quoted their DEFAULT back at a session that carried 60 — which is the vendor saying
//     our value was not in force. Their App Automate documentation describes this capability only as one you
//     INCREASE ("you can increase the timeout to a maximum of 300 seconds"); asking for less than the default
//     is not a documented request, and this measurement is consistent with it being ignored;
//   - it did not fire at 90 s either. The only number within a minute of 121.6 s is the platform's own
//     `appium:newCommandTimeout: 120`, and Appium documents that capability as ending the session.
//
// SO WHY IS IT STILL SENT? Because it costs nothing, it is within the vendor's documented range, and if a lower
// value is ever honoured it can only help. What changed is the claim: this is a REQUEST, the mechanism that
// actually ends an abandoned session is the agent's own release (see tunnel/release.go), and the seal over this
// constant asserts the range it is documented for rather than an effect it was never measured to have.
const IdleTimeoutSeconds = 60

// IdleTimeoutMaxSeconds is the vendor's documented ceiling for the capability above ("a maximum of 300
// seconds") — the one property of this number that is theirs to define and ours to stay inside.
const IdleTimeoutMaxSeconds = 300

// Credentials are the customer's, resolved on the customer's own machine and nowhere else.
//
// THE KEY IS NEVER PRINTED. It is not in the String of anything, it does not appear in a refusal, and
// TestEveryReadOfTheAccessKeyIsClassified walks the tree to keep that true of files rather than of call sites.
type Credentials struct {
	User string
	Key  string
}

// Configured reports whether BOTH halves are present. One without the other is a mistake the caller names; it
// must never be treated as "configured" and never as "absent", because both readings hide a typo.
func (c Credentials) Configured() bool { return c.User != "" && c.Key != "" }

// Partial reports the half-configured mistake — exactly one of the two supplied.
func (c Credentials) Partial() bool { return (c.User == "") != (c.Key == "") }

// MissingApp is the THIRD question, and it is deliberately not folded into the two above.
//
// Configured() answers "may we drive BrowserStack at all" and Partial() answers "did someone typo one half".
// This answers "can a session that reaches the hub possibly start", which is a different fact with a different
// remedy — and a boolean already carrying two facts is the shape this programme keeps taking apart.
func MissingApp(creds Credentials, app string) bool {
	return creds.Configured() && strings.TrimSpace(app) == ""
}

// MissingAppMessage says the WHOLE truth rather than "app is required".
//
// THE FAILURE THIS REPLACES. On 2026-09-01 a session with no build reached the hub and came back with
// "Platform and Browser not valid." A person reading that goes and checks their platform and their browser;
// both were fine. The thing actually missing was a build, which that sentence does not mention — and the agent,
// which KNEW it had sent no app, said nothing at all. One label over two facts, and a reader sent to look at
// the thing that is not wrong.
//
// A person who reads this one knows the next command to type.
const MissingAppMessage = "BrowserStack runs its own devices, so it needs the build in ITS storage before a " +
	"session can start: a session without an `app` capability is refused by the hub with a message about " +
	"platforms and browsers that names nothing about a build. Supply the app id with --browserstack-app (or " +
	"NTH_AGENT_BROWSERSTACK_APP). It is the bs://… id an upload returns, or a custom_id you gave one; this agent " +
	"can perform that upload, because it is the only side of this that holds your credentials."

// ErrUnrecognisedBody is the refusal for a session body whose capability shape this agent does not know.
//
// REFUSING BEATS FORWARDING, and this is the slice's sharpest decision. A body we cannot merge into would go up
// WITHOUT credentials and come back as an authentication failure — which reads exactly like the customer
// mistyping their own key, and would send them to look at the one thing that is not wrong. The prompt names
// that as the worst failure this slice could ship. A refusal naming the shape is strictly better than an error
// naming the wrong cause.
var ErrUnrecognisedBody = errors.New("the session request did not carry a W3C capability set this agent recognises")

// ErrNoApp is the refusal for a BrowserStack session with no build to run.
var ErrNoApp = errors.New("browserstack: no build was supplied")

// ErrNoDeviceSpec is the refusal for a session whose lease named no cloud device.
var ErrNoDeviceSpec = errors.New(
	"browserstack: the lease named no cloud device, so there is nothing to ask BrowserStack for — the farm sends " +
		"the leased device's identifier in `appium:udid`, and for a cloud row that is a `browserstack:name|version` spec")

// MergeSpecFrom reads the leased device's spec out of the session body the platform built.
//
// THE CHANNEL ALREADY EXISTED. The farm puts the leased device's identifier on the lease; the runner copies it into
// `appium:udid`; that body reaches this agent verbatim. For a local row that identifier is a serial and this
// returns false, leaving everything alone. For a cloud row it is the spec the customer configured — so the merge
// learns WHICH device was leased without one new field on the wire.
//
// The value is then removed from the capability set by the rule that removes a local serial: BrowserStack has no
// udid, and a spec is not one either.
func MergeSpecFrom(body []byte) (DeviceSpec, bool) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return DeviceSpec{}, false
	}
	capabilities, ok := root["capabilities"].(map[string]any)
	if !ok {
		return DeviceSpec{}, false
	}
	sets, _ := capabilitySets(capabilities)
	for _, set := range sets {
		if serial, ok := set[LocalSerialCapability].(string); ok {
			if spec, isSpec := ParseWire(serial); isSpec {
				return spec, true
			}
		}
	}
	return DeviceSpec{}, false
}

// IsSessionCreate answers item 0(c): which of the replayed frames is the session.
//
// MEASURED, NOT ASSUMED (see the report's item 0(a)): the Appium .NET client this platform uses sends
// `POST /session`. The agent appends the frame's path to the hub, so with the hub's /wd/hub base the target
// becomes .../wd/hub/session — which is why the suffix, not the whole path, is the test.
//
// THE NEAR MISSES ARE THE POINT. Every other Appium call is POST /session/{id}/... — an element click, an
// execute, a screenshot — and none of them ends in /session. Merging into one of those would rewrite a command
// body mid-session, which is the injection this function's shape exists to make impossible.
func IsSessionCreate(method, path string) bool {
	if !strings.EqualFold(method, http.MethodPost) {
		return false
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimRight(path, "/")
	return path == "/session" || strings.HasSuffix(path, "/session")
}

// Merge returns the session-creation body with the customer's credentials and the skeleton's device spec
// folded into every capability set the body offers.
//
// THE SHAPE, MEASURED FROM OUR OWN TRAFFIC. Appium.WebDriver 8.2.0 over Selenium 4.36 sends W3C only, and it
// puts the capabilities in `capabilities.firstMatch[0]` — NOT in `alwaysMatch`, which the design note and the
// slice prompt both assumed. A merge written against alwaysMatch would have produced a body the hub accepts and
// cannot authenticate. Both are handled because either is legal W3C and the client is free to change.
//
// THE CREDENTIAL NAMES, MEASURED FROM THEIR DOCUMENTATION. Under W3C the credentials go inside the vendor block
// `bstack:options` as userName/accessKey. The `browserstack.user` / `browserstack.key` pair is the LEGACY
// (JSONWP) spelling; in a W3C body a bare dotted name is not a valid extension capability at all, so writing it
// there risks the whole session being rejected for an invalid capability. One dialect, one spelling.
func Merge(body []byte, creds Credentials, app string, spec DeviceSpec) ([]byte, error) {
	if !creds.Configured() {
		return nil, errors.New("browserstack: refusing to merge without both a user and a key")
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnrecognisedBody, err)
	}

	capabilities, ok := root["capabilities"].(map[string]any)
	if !ok {
		return nil, ErrUnrecognisedBody
	}

	// ⚠️ THE SHAPE IS CHECKED FIRST, AND THE ORDER IS A DECISION. These two refusals used to run before the body
	// was even parsed, so a body this agent could not read at all was reported as "the lease named no device" —
	// a true sentence about a body whose real problem was that nobody could read it, which is the wrong-cause
	// defect this package keeps removing. First "can I read this?", then "does it have what it needs?".
	if spec.DeviceName == "" || spec.PlatformVersion == "" {
		// REFUSING BEATS GUESSING, and there is nothing left to guess WITH: the constants that used to stand in
		// here are gone. A session naming no device is one the hub refuses with a sentence about platforms and
		// browsers, so it never leaves the machine.
		return nil, ErrNoDeviceSpec
	}
	if MissingApp(creds, app) {
		// REFUSING BEATS FORWARDING, for the same reason. A session with no build is not one that might work.
		return nil, fmt.Errorf("%w: %s", ErrNoApp, MissingAppMessage)
	}

	// ── THE RULE, AND IT IS ONE RULE WITH THREE PARTS ──────────────────────────────────────────────────────
	//
	// 1. THE LOCAL SERIAL IS STRIPPED FROM EVERY SET, UNCONDITIONALLY. `appium:udid` is how the platform tells
	//    two LOCAL phones apart; BrowserStack has no udid, so asking for one selects nothing — and a local
	//    device's serial has no business travelling to a third party. This obligation is not conditional on
	//    anything, and that is the whole lesson of the defect below.
	//
	// 2. OUR KEYS ARE WRITTEN INTO THE TARGET SETS. With an `alwaysMatch` the target is that one set, because it
	//    applies to every alternative. Without one, every `firstMatch` entry is a target, because they are
	//    ALTERNATIVES and the hub picks one — the one it picks must not be the unauthenticated sibling.
	//
	// 3. EVERY NON-TARGET SET IS CLEARED OF THE KEYS WE WRITE. W3C §7.2 makes the same extension capability in
	//    `alwaysMatch` AND a `firstMatch` entry `invalid argument`, so choosing a write target is only half the
	//    job: a client that already sent `appium:deviceName` in an alternative would collide with us and the hub
	//    would refuse the whole request. Otherwise the rule holds only for bodies we happen to have seen.
	//
	// ⚠️ WHY PARTS 1 AND 2 ARE SEPARATE STATEMENTS. They used to be one function called under one condition. When
	// the write target became conditional (an `else if`, fixing the duplicate above), the STRIP silently became
	// conditional too — so a body carrying `alwaysMatch` never had its `firstMatch` entries touched and the
	// serial went to BrowserStack. The duplicate bug announced itself in one second with a rejected session; this
	// one announced itself to nobody, ever. Only the write may be conditional.
	sets, targets := capabilitySets(capabilities)
	if len(sets) == 0 {
		return nil, ErrUnrecognisedBody
	}
	// (4) READ BEFORE THE STRIP. The platform's neutral block is the only thing here that has to be READ rather
	// than written, so it is taken from whichever set carries it — the client puts it in a `firstMatch` entry
	// today, and a body with an `alwaysMatch` would carry it there instead. Reading only the target set would
	// have worked for exactly one of those shapes.
	facts := readRunFacts(sets)
	for i, set := range sets {
		delete(set, LocalSerialCapability)   // (1) — every set, no condition
		delete(set, PlatformFactsCapability) // (4) — every set, no condition, for its own reason
		if !targets[i] {
			for _, key := range ourKeys() { // (3)
				delete(set, key)
			}
		}
	}
	for i, set := range sets {
		if targets[i] {
			apply(set, creds, app, spec, facts) // (2)
		}
	}

	return json.Marshal(root)
}

// PlatformFactsCapability is the platform's own vendor-neutral block: which run this is, which way up the device
// should start, and which country the session should appear to connect from.
//
// ⚠️ IT IS TRANSLATED HERE AND THEN REMOVED, and the removal is the FOURTH CLAUSE OF THE SAME RULE the three
// above state — not a new mechanism. It is stripped from EVERY set, unconditionally, for the same shape of reason
// the serial is: an extension capability the hub does not recognise is a capability the hub may refuse, and this
// agent must not turn a session-create that works today into one that can be rejected. The platform's words are
// for us; only the vendor's words may leave.
const PlatformFactsCapability = "qanguru:run"

// The three facts, in the platform's vocabulary. This is the ONLY place the two vocabularies are written side by
// side — which is what makes the seam real rather than decorative.
const (
	factRunID       = "runId"
	factOrientation = "orientation"
	factCountry     = "country"
)

// readRunFacts returns the platform's block from the first set that carries one, or nil.
func readRunFacts(sets []map[string]any) map[string]any {
	for _, set := range sets {
		if facts, ok := set[PlatformFactsCapability].(map[string]any); ok {
			return facts
		}
	}
	return nil
}

// factString reads one stated fact. ABSENT MEANS ABSENT: a fact nobody stated returns "" and is never written,
// so there is no default country, no invented orientation, and a session with no stated identity stays
// "Untitled" exactly as it does today.
func factString(facts map[string]any, key string) string {
	if facts == nil {
		return ""
	}
	value, _ := facts[key].(string)
	return strings.TrimSpace(value)
}

// applyRunFacts is the translation, and it is the whole of what this agent knows about this vendor's names.
//
// THE PLATFORM SAYS WHAT IT WANTS; THIS SAYS WHERE IT GOES. `runId` → `sessionName`, `orientation` →
// `deviceOrientation`, `country` → `geoLocation` (which is the IP country the vendor routes the session
// through — NOT a GPS fix, which is `mobile: setGeolocation` and a different fact entirely; the platform's own
// field carries that name for exactly that reason).
//
// ONE NAME THE AGENT FILLS FROM WHAT IT ALREADY HOLDS: `buildName` is the app id THEY configured on THEIR
// account, so it is already on that dashboard and no new information crosses. It also groups sessions the way a
// person actually wants them grouped — by the build under test — and it is a MEASURED FACT about this run, not a
// claim about who we are.
//
// ⚠️ NO `projectName`, AND THE ABSENCE IS THE DECISION. An earlier version of this function wrote a hard-coded
// product name here, which was wrong twice over: that name belongs to a separate crowdtesting platform that
// merely consumes this one, and this platform's own name HAS NOT BEEN CHOSEN YET. Absent leaves "Untitled
// Project", which is what the dashboard said before any of this, discloses nothing, and is the only honest
// answer while there is no name to give. **A name we have not chosen cannot be published on somebody else's
// screen.** When the platform is named, whoever names it decides whether it belongs here at all — see the rename
// slice's list.
func applyRunFacts(options map[string]any, facts map[string]any, app string) {
	if runID := factString(facts, factRunID); runID != "" {
		options["sessionName"] = runID
	}
	if orientation := factString(facts, factOrientation); orientation != "" {
		options["deviceOrientation"] = orientation
	}
	if country := factString(facts, factCountry); country != "" {
		options["geoLocation"] = country
	}
	if build := strings.TrimSpace(app); build != "" && facts != nil {
		options["buildName"] = build
	}
}

// apply folds the credentials and the device spec into one capability set, preserving whatever else is there.
func apply(set map[string]any, creds Credentials, app string, spec DeviceSpec, facts map[string]any) {
	options, ok := set["bstack:options"].(map[string]any)
	if !ok {
		options = map[string]any{}
	}
	options["userName"] = creds.User
	options["accessKey"] = creds.Key
	options["idleTimeout"] = IdleTimeoutSeconds
	applyRunFacts(options, facts, app)
	set["bstack:options"] = options

	// THE DEVICE IS A SPEC HERE. Removing the serial is NOT done from this function any more — see the three-part
	// rule in Merge: a strip that lives inside a conditional write becomes conditional the moment the write does.
	set["appium:deviceName"] = spec.DeviceName
	set["appium:platformVersion"] = spec.PlatformVersion
	applyApp(set, app)
}

// LocalSerialCapability is what the platform sends to tell two LOCAL phones apart, and the one thing that is
// removed from every capability set no matter what else happens.
const LocalSerialCapability = "appium:udid"

// ourKeys are every capability this agent writes. A set that is not a write target is cleared of exactly these,
// so we can never collide with a key the client already sent.
//
// DERIVED FROM THE WRITES, not typed beside them: apply() sets four things, and this list is the same four. A
// fifth write with no entry here is a collision waiting for the body that carries it, which is why the seal
// checks that this list and apply's writes agree.
func ourKeys() []string {
	return []string{"bstack:options", "appium:deviceName", "appium:platformVersion", AppCapability}
}

// capabilitySets returns every capability set in the body, and which of them this agent writes into.
//
// THE ORDER IS alwaysMatch FIRST when it exists, because it is the target: the set that applies to every
// alternative authenticates all of them at once.
// CapabilitySets is the same W3C navigation, exported for ONE reason: the tunnel has to read a capability out
// of a create body (the session's own newCommandTimeout, which bounds how long it may go quiet), and reading it
// out of a shape learned separately is how two readers of one body drift apart. This package already had to
// measure that shape — Appium.WebDriver 8.2.0 puts the capabilities in `capabilities.firstMatch[0]`, not
// `alwaysMatch` — so the tunnel borrows the measurement rather than repeating it.
func CapabilitySets(body []byte) []map[string]any {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	capabilities, ok := root["capabilities"].(map[string]any)
	if !ok {
		return nil
	}
	sets, _ := capabilitySets(capabilities)
	return sets
}

func capabilitySets(capabilities map[string]any) (sets []map[string]any, targets []bool) {
	always, hasAlways := capabilities["alwaysMatch"].(map[string]any)
	if hasAlways {
		sets = append(sets, always)
		targets = append(targets, true)
	}
	if first, ok := capabilities["firstMatch"].([]any); ok {
		for _, entry := range first {
			if set, ok := entry.(map[string]any); ok {
				sets = append(sets, set)
				// A firstMatch entry is a target only when there is no alwaysMatch to carry the keys for it.
				targets = append(targets, !hasAlways)
			}
		}
	}
	return sets, targets
}

// applyApp puts the build where the hub reads it, for EVERY capability set the merge touches — the same
// reasoning as the credentials: a firstMatch alternative the hub is free to pick must not be the one with no
// build.
//
// THE PLACEMENT IS ONE LINE ON PURPOSE, because it is the one thing here that had to be MEASURED rather than
// reasoned about. AppCapability records what licensed it.
func applyApp(set map[string]any, app string) {
	if strings.TrimSpace(app) == "" {
		return
	}
	set[AppCapability] = app
}

// Failure names the three things that go wrong, because one sentence for all three would send a customer to
// look at the wrong one — the defect this programme has removed from the run pipeline twice.
type Failure string

const (
	// FailureUnreachable — the tunnel could not reach the hub at all. This is OUR side: DNS, egress, a proxy.
	FailureUnreachable Failure = "unreachable"
	// FailureRejected — the hub answered and refused the credentials. OBSERVED 2026-09-01: HTTP 401 with
	// {"value":{"error":"Invalid username or password",…},"sessionId":"","status":13}.
	//
	// ⚠️ THE ARM THAT DECIDED WAS NOT THE ARM THE MEASUREMENT EXISTED TO VALIDATE. That envelope arrived with
	// HTTP 401, so ClassifyResponse's status arm returned before a single keyword was examined — the keyword arm
	// ("invalid username or password") was never reached by the live request. It passes today because the text
	// happens to match bytes we have since read, not because anything exercised it. An arm that has never run is
	// not an arm that works, which is why a fixture classifies those same bytes from HTTP 200: this hub has
	// already answered 200 to a failure once, and if a rejection ever arrives that way the words are all there is.
	FailureRejected Failure = "rejected"
	// FailureNoDevice — authenticated, but the account cannot give us this device right now: no such device, or
	// no free parallel slot on the plan.
	FailureNoDevice Failure = "no-device"
	// FailureCannotStart — OBSERVED ON THE WIRE, 2026-09-01. The hub authenticated us and then could not decide
	// what to run. Its own sentence blames the platform and the browser; at least two different mistakes produce
	// it, and the wire does not say which.
	FailureCannotStart Failure = "cannot-start"
)

// Describe turns a hub outcome into the sentence a person reads. NONE OF THE THREE CONTAINS THE KEY, and none
// of them contains the merged body: a debug print of "the capabilities we sent" is the obvious next thing
// somebody adds, and it would put the key in a log file.
func (f Failure) Describe() string {
	switch f {
	case FailureUnreachable:
		return "could not reach BrowserStack at all — this machine could not open a connection to the hub, so nothing was attempted on the account"
	case FailureRejected:
		return "BrowserStack refused the credentials — check the username and access key this agent was given (they are read on this machine and never sent to the platform)"
	case FailureNoDevice:
		return "BrowserStack accepted the credentials but could not give us the device — either the account has no such device, or every parallel slot on the plan is busy"
	case FailureCannotStart:
		// ⚠️ THE DEVICE HAS SINCE BEEN PROVEN PRESENT, AND THAT DOES NOT LICENSE NARROWING THIS SENTENCE. On
		// 2026-09-01 the account was shown to offer Google Pixel 9 / 15.0, so the one failure we observed had a
		// single remaining cause: the missing build. What improved is our knowledge of ONE PAST INCIDENT, not the
		// hub's ability to tell the two apart on the wire — a customer who asks for a device their account does
		// not offer still gets this identical message. Collapsing it to name only the build is the tempting wrong
		// move now, and it would send that customer to look at a build that is perfectly fine.
		//
		// TWO CANDIDATES IN ONE SENTENCE, DELIBERATELY. The hub says "Platform and Browser not valid." and that
		// sentence has at least two causes: a device/OS pair the account does not offer, and a missing build. The
		// agent cannot tell them apart from the wire, so it names both rather than picking one — a guess dressed
		// as a finding sends a reader to the wrong place, which is the defect this whole classification exists to
		// avoid. The FIRST thing to check is the build, because a session with no `app` capability can never
		// start on App Automate.
		return "BrowserStack signed us in and then refused to start the session. Its message blames the platform " +
			"and the browser, but it cannot tell us which of two things is wrong: no build was supplied (App " +
			"Automate needs an `app` capability naming a build already uploaded to the account), or the device " +
			"and OS version asked for are not ones this account offers"
	}
	return ""
}

// ClassifyResponse reads a hub response for a session-creation request.
//
// THE RESPONSE ITSELF IS NEVER REWRITTEN — this only decides what the AGENT says on its own output. Replacing a
// WebDriver response with our own sentence would break the driver's error handling on the platform side, and a
// fabricated response is the phantom shape this programme keeps deleting. The hub's answer goes back verbatim;
// the classification is for the person watching the agent, which is where a customer with a credential problem
// actually looks.
//
// HONEST ABOUT ITS EVIDENCE, ARM BY ARM. Two envelopes have now been seen from this account, and everything
// else here is still prose:
//
//		OBSERVED  2026-09-01, HTTP 401, no session started, no device-minute spent:
//		  {"value":{"error":"Invalid username or password","message":"Invalid username or password"},
//		   "sessionId":"","status":13}
//		OBSERVED  2026-09-01, HTTP 200:
//		  {"value":{"error":"Error : Platform and Browser not valid.","message":"Error : Platform and Browser not
//		   valid."},"sessionId":"","status":13}
//
//	  - the 401/403 status arm: OBSERVED (the first envelope above).
//	  - "invalid username or password": OBSERVED, in that same envelope.
//	  - "authorization required" and "access denied": STILL PROSE. Never seen.
//	  - every no-device arm ("parallel", "no device", "device not available", …): STILL PROSE. Never seen.
//	  - "platform and browser not valid": OBSERVED (the second envelope).
//
// THE DOCUMENTED TEXT MATCHED THE WIRE EXACTLY, AND THAT IS LUCK WORTH NAMING RATHER THAN VINDICATION. The same
// documentation produced two wrong shapes earlier in this slice: it said the platform sends `alwaysMatch` (it
// sends firstMatch) and it offered `browserstack.user`/`browserstack.key` (a W3C body needs bstack:options).
// The reason to trust this string today is that somebody looked, not that it was written down.
//
// An unrecognised failure returns "" and the agent stays quiet rather than naming a cause it does not know.
// SessionIDOf reads the session id a session-create response carries, in either dialect a hub speaks — W3C puts
// it in value.sessionId, legacy JSONWP at the top level (which the observed BrowserStack refusal ALSO carries,
// empty, beside "status":13). Empty when the response started no session. The same envelope ClassifyResponse
// reads, and read here for the tunnel's routing table: a session is routed, for the rest of its life, to
// wherever it was created, and this is the name it is known by.
func SessionIDOf(body []byte) string {
	var envelope struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
		SessionID string `json:"sessionId"`
		Status    *int   `json:"status"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.Value.SessionID != "" {
		return envelope.Value.SessionID
	}
	if envelope.SessionID != "" && (envelope.Status == nil || *envelope.Status == 0) {
		return envelope.SessionID
	}
	return ""
}

func ClassifyResponse(status int, body []byte) Failure {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return FailureRejected
	}

	// THE PAYLOAD DECIDES, NOT THE TRANSPORT — and this is a correction, not a refinement. The first version of
	// this function returned "" for anything under 400, so when the hub answered HTTP 200 to a session creation
	// that had FAILED (2026-09-01, verbatim below) the agent said nothing at all:
	//
	//   {"value":{"error":"Error : Platform and Browser not valid.","message":"Error : Platform and Browser not
	//    valid."},"sessionId":"","status":13}
	//
	// A W3C success carries value.sessionId. That envelope carries a value.error object AND a legacy top-level
	// "status":13 with an empty "sessionId" — both dialects at once, and the transport claiming success over a
	// payload saying failure. A STATUS WORD READ AS A VERDICT is the defect this programme has removed six times
	// elsewhere; it was in our own code. The HTTP status is corroboration now, never the decision.
	// EVERY FIELD HERE IS READ. An earlier version carried a `Raw json.RawMessage` tagged `json:"-"` that was
	// never populated and never read, and a top-level SessionID that was parsed and never read — in a struct
	// whose entire purpose is to read one envelope precisely, a field nobody reads is a claim about the envelope
	// that nothing checks.
	var envelope struct {
		Value struct {
			SessionID string `json:"sessionId"`
			Error     string `json:"error"`
			Message   string `json:"message"`
		} `json:"value"`
		SessionID string `json:"sessionId"`
		Status    *int   `json:"status"`
	}
	_ = json.Unmarshal(body, &envelope)

	// A SESSION THAT REALLY STARTED IS NOT A FAILURE, in either dialect. W3C puts the id in value.sessionId; a
	// legacy JSONWP success puts it at the top level with status 0 — and the observed failure carried a top-level
	// "sessionId" too, EMPTY, alongside "status":13. So the top-level id counts only when it is non-empty and
	// the legacy status does not contradict it. Recognising the legacy success is a real question this function
	// used to answer by accident, because it parsed the field and then ignored it.
	if envelope.Value.SessionID != "" {
		return ""
	}
	if envelope.SessionID != "" && (envelope.Status == nil || *envelope.Status == 0) {
		return ""
	}

	lowered := strings.ToLower(string(body))
	switch {
	case strings.Contains(lowered, "authorization required"),
		strings.Contains(lowered, "invalid username or password"),
		strings.Contains(lowered, "access denied"):
		return FailureRejected
	case strings.Contains(lowered, "parallel"),
		strings.Contains(lowered, "no device"),
		strings.Contains(lowered, "device not available"),
		strings.Contains(lowered, "all parallel tests are currently in use"):
		return FailureNoDevice
	// THE ONE SENTENCE WE HAVE ACTUALLY SEEN, matched on the account's own words.
	case strings.Contains(lowered, "platform and browser not valid"):
		return FailureCannotStart
	}

	// ⚠️ A DEFAULT, NOT A DEDUCTION — and the difference matters because "status":13 looks like a signal and is
	// not one. BOTH envelopes this account has produced carry the SAME "status":13, the SAME empty top-level
	// "sessionId", and a value.error object: the rejected-credentials one and the cannot-start one. A field
	// identical across every case observed distinguishes nothing, however specific its value looks, and the next
	// author will otherwise read 13 as meaning something.
	//
	// WHAT ACTUALLY DISCRIMINATES, on everything seen so far: the HTTP STATUS (401 for the rejection, 200 for the
	// cannot-start) and THE WORDS. Nothing else has ever varied.
	//
	// So this arm is reached when the hub failed in words we have not seen before, and it names the general
	// answer rather than a cause — the alternative is the silence that let a failed session look like a success.
	if envelope.Value.Error != "" || (envelope.Status != nil && *envelope.Status != 0) {
		return FailureCannotStart
	}
	return ""
}
