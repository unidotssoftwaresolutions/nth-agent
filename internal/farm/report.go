package farm

import (
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// Report is what one probe sweep amounts to on the wire.
type Report struct {
	Devices      []WireDevice    // non-empty ONLY through AttachDevices — the driver gate
	Unusable     *[]WireUnusable // nil ONLY when no platform established anything
	Outcomes     []WireOutcome
	Reachability *string // nil = say nothing; set ONLY by DeclareTunnelOnly, from the driver's existence
	// Present names every healthy phone the probe SAW, whether or not the driver gate published a row for it.
	// nil ONLY when no platform established anything, exactly like Unusable.
	//
	// THE THREE STATES A PHONE CAN BE IN, and where each one is stated:
	//   1. OFFERABLE  — it is in Devices (a leasable row) AND in Present. The farm holds it; a run can pick it.
	//   2. PRESENT, NOT OFFERED — it is in Present and NOT in Devices. The machine sees it and cannot drive it
	//      (no --appium, or Appium/the tunnel did not answer). This is the state that had no name.
	//   3. UNUSABLE  — it is in Unusable and in NEITHER of the other two.
	//
	// WHY A PHONE CANNOT BE IN TWO AT ONCE. (3) is separated from (1) and (2) by the compiler: Unusable and
	// Offerable are different Go types and Answered's constructor takes only []Offerable, so no runtime status
	// field flips one into the other. (1) and (2) are separated by a single predicate over one id — is there a
	// leasable row for it? — so they are complements of each other by construction, not two independently
	// settable flags that could both be true.
	Present *[]WirePresentDevice
	// IosSigning is this machine's WebDriverAgent signing identity — nil = none declared. THE SAME FIELD IS THE
	// DECLARATION AND THE GATE: AttachDevices reads it to decide whether an iPhone may become a leasable row, so a
	// machine cannot offer a phone it did not declare an identity for. Stamped by DeclareIosSigning.
	IosSigning *WireIosSigning
	// AgentVersion is which build of this agent produced the report — nil = did not say. Stamped by
	// DeclareAgentVersion; see RegisterRequest.AgentVersion for what nil means on the wire and why `dev` is an
	// answer rather than an absence.
	AgentVersion *string
	// LiveView is the node's live-view declaration (NodeLiveViewAvailability) — LiveViewUnavailable from BuildReport,
	// LiveViewAvailable only through DeclareLiveView. Registration carries it; it is never nil from BuildReport, because
	// this agent is never silent about it: certainty that is never sent is silence.
	LiveView *string
	// Capture is the machine's capture declaration (NodeCaptureAvailability) — CaptureDisabled from BuildReport, anything
	// else only through DeclareCapture. Never nil from BuildReport, for the reason LiveView is not.
	Capture *string
}

// DeclareCapture stamps the machine's capture declaration — CaptureDisabled, CaptureAvailable or CaptureUnavailable, as
// internal/capture decides it. Anything else declares CaptureUnavailable: a word the farm does not know is not sent.
func DeclareCapture(report Report, availability string) Report {
	switch availability {
	case CaptureDisabled, CaptureAvailable, CaptureUnavailable:
	default:
		availability = CaptureUnavailable
	}
	report.Capture = &availability
	return report
}

// PhoneCaptureReadinessDeclaration answers one phone's capture readiness on this machine — PhoneCaptureReady,
// PhoneCaptureNotSetUp, or "" to say nothing. A nil FUNCTION says nothing for any phone.
type PhoneCaptureReadinessDeclaration func(platform WirePlatform, id string) string

// DeclarePhoneCapture stamps each present Android phone's capture readiness, as readinessFor answers it — THE ONE HOME.
// Only the farm's two words are sent; anything else, an iPhone, and a nil function say nothing (the field is omitted).
func DeclarePhoneCapture(report Report, readinessFor PhoneCaptureReadinessDeclaration) Report {
	if readinessFor == nil || report.Present == nil {
		return report
	}
	present := make([]WirePresentDevice, len(*report.Present))
	copy(present, *report.Present)
	for i := range present {
		present[i].CaptureReadiness = nil
		if present[i].Platform != WireAndroid {
			continue
		}
		switch word := readinessFor(present[i].Platform, present[i].ID); word {
		case PhoneCaptureReady, PhoneCaptureNotSetUp:
			present[i].CaptureReadiness = &word
		}
	}
	report.Present = &present
	return report
}

// DeclareLiveView stamps whether this machine can stream a live view of a phone it offers — THE ONE HOME, like
// DeclareTunnelOnly. Available exactly when a local Appium is configured: that is the only thing on this machine that
// starts a phone's screen server, so an agent without one (device health only, or cloud rows only) still cannot stream
// and keeps saying LiveViewUnavailable. Which phones have a mirror is a per-phone fact on the descriptor (MirrorPort).
func DeclareLiveView(report Report, localDriver bool) Report {
	value := LiveViewUnavailable
	if localDriver {
		value = LiveViewAvailable
	}
	report.LiveView = &value
	return report
}

// DeclareTunnelDown stamps NodeReachability.TunnelDown: tunnel-only, and that tunnel is down on evidence (tunnel.State
// says when). It is still a tunnel-only declaration, so a farm that reads "only through the tunnel" reads it too.
func DeclareTunnelDown(report Report) Report {
	value := ReachabilityTunnelDown
	report.Reachability = &value
	return report
}

// MirrorPortDeclaration answers the live-view mirror port ONE phone has on this machine — nil when it has none. A
// nil FUNCTION declares none for any phone. Same contract, and the same positional shape, as WdaPortDeclaration.
type MirrorPortDeclaration func(platform WirePlatform, udid string) *int

// DeclareAgentVersion stamps WHICH BUILD is talking.
//
// ⚠️ IT INVENTS NOTHING. Whatever `--version` prints is what crosses: a published binary's tag, or `dev` for one
// somebody built themselves. A blank or whitespace value declares NOTHING rather than an empty string — a farm
// that receives `""` cannot tell it from a value, and this side is the only place that knows the difference.
func DeclareAgentVersion(report Report, version string) Report {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return report
	}
	report.AgentVersion = &trimmed
	return report
}

