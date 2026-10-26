// Package farm is the agent's client for the the farm node plane — the knock. Wire shapes mirror
// The farm's Contracts (camelCase JSON, enum values as their C# member names).
package farm

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// WirePlatform is DevicePlatform's wire form ("Android"/"Ios" — C# member names).
type WirePlatform string

const (
	WireAndroid WirePlatform = "Android"
	WireIos     WirePlatform = "Ios"
)

// WireOutcomeStatus is PlatformInventoryStatus's wire form.
type WireOutcomeStatus string

const (
	WireAnswered    WireOutcomeStatus = "Answered"
	WireCouldNotAsk WireOutcomeStatus = "CouldNotAsk"
	WireNotAsked    WireOutcomeStatus = "NotAsked"
)

// WireOutcome mirrors PlatformInventoryOutcome. OfferableCount is DELIBERATELY A COUNT, not an
// inventory — see BuildReport for the whole reasoning (the no-phantom rule).
type WireOutcome struct {
	Platform       WirePlatform      `json:"platform"`
	Status         WireOutcomeStatus `json:"status"`
	Reason         *string           `json:"reason,omitempty"`
	Remedy         *string           `json:"remedy,omitempty"`
	OfferableCount *int              `json:"offerableCount,omitempty"`
}

// WireUnusable mirrors UnusableDevice: the serial and the platform's own state, verbatim.
type WireUnusable struct {
	Serial   string       `json:"serial"`
	State    string       `json:"state"`
	Platform WirePlatform `json:"platform"`
	Remedy   string       `json:"remedy"`
}

// WirePresentDevice NAMES A PHONE THE MACHINE HAS, whether or not it can offer it.
//
// A PHONE WE CANNOT DRIVE IS STILL THEIR PHONE. Until this existed, a healthy phone on a machine running
// without --appium survived the trip as the integer 1 inside an outcome's offerableCount: present, seen,
// counted, ANONYMOUS. It was the one phone the product could not name — a broken phone had a serial and a
// remedy, a leasable phone had a whole row, and only the healthy-but-undrivable one had nothing.
//
// IT IS NOT A DeviceRegistration AND CANNOT BECOME ONE. Three differences, each deliberate:
//   - no AppiumURL, and no field that could hold one. The farm's door requires an address
//     (RegistryRules.RequiredAddress) and a row with a fabricated URL is the phantom that cost this
//     programme two runs. This type has nowhere to put one, so no runtime flag can promote it.
//   - no Status. "Available" is OUR word about leasability, and this statement makes no claim about
//     leasability at all. The verbatim rule exists so unknown PLATFORM states survive, not to give us
//     somewhere to invent our own.
//   - a different Go type from WireDevice, exactly as Offerable is a different type from Unusable — the
//     mechanism, not the intention, is what keeps them apart.
//
// Every field is the platform's OWN answer and is OMITTED when the platform did not answer: the rule
// inventory.Offerable's docblock states, unchanged here. An absent field is honest; a guessed one is not.
type WirePresentDevice struct {
	// ID is the platform's own stable id — adb serial / iOS udid. Present so a person can tell WHICH phone
	// on their desk this is, and so the farm can match it against a leasable row of the same id.
	ID           string       `json:"id"`
	Platform     WirePlatform `json:"platform"`
	Model        *string      `json:"model,omitempty"`
	Manufacturer *string      `json:"manufacturer,omitempty"`
	OsVersion    *string      `json:"osVersion,omitempty"`
	FormFactor   *string      `json:"formFactor,omitempty"`
	// CaptureReadiness is WHETHER NETWORK CAPTURE CAN RUN ON THIS PHONE HERE — the farm's PhoneCaptureReadiness, in its
	// own words (PhoneCaptureReady, PhoneCaptureNotSetUp), from the gates a capture start reads on this machine
	// (internal/capture's Readiness). It lets the farm withhold a capture lease's pull URL from a phone nobody set up, and
	// say why, before the run starts — instead of the run learning it from the pull.
	//
	// OMITTED when this machine says nothing about the phone: it is not an Android phone, or the machine's own capture
	// declaration (Disabled, Unavailable) already says why nothing is captured here. A farm older than the field ignores it,
	// as it ignores any field it does not know; one that knows it reads absent as "said nothing", never as "not set up".
	CaptureReadiness *string `json:"captureReadiness,omitempty"`
}

