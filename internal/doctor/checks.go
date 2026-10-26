package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// WHERE EACH REQUIREMENT'S JUSTIFICATION LIVES — on the check itself, in `Need`, because a requirement nobody
// can trace to a caller is a requirement somebody guessed. Every check Diagnose appends carries one, a census in
// the seals refuses one that does not, and a person can now READ them: `doctor --why` prints each check's reason
// beside its caveats (THE_REASON_THE_DOCTOR_NEVER_SAYS), and `--json` has carried the field all along.
//
// THIS HEADER USED TO SAY THE REASONS A SECOND TIME, BY HAND, AND HAD ALREADY DRIFTED: nine entries against
// twelve appended checks — the signing identity, the WebDriverAgent project and the WebDriverAgent port were
// never added to it. The enumeration is retired rather than guarded, for the reason the last slices kept
// meeting: a hand-kept restatement beside a machine-checked source always rots, because only one of the two
// fails a build. Deeper provenance stays where it is load-bearing — on the check functions themselves (the
// xcodebuild whose-requirement paragraph, the provisioning check's measured history). What stays HERE is the one
// decision no check can carry, because it is a decision NOT to have one:
//
//   usbmuxd    internal/ios/goios.go: go-ios talks to usbmuxd for every iOS read. Named, NOT a check and not
//              remedied: on macOS it ships with the OS, and the device probe's own CouldNotAsk already carries
//              the remedy for a dead one — a second sentence here would be a second opinion.
//
// ANYTHING NOT ESTABLISHED IS Unknown, never Satisfied.

// Diagnose runs every check. outcomes are the SAME probe results `devices` reports, and they are what scopes the
// answer: a machine with no iPhone attached and no toolchain is not broken, it is an Android machine, so its iOS
// gaps are notes rather than failures.
func Diagnose(ctx context.Context, env Env, outcomes []inventory.Outcome) Report {
	report := Report{}
	// FIRST, because it is the most upstream fact this command can state: a phone in a state no run can use makes
	// every toolchain answer below it moot for that phone, and when it blocks it should LEAD the blockers.
	report.Checks = append(report.Checks, unusableDevices(outcomes), leftOutDevices(outcomes))
	report.Checks = append(report.Checks, adbBinary(ctx, env), adbServer(env))
	report.Checks = append(report.Checks, appiumBinary(ctx, env), appiumEndpoint(ctx, env))
	report.Checks = append(report.Checks, appiumDriver(ctx, env, "uiautomator2", inventory.Android, outcomes))
	// iOS gets the SAME shape Android gets, scoped the same way. It used to get one note that returned Unknown and
	// declined — and whose remedy said the phone "is not offered for runs", which was false: this agent publishes
	// iOS offerables through farm.AttachDevices and the product offers them. A diagnostic that contradicts the
	// product teaches a person to disbelieve the screen that is telling the truth.
	report.Checks = append(report.Checks, appiumDriver(ctx, env, "xcuitest", inventory.IOS, outcomes))
	report.Checks = append(report.Checks, xcodeBuildTools(ctx, env, outcomes))
	report.Checks = append(report.Checks, iosDeveloperMode(ctx, env, outcomes))
	// WHAT THIS MAC CAN SIGN AS, read before the profile check that depends on the same declaration — so a person
	// whose team id is really a certificate's code reads that sentence first (item 3).
	report.Checks = append(report.Checks, iosSigningIdentity(ctx, env, outcomes))
	report.Checks = append(report.Checks, iosProvisioningProfile(ctx, env, outcomes))
	report.Checks = append(report.Checks, iosWdaProjectTargets(ctx, env, outcomes))
	report.Checks = append(report.Checks, iosWdaPort(ctx, env, outcomes))
	// LAST, and one per phone: whether a run on it can be watched. It never stops a run — a phone with no mirror is
	// still driven — so a "no" is Advisory, and it sits after every check that can.
	report.Checks = append(report.Checks, liveViews(ctx, env, outcomes)...)
	return report
}

// liveViewNeed and liveViewLimits are every live-view check's own two sentences.
const (
	liveViewNeed = "a person watching a run sees this phone through its screen server — Appium's MJPEG broadcast, on a " +
		"port this machine reserves for the phone and the drive tunnel streams (internal/liveview, internal/tunnel/stream.go)"
	liveViewLimits = "this checks that the port is free on this machine right now and what Appium's driver list says; a " +
		"port free now can be taken before a run, and whether the screen server starts is only known when a session asks for it"
)

// liveViews answers, ONE LINE PER USABLE PHONE, whether a run on it can be watched — liveview.Judge, the same function
// the connect loop declares each phone's mirror port from, so `doctor`, `devices` and the farm cannot disagree about a
// phone. The driver list is read once for all of them.
func liveViews(ctx context.Context, env Env, outcomes []inventory.Outcome) []Check {
	type phone struct {
		platform inventory.Platform
		id       string
	}
	var phones []phone
	for _, o := range outcomes {
		if !o.IsAnswered() {
			continue
		}
		for _, d := range o.Offerable() {
			phones = append(phones, phone{o.Platform(), d.ID})
		}
	}
	if len(phones) == 0 {
		return []Check{{Name: "live view", Need: liveViewNeed, State: NotRequired,
			Detail: "no usable phone was seen on this machine, so there is nothing to watch"}}
	}

	var drivers liveview.Drivers
	if env.LookPath != nil && env.Run != nil {
		drivers = liveview.ReadDrivers(ctx, env.LookPath, env.Run)
	} else {
		drivers = liveview.Drivers{Why: "this build has no way to read Appium's driver list"}
	}
	checks := make([]Check, 0, len(phones))
	for _, p := range phones {
		c := Check{Name: "live view " + p.id, Need: liveViewNeed, Limits: liveViewLimits}
		if env.MirrorPortFor == nil {
			c.State = Unknown
			c.Detail = "this build has no way to check which ports are free here"
			checks = append(checks, c)
			continue
		}
		judged := liveview.Judge(liveview.Facts{Platform: p.platform, Reservation: env.MirrorPortFor(p.id), Drivers: drivers})
		// THE SAME SENTENCE `devices` PRINTS (Availability.Reason), in the doctor's own shape: the headline says ready
		// or missing, the remedy gets its own "what to do" line, and what a yes did not check joins the limits.
		c.Detail = judged.Reason
		if judged.Available {
			c.State = Satisfied
			if judged.Unchecked != "" {
				c.Limits = judged.Unchecked + "; " + liveViewLimits
			}
		} else {
			// TRUE AND WORTH SAYING, AND IT DOES NOT STOP A RUN: the phone is still driven, only not shown.
			c.State = Missing
			c.Advisory = true
			c.Remedy = judged.Remedy
			if c.Remedy == "" {
				c.Remedy = "nothing on this machine changes this — a run on this phone is driven and not shown; watch the phone itself"
			}
		}
		checks = append(checks, c)
	}
	return checks
}

