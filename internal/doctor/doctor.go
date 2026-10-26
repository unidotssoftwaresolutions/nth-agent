// Package doctor answers one question: WHAT DOES THIS MACHINE NEED IN ORDER TO RUN SOMEBODY'S TESTS, what does
// it have, and what should a person do about each gap.
//
// IT INSTALLS NOTHING, CHANGES NOTHING, AND STARTS NOTHING. That is the decision this package exists to serve
// (2026-08-23): software we do not own, on hardware we do not own, is not ours to place there. Every check here
// either reads a fact that is already true — a binary on PATH, a socket that is already listening, an endpoint
// that already answers — or reports that it could not establish one. Nothing in this package spawns a daemon,
// writes a file, or changes a setting.
//
// The consequence is that some things are UNKNOWABLE without changing the machine, and those are reported as
// unknown rather than as satisfied. `adb`'s server is the live example: the only way to learn whether it will
// start is to start it, so this package reports whether it is ALREADY listening and says plainly what it could
// not establish.
package doctor

import (
	"context"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// State is what a check established about one requirement. THREE STATES, because two of them are already
// confused in the field: the operator's Appium is installed, and that told us nothing about whether it answers.
type State string

const (
	// Satisfied: the thing is there AND answering. Nothing to do.
	Satisfied State = "satisfied"
	// Missing: it is not on this machine at all.
	Missing State = "missing"
	// NotRunning: it is installed and is not answering — the state that used to be reported as either of the others.
	NotRunning State = "not-running"
	// Unreachable: something IS answering somewhere, but not from here (a wrong host, a firewall, a container).
	Unreachable State = "unreachable"
	// Unknown: THIS CHECK COULD NOT ESTABLISH ITS ANSWER. Never a failure and never a pass — the same distinction
	// inventory.Outcome draws between CouldNotAsk and Answered, in the one vocabulary this agent already speaks.
	Unknown State = "unknown"
	// NotRequired: this machine does not need it (an Android-only host asked about an iOS toolchain).
	NotRequired State = "not-required"
)

// Blocking reports whether this state stops a run from happening here. Unknown and NotRequired are not blocking:
// "I could not tell" is not a fault, and neither is "you do not need this".
func (s State) Blocking() bool { return s == Missing || s == NotRunning || s == Unreachable }

// Blocks reports whether THIS CHECK's answer stops a run happening here — the state says WHAT IS TRUE, and this
// says WHAT IT COSTS, which are not the same question.
//
// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 4: `iOS WebDriverAgent project` reported MISSING and was counted in
// "2 things stop a run happening on this machine" while its OWN limits paragraph said a build that passes
// DEVELOPMENT_TEAM on the command line "overrides all of it" — which is what both builds that happen here do.
// It cannot be counted as a blocker and excused as a note in the same output. The FACT stays Missing (no team is
// on that target, and that is true), and the check says whether the fact stops anything.
//
// Advisory is opt-in per check, so every existing check keeps blocking exactly as it did.
func (c Check) Blocks() bool { return c.State.Blocking() && !c.Advisory }

// Check is one requirement and what was established about it.
type Check struct {
	// Name is what the requirement IS, in the words a person would search for.
	Name string `json:"name"`
	// Need says why the agent needs it, naming the code path that calls it — a requirement nobody can trace to a
	// caller is a requirement somebody guessed.
	Need  string `json:"need"`
	State State  `json:"state"`
	// Detail is what was actually observed: a version string, a path, an error. Empty when there was nothing to say.
	Detail string `json:"detail,omitempty"`
	// Remedy is what a person does about it. Empty ONLY when the state needs nothing done.
	Remedy string `json:"remedy,omitempty"`
	// Limits is WHAT THIS CHECK DOES NOT ESTABLISH, and it exists because one of these checks over-claimed and
	// cost an afternoon: it answered "satisfied — a profile covers …xctrunner" while xcodebuild refused the build,
	// because reading a profile off the disk is not establishing that a build will accept it. A check that states
	// only what it found teaches a person to trust it about things it never looked at. Empty is allowed and means
	// exactly one thing: this check's answer has no caveat worth a reader's time.
	Limits string `json:"limits,omitempty"`
	// Advisory says this check's answer is TRUE AND WORTH SAYING, and does not stop a run here — see
	// <see cref="Blocks"/>. Set it only where the check itself can name what makes the fact harmless (the
	// WebDriverAgent project's stored team is overridden by the DEVELOPMENT_TEAM both real builds pass on the
	// command line); an advisory nobody can justify is a blocker somebody hid.
	Advisory bool `json:"advisory,omitempty"`
}

// Report is every check, in the order they matter.
type Report struct {
	Checks []Check `json:"checks"`
	// Capture is network capture on this machine (capture.ReadStatus) — not a requirement for a run, so not a check: it is
	// stated after the checks, and never counted among what stops a run. nil = not read.
	Capture *capture.Status `json:"capture,omitempty"`
}

// Blocking is every check that stops a run happening on this machine.
func (r Report) Blocking() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Blocks() {
			out = append(out, c)
		}
	}
	return out
}