// ONE PHONE'S CAPTURE READINESS — the farm's PhoneCaptureReadiness, in its own two words, on each present phone
// (WirePresentDevice.CaptureReadiness). Only these two are ever sent: the farm binds the field to its enum, and a word it
// did not know would cost the whole heartbeat.
const (
	PhoneCaptureReady    = "Ready"
	PhoneCaptureNotSetUp = "NotSetUp"
)

// WireDevice mirrors DeviceRegistration: a device row the farm may LEASE. Published only when
// something can actually drive it — see BuildReport's no-phantom rule and the driver gate.
type WireDevice struct {
	LocalID    string         `json:"localId"`
	Descriptor WireDescriptor `json:"descriptor"`
}

// WireDescriptor mirrors DeviceDescriptor (enum values are the C# member names).
// WireDescriptor mirrors the farm's DeviceDescriptor. A ROW THAT CANNOT BE FILTERED CANNOT BE CHOSEN: the
// farm selects an unspecified lease on OsVersion, FormFactor and Model (lower-cased equality), so every one of
// those must be here, carrying the device's own answer with the rack node's meaning — and absent (omitted,
// read as null) when the device did not answer. Omitted is honest; a filter treats it as no match.
type WireDescriptor struct {
	Platform     WirePlatform `json:"platform"`
	Kind         string       `json:"kind"` // "Physical" — the agent connects real hardware
	OsVersion    *string      `json:"osVersion"`
	FormFactor   *string      `json:"formFactor,omitempty"`
	Udid         string       `json:"udid"`
	AppiumURL    string       `json:"appiumUrl"` // the LOCAL Appium the tunnel forwards to
	Status       string       `json:"status"`    // "Available"
	Manufacturer *string      `json:"manufacturer,omitempty"`
	Model        *string      `json:"model,omitempty"`
	// WdaPort is THE PORT THIS DEVICE'S WEBDRIVERAGENT SHOULD LISTEN ON, declared by the machine it is plugged
	// into — the one place that can actually know what is free there.
	//
	// PER DEVICE, NOT PER NODE, and that is why it rides the descriptor rather than sitting beside IosSigning on
	// the registration: a machine with four iPhones signs them all the same way and cannot give them one port.
	//
	// Omitted when this machine declared none (an --appium-less agent, a platform this build cannot ask about),
	// which the farm reads as null and the worker refuses to guess past. Absent and 0 are not the same thing and
	// only absent is expressible, which is why it is a pointer.
	WdaPort *int `json:"wdaPort,omitempty"`
	// Findings are WHAT THIS MACHINE ALREADY CHECKED about driving this device — the doctor's own answers, ridden
	// so the worker's failure sentence can name a cause instead of listing possibilities
	// (THE_CHECK_THAT_ALREADY_KNEW). Same road WdaPort took: machine → report → descriptor → lease → sentence.
	//
	// NO FREE TEXT CROSSES HERE, deliberately: a finding is a check NAME, a STATE word and WHEN it was computed —
	// never the doctor's prose, which can embed a library error's text. The worker authors every sentence it
	// shows a tenant from the name and the state alone, so the withholding boundary has nothing new to hold.
	//
	// Omitted when this machine computed none (an Android phone; a build without the checks) — absent is "the
	// machine said nothing", which the worker's blind sentence already answers.
	Findings []WireFinding `json:"findings,omitempty"`
	// CloudSpec is THE ONE FIELD THAT MAKES A CLOUD ROW A CLOUD ROW — "browserstack:{device}|{version}" — and it
	// is omitted for every device on hardware this machine can touch. A cloud row's Kind is still "Physical",
	// because it IS physical hardware; what differs is that nobody here can reach it, and that is what this says.
	//
	// The farm stores it on the row and carries it on the lease (LeaseResponse already ships the whole
	// descriptor), so the run record can one day say which device actually ran. Same road every per-device fact
	// takes: machine → report → descriptor → lease.
	CloudSpec *string `json:"cloudSpec,omitempty"`
	// MirrorPort is THE PORT THIS MACHINE RESERVED FOR THIS PHONE'S LIVE-VIEW MIRROR — the farm's
	// DeviceDescriptor.MirrorPort, which the farm's rack node already declares for its iPhones — CHECKED FREE here
	// (liveview.Chooser) and the only port this agent's tunnel will stream for the phone (liveview.Offered). The
	// consumer asks the session's screen server (`mjpegServerPort`) for exactly this number.
	//
	// OMITTED when this machine has no mirror for the phone (no port was free, its driver cannot open one, it is a
	// cloud row, or nothing here can drive it) — and the farm's meaning for absent is "reserved none", never "use
	// 9100". Every phone this agent offers gets its own, Android included: on a customer's machine two Android phones
	// asked for one fixed number fight over one adb forward.
	MirrorPort *int `json:"mirrorPort,omitempty"`
}