// unusableDevices names every device the probe SAW and could not use — a fact Diagnose has held in its argument
// list all along and used for exactly one thing: platformIsInPlay's scope vote. The device itself reached no
// check, so a person whose only phone sat "unauthorized" read every toolchain line green and "Nothing here is
// stopping a run" — and then their run found no device. The answer was in the argument list.
//
// ONE CHECK, NOT ONE PER PLATFORM. An unauthorized iPhone and an unauthorized Android are the same fact with
// different words, and the remedy is the PROBE'S, carried per device — iosDeveloperMode is split per platform
// because its remedy is per-platform, and this one's is not. One line at one visual weight, whatever is attached.
//
// DOES IT BLOCK? Decided per platform, from the same outcome: a platform whose EVERY seen device is unusable has
// nothing for a run to use on this machine — that blocks, and it is exactly the field case (the only Samsung
// de-authorized). A platform that still has a usable device beside the stuck one makes this TRUE AND WORTH
// SAYING and not a stopper: Advisory, the vocabulary invented for that exact sentence.
//
// THE REMEDY IS THE PROBE'S SENTENCE. With one device it is carried VERBATIM — not paraphrased, and nothing
// general written beside it (the usbmuxd rule: a second sentence would be a second opinion). With several, each
// probe sentence is carried under its device's id. A state this build has never seen reaches the reader verbatim
// (inventory's own rule), and when the probe sent no remedy with it, the check says that instead of inventing one.
// leftOutDevices names each phone a probe saw, ready, and did not offer because it is not on this machine's USB cable — and
// the flag that offers it (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1). A note, never a fault: the phones on the cable
// still run.
func leftOutDevices(outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "devices left out",
		Need: "the agent offers the farm only phones on this machine's USB cable unless " + inventory.OfferUncabledFlag +
			" is given; an emulator or a phone reached over Wi-Fi or the network is left out (internal/android/probe.go, internal/ios/probe.go)",
		Limits: "adb and usbmuxd say how each device is reached; this check repeats what they said in this invocation",
	}
	var details []string
	for _, o := range outcomes {
		for _, d := range o.LeftOut() {
			details = append(details, fmt.Sprintf("%s (%s) is %s", d.ID, o.Platform(), d.How))
		}
	}
	if len(details) == 0 {
		c.State = NotRequired
		c.Detail = "every device the probes saw is on this machine's USB cable, or " + inventory.OfferUncabledFlag + " offers them all"
		return c
	}
	c.State = Missing
	c.Advisory = true
	c.Detail = strings.Join(details, "; ") + " — the agent offers the farm only phones on this machine's USB cable"
	c.Remedy = UncabledRemedy()
	return c
}

// UncabledRemedy is the remedy for phones left out because they are not on this machine's USB cable. One of the restart
// lines NTH_AGENT_0_4_3 holds for every way the agent can be started, like the two Appium remedies below.
func UncabledRemedy() string {
	return "connect it by USB cable, or to offer these too: " + command.Line("connect … "+inventory.OfferUncabledFlag)
}

// NoAppiumRemedy is the remedy when no Appium endpoint was given.
func NoAppiumRemedy() string {
	return "start Appium and pass its URL: " + command.Line("connect --appium http://127.0.0.1:4723") + " — this command does not guess where it might be"
}

// AppiumNotHereRemedy is the remedy for an Appium endpoint that answered from another machine.
func AppiumNotHereRemedy() string {
	return "run Appium on this machine and pass its local address: " + command.Line("connect --appium http://127.0.0.1:4723")
}

func unusableDevices(outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "unusable devices",
		Need: "a run can only use a device its probe could hand over — internal/inventory.Unusable is one the probe " +
			"SAW and could not: attached, present, and invisible to every run until its state changes",
		Limits: "this is the probe's answer from this invocation: a device plugged in afterwards is not here, and a " +
			"platform whose probe could not ask has no device rows for this check to read",
	}

	type stuck struct{ id, remedy string }
	var details []string
	var found []stuck
	blocks := false
	for _, o := range outcomes {
		for _, u := range o.Unusable() {
			details = append(details, fmt.Sprintf("%s (%s) is %q", u.ID, o.Platform(), u.State))
			remedy := u.Remedy
			if remedy == "" {
				remedy = fmt.Sprintf("the probe sent no remedy with state %q — the platform's own word, shown as it arrived", u.State)
			}
			found = append(found, stuck{id: u.ID, remedy: remedy})
		}
		if len(o.Unusable()) > 0 && len(o.Offerable()) == 0 {
			blocks = true
		}
	}

	if len(found) == 0 {
		// There was no question to pass: every device the probes saw is usable, or none was seen at all.
		c.State = NotRequired
		c.Detail = "every device the probes saw is usable, or none was seen"
		return c
	}

	c.State = Missing
	c.Advisory = !blocks
	c.Detail = strings.Join(details, "; ")
	if len(found) == 1 {
		c.Remedy = found[0].remedy
	} else {
		parts := make([]string, 0, len(found))
		for _, f := range found {
			parts = append(parts, f.id+": "+f.remedy)
		}
		c.Remedy = strings.Join(parts, "; ")
	}
	return c
}

func adbBinary(ctx context.Context, env Env) Check {
	c := Check{
		Name: "adb",
		Need: "the Android probe shells out to `adb` by bare name (internal/android/adb.go), so it must resolve on PATH",
	}
	path, err := env.LookPath("adb")
	if err != nil {
		c.State = Missing
		c.Detail = "not on PATH"
		c.Remedy = "install Android platform-tools and put adb on PATH (macOS: brew install --cask android-platform-tools)"
		return c
	}
	c.State = Satisfied
	c.Detail = path
	if out, err := env.Run(ctx, "adb", "version"); err == nil {
		if line := firstLine(out); line != "" {
			c.Detail = line + " (" + path + ")"
		}
	}
	return c
}