// ── the environment, behind a seam, so every check is testable without one ──────────────────────────────────

// Env is what the checks are allowed to observe. Every member READS; none of them changes the machine.
type Env struct {
	// LookPath resolves a binary on PATH (os/exec.LookPath).
	LookPath func(string) (string, error)
	// Run executes a read-only command and returns its combined output. Used ONLY for version/inventory reads.
	Run func(ctx context.Context, name string, args ...string) (string, error)
	// DialTimeout reports whether something is ALREADY listening at an address (net.DialTimeout). It connects
	// and hangs up; it never starts anything.
	DialTimeout func(network, address string, timeout time.Duration) (net.Conn, error)
	// AppiumStatus probes a configured Appium endpoint — the SAME call the connect loop makes before offering a
	// device (internal/appium.Status), so doctor and the drive path cannot disagree about what "up" means.
	AppiumStatus func(ctx context.Context, baseURL string) error

	// IosWdaBundleID is the WebDriverAgent bundle id a person WOULD sign with — from --ios-wda-bundle-id or the
	// environment. Empty = not given, and this command does not guess one (the same rule AppiumURL follows).
	IosWdaBundleID string
	// IosTeamID is the APPLE TEAM a person would build as. Empty = not given, and the checks then answer the
	// bundle-id half of the question and say so — never the whole question as if it had been asked.
	IosTeamID string
	// IosTeamIDSource is WHERE IosTeamID was read from — the flag's own name ("--ios-team-id") or the environment
	// variable's ("NTH_AGENT_IOS_TEAM_ID"). Empty when no team was given, and never a value: this says where to go
	// and change it, which is the one thing three doctor runs could not say while an evening went by
	// (A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 1). The resolver that knows the answer is the only thing that
	// ever knew it, so it is carried rather than re-derived — a check cannot ask an environment it was not given.
	IosTeamIDSource string
	// IosProfiles reads every provisioning profile on this machine. nil = this build cannot ask.
	IosProfiles func(ctx context.Context) ([]ios.Profile, error)
	// IosIdentities reads what THIS MAC CAN SIGN AS — internal/ios.Identities, i.e. macOS's own
	// `security find-identity -v -p codesigning` plus each certificate's subject. nil = this build cannot ask,
	// which the check renders as Unknown; an ERROR from it is also Unknown and says which, because `security`
	// missing and `security` refusing are different machines and neither is "no identities".
	IosIdentities func(ctx context.Context) ([]ios.Identity, error)
	// WdaProjectPath is Appium's WebDriverAgent project, or empty when it could not be found here.
	WdaProjectPath string
	// IosLocalPortStatus asks what is listening on one LOCAL port of this machine, in WebDriverAgent's own terms.
	// nil = this build cannot ask. Used for the collision check: the derived port is a fixed number for a phone,
	// and a fixed number is one something else here can already hold.
	IosLocalPortStatus func(ctx context.Context, port uint16) wda.RunnerStatus

	// AdbServer is where adb's server is for this process (android.ServerAddress) or why it will not be used; nil = adb's
	// own default, 127.0.0.1:5037. AdbServerOwner names the account behind a connection to it; nil = this build cannot
	// ask, and a listening server is then Unknown, never Satisfied (A_MACHINE_THAT_STAYS_THE_CUSTOMERS).
	AdbServer      func() (address, refusal string)
	AdbServerOwner func(net.Conn) (uid int, found bool)

	// AppiumURL is what the person told us, or empty. We do not guess one.
	AppiumURL string
	// IosDeveloperMode asks every attached iPhone whether Developer Mode is on — internal/ios.ReadDeveloperMode,
	// a pure read of the phone's mobile_image_mounter service. A seam, so a test can plant a machine rather than
	// need a handset. Nil ⇒ the check reports that it could not ask.
	IosDeveloperMode func(ctx context.Context) ([]DeveloperModeReading, error)

	// IosWdaOnPhone asks ONE phone's WebDriverAgent whether it answers, on the device port the driver actually
	// uses (wda.PortFor over a usbmuxd forward — internal/ios.WithWdaPort + wda.ReadStatus, the same probe the
	// idle tracker already trusts). Nil ⇒ this build cannot ask. THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE.
	IosWdaOnPhone func(ctx context.Context, udid string) wda.RunnerStatus
	// IosInstalledOnPhone asks ONE phone whether an app with this bundle id is installed — installation_proxy
	// over usbmuxd, the same transport the device listing uses; no tunnel, nothing privileged. Nil ⇒ cannot ask.
	// It exists to tell "installed and not answering" (the trust signature) from "never installed" (an ordinary
	// first run), which are different findings wearing the same silence.
	IosInstalledOnPhone func(ctx context.Context, udid, bundleID string) (bool, error)

	// MirrorPortFor reserves this machine's live-view mirror port for one phone — liveview.Chooser.For, the rule and
	// the loopback check the connect loop declares from. nil = this build cannot ask, and each phone's live view is
	// reported as unknown rather than as either answer.
	MirrorPortFor func(udid string) liveview.Reservation
}