// WireFinding is ONE answer the machine already computed about one device — a doctor check, reduced to what may
// cross the seam: which check, its state word ("satisfied" | "missing" | "unknown" — the doctor's own vocabulary),
// and WHEN it was computed. The timestamp is the load-bearing field: a stale answer naming a cause that is no
// longer the cause is worse than today's four possibilities, so a finding with no stated freshness must never be
// minted — CheckedAtUtc is required at every producer, and the consumer drops any finding that arrives without it.
type WireFinding struct {
	Check        string    `json:"check"`
	State        string    `json:"state"`
	CheckedAtUtc time.Time `json:"checkedAtUtc"`
	// ExpiresAtUtc is WHEN THE THING THIS CHECK READ STOPS BEING TRUE — today only the provisioning profile's own
	// ExpirationDate, carried because the machine reads it, prints it locally, and then dropped it here.
	//
	// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 5: without it the platform's failure sentence guessed at an
	// ACCOUNT TIER ("free Apple accounts expire weekly") to explain a lapsed certificate — and the guess is wrong
	// for every paid customer. Measured 2026-08-26: a PAID Apple Developer Program membership issued a profile
	// expiring in six days. The tier was never the fact; the date is, and the date was already on this machine.
	//
	// A DATE IS NOT PROSE. It is one field with one meaning, like CheckedAtUtc beside it — the withholding
	// boundary is unchanged and the consumer still authors every sentence it shows a tenant.
	//
	// Omitted when this check has no expiry to state, or when the machine could not read one. Absent means "not
	// said", never "does not expire" — a pointer, for the reason WdaPort is one.
	ExpiresAtUtc *time.Time `json:"expiresAtUtc,omitempty"`
}

// WireIosSigning mirrors the farm's NodeIosSigning — THE SIGNATURE BELONGS TO THE MACHINE THE PHONE IS PLUGGED
// INTO. Appium's xcuitest driver builds and SIGNS WebDriverAgent before it can drive a real iPhone, and the
// identity it signs with describes ONE Mac: the team that owns it and the WDA bundle id registered to that team.
// The platform holds exactly one such identity, and it is not every machine's — which is why a run on a
// customer's own iPhone was refused outright rather than signed with ours.
//
// NOT A SECRET, AND STILL THE CUSTOMER'S. An Apple Team id and a bundle id are printed in Xcode's UI and appear
// in any .ipa; they authorise nothing on their own. They are still facts about somebody's development account, so
// they travel where the agent token travels (this declaration, over the same authenticated https call) and are
// kept out of everything the token is kept out of: narration, error text, and any tenant-visible listing. The
// leak seal walks for that.
type WireIosSigning struct {
	TeamID      string `json:"teamId"`
	WdaBundleID string `json:"wdaBundleId"`
	SigningID   string `json:"signingId"`
}