func adbServer(env Env) Check {
	c := Check{
		Name: "adb server",
		Need: "the Android probe's `adb devices` talks to adb's background server, and the agent uses it only when this " +
			"account (or root) runs it (internal/android/adbserver.go)",
	}
	address, refusal := adbServerPort, ""
	if env.AdbServer != nil {
		address, refusal = env.AdbServer()
	}
	if refusal != "" {
		c.State = Unreachable
		c.Detail = refusal
		c.Remedy = android.ServerRemedy
		return c
	}
	conn, err := env.DialTimeout("tcp", address, dialTimeout)
	if err != nil {
		// NOT A FAILURE, AND NOT A PASS. adb starts its server on first use, so "nothing is listening" does not mean
		// the probe will fail — and the only way to find out is to start it, which this command does not do.
		c.State = Unknown
		c.Detail = "nothing is listening on " + address + " yet"
		c.Remedy = "nothing, probably — adb starts its own server on first use. This check does not start it, so it cannot tell you more than that"
		return c
	}
	defer conn.Close()
	// WHOSE SERVER IT IS, asked of the kernel: a listener is not this account's adb because it listens.
	if env.AdbServerOwner == nil {
		c.State = Unknown
		c.Detail = "something is listening on " + address + ", and this build cannot tell which account runs it"
		c.Remedy = "nothing to do here — on this system the agent does not use an adb server it cannot tell you run"
		return c
	}
	uid, found := env.AdbServerOwner(conn)
	switch {
	case !found:
		c.State = Unknown
		c.Detail = "something is listening on " + address + ", and the system would not say which account runs it, so the agent will not use it"
		c.Remedy = android.ServerRemedy
	case uid == os.Geteuid():
		c.State = Satisfied
		c.Detail = "listening on " + address + ", run by you"
	case uid == 0:
		c.State = Satisfied
		c.Detail = "listening on " + address + ", run by the administrator (root)"
	default:
		c.State = Unreachable
		c.Detail = fmt.Sprintf("the server listening on %s runs as another account on this machine (uid %d), so the agent will not use it", address, uid)
		c.Remedy = android.ServerRemedy
	}
	return c
}

func appiumBinary(ctx context.Context, env Env) Check {
	c := Check{
		Name: "Appium",
		Need: "a local Appium is what drives a device for a real run; the agent connects to it with --appium",
	}
	path, err := env.LookPath("appium")
	if err != nil {
		c.State = Missing
		c.Detail = "not on PATH"
		c.Remedy = "install Appium 2 (npm i -g appium) — we do not install it for you: it is your machine"
		return c
	}
	c.State = Satisfied
	c.Detail = path
	if out, err := env.Run(ctx, "appium", "--version"); err == nil {
		if line := firstLine(out); line != "" {
			c.Detail = "v" + strings.TrimPrefix(line, "v") + " (" + path + ")"
		}
	}
	return c
}

func appiumEndpoint(ctx context.Context, env Env) Check {
	c := Check{
		Name: "Appium endpoint",
		Need: "internal/appium.Status probes GET {--appium}/status before any device is offered; no answer, no devices",
	}
	if strings.TrimSpace(env.AppiumURL) == "" {
		// WE DO NOT GO LOOKING FOR AN APPIUM WE WERE NOT TOLD ABOUT. Guessing an endpoint and dialling it is a
		// different decision from reporting one; a default that happened to be right on one machine would be a
		// silent wrong answer on the next.
		c.State = Unknown
		c.Detail = "no endpoint was given (--appium or NTH_AGENT_APPIUM)"
		c.Remedy = NoAppiumRemedy()
		return c
	}
	if err := env.AppiumStatus(ctx, env.AppiumURL); err != nil {
		// NOT YOURS, OR NOT HERE: the agent did not use what is at that address, and says why in its own words
		// (internal/localconn; A_MACHINE_THAT_STAYS_THE_CUSTOMERS). The URL may well be right.
		var notOurs *localconn.NotThisAccount
		var elsewhere *localconn.NotOnThisMachine
		switch {
		case errors.As(err, &notOurs):
			c.State = Unreachable
			c.Detail = notOurs.Error()
			c.Remedy = "start Appium yourself, as this account, on that port — the agent sends runs only to an Appium you run"
			return c
		case errors.As(err, &elsewhere):
			c.State = Unreachable
			c.Detail = elsewhere.Error()
			c.Remedy = AppiumNotHereRemedy()
			return c
		}
		// THE THREE STATES SPLIT HERE, and this is the split the field case needed. A refused connection is a
		// server that is not up; an answer that is not 2xx is something up and not usable from here.
		c.Detail = fmt.Sprintf("%s did not answer: %v", env.AppiumURL, err)
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
			c.State = NotRunning
			c.Remedy = "start Appium there (appium), then re-run this command — nothing else on this machine needs to change"
			return c
		}
		c.State = Unreachable
		c.Remedy = "something answered at that address but not as Appium — check the URL, the port, and anything between this machine and it"
		return c
	}
	c.State = Satisfied
	c.Detail = env.AppiumURL + " answered /status"
	return c
}

// appiumDriver reads the drivers Appium has installed. SCOPED: it is only required for a platform this machine
// can actually see, so an Android driver is not a gap on a host with no Android devices.
func appiumDriver(ctx context.Context, env Env, driver string, platform inventory.Platform, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "Appium driver " + driver,
		Need: "an Appium session for " + string(platform) + " needs the " + driver + " driver installed in Appium",
	}
	if !platformIsInPlay(platform, outcomes) {
		c.State = NotRequired
		c.Detail = "no " + string(platform) + " device was seen on this machine"
		return c
	}
	if _, err := env.LookPath("appium"); err != nil {
		c.State = Unknown
		c.Detail = "Appium is not on PATH, so its driver list cannot be read"
		c.Remedy = "install Appium first; this check can then say whether " + driver + " is there"
		return c
	}
	out, err := env.Run(ctx, "appium", "driver", "list", "--installed")
	if err != nil {
		c.State = Unknown
		c.Detail = "could not read Appium's driver list: " + firstLine(out)
		c.Remedy = "run `appium driver list --installed` by hand to see what this machine has"
		return c
	}
	if strings.Contains(out, driver) {
		c.State = Satisfied
		c.Detail = driver + " is installed"
		return c
	}
	c.State = Missing
	c.Detail = driver + " is not in Appium's installed drivers"
	c.Remedy = "appium driver install " + driver
	return c
}

