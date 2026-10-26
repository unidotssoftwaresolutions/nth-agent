// Package connect is the agent's standing loop: register once, then heartbeat — the knock, repeated.
package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// Config wires the loop. Every timing is a parameter so tests drive the loop without one sleep.
type Config struct {
	Client   *farm.Client
	NodeID   string
	HostName string
	// AgentVersion is what `--version` prints — a published binary's tag, or `dev`. Empty declares nothing, which
	// is what every agent built before this field existed sends. See farm.DeclareAgentVersion.
	AgentVersion string
	// Probes produce this tick's truth — the same probes `devices` runs.
	Probes func(ctx context.Context) []inventory.Outcome
	// Interval is chosen AGAINST the farm's NodeTtlSeconds (30s): a third of it, so the farm gets
	// three chances to hear us per staleness window and one lost packet never reads as a dead node.
	Interval time.Duration
	// BackoffBase/BackoffCeiling bound transport-failure retries (with jitter, so a fleet that lost
	// the same network moment does not knock again in unison) — and a 429's, the farm's limits.
	BackoffBase    time.Duration
	BackoffCeiling time.Duration
	// MaxTicks stops the loop after N cycles; 0 = run until ctx ends. Tests set it.
	MaxTicks int
	// Sleep is the wait seam (tests replace it; production uses a ctx-aware sleep).
	Sleep func(ctx context.Context, d time.Duration)
	// Out receives the narration. STATE CHANGES ONLY — a loop that prints every heartbeat is a loop
	// nobody reads, and the first sixty seconds are the whole product.
	Out io.Writer
	// Driver is the drive path (nil = no --appium: TODAY'S BEHAVIOUR, byte for byte — counts on the
	// outcomes, no device rows, no tunnel; the customer who has not installed Appium sees exactly what
	// they saw yesterday, sealed as bytes).
	Driver *Driver

	// CloudDevices are the device specs the customer configured on THEIR machine — one leasable row each, on a
	// BrowserStack account only they hold the credentials for. Empty = today's behaviour, byte for byte.
	CloudDevices []farm.CloudDeviceSpec
	// OnRegistered runs after EVERY successful registration — the moment the farm knows this node's id. The
	// drive tunnel is dialled from here (main wires it), because a dial before registration addresses a node the
	// farm has never seen and is answered 404: the first thing a customer read was a failure that was not one.
	//
	// ⚠️ IT USED TO SAY "ONCE", AND THE GUARD THAT MADE IT ONCE LIVED HERE — a package-level `var
	// registeredOnce bool`, which is the wrong package to hold it. This package cannot see what the closure
	// does. `main`'s closure starts the drive tunnel, and `main` knows why a second one would be a defect
	// (Tunnel.Run is an infinite reconnect loop; one goroutine is enough) — so `main` holds a sync.Once and
	// states that reason where it is true. What this package can honestly report is a FACT it observes: the farm
	// accepted a registration. Whether acting on it twice is wrong is a property of the ACTION, and the action
	// belongs to the caller.
	//
	// So a hook that SHOULD run after a re-registration now does. The CallNotFound branch above re-registers
	// with the same identity when the farm has forgotten this node, and that path fires this hook like any
	// other — which is the question a reader of that branch was previously sent 130 lines away to answer, into
	// an unexported variable in another function.
	//
	// A caller that needs once-ness wraps its own closure. A caller that does not, gets every registration.
	OnRegistered func()
	// IosTeamID / IosWdaBundleID / IosSigningID are THIS MACHINE'S OWN WebDriverAgent signing identity, from the
	// person who started the agent (flags or environment — see the usage text). Empty = none declared, which is
	// the ordinary state of an Android-only machine and of a Mac whose owner has not set one up: its iPhones stay
	// present-but-not-offered rather than being offered as phones nothing can drive.
	IosTeamID      string
	IosWdaBundleID string
	IosSigningID   string
	// KeepWdaWarm / WdaIdleAfter / WdaProbe / WdaStop are PUTTING THE PHONE BACK THE WAY WE FOUND IT — see
	// wdaidle.go. WdaProbe nil = this build cannot ask, so nothing is ever stopped and the loop is byte-for-byte
	// what it was: a cleanup that cannot establish its precondition does not happen.
	KeepWdaWarm  bool
	WdaIdleAfter time.Duration
	WdaProbe     func(ctx context.Context, udid string, port uint16) wda.RunnerStatus
	WdaStop      func(ctx context.Context, udid string, port uint16) error
	// WdaPortFor is the port THIS MACHINE declares for one device's WebDriverAgent — the one number the
	// registration publishes and the probe asks on, so the two cannot disagree about what they are talking about.
	// nil = this machine declares none, and then nothing is published and nothing is probed.
	WdaPortFor func(ctx context.Context, udid string) uint16
	// DeviceFindings computes WHAT THIS MACHINE ALREADY CHECKED about its iOS devices — the doctor's answers,
	// reduced to per-udid wire findings (THE_CHECK_THAT_ALREADY_KNEW). nil = this build declares none.
	//
	// THE FRESHNESS RULE LIVES HERE, not at the producer: the checks shell out (xcodebuild takes seconds), so
	// running them every 10s heartbeat would be wrong — and a stale answer naming a cause that is no longer the
	// cause is worse than no answer. The loop recomputes when the cache is older than FindingsMaxAge and STAMPS
	// each finding with its computation time, so a consumer always knows the age and applies its own trust rule.
	DeviceFindings func(ctx context.Context, outcomes []inventory.Outcome) map[string][]farm.WireFinding
	// FindingsMaxAge is how old the findings cache may grow before the next heartbeat recomputes it.
	// <= 0 = DefaultFindingsMaxAge. The facts these checks read change only by human action on this machine
	// (installing a profile, editing the project, toggling Developer Mode), so minutes-old is honest — and the
	// age always rides, so honesty is checkable downstream.
	FindingsMaxAge time.Duration
	// LiveView decides one offered phone's live view on this machine (liveview.Judge over its reserved mirror port and
	// this machine's Appium drivers). Mirrors is the set the drive tunnel will stream, rewritten from the rows every
	// tick publishes — a phone that stops being offered stops being streamable that tick, and its stream ends. Both
	// nil = this build declares no mirror for any phone, and the node keeps saying it cannot stream.
	LiveView func(platform inventory.Platform, udid string) liveview.Availability
	Mirrors  *liveview.Offered
	// Phones is the set of phones the drive tunnel will start a session on, rewritten from the rows every tick publishes
	// (only rows for this machine's own phones; a cloud row is the hub's). nil = this build keeps no such set.
	Phones *allowed.Phones
	// HintAfter is how long a platform may stay could-not-ask before the narration offers --skip once
	// (default DefaultHintAfter). Now is the clock seam; nil = time.Now.
	HintAfter time.Duration
	Now       func() time.Time
	// Capture is this machine's network capture (capture.Controller): its Availability is the registration's capture
	// declaration. nil = Disabled, which is what a machine that never turned capture on says.
	Capture CaptureDeclaration
	// Install is how this agent was started (HowStarted), which decides how a newer release is said to be installed, and how
	// every command connect prints is spelled (NTH_AGENT_0_4_3). The zero value is "cannot tell": the npx form with @latest.
	Install Install
}