// DeveloperModeReading is ONE phone's answer. Enabled is meaningful only when Err is empty: a phone that could not
// be asked is not a phone that said no, and this type refuses to let those two collapse into one boolean.
type DeveloperModeReading struct {
	UDID    string
	Enabled bool
	Err     string
}

// RealEnv is the production environment. iosDeveloperMode is injected by the caller rather than imported here, so
// this package keeps depending on nothing but the inventory vocabulary.
func RealEnv(appiumURL string, appiumStatus func(context.Context, string) error,
	iosDeveloperMode func(ctx context.Context) ([]DeveloperModeReading, error),
	iosWdaBundleID, iosTeamID, iosTeamIDSource string, iosProfiles func(ctx context.Context) ([]ios.Profile, error),
	iosIdentities func(ctx context.Context) ([]ios.Identity, error),
	wdaProjectPath string, iosLocalPortStatus func(ctx context.Context, port uint16) wda.RunnerStatus) Env {
	return Env{
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(out), err
		},
		DialTimeout:      net.DialTimeout,
		AdbServer:        func() (string, string) { return android.ServerAddress(os.Getenv) },
		AdbServerOwner:   netcap.OwnerOfListenerBehind,
		AppiumStatus:     appiumStatus,
		AppiumURL:        appiumURL,
		IosDeveloperMode: iosDeveloperMode,

		IosWdaBundleID:  iosWdaBundleID,
		IosTeamID:       iosTeamID,
		IosTeamIDSource: iosTeamIDSource,
		IosProfiles:     iosProfiles,
		IosIdentities:   iosIdentities,
		WdaProjectPath:  wdaProjectPath,

		IosLocalPortStatus: iosLocalPortStatus,

		MirrorPortFor: (&liveview.Chooser{}).For,
	}
}

// adbServerPort is adb's own default server port. Dialling it says whether a server is ALREADY up without
// starting one — `adb devices` and `adb start-server` would both start it, which this package may not do.
const adbServerPort = "127.0.0.1:5037"

const dialTimeout = 700 * time.Millisecond

// firstLine trims a command's output to something a person can read on one line.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
