// Package inventory carries the one distinction this repo starts with in the type instead of a log
// line: an empty list is a true answer; not having asked is not. See the three-outcome doc below.
package inventory

import "strings"

// Platform is a FIELD on a device, never a verb the reader has to fork on. The values are the only
// ones this repo builds and claims (see CI); a platform we do not compile is a platform we do not name.
type Platform string

const (
	Android Platform = "android"
	IOS     Platform = "ios"
)

// Offerable is a device a probe can offer for use.
//
// It is a DIFFERENT TYPE from Unusable, and that is the whole mechanism behind "a device you cannot
// offer may never become an offerable one": Answered's constructor takes an []Offerable, so an
// Unusable cannot be appended to it — the compiler rejects it, no runtime status field flips one into
// the other. A phantom published as Available once cost two runs; here it cannot be expressed.
type Offerable struct {
	ID string // the platform's own stable id: adb serial / iOS udid
	// THE FIELDS A LEASE REQUEST IS WRITTEN IN. The farm filters an unspecified lease on OsVersion, FormFactor
	// and Model (lower-cased equality), so a row published without them cannot be asked for: publishing a
	// device is not offering it. Each field carries the platform's OWN answer with the same meaning the rack
	// node's provider gives it (Android: ro.product.model / ro.product.manufacturer / ro.build.version.release /
	// ro.build.characteristics), and is EMPTY when the platform did not answer — never a listing token, never a
	// default, never a guess. An empty field a filter treats as "no match" is honest; a guessed one is not.
	Model        string // Android: ro.product.model (the rack's meaning — not adb's sanitised listing token)
	Manufacturer string // Android: ro.product.manufacturer; iOS: "Apple" (definitional for a usbmuxd device)
	OsVersion    string // Android: ro.build.version.release; iOS: unset (needs a lockdown read — see ios.UsbmuxdLister)
	FormFactor   string // "phone" / "tablet" when the platform said so; unset when it said something else
}

// Unusable is a device the probe SAW and cannot offer. Attached, present, and not available — which
// is a real fact worth showing ("iPhone 15 — tap Trust to continue" beats a device that never
// appears), and a strictly different thing from an empty inventory.
type Unusable struct {
	ID string
	// State is the platform's OWN word, verbatim and lower-cased — "unauthorized", "offline",
	// "no permissions", and whatever a future adb or go-ios prints. NOT an enum: an unknown state
	// survives the trip and is shown, never flattened to "other".
	State  string
	Remedy string // what the reader does about it — never empty for a state we recognise
}

// NewUnusable lower-cases the platform's state verbatim (the verbatim-state rule) and trims it.
func NewUnusable(id, state, remedy string) Unusable {
	return Unusable{ID: id, State: strings.ToLower(strings.TrimSpace(state)), Remedy: remedy}
}

// LeftOut is a device the probe SAW, ready to use, and did not offer because it is not on this machine's USB cable — an
// emulator, or a phone reached over Wi-Fi or the network — and the customer did not ask for those to be offered too
// (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1). It is said on this machine, by connect and doctor, and NEVER sent to the
// farm: the farm is told only about what is offered, and what is attached and unusable.
type LeftOut struct {
	ID string
	// How the device is reached, in the words a sentence about it uses: "an emulator", "reached over Wi-Fi".
	How string
}

// OfferUncabledFlag is the flag that offers them too. THE ONE HOME of its name: the probes, the CLI table and every
// sentence that names it read it here.
const OfferUncabledFlag = "--offer-uncabled-devices"

// kind is the discriminant of the three outcomes. Unexported with no iota gaps exposed, so the only
// way to obtain an Outcome of a given kind is its constructor below.
type kind int

const (
	kindAnswered kind = iota
	kindCouldNotAsk
	kindNotAsked
)

// Outcome is the result of ONE platform probe: exactly one of Answered / CouldNotAsk / NotAsked.
//
// The fields are unexported and there is no literal constructor, so a CouldNotAsk carrying a device
// list — the "empty inventory that is really a failure" this repo exists to forbid — is not
// representable: you cannot hand-assemble one. Build every Outcome through Answered / CouldNotAsk /
// NotAsked, and read it through the accessors.
type Outcome struct {
	platform  Platform
	kind      kind
	offerable []Offerable
	unusable  []Unusable
	// reason/remedy carry TWO different things, and which one is decided by the kind — never by their
	// content. CouldNotAsk: what failed, and what a person does about it. Answered: A STATED BLIND SPOT
	// (see AnsweredWithCaveat) — what this probe could not establish, and what a person checks by hand.
	// Both empty on a plain Answered, which is how HasCaveat can tell "answered, nothing to add" from
	// "answered, and here is what I cannot see".
	reason string
	remedy string
	// leftOut is what an ANSWERED probe saw and did not offer — see LeftOut. Never part of what the farm is told.
	leftOut []LeftOut
}

// Answered: the probe ran and established the truth. Two lists, either possibly empty — empty here is
// a real answer. The signature is the guarantee: an Unusable cannot be passed where []Offerable is.
func Answered(platform Platform, offerable []Offerable, unusable []Unusable) Outcome {
	if offerable == nil {
		offerable = []Offerable{}
	}
	if unusable == nil {
		unusable = []Unusable{}
	}
	return Outcome{platform: platform, kind: kindAnswered, offerable: offerable, unusable: unusable}
}