// DeclareTunnelOnly stamps the node's reachability declaration on the report: this machine's devices
// are reachable only through its tunnel. THE ONE HOME: the caller derives the call from the same fact
// that makes it dial the tunnel (a configured driver). It is a statement about the machine's shape, so
// it is made every tick a driver exists — whether or not the tunnel happens to be up this tick.
func DeclareTunnelOnly(report Report) Report {
	value := ReachabilityTunnelOnly
	report.Reachability = &value
	return report
}

// AttachDevices publishes the outcomes' offerable devices as real rows — THE ONE DOOR through which a
// DeviceRegistration can exist, and the caller may open it only when the local Appium has ANSWERED its
// status probe AND the drive tunnel is CONNECTED. No other code path constructs a WireDevice: the seal
// "no code path publishes a DeviceRegistration whose Appium endpoint has not answered" leans on this
// being the single producer.
// optional is the wire's absence: an empty probe answer is OMITTED, never sent as "".
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// DeclareIosSigning stamps this machine's OWN WebDriverAgent signing identity on the report. THE ONE HOME, for
// the reason DeclareTunnelOnly is: the caller derives it from the same values it was given, and every reader —
// the registration that sends it and the gate that offers phones because of it — reads this one field. A nil or
// incomplete identity declares nothing: a partial one (a team with no bundle id) cannot sign, so it is not an
// identity, and saying "some of it" would be worse than saying nothing.
func DeclareIosSigning(report Report, teamID, wdaBundleID, signingID string) Report {
	if teamID == "" || wdaBundleID == "" {
		return report
	}
	if signingID == "" {
		signingID = DefaultIosSigningID
	}
	report.IosSigning = &WireIosSigning{TeamID: teamID, WdaBundleID: wdaBundleID, SigningID: signingID}
	return report
}