// xcodeBuildTools reports whether the toolchain Appium's XCUITest driver needs for a REAL device is here.
//
// WHOSE REQUIREMENT THIS IS, stated because the file's rule is that every requirement traces to a caller: it is
// not this agent's. This agent never shells out to xcodebuild — it reads devices through usbmuxd and hands the
// driving to Appium. It is APPIUM'S XCUITEST DRIVER's requirement: on a real device that driver builds, signs and
// installs WebDriverAgent itself, and it does that with Xcode's command-line tools. A simulator-only machine does
// not need them for a session, which is why this is scoped to a machine that actually has an iPhone attached.
func xcodeBuildTools(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "Xcode command-line tools",
		Need: "Appium's xcuitest driver builds and signs WebDriverAgent with xcodebuild before it can drive a real iPhone",
	}
	if !platformIsInPlay(inventory.IOS, outcomes) {
		c.State = NotRequired
		c.Detail = "no iOS device was seen on this machine"
		return c
	}
	path, err := env.LookPath("xcodebuild")
	if err != nil {
		c.State = Missing
		c.Detail = "xcodebuild is not on PATH"
		c.Remedy = "install Xcode from the App Store and open it once, then run `xcode-select --install`"
		return c
	}
	c.State = Satisfied
	c.Detail = path
	// The version line is what a person compares against a driver's requirements; the developer-directory line is
	// what tells them a bare Command Line Tools install is standing in for a full Xcode.
	if out, err := env.Run(ctx, "xcodebuild", "-version"); err == nil {
		if line := firstLine(out); line != "" {
			c.Detail = line
		}
	}
	if out, err := env.Run(ctx, "xcode-select", "-p"); err == nil {
		if line := firstLine(out); line != "" {
			c.Detail += " (" + line + ")"
		}
	}
	return c
}

// iosDeveloperMode asks the PHONE. Since iOS 16 a handset refuses to install a developer-signed app — WebDriverAgent
// included — until Developer Mode is switched on in Settings BY A PERSON, on the device. No amount of toolchain on
// the Mac substitutes for it, so an operator with Xcode, the driver and a trusted cable can still be one toggle
// away from every session failing.
//
// IT IS A READ (internal/ios.ReadDeveloperMode → mobile_image_mounter's QueryDeveloperModeStatus), the same class
// of call the device listing already makes. It mounts nothing and changes nothing.
func iosDeveloperMode(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "iOS Developer Mode",
		Need: "iOS 16+ refuses to install WebDriverAgent until Developer Mode is enabled on the phone itself",
	}
	if !platformIsInPlay(inventory.IOS, outcomes) {
		c.State = NotRequired
		c.Detail = "no iOS device was seen on this machine"
		return c
	}
	if env.IosDeveloperMode == nil {
		c.State = Unknown
		c.Detail = "this build has no way to ask the phone"
		c.Remedy = "check it by hand on the device: Settings → Privacy & Security → Developer Mode"
		return c
	}
	readings, err := env.IosDeveloperMode(ctx)
	if err != nil {
		c.State = Unknown
		c.Detail = "the phones could not be asked: " + strings.TrimSpace(err.Error())
		c.Remedy = "check it by hand on the device: Settings → Privacy & Security → Developer Mode"
		return c
	}
	if len(readings) == 0 {
		// The probe saw a device and this read saw none. That is a disagreement between two reads of one machine,
		// and it is UNKNOWN rather than either answer — the rule the whole vocabulary turns on.
		c.State = Unknown
		c.Detail = "a device was seen by the probe and none answered this question"
		c.Remedy = "re-plug the cable and re-run this command; if it persists, check Developer Mode by hand"
		return c
	}

	var off, unasked []string
	for _, r := range readings {
		switch {
		case r.Err != "":
			unasked = append(unasked, r.UDID)
		case !r.Enabled:
			off = append(off, r.UDID)
		}
	}
	switch {
	case len(off) > 0:
		c.State = Missing
		c.Detail = "Developer Mode is OFF on " + strings.Join(off, ", ")
		c.Remedy = "on each of those phones: Settings → Privacy & Security → Developer Mode → on, then restart the phone"
	case len(unasked) > 0:
		c.State = Unknown
		c.Detail = "could not ask " + strings.Join(unasked, ", ")
		c.Remedy = "unlock the phone and tap Trust on this computer, then re-run; or check Developer Mode by hand"
	default:
		c.State = Satisfied
		c.Detail = fmt.Sprintf("on for %d attached device(s)", len(readings))
	}
	return c
}

// platformIsInPlay: did this machine actually see a device of that platform? An UNANSWERED platform counts as in
// play — "I could not ask" must never be read as "there is nothing there", which is the rule the whole inventory
// vocabulary turns on.
func platformIsInPlay(platform inventory.Platform, outcomes []inventory.Outcome) bool {
	for _, o := range outcomes {
		if o.Platform() != platform {
			continue
		}
		if o.IsCouldNotAsk() {
			return true
		}
		if o.IsAnswered() {
			return len(o.Offerable()) > 0 || len(o.Unusable()) > 0
		}
	}
	return false
}