// AnsweredWithCaveat is Answered plus A STATED BLIND SPOT — the probe established the offerable truth
// AND names something about this platform it could NOT establish.
//
// A BLIND SPOT IS NOT A FAILURE, and that distinction is the whole reason this is not a fourth kind: the
// offerable half is answered and every consumer must keep acting on it. The farm already speaks this —
// PlatformInventoryStatus.Answered's own doc says an answered outcome may carry a stated blind spot in
// Reason/Remedy ("devicectl cannot tell an untrusted iPhone from an unplugged one"), so a caveat rides the
// existing vocabulary to whatever renders a machine's health, with no new field on the wire.
//
// The reason is what could not be established; the remedy is what a person does about it by hand. A caveat
// with nothing to say is not a caveat: an empty reason yields a plain Answered, so silence is never dressed
// up as a statement. An empty remedy takes the same loud-beats-blank substitution as CouldNotAsk.
func AnsweredWithCaveat(platform Platform, offerable []Offerable, unusable []Unusable, reason, remedy string) Outcome {
	o := Answered(platform, offerable, unusable)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return o
	}
	o.reason = reason
	o.remedy = strings.TrimSpace(remedy)
	if o.remedy == "" {
		o.remedy = MissingRemedyMarker
	}
	return o
}

// WithLeftOut is an ANSWERED outcome carrying the devices its probe saw and did not offer. Any other outcome holds no
// devices, and is returned as it is.
func WithLeftOut(o Outcome, leftOut []LeftOut) Outcome {
	if o.kind != kindAnswered || len(leftOut) == 0 {
		return o
	}
	o.leftOut = append([]LeftOut(nil), leftOut...)
	return o
}

// HasCaveat reports whether an ANSWERED outcome carries a stated blind spot. False for CouldNotAsk, whose
// reason is the failure itself and not a caveat — a reader must be able to tell those two apart.
func (o Outcome) HasCaveat() bool { return o.kind == kindAnswered && o.reason != "" }

// CouldNotAsk: the probe ran and failed — missing tool, unreachable daemon, timeout, non-zero exit.
// This is NOT an empty inventory; it is the absence of an answer. Both a reason (what happened) and a
// remedy (what to do) are required — a reason without a remedy is the support ticket this agent
// exists to delete — so a blank remedy is replaced with MissingRemedyMarker: loud beats blank.
//
// WHAT THE SUBSTITUTION DOES AND DOES NOT GUARANTEE. It guarantees a reader is never handed an empty
// "what to do". It guarantees NOTHING about the probes: an assertion of the form
// `TrimSpace(o.Remedy()) != ""` is answered by THIS LINE, not by the code that built the outcome —
// A CHECK WHOSE ANSWER IS SUPPLIED BY THE CODE UNDER TEST MEASURES THAT CODE'S DEFAULTING, NOT ITS
// BEHAVIOUR. (An earlier comment here claimed this "also guards construction sites"; it guarded
// nothing but itself, and a probe shipped with an empty remedy would have gone green and printed the
// marker to a customer.) The guard that can actually fail is the MARKER'S ABSENCE, asserted against
// every production path that builds a CouldNotAsk — see the marker seals in the probe packages.
func CouldNotAsk(platform Platform, reason, remedy string) Outcome {
	if strings.TrimSpace(remedy) == "" {
		remedy = MissingRemedyMarker
	}
	return Outcome{platform: platform, kind: kindCouldNotAsk, reason: reason, remedy: remedy}
}

// MissingRemedyMarker is what a reader sees when a probe supplied no remedy — a bug in that probe,
// made loud rather than blank. IT IS THE THING THE SEALS ASSERT THE ABSENCE OF: every production
// CouldNotAsk site is driven through its own seam and must produce a real remedy, never this.
const MissingRemedyMarker = "(no remedy provided — this is a bug in the probe)"

// NotAsked: the probe was switched off on this host and holds no opinion at all. A disabled probe is
// not a faulty one — the NodeCaptureAvailability.Disabled-vs-Unavailable distinction, one layer down.
func NotAsked(platform Platform) Outcome {
	return Outcome{platform: platform, kind: kindNotAsked}
}

func (o Outcome) Platform() Platform     { return o.platform }
func (o Outcome) IsAnswered() bool       { return o.kind == kindAnswered }
func (o Outcome) IsCouldNotAsk() bool    { return o.kind == kindCouldNotAsk }
func (o Outcome) IsNotAsked() bool       { return o.kind == kindNotAsked }
func (o Outcome) Offerable() []Offerable { return o.offerable }
func (o Outcome) Unusable() []Unusable   { return o.unusable }
func (o Outcome) LeftOut() []LeftOut     { return o.leftOut }
func (o Outcome) Reason() string         { return o.reason }
func (o Outcome) Remedy() string         { return o.remedy }

// KindString is the stable machine token for the outcome — used by the JSON rendering and the census
// so the three are distinguishable without reading prose. THE source-count guard reads AllKinds below.
func (o Outcome) KindString() string {
	switch o.kind {
	case kindAnswered:
		return "answered"
	case kindCouldNotAsk:
		return "could_not_ask"
	case kindNotAsked:
		return "not_asked"
	default:
		return "unknown"
	}
}

// AllKinds is the closed list of outcome kinds. It has ONE home so the census cannot drift from the
// type: the source-count guard asserts the number of `kind` constants declared above equals len here,
// and every renderer/census walks this slice. Adding a fourth kind without extending this list — and
// then the renderers — is what the guard and the census are built to catch.
func AllKinds() []string { return []string{"answered", "could_not_ask", "not_asked"} }