// DefaultIosSigningID is Appium's own default for xcodeSigningId and the name a free Personal Team's certificate
// actually carries on this decade's Xcode ("Apple Development", not the retired "iOS Development").
const DefaultIosSigningID = "Apple Development"

// IosSigningRemedy is what a person does about an iPhone this machine sees and will not offer. THE AGENT'S OWN
// SENTENCE, in one place, because the Devices screen renders it verbatim and a second copy would drift. The screen is
// read away from this machine, so its command carries no path of this machine (command.OnThatMachine).
func IosSigningRemedy() string {
	return "This machine has no WebDriverAgent signing identity, so its iPhones are not offered for runs. " +
		"Restart the agent with --ios-team-id and --ios-wda-bundle-id (your own Apple team, not the platform's), " +
		"and run '" + command.OnThatMachine("doctor") + "' to check the provisioning profile is in place."
}

// IosSigningReason is the matching statement of fact — what IS, as against what to do about it.
const IosSigningReason = "iPhones are attached and are not offered: driving one needs a WebDriverAgent signing " +
	"identity belonging to THIS machine, and none was declared."

// CanOffer reports whether a platform's healthy phones may become LEASABLE ROWS on this machine. Android always
// may. iOS may only when this machine declared its own signing identity: an Appium session against a real iPhone
// builds and signs WebDriverAgent first, so a machine with no identity cannot drive the phone it can see — and a
// phone offered and then refused costs a customer their time, which is the whole defect this gate closes.
func CanOffer(platform WirePlatform, signing *WireIosSigning) bool {
	return platform != WireIos || signing != nil
}

// WdaPortDeclaration answers the port ONE device's WebDriverAgent should listen on. A nil RESULT means this
// machine declares none for that device, which is a real answer and not a zero; a nil FUNCTION means it declares
// none for any of them.
type WdaPortDeclaration func(udid string) *int

// FindingsDeclaration answers what this machine ALREADY CHECKED about one device (THE_CHECK_THAT_ALREADY_KNEW) —
// the doctor's answers reduced to {check, state, checkedAtUtc}, no prose. nil function or nil result = the
// machine declares none, which the worker's blind sentence already answers. Same contract as WdaPortDeclaration,
// and deliberately the same positional-parameter shape: every call site is asked the question by the compiler.
type FindingsDeclaration func(udid string) []WireFinding

func AttachDevices(report Report, outcomes []inventory.Outcome, appiumURL string, portFor WdaPortDeclaration, findingsFor FindingsDeclaration, mirrorFor MirrorPortDeclaration) Report {
	devices := []WireDevice{}
	for _, o := range outcomes {
		if !o.IsAnswered() {
			continue
		}
		platform := wirePlatform(o.Platform())
		if !CanOffer(platform, report.IosSigning) {
			// PRESENT, NOT OFFERED — state (2) above, and it already has a home: the phone stays in Present (the
			// machine really does see it) and no leasable row is minted. It is NOT moved to Unusable: that list
			// carries the PLATFORM'S own word for a device the platform itself cannot offer, and a state we coin
			// is not the platform's word. The WHY rides the platform outcome's caveat — the same Reason/Remedy an
			// answered platform already uses — so the screen that renders a machine's health renders this too.
			report.Outcomes = withCaveat(report.Outcomes, platform, IosSigningReason, IosSigningRemedy())
			continue
		}
		for _, d := range o.Offerable() {
			// Every field a lease is written in, from the probe's own answers; empty stays absent.
			// THE MACHINE SAYS WHICH PORT, per device — see WireDescriptor.WdaPort. Only iOS has one to say:
			// WebDriverAgent is an iOS thing, and declaring a port for an Android phone would be a number nobody
			// reads pretending to be a fact.
			var port *int
			if platform == WireIos && portFor != nil {
				port = portFor(d.ID)
			}
			// WHAT THIS MACHINE ALREADY CHECKED rides beside the port it declares — same per-device road, same
			// iOS-only scope (the findings computer produces none for anything else, and an Android row carrying
			// an empty list would be a claim of "checked, nothing found" rather than the truth, "not checked").
			var findings []WireFinding
			if platform == WireIos && findingsFor != nil {
				findings = findingsFor(d.ID)
			}
			// THE PHONE'S MIRROR, BOTH PLATFORMS — unlike WdaPort above, a screen server is not an iOS thing:
			// UiAutomator2 has one too, and an Android phone with no port of its own shares a fixed one with every
			// other Android phone here.
			var mirror *int
			if mirrorFor != nil {
				mirror = mirrorFor(platform, d.ID)
			}
			devices = append(devices, WireDevice{
				LocalID: d.ID,
				Descriptor: WireDescriptor{
					Platform: platform, Kind: "Physical", Udid: d.ID,
					AppiumURL: appiumURL, Status: "Available",
					Model: optional(d.Model), Manufacturer: optional(d.Manufacturer),
					OsVersion: optional(d.OsVersion), FormFactor: optional(d.FormFactor),
					WdaPort: port, Findings: findings, MirrorPort: mirror,
				},
			})
		}
	}
	report.Devices = devices
	return report
}