// iosProvisioningProfile answers whether THIS machine can sign WebDriverAgent for the bundle id AND the team a
// person would run with. It reads .mobileprovision files and decodes them with the OS's own `security` — it
// installs nothing, registers nothing with Apple, and asks the phone nothing.
//
// IT USED TO ANSWER HALF THE QUESTION AND CLAIM THE WHOLE ONE. It compared bundle ids only, so on a machine whose
// declared team did not match its profiles it said "satisfied — a profile covers <bundle>.xctrunner" while
// xcodebuild answered, about that same bundle id, "No profiles for '<bundle>.xctrunner' were found". MEASURED,
// both halves, on 2026-08-24: the profiles on that Mac carried TeamIdentifier TEAM7QZ4X2; the configured
// xcodeOrgId was CERT8KD3M1, which is the ten characters inside the CERTIFICATE'S COMMON NAME and not a team at
// all (the certificate's own subject says OU=TEAM7QZ4X2). One value, confused for another, and a check that could
// not see the difference.
//
// THE PROFILE IS A PRECONDITION, NOT SOMETHING A RUN CREATES — measured on a real Mac, 2026-08-24: with zero
// profiles present, a WDA build for a real device fails with "No profiles for '<wda bundle id>.xctrunner' were
// found … Automatic signing is disabled and unable to generate a profile", and Appium passes
// -allowProvisioningUpdates only when the caller sets appium:allowProvisioningDeviceRegistration (default off —
// appium-webdriveragent lib/xcodebuild.ts:367). This check is what tells a person whether the one-time Xcode
// step has been done. (Moved here from the file header when its enumeration was retired — the evidence belongs
// beside the check it justifies.)
//
// THE BUNDLE ID IS A PARAMETER AND IS NEVER GUESSED, for the same reason --appium is not.
func iosProvisioningProfile(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "iOS provisioning profile",
		Need: "xcodebuild refuses to build WebDriverAgent for a real iPhone without a provisioning profile for <wda bundle id>.xctrunner that belongs to the team it builds as, and Appium does not create one",
		Limits: "reading a profile off this disk is not running the build: it does not establish that Xcode can reach Apple, " +
			"and it CANNOT say whether the developer certificate is trusted on the phone — iOS exposes no such query (go-ios " +
			"v1.3.2 has none, and its misagent profile copy errors), so the first session on a phone may still ask you on the device. " +
			// THE THIRD LIMIT, written down the day the field it raises was finally read (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE):
			ProfileDeviceCoverageLimit,
	}
	if !platformIsInPlay(inventory.IOS, outcomes) {
		c.State = NotRequired
		c.Detail = "no iOS device was seen on this machine"
		return c
	}
	if env.IosWdaBundleID == "" {
		c.State = Unknown
		c.Detail = "no WebDriverAgent bundle id was given (--ios-wda-bundle-id or NTH_AGENT_IOS_WDA_BUNDLE_ID)"
		c.Remedy = "pass the bundle id you sign with: " + command.Line("doctor --ios-wda-bundle-id com.example.WebDriverAgentRunner") + " — this command does not guess one"
		return c
	}
	runner := ios.WdaRunnerBundleID(env.IosWdaBundleID)
	if env.IosProfiles == nil {
		c.State = Unknown
		c.Detail = "this build has no way to read the profiles on this machine"
		return c
	}
	profiles, err := env.IosProfiles(ctx)
	if err != nil {
		c.State = Unknown
		c.Detail = "the profiles on this machine could not be read: " + strings.TrimSpace(err.Error())
		c.Remedy = "check them by hand: ls ~/Library/Developer/Xcode/UserData/Provisioning\\ Profiles/"
		return c
	}

	match := ios.Match(profiles, runner, env.IosTeamID)
	switch {
	case match.ForTeam != nil:
		// A PROFILE'S WHOLE JOB IS TO SAY WHICH DEVICES IT COVERS, and a machine-wide "satisfied" used to silence
		// that per-device truth: this check said ready on a Mac where xcodebuild refused the attached phone by
		// name ("doesn't include the currently selected device"). So the DEVICE question is asked here, against
		// every attached offerable iPhone, and one uncovered phone makes the whole check Missing WITH ITS UDID —
		// the same granularity the findings pipeline rides per device.
		if uncovered := uncoveredIosDevices(match, outcomes); len(uncovered) > 0 {
			c.State = Missing
			// ⚠️ NO PROFILE IS NAMED HERE ANY MORE, AND THE ABSENCE IS THE POINT. This printed
			// `match.ForTeam.Name` — the FIRST profile matching the bundle id and the team — beside a udid
			// computed over ForTeamAll. For a phone that NOTHING covers there is no "the profile in play" to
			// name, and naming one told a reader that a specific profile was the one failing them when the
			// truth is that none of the matching profiles lists their phone. The count is the honest fact.
			//
			// ⚠️ THE TEAM PRINTED IS THE PROFILES' OWN, NOT THE DECLARED ONE. `declared(env)` answers "what did
			// you declare" — it returns "no team" when nothing was, and the phrase reads correctly in
			// "…and you declared no team" and as nonsense in "(team no team)". Worse, it DROPS a fact: with no
			// team declared, ios.Match accepts every profile covering the bundle id whatever team it belongs to,
			// so the profiles' real teams are the thing a person needs and the only place to read them. This
			// check already calls naming a profile's actual team "THE MOST USEFUL SENTENCE THIS MACHINE CAN SAY"
			// in the OtherTeams branch below; losing it here was a regression against that sentence.
			c.Detail = fmt.Sprintf("%d profile(s) cover %s (team %s) and NONE of them includes attached device %s — xcodebuild will refuse that phone by name",
				len(match.ForTeamAll), runner, teamsOf(match.ForTeamAll), strings.Join(uncovered, ", "))
			c.Remedy = "run '" + command.Line("setup-ios") + "' with that phone attached (a development profile is regenerated to include it), " +
				"or register the device in Xcode: open the WebDriverAgent project, select the phone as the run destination, and let " +
				"Xcode 'Register Device' — then build for testing once"
			return c
		}
		c.State = Satisfied
		// ⚠️ PER PHONE, THROUGH THE SAME TWO FUNCTIONS THE FINDINGS PIPELINE USES — and that is the whole point
		// of this branch's rewrite.
		//
		// It printed `match.ForTeam.Name … expires match.ForTeam.Expires`: one machine-level date, off the first
		// profile matching the bundle id and the team, coverage never asked. Before the findings pipeline was
		// fixed both halves of this package read ForTeam, so both were wrong and they AGREED. Then one was
		// corrected and they did not: a customer running `nth-agent doctor` and an operator reading the
		// Devices grid could see TWO DIFFERENT EXPIRY DATES FOR THE SAME PHONE, with neither surface saying what
		// it was answering about. A wrong answer that agrees with itself is easier to trust than two that differ.
		//
		// So this line now asks CoveringDevice and soonestExpiry — the same population and the same ranking the
		// per-device finding uses — which makes the two surfaces agree BY CONSTRUCTION rather than by anyone
		// remembering to keep them in step. One line cannot honestly state one date when the phones differ, so
		// it states one clause PER PHONE; the Missing branch above has named devices in one line since the day
		// this check learned that a profile's whole job is to say which devices it covers.
		//
		// ⚠️ NO MACHINE-LEVEL TEAM ON THIS LINE, FOR THE SAME TWO REASONS AS ABOVE — and here there is a better
		// place for it: the team belongs to the PROFILE, and the profile is already named per phone. With no team
		// declared, two phones can legitimately be covered by profiles of two different teams, which a single
		// machine-level team could not state at all.
		c.Detail = fmt.Sprintf("a profile covers %s for every attached iPhone — %s",
			runner, coveringPerPhone(match, outcomes))
		if env.IosTeamID == "" {
			// The half-question, said out loud rather than passed off as the whole one.
			c.Detail += " — no team was given, so only the bundle id was checked"
			c.Limits += "; and no --ios-team-id was given, so this did not check that the profile's team is the one you build as"
		}
		return c
	case len(match.OtherTeams) > 0:
		// THE MOST USEFUL SENTENCE THIS MACHINE CAN SAY. The profile is there, the team is wrong, and the right
		// team id is right here — which is the fact whose absence cost the afternoon this check was rewritten for.
		other := match.OtherTeams[0]
		c.State = Missing
		c.Detail = fmt.Sprintf("a profile covers %s but belongs to team %s, and you declared %s", runner, other.TeamID, declared(env))
		// THE REMEDY MUST NAME THE VARIABLE THIS PROGRAM READS (item 2). This offered IOS_XCODE_ORG_ID "for the
		// platform's own config" — honest about whose variable it is and useless to the person holding the agent:
		// set it and nothing changes, because the value that was wrong was sitting in NTH_AGENT_IOS_TEAM_ID, which
		// is what this program reads. A remedy that lists every way to set a value except its own is not a remedy.
		c.Remedy = fmt.Sprintf("your Apple team id is %s — declare that one: --ios-team-id %s, or NTH_AGENT_IOS_TEAM_ID=%s (the variable this agent reads). "+
			"The ten characters in a certificate's name are NOT the team id; the team is what the profile above carries.", other.TeamID, other.TeamID, other.TeamID)
		return c
	}

	c.State = Missing
	c.Detail = fmt.Sprintf("no profile on this machine covers %s for team %s (%d profile(s) read)", runner, declared(env), len(profiles))
	c.Remedy = "run '" + command.Line("setup-ios") + "' — it builds Appium's own WebDriverAgent project for an attached iPhone " +
		"with your team and the flags Appium does not pass, states what it will do, and waits for your yes. " +
		"To do it by hand instead: open " + wdaProjectHint + " in Xcode, set your team under Signing & Capabilities on " +
		"BOTH WebDriverAgentRunner and WebDriverAgentLib, and use Product > Build For Testing (a plain Build produces " +
		"no .xctrunner, which is what a session needs)."
	return c
}