// RegisterRequest mirrors RegisterNodeRequest. Devices carries rows ONLY when the driver gate is open
// (Appium probed answering AND the tunnel connected); otherwise it is empty, byte for byte the
// pre-driver behaviour. UnusableDevices pointer-vs-nil carries the farm's null-is-not-an-empty-list rule.
type RegisterRequest struct {
	HostName         string          `json:"hostName"`
	Devices          []WireDevice    `json:"devices"`
	NodeID           string          `json:"nodeId"`
	UnusableDevices  *[]WireUnusable `json:"unusableDevices,omitempty"`
	PlatformOutcomes []WireOutcome   `json:"platformOutcomes"`
	// Reachability is the farm's NodeReachability — a standing fact about THIS MACHINE, restated every
	// tick. nil = say nothing (today's wire, byte for byte); ReachabilityTunnelOnly = "my devices are
	// reachable only through my tunnel". Derived from the one fact that makes the agent dial a tunnel
	// (a configured driver), never from a second switch — see connect.buildReport.
	Reachability *string `json:"reachability,omitempty"`
	// CaptureAvailability is the farm's NodeCaptureAvailability — see CaptureDisabled. Registration-only, exactly like
	// the C# node: a standing fact about the machine. When `capture enable` or `capture disable` changes it while the agent
	// runs, the connect loop registers again, so the farm hears the new one (connect.Run).
	CaptureAvailability *string `json:"captureAvailability,omitempty"`

	// LiveViewAvailability is the farm's NodeLiveViewAvailability — see LiveViewUnavailable. Registration-only and
	// nullable for the same reason as the field above: a node that says nothing keeps today's answer.
	LiveViewAvailability *string `json:"liveViewAvailability,omitempty"`

	// IosSigning is this machine's OWN WebDriverAgent signing identity — see WireIosSigning. nil = this machine
	// declared none, which is not a failure: an Android-only machine has no iPhone to sign for, and a Mac whose
	// owner has not set one up yet keeps its iPhones PRESENT-but-not-offered rather than offering a phone nothing
	// can drive.
	//
	// REGISTRATION-ONLY, for the reason CaptureAvailability and LiveViewAvailability are: this is a standing fact
	// about the machine, established once at start-up from a flag or the environment, not a per-tick measurement.
	// A person who changes it restarts the agent, and if the farm ever forgets this node the loop re-registers
	// with the same identity (see connect.Run's purged-row branch), so the declaration is restated exactly when
	// the row that holds it is rebuilt. Reachability rides every tick because a lease decision reads it every
	// tick; nothing reads this one between registrations.
	IosSigning *WireIosSigning `json:"iosSigning,omitempty"`

	// AgentVersion is WHICH BUILD OF THIS AGENT produced the row — the value `--version` prints, which for a
	// published binary is its tag and for one somebody built themselves is `dev`.
	//
	// ⚠️ NIL MEANS AN AGENT THAT DID NOT SAY, which is every agent running today, and must never be rendered as
	// anything else — not "unknown build", not a blank that reads as a value. Same nullable idiom, and the same
	// rule, as the four standing facts above it: an omitted field and a farm that has never heard of it produce
	// identical bytes.
	//
	// ⚠️ AND `dev` IS AN ANSWER, NOT AN ABSENCE. A build somebody made themselves says `dev` because that is what
	// it is; this agent does not substitute a git SHA or a date it would be guessing at. The screen's job is to
	// make that legible rather than pretty.
	//
	// Registration-only, like the three availability facts: it is a standing property of the binary, not
	// something that can change between heartbeats without the process being replaced.
	AgentVersion *string `json:"agentVersion,omitempty"`

	// PresentDevices names the phones this machine HAS — see WirePresentDevice. Pointer-vs-nil for the same
	// reason as UnusableDevices above: nil is "no platform established anything", an empty list is "we looked
	// and there are none", and collapsing those two is the coarse one-voice lie the farm's door stopped telling.
	// Sent on registration AND every heartbeat, because a first tick must not silently know less than every
	// tick after it.
	PresentDevices *[]WirePresentDevice `json:"presentDevices,omitempty"`
}

// ReachabilityTunnelOnly is the farm's NodeReachability.TunnelOnly on the wire (string enums).
const ReachabilityTunnelOnly = "TunnelOnly"

// ReachabilityTunnelDown is the farm's NodeReachability.TunnelDown (A_TUNNEL_THAT_KNOWS_IT_DIED): everything
// TunnelOnly says, AND that tunnel is down right now — said on EVIDENCE, never by a clock (tunnel.State). The farm
// answers it by taking this node's devices off offer and ending their leases with TunnelWentDown.
//
// ⚠️ A FARM OLDER THAN THAT VALUE refuses the heartbeat carrying it (400). That is NOT "fails closed": a 400 is no
// answer, which voids the unheard evidence, so the next heartbeat says TunnelOnly and lands, and the two alternate.
// What keeps it harmless is the driver gate — with the tunnel down this agent publishes no device rows at all, so
// nothing is offered either way. (The review's finding, 2026-10-03; an earlier draft of this sentence claimed the
// reaper would offline the node.)
const ReachabilityTunnelDown = "TunnelDown"

// THE MACHINE'S CAPTURE DECLARATION — the farm's NodeCaptureAvailability, in its own three words, sent at registration
// (a standing fact about the machine, like the C# node's). CERTAINTY THAT IS NEVER SENT IS SILENCE, and the farm's rule for
// a node that says nothing is to hand the consumer an optimistic HAR pull URL anyway — so this agent always says which:
//
//   - CaptureDisabled: capture is switched off on this machine (no `nth-agent capture enable`). The farm withholds the
//     pull URL with CaptureWithheldReason.NodeCaptureDisabled — "off", not "broken".
//   - CaptureAvailable: it is on, the capture CA can be used and adb resolves; a capture lease here gets its pull URL, and
//     the machine's own gates decide each start (internal/capture: the phone must be set up here, for this CA).
//   - CaptureUnavailable: it is on and this machine cannot run it now (no adb, a CA that expired or that others can read);
//     the farm withholds with NodeCannotCapture.
//
// Before AN_AGENT_THAT_CAN_RECORD_ITS_PHONE this agent could not capture at all, and said Unavailable on every machine.
const (
	CaptureDisabled    = "Disabled"
	CaptureAvailable   = "Available"
	CaptureUnavailable = "Unavailable"
)