// BuildReport maps the agent's three-outcome truth onto the farm's wire — and sends NO offerable
// device rows at all.
//
// WHY (measured, and it decides this slice's shape): the farm's DeviceDescriptor requires a
// non-nullable AppiumUrl and DeviceStatus has no "present but not drivable" — and this agent has no
// driver endpoint yet. A registered healthy phone would therefore be a device with a FABRICATED URL
// that any lease would hand out and nobody could drive: the phantom that cost this programme two
// runs. So a healthy phone travels as OfferableCount on its platform's Answered outcome — "it is
// there and healthy" without minting an identity nobody can address — and it is NOT described as
// unusable either: a state we coin is not the platform's own word, and the verbatim rule exists so
// unknown PLATFORM states survive, not to give us a place to put our own.
//
// The node-wide unusable list is nil ONLY when no platform established anything. If android answered
// and ios could not, the list is what android established — possibly empty, WHICH IS A REAL ANSWER —
// and the ios failure rides its own outcome. Rebuilding the coarse one-voice lie the farm's door just
// stopped telling is the one thing this mapping must not do.
func BuildReport(outcomes []inventory.Outcome) Report {
	wireOutcomes := make([]WireOutcome, 0, len(outcomes))
	unusable := []WireUnusable{}
	present := []WirePresentDevice{}
	anyAnswered := false

	for _, o := range outcomes {
		platform := wirePlatform(o.Platform())
		switch {
		case o.IsAnswered():
			anyAnswered = true
			count := len(o.Offerable())
			answered := WireOutcome{Platform: platform, Status: WireAnswered, OfferableCount: &count}
			// A STATED BLIND SPOT on an ANSWERED platform — the farm already speaks this (see
			// PlatformInventoryStatus.Answered: "an answered platform may still carry a stated blind spot in
			// Reason/Remedy"), so it needs no new wire field and whatever renders a machine's health renders it.
			// Status stays Answered on purpose: the offerable half IS established, and the farm's reconcile must
			// keep acting on it. Sending this as CouldNotAsk would throw away an answer we have.
			if o.HasCaveat() {
				reason, remedy := o.Reason(), o.Remedy()
				answered.Reason, answered.Remedy = &reason, &remedy
			}
			wireOutcomes = append(wireOutcomes, answered)
			// NAME THE PHONES, not just count them. The count above stays exactly what it was — this adds the
			// identity the probe already held and used to throw away. No address and no status of ours: see
			// WirePresentDevice for why this cannot become a leasable row.
			for _, d := range o.Offerable() {
				present = append(present, WirePresentDevice{
					ID: d.ID, Platform: platform,
					Model: optional(d.Model), Manufacturer: optional(d.Manufacturer),
					OsVersion: optional(d.OsVersion), FormFactor: optional(d.FormFactor),
				})
			}
			for _, u := range o.Unusable() {
				unusable = append(unusable, WireUnusable{
					Serial: u.ID, State: u.State, Platform: platform, Remedy: u.Remedy,
				})
			}
		case o.IsCouldNotAsk():
			reason, remedy := o.Reason(), o.Remedy()
			wireOutcomes = append(wireOutcomes, WireOutcome{
				Platform: platform, Status: WireCouldNotAsk, Reason: &reason, Remedy: &remedy,
			})
		default:
			wireOutcomes = append(wireOutcomes, WireOutcome{Platform: platform, Status: WireNotAsked})
		}
	}

	report := Report{Devices: []WireDevice{}, Outcomes: wireOutcomes}
	// NOT SILENT ABOUT THE LIVE VIEW: unavailable until the one fact that makes it possible is declared.
	report = DeclareLiveView(report, false)
	// NOR ABOUT CAPTURE: off until the machine says otherwise (DeclareCapture).
	report = DeclareCapture(report, CaptureDisabled)
	if anyAnswered {
		report.Unusable = &unusable
		// SAME GATE, SAME REASON. An empty list here is a real answer — "every platform that answered looked and
		// has no phone" — and nil is "nobody established anything". A machine that said nothing must never read
		// as a machine with nothing attached.
		report.Present = &present
	}
	return report
}