// CaptureDeclaration answers the machine's capture declaration — farm.CaptureDisabled, CaptureAvailable or
// CaptureUnavailable.
type CaptureDeclaration interface {
	Availability() string
}

// PhoneCaptureDeclaration is a CaptureDeclaration that also answers each phone's capture readiness
// (capture.Controller.Readiness): farm.PhoneCaptureReady, PhoneCaptureNotSetUp, or "" to say nothing. One that does not
// says nothing about any phone.
type PhoneCaptureDeclaration interface {
	Readiness(serial string) string
}

// phoneCaptureDeclared is this tick's per-phone capture readiness, or nil.
func phoneCaptureDeclared(cfg Config) farm.PhoneCaptureReadinessDeclaration {
	phones, ok := cfg.Capture.(PhoneCaptureDeclaration)
	if !ok {
		return nil
	}
	return func(_ farm.WirePlatform, id string) string { return phones.Readiness(id) }
}

// captureDeclared is this tick's capture declaration.
func captureDeclared(cfg Config) string {
	if cfg.Capture == nil {
		return farm.CaptureDisabled
	}
	return cfg.Capture.Availability()
}

// DefaultInterval — NodeTtlSeconds(30s) / 3. The relationship is the point; see Config.Interval.
const DefaultInterval = 10 * time.Second

// DefaultFindingsMaxAge — how old the doctor-findings cache may grow before a heartbeat recomputes it. Five
// minutes: thirty heartbeats between xcodebuild invocations (cheap enough to leave running on a laptop), and
// fresh enough that the worker's own trust window (which reads the stamped age, not this constant) still accepts
// a finding computed one cache-lifetime ago.
const DefaultFindingsMaxAge = 5 * time.Minute

// Driver wires the local Appium into the report: A DEVICE IS OFFERED ONLY WHEN SOMETHING CAN DRIVE
// IT — the probe must answer AND the tunnel must be connected on the same tick, or the report carries
// no rows (a flag is a claim, not a capability; a registered device with a dead driver is the phantom).
type Driver struct {
	AppiumURL string
	// Probe answers "is the local Appium alive right now" (appium.Status in production).
	Probe func(ctx context.Context) error
	// TunnelUp answers "is the drive tunnel connected right now" (Tunnel.Connected in production).
	TunnelUp func() bool
	// TunnelEverUp answers "has the drive tunnel ever been established" (Tunnel.EverConnected in
	// production) — the fact that separates "not connected yet" from "dropped" in what the reader is told.
	// nil reads as never.
	TunnelEverUp func() bool
	// Evidence is the drive tunnel's own state (tunnel.State in production): this loop tells it whether the farm
	// answered each registration and heartbeat, and asks it whether to say TunnelDown. nil = say TunnelOnly whenever a
	// tunnel is dialled, which is all this loop could say before.
	Evidence TunnelEvidence
}

// TunnelEvidence is what the heartbeat tells the drive tunnel's state and what it asks back — the node's
// RelayTunnelState contract (A_TUNNEL_THAT_KNOWS_IT_DIED): "down" is said only on evidence that the TUNNEL is the
// problem, never by a clock, because a farm restart drops the heartbeats too.
type TunnelEvidence interface {
	// FarmAnswered: a registration or heartbeat got an answer (a 404 "unknown node" included). While the tunnel is
	// down this also wakes its reconnect at once.
	FarmAnswered()
	// FarmTurnedAway: it got a 429 — an answer, at one of the farm's limits. Evidence as FarmAnswered is, but it does not
	// wake the reconnect: a 429 must not make the tunnel dial faster than it did when it counted as no answer.
	FarmTurnedAway()
	// FarmDidNotAnswer: it got none — a transport failure, or a 503 that a gateway gives for an absent farm too.
	FarmDidNotAnswer()
	// DeclareDown is this report's word: TunnelDown or not, and the tunnel's own account of why.
	DeclareDown() (down bool, why string)
}

// DefaultHintAfter — how long a platform fails before the narration offers --skip. Ten minutes: long
// enough that a rebooting daemon or a re-plugged cable has had every chance, short enough that a
// customer still watching their first session sees the way out. A permanent error the customer
// cannot act on becomes a one-time decision.
const DefaultHintAfter = 10 * time.Minute