// LiveViewUnavailable is the farm's NodeLiveViewAvailability.Unavailable on the wire. THE SAME SENTENCE AS
// CAPTURE, ABOUT A DIFFERENT CAPABILITY: this agent refuses every stream-start frame — there is no screencast
// mirror here until the stream family is ported — and certainty that is never sent is silence. Without this the
// consumer cannot tell an EMPTY mirror from an IMPOSSIBLE one, and a customer watching their own phone is left
// staring at a black rectangle instead of being told to look at the device in their hand.
//
// IT IS A SEPARATE FACT FROM OWNERSHIP, and deliberately so. Today the two are perfectly correlated by
// construction — a customer cannot run the C# node (it authenticates only with the platform's pre-shared node
// key), so workspace-owned implies this agent implies no mirror — but they have DIFFERENT LIFETIMES. Ownership is
// durable; streamability is a property of this month's agent build, and the day the stream family is ported a
// customer-owned device HAS a mirror. Fusing them now would mean unfusing them then.
//
// THAT DAY CAME (AN_AGENT_THAT_SHOWS_ITS_PHONE): the stream family is ported, and this value is now what an agent
// WITHOUT a local Appium says — nothing on such a machine can start a phone's screen server, so it still cannot
// stream. An agent with one says LiveViewAvailable (see DeclareLiveView). Ownership did not move.
const LiveViewUnavailable = "Unavailable"

// LiveViewAvailable is the farm's NodeLiveViewAvailability.Available: this machine can stream a live view of a phone
// it offers. Per phone, the descriptor's MirrorPort says which ones — a phone without one has no mirror here.
const LiveViewAvailable = "Available"

// HeartbeatRequest mirrors the farm's — the same facts as registration, every tick.
type HeartbeatRequest struct {
	Devices          []WireDevice    `json:"devices"`
	UnusableDevices  *[]WireUnusable `json:"unusableDevices,omitempty"`
	PlatformOutcomes []WireOutcome   `json:"platformOutcomes"`
	Reachability     *string         `json:"reachability,omitempty"` // same declaration, every tick (LWW on the farm)
	// The same present-devices statement as registration — see RegisterRequest.PresentDevices.
	PresentDevices *[]WirePresentDevice `json:"presentDevices,omitempty"`
}

// envelope mirrors ApiResponse<T>.
type envelope[T any] struct {
	Success bool    `json:"success"`
	Message *string `json:"message"`
	Data    T       `json:"data"`
}

type registerData struct {
	NodeID string `json:"nodeId"`
	// NewerAgentVersion is read raw, so a value of any other shape costs nothing but itself (see NewerAgentVersion).
	NewerAgentVersion json.RawMessage `json:"newerAgentVersion"`
}

// heartbeatData carries the work lists. This slice PULLS NO WORK — but a non-empty list is a fact
// worth printing, never a thing to silently discard (the loop narrates it).
//
// Only their LENGTHS are used, so each element is read as an empty struct: nothing of it is kept.
type heartbeatData struct {
	ProvisionTasks    []struct{}      `json:"provisionTasks"`
	Commands          []struct{}      `json:"commands"`
	NewerAgentVersion json.RawMessage `json:"newerAgentVersion"`
}

// releaseVersion is the only shape of newerAgentVersion this agent reads: major.minor.patch, with at most one leading `v`
// — the farm's own rule for what it compares (AgentRelease.NewerFor). A pre-release, a word, or anything a terminal could
// take for an instruction is not a version.
var releaseVersion = regexp.MustCompile(`^v?[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$`)

// NewerAgentVersion is the farm's newerAgentVersion as this agent uses it: the version as the farm wrote it, trimmed, or ""
// when the field is missing, null, not a string, or not a version. It never fails the call it came with: an older agent
// keeps working whatever the farm puts there (AN_AGENT_THAT_KNOWS_IT_IS_OLD).
func NewerAgentVersion(raw json.RawMessage) string {
	var text string
	if len(raw) == 0 || json.Unmarshal(raw, &text) != nil {
		return ""
	}
	text = strings.TrimSpace(text)
	if !releaseVersion.MatchString(text) {
		return ""
	}
	return text
}