// ProfileDeviceCoverageLimit is the THIRD limit, the one that existed all along and nobody could name because
// nobody had read the field that raises it: a development profile lists the devices it covers, and covering the
// bundle id and the team says NOTHING about covering this phone. What reading the list establishes — and does
// not: a udid IN the list means xcodebuild will not refuse the device by name; it does NOT mean the build will
// run there (trust is a different gate, stated above), and a profile with NO device list (distribution /
// enterprise) restricts nothing, so "covered" through one of those is the absence of a refusal, not a promise.
const ProfileDeviceCoverageLimit = "A development profile also LISTS THE DEVICES it covers, and this check reads that list: " +
	"a udid missing from every matching profile fails the build by name before the phone is touched. Coverage here means " +
	"only that xcodebuild will not refuse the device — a profile without a device list (distribution/enterprise) restricts " +
	"nothing and counts as covering, and neither form says anything about trust on the phone"

// coveringPerPhone says, for each attached offerable iPhone, WHICH profile this answer is about and when it
// stops being true — the sentence a machine-level `expires …` could not honestly be.
//
// ⚠️ IT REUSES THE FINDINGS PIPELINE'S RANKING RATHER THAN REPEATING IT. soonestExpiry is the one place that
// turns Expires strings into an order, and soonestCoveringProfile below only LOCATES the profile carrying that
// answer. Two rankings would be two chances for these two surfaces to disagree again, which is the defect this
// function exists to end.
//
// With no offerable iPhone in hand there is no phone to state a date about, and it says that rather than
// inventing a machine-level number to fill the gap.
func coveringPerPhone(match ios.MatchFor, outcomes []inventory.Outcome) string {
	var clauses []string
	for _, o := range outcomes {
		if o.Platform() != inventory.IOS || !o.IsAnswered() {
			continue
		}
		for _, d := range o.Offerable() {
			covering := match.CoveringDevice(d.ID)
			if len(covering) == 0 {
				continue // the Missing branch above owns this case and has already returned
			}
			p := soonestCoveringProfile(covering)
			clauses = append(clauses, fmt.Sprintf("%s: %q (team %s) expires %s", d.ID, p.Name, p.TeamID, expiresOrNotSaid(*p)))
		}
	}
	if len(clauses) == 0 {
		return "no attached iPhone is offerable, so there is no phone to state a date about"
	}
	return strings.Join(clauses, "; ")
}

// teamsOf names the teams a set of profiles actually belongs to, deduplicated and in the order first seen.
//
// ⚠️ IT EXISTS BECAUSE ONE TEAM IS NOT ALWAYS THE ANSWER. With a team declared, ios.Match keeps only profiles of
// that team and this prints the one; with NO team declared it keeps every profile covering the bundle id, so the
// set can hold several and the honest sentence names them all. A profile that states no team prints as
// "(none stated)" rather than as an empty gap — the same not-said rule this file keeps for a date.
func teamsOf(profiles []ios.Profile) string {
	var seen []string
	for _, p := range profiles {
		team := strings.TrimSpace(p.TeamID)
		if team == "" {
			team = "(none stated)"
		}
		if !slices.Contains(seen, team) {
			seen = append(seen, team)
		}
	}
	if len(seen) == 0 {
		return "(none stated)"
	}
	return strings.Join(seen, ", ")
}

// soonestCoveringProfile is the covering profile this answer is ABOUT: the one that lapses first, because that
// is the moment the phone stops working. The ORDER comes from soonestExpiry — this only finds which profile
// carries it. When none of them states a readable date there is nothing to rank, and the first is named with
// its text shown as it was stored.
func soonestCoveringProfile(covering []ios.Profile) *ios.Profile {
	soonest := soonestExpiry(covering)
	if soonest == nil {
		return &covering[0]
	}
	for i := range covering {
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(covering[i].Expires)); err == nil && at.UTC().Equal(*soonest) {
			return &covering[i]
		}
	}
	return &covering[0]
}

// expiresOrNotSaid keeps ios.Profile.Expires VERBATIM — what the doctor prints is what the plist said — and says
// so plainly when the profile stated nothing this build could read. Never "does not expire".
func expiresOrNotSaid(p ios.Profile) string {
	if strings.TrimSpace(p.Expires) == "" {
		return "(not said)"
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(p.Expires)); err != nil {
		return fmt.Sprintf("%s (this build cannot read that as a date)", p.Expires)
	}
	return p.Expires
}

// uncoveredIosDevices names the attached offerable iPhones that no bundle-and-team-matching profile covers.
func uncoveredIosDevices(match ios.MatchFor, outcomes []inventory.Outcome) []string {
	var uncovered []string
	for _, o := range outcomes {
		if o.Platform() != inventory.IOS || !o.IsAnswered() {
			continue
		}
		for _, d := range o.Offerable() {
			if !match.CoversDevice(d.ID) {
				uncovered = append(uncovered, d.ID)
			}
		}
	}
	return uncovered
}