// Run executes the loop. It returns nil on context end or MaxTicks, and an error only for the
// terminal cases (transport unsafe; auth refused; registration impossible).
func Run(ctx context.Context, cfg Config) error {
	// EVERYTHING THIS LOOP SAYS goes through the agent's terminal filter (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST).
	narrator := &narrator{out: farm.TerminalWriter(cfg.Out), hintAfter: cfg.HintAfter, now: cfg.Now, install: cfg.Install}
	if narrator.now == nil {
		narrator.now = time.Now
	}
	if narrator.hintAfter <= 0 {
		narrator.hintAfter = DefaultHintAfter
	}

	narrator.say("connecting to %s", cfg.Client.BaseURL)

	declared := captureDeclared(cfg)
	if err := registerWithBackoff(ctx, cfg, narrator); err != nil {
		return err
	}

	wdaIdle := WdaIdleTracker{After: cfg.WdaIdleAfter, KeepWarm: cfg.KeepWdaWarm}
	findings := &findingsCache{}
	ticks := 0
	for {
		if cfg.MaxTicks > 0 {
			ticks++
			if ticks > cfg.MaxTicks {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// CAPTURE IS A REGISTRATION FACT, and `nth-agent capture enable` or `disable` can change it while this runs: the farm
		// hears the new one only when this node registers, so it registers anew the tick it changes.
		if now := captureDeclared(cfg); now != declared {
			narrator.say("network capture is now %s on this machine — registering this node anew so the farm hears it", strings.ToLower(now))
			if err := registerWithBackoff(ctx, cfg, narrator); err != nil {
				return err
			}
			declared = now
		}

		outcomes := cfg.Probes(ctx)
		tidyWda(ctx, cfg, narrator, &wdaIdle, outcomes)
		report := buildReport(ctx, cfg, narrator, outcomes, findings)
		narrator.narrateOutcomes(outcomes)

		answer, status, err := cfg.Client.Heartbeat(ctx, cfg.NodeID, report)
		if stopping(ctx, err) {
			return nil
		}
		tellTheTunnel(cfg, status)
		switch status {
		case farm.CallOK:
			narrator.narrateWork(answer.HeartbeatWork)
			narrator.newerRelease(cfg.AgentVersion, answer.NewerAgentVersion, cfg.Install)
			narrator.recovered("heartbeat")
		case farm.CallUnauthorized:
			// TERMINAL. A refused token does not heal by retrying; hammering an auth failure is how a customer's
			// laptop becomes our incident. What is SAID about it is narrator.refused's — the same words as at
			// registration, the farm's reason when it gave one, and never a cause of the agent's own.
			narrator.refused(err)
			return fmt.Errorf("credential refused: %w", err)
		case farm.CallNotFound:
			// The farm did not apply this heartbeat, and this agent registers again; GF2 makes the same id safe. WHICH CAUSE IT WAS
			// IS THE FARM'S TO SAY (NTH_AGENT_0_4_2): the farm answers a 404 with "unknown node" (the row is gone, a purge) or with
			// "this node said it was leaving" (the goodbye of a stop, heard before this heartbeat). Its sentence when it gave one;
			// the old line, with its guess, only when it gave none.
			//
			// ⚠️ AND THIS RE-REGISTRATION FIRES OnRegistered AGAIN, which is the question a reader asks right
			// here and could not previously answer without finding an unexported flag in another function. It
			// is safe because the caller owns the once-ness: main's closure is wrapped in a sync.Once, so a
			// second call starts no second tunnel. See Config.OnRegistered.
			if _, reason, _ := farm.WhatTheFarmSaid(err); reason != "" {
				narrator.say("the farm did not apply this heartbeat (404). It says: “%s” — re-registering with the same identity", reason)
			} else {
				narrator.say("the farm forgot this node (it may have been purged) — re-registering with the same identity")
			}
			if err := registerWithBackoff(ctx, cfg, narrator); err != nil {
				return err
			}
			continue // heartbeat again without waiting a full interval
		case farm.CallBusy:
			// The farm said: nothing was applied, retry next tick. Doing MORE than waiting — like
			// re-registering — would undo the contention fix the farm just made.
			//
			// A 503 WITH NO ENVELOPE IS NOT THE FARM'S (NTH_AGENT_0_4_4 item 1): its address answered in its place, while the farm
			// restarts. The farm was not reached; that is said once, and the wait is the tick, as for every 503.
			if _, reason, answered := farm.WhatTheFarmSaid(err); !answered {
				narrator.transportOrAnswer("heartbeat", err, "retrying next tick")
			} else if reason != "" {
				narrator.sayOnChange("the farm did not apply this heartbeat (503). It says: “%s” — waiting for the next tick", reason)
			} else {
				narrator.sayOnChange("the farm is busy; nothing was applied this tick — waiting for the next one")
			}
		case farm.CallTooMany:
			// THE FARM ANSWERED, AT A LIMIT (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO). The farm asks the agent to back off, to a
			// minute, and never to re-register for it — and NEVER SOONER THAN THE TICK: the farm counted this heartbeat against
			// its pace (a 429 is counted), so one a second later is too soon and is answered 503 instead.
			narrator.notTaken("heartbeat", err)
			backoffSleep(ctx, cfg, narrator, cfg.Interval)
			continue
		case farm.CallUnsafeTransport:
			narrator.say("%v", err)
			return fmt.Errorf("unsafe transport: %w", err)
		case farm.CallTransport:
			narrator.transportOrAnswer("heartbeat", err, "retrying with backoff")
			backoffSleep(ctx, cfg, narrator, 0)
			continue
		}

		cfg.Sleep(ctx, cfg.Interval)
	}
}

// buildReport applies THE DRIVER GATE: device rows exist only when the probe answered AND the tunnel
// is up, both checked THIS tick — a dead Appium or a dropped tunnel empties the next report, and the
// farm's reconcile offlines the previously-registered rows (and frees their leases) on that report:
// the bounded path, not instantly and not never.
// findingsCache holds the last doctor-findings computation and when it happened — the loop's freshness state.
// A fresh cache is reused; an aged one is recomputed on the next tick that publishes devices. The stamp each
// finding CARRIES is its own computation time, so a consumer never depends on this cache being well-behaved.
type findingsCache struct {
	at     time.Time
	byUdid map[string][]farm.WireFinding
	primed bool
}

// declaredFindings resolves the FindingsDeclaration for this tick: recompute when the cache is stale (or never
// primed), reuse otherwise. Returns nil when this build computes none — AttachDevices then attaches none, which
// the worker reads as "the machine said nothing".
func declaredFindings(ctx context.Context, cfg Config, cache *findingsCache, outcomes []inventory.Outcome) farm.FindingsDeclaration {
	if cfg.DeviceFindings == nil || cache == nil {
		return nil
	}
	maxAge := cfg.FindingsMaxAge
	if maxAge <= 0 {
		maxAge = DefaultFindingsMaxAge
	}
	now := time.Now()
	if cfg.Now != nil {
		now = cfg.Now()
	}
	if !cache.primed || now.Sub(cache.at) >= maxAge {
		cache.byUdid = cfg.DeviceFindings(ctx, outcomes)
		cache.at = now
		cache.primed = true
	}
	byUdid := cache.byUdid
	return func(udid string) []farm.WireFinding { return byUdid[udid] }
}

func buildReport(ctx context.Context, cfg Config, narrator *narrator, outcomes []inventory.Outcome, findings *findingsCache) farm.Report {
	report := assembleReport(ctx, cfg, narrator, outcomes, findings)
	offerMirrors(cfg, report)
	offerPhones(cfg, report)
	return report
}

// offerPhones tells the drive tunnel which phones a session may be started on: exactly this machine's phones on the rows
// this report publishes. A phone missing from them — left out, unplugged, or every row withheld this tick — is no longer
// one the farm can start a session on (internal/allowed).
func offerPhones(cfg Config, report farm.Report) {
	if cfg.Phones == nil {
		return
	}
	var udids []string
	for _, device := range report.Devices {
		if device.Descriptor.CloudSpec == nil {
			udids = append(udids, device.Descriptor.Udid)
		}
	}
	cfg.Phones.Offer(udids)
}

// offerMirrors tells the drive tunnel which mirrors it may stream: exactly the ports on the rows this report
// publishes. A phone missing from them — unplugged, its platform gone quiet, or every row withheld because Appium or
// the tunnel is down — is withdrawn this tick, and any live view of it ends (liveview.Offered).
func offerMirrors(cfg Config, report farm.Report) {
	if cfg.Mirrors == nil {
		return
	}
	ports := map[string]uint16{}
	for _, device := range report.Devices {
		if device.Descriptor.MirrorPort != nil {
			ports[device.Descriptor.Udid] = uint16(*device.Descriptor.MirrorPort)
		}
	}
	cfg.Mirrors.Offer(ports)
}

// mirrorPortFor is the per-phone declaration AttachDevices asks for: the reserved mirror port when this machine can
// show the phone, nothing otherwise — liveview.Judge decides, from the port, the platform and this machine's drivers.
func mirrorPortFor(cfg Config) farm.MirrorPortDeclaration {
	if cfg.LiveView == nil || cfg.Mirrors == nil {
		return nil
	}
	return func(platform farm.WirePlatform, udid string) *int {
		judged := cfg.LiveView(inventoryPlatform(platform), udid)
		if !judged.Available {
			return nil
		}
		port := int(judged.Port)
		return &port
	}
}

func inventoryPlatform(platform farm.WirePlatform) inventory.Platform {
	if platform == farm.WireIos {
		return inventory.IOS
	}
	return inventory.Android
}

func assembleReport(ctx context.Context, cfg Config, narrator *narrator, outcomes []inventory.Outcome, findings *findingsCache) farm.Report {
	report := farm.BuildReport(outcomes)
	// THE DECLARATION COMES BEFORE THE DRIVER GATE, and unconditionally: it is a fact about the machine, not
	// about this tick's Appium. It is also what AttachDevices reads to decide whether an iPhone may be offered,
	// so stamping it here means the gate and the declaration can never disagree — there is one field.
	report = farm.DeclareIosSigning(report, cfg.IosTeamID, cfg.IosWdaBundleID, cfg.IosSigningID)
	// WHICH BUILD IS TALKING. `--version` answers the person standing at the machine, and nobody is ever standing
	// at the machine — so the same value crosses to the farm, where the operator can read it. Blank declares
	// nothing; see farm.DeclareAgentVersion.
	report = farm.DeclareAgentVersion(report, cfg.AgentVersion)
	// WHETHER THIS MACHINE CAPTURES — off, on, or on and unable — decided by internal/capture from the machine's own state.
	report = farm.DeclareCapture(report, captureDeclared(cfg))
	// …AND, PER PRESENT PHONE, WHETHER IT IS SET UP FOR IT HERE — so the farm can say "not set up" before a run starts.
	report = farm.DeclarePhoneCapture(report, phoneCaptureDeclared(cfg))
	// CAN THIS MACHINE SHOW A PHONE AT ALL — only with a local Appium to start the phone's screen server, and only
	// in a build that keeps the mirrors the tunnel streams from. Which phones: their rows' MirrorPort, below.
	report = farm.DeclareLiveView(report, cfg.Driver != nil && cfg.Driver.AppiumURL != "" && cfg.LiveView != nil && cfg.Mirrors != nil)
	// TUNNEL-ONLY, OR TUNNEL-DOWN ON EVIDENCE — asked ONCE per report (the answer is a declaration the farm then
	// hears or does not), and applied at every exit below that declares reachability at all.
	reachability := farm.DeclareTunnelOnly
	if cfg.Driver != nil {
		reachability = declaredReachability(cfg, narrator)
	}

	// ⚠️ CLOUD ROWS ARE ATTACHED AT EVERY EXIT, AND THIS IS WHY IT IS A CLOSURE. The first version attached them
	// once, before the local gate — and a MIXED agent published none of them, because AttachDevices does not
	// append: it builds its own slice and ASSIGNS report.Devices (report.go), so it discarded whatever was
	// already there. The comment then said "the local door appends to the same slice", which was a claim about
	// code nobody had read. Every return below goes through here instead.
	//
	// THE GATE IS THE TUNNEL, AND ONLY THE TUNNEL. A consumer reaches a cloud device through this agent exactly
	// as it reaches a local one, so a row published while the tunnel is down would be the phantom the driver gate
	// exists to prevent. What it does NOT need is a local Appium — see farm.AttachCloudDevices for why the local
	// prerequisite's own reason does not reach this path.
	withCloud := func(r farm.Report) farm.Report {
		if len(cfg.CloudDevices) == 0 || cfg.Driver == nil || !cfg.Driver.TunnelUp() {
			return r
		}
		return farm.AttachCloudDevices(reachability(r), cfg.CloudDevices)
	}

	if cfg.Driver == nil {
		// THE ONE THING STANDING BETWEEN A CUSTOMER AND A WORKING PRODUCT USED TO BE THE ONE THING WE WERE
		// SILENT ABOUT. This branch published no device rows and said nothing at all — no outcome, no warning,
		// no line in the transcript — so a person watching their phone appear on the Devices screen and then
		// fail to be pickable had nothing to read. The report itself is unchanged, byte for byte; what changed
		// is that the agent now says why it is empty.
		narrator.noDriver()
		return withCloud(report)
	}
	// A CLOUD-ONLY AGENT HAS NOTHING LOCAL TO PROBE, and running the local gate for it would narrate a missing
	// Appium every tick at a customer who was never asked for one.
	if cfg.Driver.AppiumURL == "" {
		return withCloud(reachability(report))
	}
	// REACHABILITY IS A FACT ABOUT THE NODE, NOT ABOUT THIS POD. A driver exists exactly when main dialed
	// the tunnel (one block, one condition: --appium), and an agent that dials a tunnel is an agent whose
	// devices are reachable ONLY through it — so the declaration is derived from the driver's existence,
	// not from a second flag that could one day disagree with whether the tunnel is dialed. It is stated
	// every tick a driver exists, up or down: the machine's shape does not change with the socket.
	report = reachability(report)
	if err := cfg.Driver.Probe(ctx); err != nil {
		// ANSWERED, AND NOT YOURS — or not on this machine: the agent did not use it (internal/localconn), and "start
		// Appium there" would send the person to start something that is running already.
		var notOurs *localconn.NotThisAccount
		var elsewhere *localconn.NotOnThisMachine
		switch {
		case errors.As(err, &notOurs):
			narrator.driverDown(fmt.Sprintf("the Appium endpoint at %s was not used: %s", cfg.Driver.AppiumURL, notOurs),
				"start Appium yourself, as this account, on that port — the agent sends runs only to an Appium you run")
			return withCloud(report)
		case errors.As(err, &elsewhere):
			narrator.driverDown(fmt.Sprintf("the Appium endpoint at %s was not used: %s", cfg.Driver.AppiumURL, elsewhere),
				AppiumNotHereRemedy(cfg.Install))
			return withCloud(report)
		}
		narrator.driverDown(fmt.Sprintf("the Appium endpoint at %s is not answering (%v)", cfg.Driver.AppiumURL, err),
			"start Appium there, then the devices are offered automatically — `"+cfg.Install.Line("doctor")+"` reports what this machine has. A local Appium is a prerequisite and stays one: we do not install software on a machine we do not own")
		return withCloud(report)
	}
	if !cfg.Driver.TunnelUp() {
		// A FIRST-TIME CUSTOMER'S TRANSCRIPT: on the first tick the tunnel has never connected (it is dialled
		// after registration), and "reconnects … until it is back" describes a history that did not happen.
		if cfg.Driver.TunnelEverUp != nil && cfg.Driver.TunnelEverUp() {
			narrator.driverDown("the drive tunnel is not connected",
				"nothing to do — the agent reconnects with backoff; devices stay unoffered until the tunnel is back")
		} else {
			// NOT A FAULT, AND NOT SAID AS ONE (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 5). main dials the tunnel once the farm has
			// accepted a registration, so the registration's own report always gets here: this used to print "devices are
			// NOT being offered" with "what to do: nothing to do" at every start with --appium.
			narrator.driverWaiting()
		}
		return withCloud(report)
	}
	narrator.driverUp()
	// AFTER the local door, never before: AttachDevices assigns report.Devices rather than appending to it.
	return withCloud(farm.AttachDevices(report, outcomes, cfg.Driver.AppiumURL, func(udid string) *int {
		if cfg.WdaPortFor == nil {
			return nil
		}
		port := int(cfg.WdaPortFor(ctx, udid))
		return &port
	}, declaredFindings(ctx, cfg, findings, outcomes), mirrorPortFor(cfg)))
}

// tellTheTunnel hands the farm's answer to the drive tunnel's state — the node's rule for which answers count: one the
// farm gave (2xx, or a 404 "unknown node") says it can be reached; a transport failure or a 503 — which a gateway in
// front of an absent farm gives as well — says nothing about the tunnel. Only the farm's own calls count, never the
// rest of the tick. The two terminal answers end the loop and are not evidence of anything.
//
// A 429 IS AN ANSWER (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO): the farm is there and says this machine is at a limit. It
// counts as one, without waking the reconnect (TunnelEvidence.FarmTurnedAway). Any other non-2xx — nginx's 413, a 400 —
// stays what it was, no answer about the tunnel, as its status stays CallTransport.
func tellTheTunnel(cfg Config, status farm.CallStatus) {
	if cfg.Driver == nil || cfg.Driver.Evidence == nil {
		return
	}
	switch status {
	case farm.CallOK, farm.CallNotFound:
		cfg.Driver.Evidence.FarmAnswered()
	case farm.CallTooMany:
		cfg.Driver.Evidence.FarmTurnedAway()
	case farm.CallBusy, farm.CallTransport:
		cfg.Driver.Evidence.FarmDidNotAnswer()
	}
}

// declaredReachability is THE ONE PLACE this loop words its reachability, every report, when a tunnel is dialled:
// TunnelDown when the tunnel's state has the evidence, else TunnelOnly — and the change said once each way.
func declaredReachability(cfg Config, n *narrator) func(farm.Report) farm.Report {
	down := false
	if cfg.Driver != nil && cfg.Driver.Evidence != nil {
		var why string
		down, why = cfg.Driver.Evidence.DeclareDown()
		n.tunnelDown(down, why, cfg.Driver.TunnelUp != nil && cfg.Driver.TunnelUp())
	}
	return func(r farm.Report) farm.Report {
		if down {
			return farm.DeclareTunnelDown(r)
		}
		return farm.DeclareTunnelOnly(r)
	}
}

func registerWithBackoff(ctx context.Context, cfg Config, narrator *narrator) error {
	// Registration computes its own findings (a fresh process has no cache to reuse) — the first report a farm
	// sees from this machine already says what the machine already checked.
	cache := &findingsCache{}
	for {
		outcomes := cfg.Probes(ctx)
		report := buildReport(ctx, cfg, narrator, outcomes, cache)
		confirmed, status, err := cfg.Client.Register(ctx, cfg.NodeID, cfg.HostName, report)
		if stopping(ctx, err) {
			return nil
		}
		tellTheTunnel(cfg, status)
		switch status {
		case farm.CallOK:
			// THE ID THIS AGENT SENT, AND ONLY IT. Today's farm answers with the id it was sent; a server that
			// answers with anything else — another id, or escape sequences in its place — is not printed, and this agent
			// goes on as the node it is.
			narrator.say("registered as node %s", cfg.NodeID)
			if confirmed.NodeID != "" && !strings.EqualFold(confirmed.NodeID, cfg.NodeID) {
				narrator.say("the farm answered the registration with a different node id than the one this agent sent — this agent keeps its own id and does not print the farm's")
			}
			narrator.newerRelease(cfg.AgentVersion, confirmed.NewerAgentVersion, cfg.Install)
			narrator.narrateOutcomes(outcomes)
			narrator.recovered("registration")
			// EVERY successful registration, including the re-registration the CallNotFound branch performs.
			// See Config.OnRegistered for why the once-ness moved to the caller.
			if cfg.OnRegistered != nil {
				cfg.OnRegistered()
			}
			return nil
		case farm.CallUnauthorized:
			narrator.refused(err)
			return fmt.Errorf("credential refused: %w", err)
		case farm.CallNotFound:
			// Register answering 404 means the id names a row owned by somebody else — a state this
			// machine cannot fix by itself. IT IS THE ONLY CAUSE (NTH_AGENT_0_4_2), so the line says it without "may": the farm
			// refuses a registration with a 404 only for an id whose row exists and whose owner is not the token's workspace
			// (RegisterNodeAsync's guarded write; RequireNodeOwnershipAttribute's NodeIdIsFreeOrOwnedByAsync — an unborn id is
			// how every node is born). The owner can be another workspace or the farm's own rack, so the line names neither.
			// The farm's sentence ("Re-register.") is the same at every node door on purpose, and is the wrong advice here.
			// STOP FIRST (A_MACHINE_THAT_STAYS_THE_CUSTOMERS): this advice, followed while an earlier connect still
			// ran, was how one folder came to hold two identities offering the same phones.
			narrator.say("the farm refused this node's identity: this id is already registered at the farm, but not to the workspace of this agent's token. First stop every npx nthcase-agent connect running on this machine; then remove the node-id file in the agent's config folder to mint a fresh identity, or contact your operator.")
			return fmt.Errorf("identity refused: %w", err)
		case farm.CallBusy:
			// A 503 with no envelope is the farm's address answering in its place (NTH_AGENT_0_4_4), as at the heartbeat.
			if _, reason, answered := farm.WhatTheFarmSaid(err); !answered {
				narrator.transportOrAnswer("registration", err, "retrying next tick")
			} else if reason != "" {
				narrator.sayOnChange("the farm did not apply this registration (503). It says: “%s” — retrying registration next tick", reason)
			} else {
				narrator.sayOnChange("the farm is busy — retrying registration next tick")
			}
			cfg.Sleep(ctx, cfg.Interval)
		case farm.CallTooMany:
			// THE FARM ANSWERED, AT A LIMIT: a new machine past its workspace's machines, or a report past one machine's devices.
			// Backoff, 1 s doubling to a minute, as the farm asks; never a stop. Nothing paces a registration at the farm, so
			// the backoff starts at its base here.
			narrator.notTaken("registration", err)
			backoffSleep(ctx, cfg, narrator, 0)
		case farm.CallUnsafeTransport:
			// TERMINAL, like the revoked token: retrying never turns http into https. The client's own
			// pre-flight refused before anything crossed the wire; this is the one narration home.
			narrator.say("%v", err)
			return fmt.Errorf("unsafe transport: %w", err)
		case farm.CallTransport:
			narrator.transportOrAnswer("registration", err, "retrying with backoff")
			backoffSleep(ctx, cfg, narrator, 0)
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

// stopping is a call that failed because this agent is stopping: Ctrl-C cancelled it in flight (NTH_AGENT_0_4_4 item 2). It
// is said nowhere — not "cannot reach the farm", which the farm was not, and not "retrying", which nothing will — and the
// tunnel's state is told nothing. The stop's own lines (the hand-back, the goodbye) are main's.
func stopping(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() != nil && (errors.Is(err, ctx.Err()) || errors.Is(err, context.Cause(ctx)))
}

// backoffSleep waits the next backoff — never less than floor (0 = none), before its jitter.
func backoffSleep(ctx context.Context, cfg Config, n *narrator, floor time.Duration) {
	d := cfg.BackoffBase << n.failures
	if d > cfg.BackoffCeiling || d <= 0 {
		d = cfg.BackoffCeiling
	}
	if d < floor {
		d = floor
	}
	if n.failures < 30 {
		n.failures++
	}
	// Jitter: up to +50%, so a fleet that lost the same network moment does not retry in unison.
	d += time.Duration(rand.Int63n(int64(d)/2 + 1))
	cfg.Sleep(ctx, d)
}

// narrator prints STATE CHANGES, never ticks.
type narrator struct {
	out         io.Writer
	last        []inventory.Outcome
	lastWork    farm.HeartbeatWork
	down        bool
	failures    uint
	hintAfter   time.Duration
	now         func() time.Time
	failing     map[inventory.Platform]time.Time
	hinted      map[inventory.Platform]bool
	driverWasUp *bool
	wdaSaid     bool
	saidDown    bool            // the reports now declare TunnelDown, and that was said
	leftOutSaid map[string]bool // each left-out phone is named once per run (sayLeftOut)
	newerSaid   string          // the newer release last said (newerRelease), so each is said once
	saidAnswer  string          // the line last said for an answer the farm did not take a call with (sayOnChange); "" = none
	install     Install         // how this agent was started: every command it says is spelled from it (Config.Install)
}

// tunnelDown says ONCE when this agent starts telling the farm its tunnel is down, and once when it stops — the two
// heartbeats the farm acts on (it takes the devices off offer, then offers them).
//
// NEITHER SENTENCE DESCRIBES A PAST: "down" can be declared on a first run too — a tunnel that was never opened, while
// the farm answers every heartbeat, is the same evidence — so the words are "could not be opened" and "is up", which
// are true of a first run and of every outage after it.
//
// ⚠️ AND "DOWN" CAN STOP BEING SAID WITHOUT THE TUNNEL COMING UP: when the heartbeat that carried it got no answer, the
// farm never heard it, and the evidence is void (tunnel.State.FarmDidNotAnswer — the farm may be the one away). So the
// second sentence asks the tunnel, rather than assuming the happy reason.
func (n *narrator) tunnelDown(down bool, why string, tunnelUp bool) {
	switch {
	case down && !n.saidDown:
		n.saidDown = true
		if why == "" {
			why = "no reason recorded"
		}
		n.say("the drive tunnel is down and could not be opened although the farm answers (%s) — this agent now tells the "+
			"farm so, and the farm keeps this machine's devices off offer until the tunnel is up", why)
	case !down && n.saidDown && tunnelUp:
		n.saidDown = false
		n.say("the drive tunnel is up — the farm is told so, and offers this machine's devices")
	case !down && n.saidDown:
		n.saidDown = false
		n.say("the farm did not answer the heartbeat that said the drive tunnel is down, so that is no longer evidence " +
			"about the tunnel — this agent says tunnel-only until the farm answers")
	}
}

// wdaPolicy says ONCE what this agent leaves running on a phone and when it stops it. Once, because a person needs
// to hear it before their status bar tells them, and a line every ten seconds is a line nobody reads — the same
// state-change discipline every other sentence in this narrator follows.
func (n *narrator) wdaPolicy(statement string) {
	if n.wdaSaid {
		return
	}
	n.wdaSaid = true
	n.say("%s", statement)
}

// say writes one line. An error in it is folded to one line first: its text can be a farm's or a network's, and a line break
// in it would be a line nobody wrote. The writer strips what is left (farm.TerminalWriter).
func (n *narrator) say(format string, args ...any) {
	for i, arg := range args {
		if err, ok := arg.(error); ok {
			args[i] = farm.OneLineForTerminal(err.Error())
		}
	}
	fmt.Fprintf(n.out, format+"\n", args...)
}

// refused is THE ONE PLACE a refused credential becomes words — at registration and at heartbeat alike
// (AN_AGENT_THAT_GUESSES_WHY_IT_WAS_REFUSED).
//
// THE FARM KNOWS WHY; THE AGENT KNOWS WHAT TO DO NEXT, and neither says the other's part. The first line is the
// farm's reason, quoted — or the plain fact that it gave none this agent could read. The second is the remedy, and
// the remedy names NO CAUSE: this agent used to print "it may have been revoked", a guess, and the commonest refusal
// is now a token that simply reached the end it was given. Not knowing why is a fact the agent can state; guessing
// why is the defect.
//
// ⚠️ The farm's words ride as an ARGUMENT, never as the format: a server's sentence containing `%s` or `%v` must
// print as itself. They arrive already made safe for a terminal (farm.SafeForTerminal).
func (n *narrator) refused(err error) {
	var refusal *farm.RefusedError
	if errors.As(err, &refusal) && refusal.Reason != "" {
		n.say("the farm refused this agent's token. It says: “%s”", refusal.Reason)
	} else {
		n.say("the farm refused this agent's token and gave no reason this agent could read.")
	}
	n.say("put a new token from your workspace in NTH_AGENT_TOKEN (a workspace owner creates one in Settings → Agent tokens), then restart the agent.")
}

// narrateOutcomes prints each platform's situation when it CHANGES from the last printed one — and
// offers the --skip switch ONCE when a platform has failed for hintAfter (that check runs every tick,
// before the unchanged-state early return, or a permanently-failing platform would never age).
func (n *narrator) narrateOutcomes(outcomes []inventory.Outcome) {
	n.offerSkipHints(outcomes)
	n.sayLeftOut(outcomes)
	if reflect.DeepEqual(outcomes, n.last) {
		return
	}
	for _, o := range outcomes {
		switch {
		case o.IsAnswered():
			n.say("%s: %d usable, %d attached but unusable", o.Platform(), len(o.Offerable()), len(o.Unusable()))
			for _, u := range o.Unusable() {
				n.say("  %s [%s] — %s", u.ID, u.State, u.Remedy)
			}
		case o.IsCouldNotAsk():
			n.say("%s: could not check — %s", o.Platform(), o.Reason())
			n.say("  what to do: %s", o.Remedy())
		default:
			n.say("%s: not checked on this host", o.Platform())
		}
	}
	n.last = outcomes
}

// sayLeftOut names, ONCE per phone per run, each phone a probe saw and did not offer because it is not on this machine's
// USB cable, how it is reached, and the flag that offers it (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1). It runs
// before the unchanged-state return, so a phone that appears later is still named when it does.
func (n *narrator) sayLeftOut(outcomes []inventory.Outcome) {
	if n.leftOutSaid == nil {
		n.leftOutSaid = map[string]bool{}
	}
	var lines []string
	for _, o := range outcomes {
		for _, d := range o.LeftOut() {
			key := string(o.Platform()) + " " + d.ID
			if n.leftOutSaid[key] {
				continue
			}
			n.leftOutSaid[key] = true
			lines = append(lines, fmt.Sprintf("  %s %s — %s", o.Platform(), d.ID, d.How))
		}
	}
	if len(lines) == 0 {
		return
	}
	n.say("not offered, because the agent offers the farm only phones on this machine's USB cable:")
	for _, line := range lines {
		n.say("%s", line)
	}
	n.say("  %s", OfferUncabledRemedy(n.install))
}

// AppiumNotHereRemedy is what to do about an Appium endpoint that answered from another machine: start connect again with
// this machine's own. One of the restart lines NTH_AGENT_0_4_3 holds for every way the agent can be started.
func AppiumNotHereRemedy(install Install) string {
	return "run Appium on this machine and restart with its local address: " + install.Line("connect --appium http://127.0.0.1:4723")
}

// OfferUncabledRemedy is how to offer the phones left out because they are not on this machine's USB cable: start connect
// again with the flag. A restart line, like AppiumNotHereRemedy.
func OfferUncabledRemedy(install Install) string {
	return "to offer these too, restart with: " + install.Line("connect … "+inventory.OfferUncabledFlag)
}

// narrateWork prints assigned work — which this slice cannot perform. Silently discarding a task
// list would be the farm believing work is underway that never started.
func (n *narrator) narrateWork(work farm.HeartbeatWork) {
	if work == n.lastWork {
		return
	}
	if work.ProvisionTasks > 0 || work.Commands > 0 {
		n.say("the farm assigned work this agent cannot perform yet (%d provision task(s), %d command(s)) — it stays queued on the farm; a future agent version will pull it",
			work.ProvisionTasks, work.Commands)
	}
	n.lastWork = work
}

// offerSkipHints turns a sustained could-not-ask into a one-time decision: after hintAfter of
// continuous failure the narration names the switch, once per run per platform. Answering (or being
// switched off) resets the clock.
func (n *narrator) offerSkipHints(outcomes []inventory.Outcome) {
	if n.failing == nil {
		n.failing = map[inventory.Platform]time.Time{}
		n.hinted = map[inventory.Platform]bool{}
	}
	now := n.now()
	for _, o := range outcomes {
		platform := o.Platform()
		if !o.IsCouldNotAsk() {
			delete(n.failing, platform)
			n.hinted[platform] = false
			continue
		}
		since, known := n.failing[platform]
		if !known {
			n.failing[platform] = now
			continue
		}
		if !n.hinted[platform] && now.Sub(since) >= n.hintAfter {
			n.hinted[platform] = true
			n.say("%s has not answered for %s — if this machine will never drive %s devices, start the agent with --skip %s and it will stop asking.",
				platform, now.Sub(since).Round(time.Second), platform, platform)
		}
	}
}

// noDriver narrates the ONE fact this path used to keep to itself. It shares driverWasUp with driverDown so
// it is said once rather than every tick, and so a later --appium run's driverUp still reads as a change.
//
// IT DOES NOT GO LOOKING FOR AN APPIUM IT WAS NOT TOLD ABOUT. Guessing an endpoint and dialling it is a
// different decision from reporting one; this is the reporting one, and `doctor` is where a person finds out
// what this machine has.
func (n *narrator) noDriver() {
	if n.driverWasUp == nil || *n.driverWasUp {
		down := false
		n.driverWasUp = &down
		n.say("devices are not being offered — this agent was started without --appium, so nothing attached here can be driven")
		n.say("  what to do: run `%s` to see what this machine is missing, then restart with --appium URL", n.install.Line("doctor"))
		n.say("  your phones stay visible on the Devices screen either way — they are reported as present, just not offered")
	}
}

// driverDown / driverUp narrate the drive path's state ONCE per change.
func (n *narrator) driverDown(reason, remedy string) {
	if n.driverWasUp == nil || *n.driverWasUp {
		down := false
		n.driverWasUp = &down
		n.say("devices are not being offered — %s", reason)
		n.say("  what to do: %s", remedy)
	}
}

// driverWaiting is the drive path not up YET, on a start: the same state as driverDown, said once, as what happens next.
func (n *narrator) driverWaiting() {
	if n.driverWasUp == nil || *n.driverWasUp {
		down := false
		n.driverWasUp = &down
		n.say("the drive tunnel has not connected yet — it opens once this node is registered, and this machine's devices are offered on the first heartbeat after it connects")
	}
}

func (n *narrator) driverUp() {
	if n.driverWasUp == nil || !*n.driverWasUp {
		up := true
		n.driverWasUp = &up
		n.say("the drive path is ready — offerable devices are now registered for leasing")
	}
}

// transportDown says ONCE that the farm cannot be reached, and what this agent does next (then): "retrying with backoff", or
// "retrying next tick" for a gateway's 503, which waits for the tick as every 503 does.
func (n *narrator) transportDown(err error, then string) {
	if !n.down {
		n.say("cannot reach the farm (%v) — %s", err, then)
		n.down = true
	}
	n.saidAnswer = ""
}

// transportOrAnswer is a CallTransport: no answer at all, or an answer this client has no status of its own for — nginx's
// 413, a 400, the farm's own 502. AN ANSWER CAME BACK, so it is never "cannot reach the farm" (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO).
//
// A 2xx THIS AGENT COULD NOT USE is an answer too (NTH_AGENT_0_4_3 item 5): the farm took the call, so the line says it
// answered, with its status and what was wrong with the answer. Once, like the farm's refusals; the backoff is as before.
//
// A 502, 503 OR 504 WITH NO ENVELOPE IS NO ANSWER OF THE FARM'S (NTH_AGENT_0_4_4 item 1): its address answered in its place
// (farm.GatewayError), so WhatTheFarmSaid does not read it, and it is said as the farm not reached — "connection to the farm
// restored" when it is back, never "the farm took …". A gateway's 503 comes here from the CallBusy branches, with its own then.
func (n *narrator) transportOrAnswer(call string, err error, then string) {
	if _, _, answered := farm.WhatTheFarmSaid(err); answered {
		n.notTaken(call, err)
		return
	}
	var unusable *farm.UnusableAnswerError
	if errors.As(err, &unusable) {
		what := "it " + unusable.What
		if unusable.Err != nil {
			what += " (" + unusable.Err.Error() + ")"
		}
		n.sayOnChange("the farm answered this machine's %s (%d), and this agent could not use the answer: %s — retrying with backoff",
			call, unusable.Status, farm.OneLineForTerminal(what))
		return
	}
	n.transportDown(err, then)
}

// notTaken says that the farm answered a call and did not take it, with its status and its sentence — or that it gave none
// this agent could read. The farm's sentence says what to do; the agent says what it does. Once (sayOnChange).
func (n *narrator) notTaken(call string, err error) {
	status, reason, _ := farm.WhatTheFarmSaid(err)
	if reason == "" {
		n.sayOnChange("the farm did not take this machine's %s (%d) and gave no reason this agent could read — retrying with backoff", call, status)
		return
	}
	n.sayOnChange("the farm did not take this machine's %s (%d). It says: “%s” — retrying with backoff", call, status, reason)
}

// sayOnChange says a line about an answer the farm did not take a call with ONCE: when it starts, and again only when it
// changes — another sentence from the farm, another status, another call. A machine left at a limit for a day used to print
// a line every tick at a 503; it prints a handful now. The farm answered, so a "cannot reach" outage is over.
//
// ⚠️ The farm's words ride as ARGUMENTS, never in the format: a sentence holding `%s` prints as itself.
func (n *narrator) sayOnChange(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	n.down = false
	if line == n.saidAnswer {
		return
	}
	n.saidAnswer = line
	n.say("%s", line)
}

// recovered is a call the farm took. ONCE, when an outage or a refusal ends, it says so — "connection to the farm restored"
// after the farm could not be reached, and that the farm took the call after it had answered without taking it.
func (n *narrator) recovered(call string) {
	switch {
	case n.down:
		n.say("connection to the farm restored")
	case n.saidAnswer != "":
		n.say("the farm took this machine's %s", call)
	}
	n.down = false
	n.saidAnswer = ""
	n.failures = 0
}
