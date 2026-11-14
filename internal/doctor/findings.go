package doctor

import (
	"context"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// THE CHECK THAT ALREADY KNEW — the doctor computes answers on the machine where they live and, until this file,
// threw them away unless a person was watching. DeviceFindings reduces the checks a session-start failure can be
// EXPLAINED by to per-device findings that ride the farm report, the road WdaPort already proved.
//
// WHICH CHECKS RIDE, AND WHICH DO NOT — decided here, not per call site:
//
//   - iOS Developer Mode      RIDES, per device. A read of the phone's own mobile_image_mounter — the same class
//     of call the probe already makes every tick. It answers per UDID, so each phone carries its own answer.
//   - iOS provisioning profile RIDES, machine-wide (stamped on every iOS device of this machine). A disk read
//     plus `security` decode; cheap, and the fact changes only when a person installs or revokes a profile.
//   - iOS WebDriverAgent project RIDES, machine-wide. Two `xcodebuild -showBuildSettings` reads — seconds, not
//     milliseconds, which is why the CALLER refreshes on an interval (see connect.Config.FindingsMaxAge) rather
//     than every heartbeat. The fact changes only when a person edits the project, so minutes-old is honest —
//     and the age rides on every finding so the consumer can decide for itself.
//   - iOS WebDriverAgent port DOES NOT RIDE. Its own Limits say "this machine only, and only right now: a port
//     free here at this moment can be taken before the next run starts" — a freshness that cannot be usefully
//     stated at lease time. The port failure already has its own run-time sentence naming the number
//     (DriverFailureMessages.RealDeviceSessionStartFailedOnPort); a stale "free" here would name a cause that is
//     not the cause, which is the exact failure this slice exists to avoid.
//   - adb / Appium / driver checks DO NOT RIDE. The driver gate already withholds the DEVICE ROW itself when
//     Appium is not answering — an unoffered phone needs no finding, and an offered one has an answering Appium
//     by construction.
//
// NO PROSE LEAVES THIS FUNCTION. The doctor's Detail/Remedy strings can embed a library error's text (an
// "could not be asked: <err>" detail); what rides is the check name, the state word and the timestamp, and the
// worker authors every tenant-facing sentence from those alone.

// Finding check names on the wire — matched by the worker's own vocabulary, so they are constants, not prose.
const (
	FindingWdaProject   = "IosWdaProject"
	FindingProvisioning = "IosProvisioningProfile"
	FindingDevMode      = "IosDeveloperMode"
	// FindingWdaOnPhone is whether the phone's WebDriverAgent ANSWERS on the port the driver uses
	// (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE). satisfied = it answers; missing = it is INSTALLED and does not
	// answer — the signature the trust inference reads, established as two facts, never guessed; unknown = it
	// does not answer and is not known to be installed (an ordinary first run installs it during the session),
	// or the phone could not be asked at all.
	//
	// THE RESTING STATE OF A HEALTHY PHONE IS "missing", and that is not a defect of the finding — this agent's
	// own idle tracker STOPS WebDriverAgent between sessions, so installed-and-silent is Tuesday. The finding
	// proves nothing alone; it becomes meaningful only CONDITIONED ON A SESSION-START FAILURE, which is exactly
	// where the worker reads it — and why its sentence says "inferred from the order", never "checked".
	FindingWdaOnPhone = "IosWdaOnPhone"
)

// DeviceFinding is one answer for one device, timestamped at computation — the freshness the whole design turns
// on. States are the doctor's own words: "satisfied" | "missing" | "unknown". NotRequired never rides: findings
// are computed only for iOS devices that are actually in play.
type DeviceFinding struct {
	Check        string
	State        State
	CheckedAtUtc time.Time
	// ExpiresAtUtc is when the thing this check read stops being true — see farm.WireFinding.ExpiresAtUtc for why
	// a date rides where prose may not. Nil = this check has no expiry to state, or none could be read.
	ExpiresAtUtc *time.Time
}

// DeviceFindings computes the riding checks for every offerable iOS device in outcomes, keyed by UDID.
// now is a parameter so a test can pin the stamp; every finding carries it — a finding with no stated freshness
// is not mintable through this function, which is what the wire seal leans on.
func DeviceFindings(ctx context.Context, env Env, outcomes []inventory.Outcome, now time.Time) map[string][]DeviceFinding {
	var udids []string
	for _, o := range outcomes {
		if o.Platform() != inventory.IOS || !o.IsAnswered() {
			continue
		}
		for _, d := range o.Offerable() {
			udids = append(udids, d.ID)
		}
	}
	if len(udids) == 0 {
		return nil
	}

	// Machine-wide answers computed once; the profile and WDA answers are PER DEVICE — a profile lists the
	// devices it covers, and a phone answers or does not answer for itself.
	project := projectFinding(ctx, env, now)
	profileFor := profileFindings(ctx, env, now)
	devMode := developerModeFindings(ctx, env, now)

	findings := make(map[string][]DeviceFinding, len(udids))
	for _, udid := range udids {
		perDevice := []DeviceFinding{project, profileFor(udid)}
		if reading, ok := devMode[udid]; ok {
			perDevice = append(perDevice, reading)
		} else {
			// The probe saw this phone and the Developer Mode read did not answer for it — a disagreement between
			// two reads of one machine, and it is UNKNOWN rather than either answer (the doctor's own rule).
			perDevice = append(perDevice, DeviceFinding{Check: FindingDevMode, State: Unknown, CheckedAtUtc: now})
		}
		perDevice = append(perDevice, wdaOnPhoneFinding(ctx, env, udid, now))
		findings[udid] = perDevice
	}
	return findings
}

// profileFindings is iosProvisioningProfile reduced to a PER-DEVICE state: does a profile on this machine cover
// the runner for the declared team AND this phone? A profile's whole job is to say which devices it covers, and
// the machine-wide answer silenced that truth (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE): a Mac said ready while
// xcodebuild refused the attached phone by name. Anything not established is Unknown, never Satisfied.
func profileFindings(ctx context.Context, env Env, now time.Time) func(udid string) DeviceFinding {
	unknown := DeviceFinding{Check: FindingProvisioning, State: Unknown, CheckedAtUtc: now}
	if env.IosWdaBundleID == "" || env.IosProfiles == nil {
		return func(string) DeviceFinding { return unknown }
	}
	profiles, err := env.IosProfiles(ctx)
	if err != nil {
		return func(string) DeviceFinding { return unknown }
	}
	// The SAME rule the doctor's own check applies (ios.Match): a profile counts only when it covers the runner
	// bundle id AND belongs to the declared team — and now the DEVICE leg: any matching profile must list this
	// udid (or carry no device list at all, which restricts nothing). Half an answer is Missing either way: the
	// build fails identically for a wrong team and for an unlisted phone, and Apple's refusal names the device.
	match := ios.Match(profiles, ios.WdaRunnerBundleID(env.IosWdaBundleID), env.IosTeamID)

	// WHEN THE PROFILE STOPS BEING TRUE, carried whatever the state turns out to be (item 5). The date rides on
	// a SATISFIED finding too, and that is the case that matters most: a profile that covers this phone today
	// and expired an hour ago is exactly the failure nobody could name — the runner's DeviceSessionStart reads this
	// date BEFORE the state word for precisely that reason.
	//
	// ⚠️ AND IT IS NOW ASKED PER DEVICE, OVER THE PROFILES THAT COVER THAT DEVICE. It used to be
	// `expires := profileExpiry(match)` — computed ONCE, here, outside the closure, from `match.ForTeam`: the
	// first bundle-and-team match, coverage never asked. So every phone on the machine reported the SAME date,
	// and that date belonged to whichever profile the directory enumeration returned first. The population was
	// already in the struct one field away (ForTeamAll) and CoversDevice already walked it; the lesson had been
	// applied to the state word and not to the date beside it — the same sentence the farm's node needed two
	// slices ago, learned twice and applied once each time.
	return func(udid string) DeviceFinding {
		covering := match.CoveringDevice(udid)

		// ⚠️ NO COVERAGE, NO DATE — BY CONSTRUCTION. The struct literal used to open with the expiry already
		// attached and decide the state afterwards, so a phone no profile covers was answered with a confident
		// date belonging to a profile that is not about it. On the customer path the runner would then read that
		// date first and tell a paying tenant their WebDriverAgent profile expired on it.
		if len(covering) == 0 {
			return DeviceFinding{Check: FindingProvisioning, State: Missing, CheckedAtUtc: now}
		}
		return DeviceFinding{
			Check: FindingProvisioning, State: Satisfied, CheckedAtUtc: now,
			ExpiresAtUtc: soonestExpiry(covering),
		}
	}
}

// soonestExpiry is the SOONEST ExpirationDate among the profiles handed to it — nil when none of them states one
// this build can read.
//
// ⚠️ SOONEST, NOT FIRST, and the reason is the farm's from the slice before this one: among profiles that all
// cover the phone, the soonest is the moment the phone can stop working, and it never overstates the runway.
// Nothing here knows which profile Xcode will pick — this package's own MatchFor.ForTeam is file order — so
// "the one that will be used" would be a second guess wearing the clothes of knowledge. First is the one answer
// that cannot be argued at all: it is whatever the directory enumeration returned.
//
// NIL IS "NOT SAID", NEVER "DOES NOT EXPIRE". ios.Profile.Expires stays the plist value VERBATIM (a string,
// because what the doctor prints is what the plist said) — parsing here is for ordering and never replaces the
// stored text. A value that does not parse contributes NOTHING to the ordering rather than sorting as a
// far-future date: it cannot make the soonest date sooner or later, so dropping it neither invents runway nor
// hides a real date beside it.
func soonestExpiry(profiles []ios.Profile) *time.Time {
	var soonest *time.Time
	for _, p := range profiles {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(p.Expires))
		if err != nil {
			continue
		}
		at = at.UTC()
		if soonest == nil || at.Before(*soonest) {
			soonest = &at
		}
	}
	return soonest
}