// iosWdaProjectTargets reads the team STORED in Appium's WebDriverAgent project, per target.
//
// WALL 4: a person who sets the team in Xcode sets it on the target they clicked, and the project ships with a
// team on WebDriverAgentLib too. A build started from Xcode uses what the project stores, so a library target
// left on somebody else's team fails a build whose runner target looks perfectly configured — and the
// provisioning check above, which reads profiles rather than the project, cannot see it.
func iosWdaProjectTargets(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "iOS WebDriverAgent project",
		Need: "xcodebuild signs EVERY target it builds, so a WebDriverAgent target carrying no team (or another team) fails the build",
		Limits: "this reads what the PROJECT stores; a build that passes DEVELOPMENT_TEAM on the command line — which " +
			"'" + command.Line("setup-ios") + "' does, and which covers every target — overrides all of it",
	}
	if !platformIsInPlay(inventory.IOS, outcomes) {
		c.State = NotRequired
		c.Detail = "no iOS device was seen on this machine"
		return c
	}
	if env.WdaProjectPath == "" {
		c.State = Unknown
		c.Detail = "Appium's WebDriverAgent project was not found on this machine"
		c.Remedy = "install the xcuitest driver (appium driver install xcuitest), or pass --wda-project to setup-ios"
		return c
	}

	var unset, foreign []string
	for _, target := range []string{wda.LibTarget, wda.RunTarget} {
		out, err := env.Run(ctx, "xcodebuild", "-showBuildSettings", "-project", env.WdaProjectPath, "-target", target)
		if err != nil {
			c.State = Unknown
			c.Detail = fmt.Sprintf("could not read %s's build settings: %s", target, firstLine(out))
			return c
		}
		team := settingValue(out, "DEVELOPMENT_TEAM")
		switch {
		case team == "":
			unset = append(unset, target)
		case env.IosTeamID != "" && !strings.EqualFold(team, env.IosTeamID):
			foreign = append(foreign, fmt.Sprintf("%s is on team %s", target, team))
		}
	}

	// A FACT WORTH STATING THAT DOES NOT STOP A RUN — decided here, once, for both failing branches (item 4).
	//
	// This check reported MISSING and was counted in "2 things stop a run happening on this machine" while its own
	// Limits said a build passing DEVELOPMENT_TEAM on the command line "overrides all of it". Both builds that
	// happen on this machine do exactly that: 'nth-agent setup-ios' passes it (internal/wda), and a platform
	// session passes the SAME team as Appium's xcodeOrgId, from the identity this agent declared. So when a team
	// is declared, what the project stores cannot stop a run here — the fact is true and is still worth saying (a
	// person building by hand in Xcode WILL hit it), and it is no longer counted as a blocker.
	//
	// WITH NO TEAM DECLARED THERE IS NOTHING TO OVERRIDE WITH, and the stored value is what a build would use — so
	// that case keeps blocking. The distinction is the whole decision: the override is a fact about a team we have,
	// not a property of the check.
	overridden := env.IosTeamID != ""
	switch {
	case len(unset) > 0:
		c.State = Missing
		c.Advisory = overridden
		c.Detail = "no team is set on " + strings.Join(unset, ", ")
		if overridden {
			c.Detail += " — this does not stop a run here: setup-ios and a platform session both pass " + declared(env) + " on the command line, which covers every target"
		}
		c.Remedy = "run '" + command.Line("setup-ios") + "' (it passes the team on the command line, which covers every target), " +
			"or set your team under Signing & Capabilities on EVERY WebDriverAgent target in Xcode — the library one included"
	case len(foreign) > 0:
		c.State = Missing
		c.Advisory = overridden // a foreign team is only reachable WITH a declared team, so this is always true here
		c.Detail = strings.Join(foreign, "; ") + ", and you declared " + declared(env) +
			" — this does not stop a run here: both builds pass the declared team on the command line, which overrides what the project stores"
		c.Remedy = "run '" + command.Line("setup-ios") + "', which builds with the team you declare whatever the project stores"
	default:
		c.State = Satisfied
		c.Detail = "every WebDriverAgent target carries a team"
	}
	return c
}

// settingValue pulls one `NAME = value` out of xcodebuild -showBuildSettings. An unset setting is PRESENT WITH AN
// EMPTY VALUE in that output, which is a different fact from absent and is why this returns "" for both and the
// caller says "no team is set" rather than "could not read".
func settingValue(output, name string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, name+" ") && !strings.HasPrefix(trimmed, name+"=") {
			continue
		}
		if eq := strings.Index(trimmed, "="); eq >= 0 {
			return strings.Trim(strings.TrimSpace(trimmed[eq+1:]), "\"")
		}
	}
	return ""
}

// wdaProjectHint is where Appium keeps the WebDriverAgent project it builds. Named rather than resolved: the path
// is a property of the Appium installation, and a check that resolved it would be answering a different question.
const wdaProjectHint = "~/.appium/node_modules/appium-xcuitest-driver/node_modules/appium-webdriveragent/WebDriverAgent.xcodeproj"

// iosWdaPort answers whether the port each attached iPhone's WebDriverAgent will use is free ON THIS MACHINE.
//
// A COLLISION IS POSSIBLE AND MUST NOT BE SILENT. The port is DERIVED from the device's udid (wda.PortFor) so the
// worker and this agent agree on it without a conversation — which means it is a fixed number for that phone
// rather than one the operating system just handed out, and a fixed number is one something else here can already
// hold. Appium binds it locally as well as on the phone (the driver resolves the device port from wdaLocalPort),
// so a local listener is exactly the collision.
//
// WEBDRIVERAGENT ITSELF ANSWERING IS NOT A COLLISION, and telling the two apart is why this asks in WDA's own
// terms rather than merely dialling: during and after a session Appium's own forward holds that port, and a check
// that called that "occupied" would cry wolf at the one moment everything is working.
func iosWdaPort(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "iOS WebDriverAgent port",
		Need: "the port a phone's WebDriverAgent listens on is derived from its udid, so it is a fixed number that something else on this machine could already hold",
		Limits: "this checks THIS machine only, and only right now: a port free here at this moment can be taken " +
			"before the next run starts, and nothing here can see what is free on any other machine",
	}
	if !platformIsInPlay(inventory.IOS, outcomes) {
		c.State = NotRequired
		c.Detail = "no iOS device was seen on this machine"
		return c
	}
	if env.IosLocalPortStatus == nil {
		c.State = Unknown
		c.Detail = "this build has no way to ask what is listening here"
		return c
	}

	var taken []string
	var checked int
	for _, outcome := range outcomes {
		if outcome.Platform() != inventory.IOS || !outcome.IsAnswered() {
			continue
		}
		for _, device := range outcome.Offerable() {
			port := wda.PortFor(device.ID)
			checked++
			switch env.IosLocalPortStatus(ctx, port).State {
			case wda.RunnerIdle, wda.RunnerInUse:
				// WebDriverAgent's own answer: this is ours, held for that phone. Not a collision.
			case wda.RunnerUnknown:
				// Something answered and it was not WebDriverAgent.
				taken = append(taken, fmt.Sprintf("%d (%s)", port, device.ID))
			}
		}
	}

	switch {
	case checked == 0:
		c.State = Unknown
		c.Detail = "no iPhone was offered, so there was no port to check"
	case len(taken) > 0:
		c.State = Missing
		c.Detail = "something on this machine already holds port " + strings.Join(taken, ", ") + " and it is not WebDriverAgent"
		c.Remedy = "free that port, or stop whatever is using it — a run for that phone asks Appium to listen there, " +
			"and the run will fail at session start naming the same number"
	default:
		c.State = Satisfied
		c.Detail = fmt.Sprintf("the derived port is available for %d attached iPhone(s)", checked)
	}
	return c
}