// withCaveat states a reason and a remedy on one ANSWERED platform's outcome without throwing away one the probe
// already established. Two caveats on one platform are joined rather than overwritten: the probe's is about the
// phones, this one is about the machine, and a person needs both.
func withCaveat(outcomes []WireOutcome, platform WirePlatform, reason, remedy string) []WireOutcome {
	for i := range outcomes {
		if outcomes[i].Platform != platform || outcomes[i].Status != WireAnswered {
			continue
		}
		joined := func(existing *string, add string) *string {
			if existing == nil || *existing == "" {
				value := add
				return &value
			}
			value := *existing + " " + add
			return &value
		}
		outcomes[i].Reason = joined(outcomes[i].Reason, reason)
		outcomes[i].Remedy = joined(outcomes[i].Remedy, remedy)
	}
	return outcomes
}

func wirePlatform(p inventory.Platform) WirePlatform {
	if p == inventory.IOS {
		return WireIos
	}
	return WireAndroid
}

// CloudPlaceholderAppiumURL is the address a cloud row stores.
//
// THE COLUMN IS NOT NULL AND THIS ROW HAS NO LOCAL APPIUM. The farm requires every offered device to carry an
// address (`RegistryRules.RequiredAddress` throws on a blank one) because "the relay's forwarding target is
// built from it on the node" — and for a cloud row that target is BrowserStack's hub, chosen inside forward()
// and never from this field. The hub URL does not go here: the column's stated meaning is the node's forwarding
// target, and putting a value there that IS used elsewhere invites somebody to use this one.
//
// SO IT IS A RESERVED-BY-RFC NAME THAT CANNOT RESOLVE. If this address is ever actually dialled, it fails
// immediately and by name instead of quietly reaching the dialler's own loopback — which is precisely the defect
// the farm's own rule cites when it refuses an empty string. It is withheld from consumers anyway: an agent
// declares TunnelOnly, so RegistryRules.PublishableAddress nulls it on every consumer read.
const CloudPlaceholderAppiumURL = "http://cloud-device.invalid"