// wdaOnPhoneFinding asks the phone itself: does WebDriverAgent answer on the port the driver uses? The MISSING
// state is deliberately two facts (installed AND not answering) — silence alone is the resting state this
// agent's own idle tracker produces, and a first-ever run has nothing installed yet; neither may wear the
// signature. The install question is asked only when the port is silent: an answering WDA is already the answer.
func wdaOnPhoneFinding(ctx context.Context, env Env, udid string, now time.Time) DeviceFinding {
	f := DeviceFinding{Check: FindingWdaOnPhone, State: Unknown, CheckedAtUtc: now}
	if env.IosWdaOnPhone == nil {
		return f
	}
	switch env.IosWdaOnPhone(ctx, udid).State {
	case wda.RunnerIdle, wda.RunnerInUse:
		f.State = Satisfied
	case wda.RunnerNotListening:
		if env.IosInstalledOnPhone == nil || env.IosWdaBundleID == "" {
			return f // silence with no install answer is just silence
		}
		installed, err := env.IosInstalledOnPhone(ctx, udid, ios.WdaRunnerBundleID(env.IosWdaBundleID))
		if err != nil || !installed {
			return f
		}
		f.State = Missing
	}
	return f
}

// projectFinding is iosWdaProjectTargets reduced to a state: does every WebDriverAgent target carry a team this
// machine can build as?
func projectFinding(ctx context.Context, env Env, now time.Time) DeviceFinding {
	f := DeviceFinding{Check: FindingWdaProject, State: Unknown, CheckedAtUtc: now}
	if env.WdaProjectPath == "" || env.Run == nil {
		return f
	}
	for _, target := range []string{wda.LibTarget, wda.RunTarget} {
		out, err := env.Run(ctx, "xcodebuild", "-showBuildSettings", "-project", env.WdaProjectPath, "-target", target)
		if err != nil {
			return f // could not read — Unknown, never a guess
		}
		team := settingValue(out, "DEVELOPMENT_TEAM")
		if team == "" || (env.IosTeamID != "" && !strings.EqualFold(team, env.IosTeamID)) {
			f.State = Missing
			return f
		}
	}
	f.State = Satisfied
	return f
}

// developerModeFindings is iosDeveloperMode reduced to per-UDID states.
func developerModeFindings(ctx context.Context, env Env, now time.Time) map[string]DeviceFinding {
	if env.IosDeveloperMode == nil {
		return nil
	}
	readings, err := env.IosDeveloperMode(ctx)
	if err != nil {
		return nil
	}
	out := make(map[string]DeviceFinding, len(readings))
	for _, r := range readings {
		state := Unknown
		switch {
		case r.Err != "":
			state = Unknown
		case r.Enabled:
			state = Satisfied
		default:
			state = Missing
		}
		out[r.UDID] = DeviceFinding{Check: FindingDevMode, State: state, CheckedAtUtc: now}
	}
	return out
}