// declared renders the team a person gave AND WHERE IT WAS READ FROM — "CERT8KD3M1 (from NTH_AGENT_IOS_TEAM_ID)".
//
// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 1. Three doctor runs said "you declared CERT8KD3M1" and none said
// where that came from, so a person who had set the value in one place went looking in another — and the remedy
// they were handed named a third. The provenance turns a five-second fix back into a five-second fix.
//
// A team with no recorded source renders bare rather than inventing one, and no team at all says so in words:
// "" would read as a value of nothing, which is the shape this package refuses everywhere else.
func declared(env Env) string {
	switch {
	case env.IosTeamID == "":
		return "no team"
	case env.IosTeamIDSource == "":
		return env.IosTeamID
	default:
		return fmt.Sprintf("%s (from %s)", env.IosTeamID, env.IosTeamIDSource)
	}
}

// iosSigningIdentity reads what THIS MAC CAN SIGN AS — `security find-identity -v -p codesigning`, one line per
// identity, and the code in parentheses belongs to the CERTIFICATE.
//
// WHY THE AGENT NOW RUNS A COMMAND IT NEVER RAN (A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 3). The confusion this
// answers was not "I typed the wrong string": it was "the ten characters in my certificate's name look exactly
// like a team id". Our own documentation warns about that trap in a boxed note, the doctor knew the RIGHT team
// (it reads it off a profile one check below) — and it could not say why the WRONG one had been reached, so three
// runs said "you declared CERT8KD3M1" and an evening went by. `security` is macOS's own binary and this is a read.
//
// TWO THINGS IT REFUSES TO DO, and they are the reason this is a check rather than a line:
//
//   - IT CLAIMS NOTHING ABOUT THE PERSON'S APPLE ACCOUNT. It says what is in this Mac's keychain. The profile
//     check one below already names the real team when it finds one; this one never says a team is or is not
//     somebody's — an identity absent HERE is Xcode being unable to sign as it HERE AND NOW.
//   - ABSENCE IS NOT A VERDICT. `security` missing, or refusing to read the keychain, is Unknown AND SAYS WHICH —
//     the same discipline every other check in this file keeps. A machine that could not be asked has not
//     answered no.
func iosSigningIdentity(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "iOS signing identity",
		Need: "xcodebuild signs WebDriverAgent with a certificate from THIS Mac's keychain, and the ten-character code " +
			"in a certificate's name is NOT the Apple team id a build wants",
		Limits: "this reads this Mac's keychain; it says nothing about which teams the Apple account owns, and a " +
			"certificate present here can still be revoked or expired at Apple",
	}
	if !platformIsInPlay(inventory.IOS, outcomes) {
		c.State = NotRequired
		c.Detail = "no iOS device was seen on this machine"
		return c
	}
	if env.IosIdentities == nil {
		c.State = Unknown
		c.Detail = "this build has no way to read this machine's signing identities"
		return c
	}
	identities, err := env.IosIdentities(ctx)
	if err != nil {
		// SAYS WHICH (item 3): not-installed and refused are different machines and the sentence names the one it met.
		c.State = Unknown
		c.Detail = "this machine's signing identities could not be read: " + firstLine(strings.TrimSpace(err.Error()))
		c.Remedy = "check by hand: security find-identity -v -p codesigning"
		return c
	}
	if len(identities) == 0 {
		c.State = Missing
		c.Detail = "no code-signing identity is in this Mac's keychain"
		c.Remedy = "open Xcode > Settings > Accounts, add the Apple ID that owns the team, and use 'Manage Certificates' " +
			"to create an Apple Development certificate on this Mac"
		return c
	}

	names := make([]string, 0, len(identities))
	for _, id := range identities {
		names = append(names, id.Name)
	}
	canSignAs := "this Mac can sign as: " + strings.Join(names, "; ")

	// THE SENTENCE THAT ENDS THE EVENING. The declared value IS one of these certificates' codes, so it cannot be
	// the team — and the person is told exactly that, with where they declared it.
	if ios.CertCodeMatches(identities, env.IosTeamID) {
		c.State = Missing
		c.Detail = fmt.Sprintf("%s is a CERTIFICATE's code, not an Apple team id — %s", declared(env), canSignAs)
		c.Remedy = "the team id is what a provisioning profile's TeamIdentifier carries (the profile check below names it " +
			"when it finds one), or read it in Xcode > Settings > Accounts beside the team name — then set it with " +
			"--ios-team-id, or NTH_AGENT_IOS_TEAM_ID (the variable this agent reads)"
		return c
	}

	if env.IosTeamID == "" {
		c.State = Satisfied
		c.Detail = canSignAs + " — no team was given, so nothing was checked against one"
		c.Limits += "; and no --ios-team-id was given, so this did not check that any identity here matches the team you build as"
		return c
	}

	// CAN THIS MAC SIGN AS THE TEAM YOU DECLARED? The team is the certificate subject's OU, which `find-identity`
	// does not print — so the answer can be genuinely UNESTABLISHED, and that is reported as such rather than as a
	// refusal (item 3: absence is not a verdict).
	canSign, established := ios.CanSignAsTeam(identities, env.IosTeamID)
	switch {
	case !established:
		c.State = Unknown
		c.Detail = canSignAs + ", but which team each belongs to could not be read from their certificates, so " +
			declared(env) + " was not checked against them"
		c.Remedy = "check by hand: security find-certificate -c '<the identity above>' -p | openssl x509 -noout -subject (the OU is the team)"
		return c
	case !canSign:
		// THE SENTENCE MAY NOT SAY THE TEAM IS NOT THEIRS. This Mac's keychain is the subject; an Apple account
		// can own teams this machine holds no certificate for, and saying otherwise would be a claim about
		// somebody's account made from a keychain read.
		c.State = Missing
		c.Detail = fmt.Sprintf("no certificate on this Mac belongs to %s — %s (%s). Xcode cannot sign as that team HERE; "+
			"this says nothing about which teams the Apple account owns",
			declared(env), canSignAs, "their teams: "+strings.Join(ios.TeamsHere(identities), ", "))
		c.Remedy = "open Xcode > Settings > Accounts, sign in with the Apple ID that owns that team and use 'Manage Certificates' " +
			"to create an Apple Development certificate on this Mac — or declare a team this Mac already holds one for " +
			"(--ios-team-id, or NTH_AGENT_IOS_TEAM_ID, the variable this agent reads)"
		return c
	}

	c.State = Satisfied
	c.Detail = fmt.Sprintf("%s, which covers %s", canSignAs, declared(env))
	return c
}