// AttachCloudDevices publishes one leasable row per device spec the customer configured — A SECOND DOOR, with
// its own preconditions, deliberately not a widening of the first.
//
// WHY NOT AttachDevices. That door's gate is "the local Appium ANSWERED and the tunnel is CONNECTED", and its
// content comes from `inventory` — local probe results. Neither applies here: there is no local Appium to
// answer and no local probe that can see a phone on somebody else's account. Publishing a cloud row is a NEW
// SOURCE OF ROWS, not a gate change, so it is a new function whose gate can be stated on its own terms.
//
// THE GATE THIS ONE NEEDS is the tunnel and nothing else. A consumer reaches a cloud device exactly as it
// reaches a local one — through this agent's tunnel — so a row published while the tunnel is down would be the
// phantom the driver gate exists to prevent. What it does NOT need is a local Appium: the reason the local
// prerequisite exists ("we do not install software on a machine we do not own", connect/loop.go) is about not
// putting Appium on the customer's machine, and on this path the agent installs nothing and drives nothing
// locally — the session is created against BrowserStack's hub. A prerequisite that survives because nobody
// re-read its reason is how the last three defects in this programme happened.
//
// THE PLATFORM COMES FROM THE ACCOUNT'S OWN CATALOGUE, and every spec that reaches here carries one. It used to
// be hard-coded Android, which made `--browserstack-device "iPhone 15|17.0"` publish a row asserting a platform
// nobody established — a spec BrowserStack really offers, so validation passed it. A row that is WRONG is worse
// than a row that is absent, and the truth was already in hand: the same devices.json read that validates a spec
// carries each device's `os`.
func AttachCloudDevices(report Report, specs []CloudDeviceSpec) Report {
	if len(specs) == 0 {
		return report
	}
	for _, s := range specs {
		wire := s.Wire
		platform := s.Platform
		if platform == "" {
			// UNREACHABLE FROM THE ONE PRODUCER — main derives the platform from the catalogue entry that matched
			// the spec, and publishes nothing when it could not read the catalogue. It is checked anyway because
			// the alternative to a check here is a row that names a platform nobody established, which is the
			// defect this parameter exists to remove.
			continue
		}
		device := WireDevice{
			// THE LOCAL ID IS THE SPEC, and it is what makes the row's identity honest. (NodeId, LocalId) is the
			// farm's unique key, so one node cannot offer the same spec twice; two customers offering the same
			// spec are two rows on two nodes, which is what they are.
			LocalID: wire,
			Descriptor: WireDescriptor{
				Platform: platform,
				// PHYSICAL, AND THAT IS THE DECISION THIS SLICE MADE. A BrowserStack device IS physical hardware;
				// what differs is who owns it and that nobody here can adb to it, which CloudSpec says.
				Kind: "Physical",
				// The identifier the platform will put in `appium:udid` — a spec, not a serial, which is exactly
				// how the merge learns which device was leased.
				Udid:      wire,
				AppiumURL: CloudPlaceholderAppiumURL,
				// AVAILABLE RESTS ON A CATALOGUE HERE, NOT ON A PROBE, and that is weaker than it is for a local
				// row. For a local device the word means an Appium answered on this machine; for this one it means
				// the agent is alive and the account's device list contains this spec. Nothing here can ask
				// BrowserStack whether a session would actually start — the account's parallel limit is a real
				// fourth state with no name, and it is NOT given one: DeviceStatus is persisted as an int and a
				// value cannot be taken back cheaply. The failure surfaces at session-create instead.
				Status:     "Available",
				OsVersion:  optional(s.PlatformVersion),
				Model:      optional(s.DeviceName),
				FormFactor: nil,
				CloudSpec:  &wire,
			},
		}
		report.Devices = append(report.Devices, device)
	}
	return report
}

// CloudDeviceSpec is one configured device, flattened for the wire so this package does not import browserstack
// (which would make the farm wire depend on a vendor).
type CloudDeviceSpec struct {
	DeviceName      string
	PlatformVersion string
	Wire            string
	// Platform is the account's own answer for this device, read from the catalogue entry that matched the spec.
	// Never guessed: a spec whose platform could not be established is not published at all.
	Platform WirePlatform
}
