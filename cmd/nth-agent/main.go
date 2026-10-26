// Command nth-agent reports what devices are attached to this host, and — when it cannot find
// out — says so instead of reporting an empty list.
//
// PLATFORM NEVER APPEARS IN THE VERB: `devices` covers every platform this agent builds, and the
// platform is a field on each device. (mobilecli's whole surface is devices/apps/io/screenshot — not
// one verb names a platform; that is the bar.)
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/appium"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/state"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/termwrap"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// TWO_SURFACES_ONE_KEY — the two lines below that set a credential are FLUSH LEFT, and that is a decision.
//
// A command in a help text exists to be copied, and both of them are already 115 and 139 columns; indented to
// the description column they would be 148 and 172, which wraps in an ordinary terminal — and a WRAPPED command
// is one a person copies in two halves and gets wrong, which is this slice's whole subject. Leading spaces are
// harmless to a shell, so indentation is not unsafe; it is just worse here. The README shows the identical two
// lines inside fenced blocks, and TestBothSurfacesTellOneStoryAboutSettingACredential is what keeps them
// identical rather than merely similar.
const usageTemplate = `nth-agent — the NthCase device agent.

Usage:
  {{RUN}} devices          what is attached here, what is not, and what to do about it
  {{RUN}} devices --json   the same facts, machine-readable
  {{RUN}} connect          register this machine with your NthCase cloud and keep it updated
  {{RUN}} doctor           what this machine needs to run tests, what it has, and what to do
  {{RUN}} doctor --json    the same facts, machine-readable
  {{RUN}} setup-ios        the one-time WebDriverAgent build a real iPhone needs, on this machine
  {{RUN}} upload           put a build on your BrowserStack account and print the id connect needs
  {{RUN}} capture ...      network capture on this machine: enable, setup [SERIAL], disable (see below)
  {{RUN}} notices          the third-party licences this program carries (THIRD_PARTY_NOTICES.txt)
  {{RUN}} --help | -h      this text (exit 0); --version prints the build version and where the notices are (exit 0)

flags (both commands):
  --skip PLATFORM   do not ask about this platform on this machine (repeatable, comma-ok: android, ios).
                    The platform reports "not asked" — a deliberate decision, not a failure.

connect flags:
  --farm URL      the farm's base URL (or NTH_AGENT_FARM). Must be https:// — a plain http URL would
                  send your workspace token in cleartext; http is allowed only for exactly localhost
                  or a loopback address like 127.0.0.1, and never through a proxy.
  --appium URL    the LOCAL Appium endpoint next to your devices (or NTH_AGENT_APPIUM). With it, your
                  usable devices are registered for real runs and driven through an outbound tunnel.
                  A PREREQUISITE, AND IT STAYS ONE (decided 2026-08-23): you install and start Appium
                  yourself. We do not put software we do not own on hardware we do not own — so the
                  agent reports what is missing instead. Run '{{RUN}} doctor' to see exactly what
                  this machine needs. Without this flag the agent reports device health only.
  --token TOKEN   the workspace agent token (or NTH_AGENT_TOKEN). TWO EXPOSURES, and they are different:
                  passing --token puts the value in argv, visible to every process on this machine
                  (ps) for as long as the agent runs; typing NTH_AGENT_TOKEN=... inline before the
                  command writes it verbatim into your shell's HISTORY FILE, which outlives the
                  process, the terminal and the reboot. The second is longer-lived, so "prefer the
                  variable" on its own would be poor advice. This form avoids both, and says on screen
                  what it is asking for so it cannot be mistaken for your machine's login password:
printf 'workspace agent token: ' && read -rs NTH_AGENT_TOKEN && echo && export NTH_AGENT_TOKEN  # paste this line ALONE
                  The token is never written to disk or into any output.

BrowserStack — YOUR account, and the key never leaves this machine:
  --browserstack-user NAME   your BrowserStack username (or NTH_AGENT_BROWSERSTACK_USER).
  --browserstack-device SPEC  a device to offer, as "Name|OsVersion" — e.g. "Google Pixel 9|15.0" (or
                             NTH_AGENT_BROWSERSTACK_DEVICE). REPEATABLE: pass it once per device. Checked
                             against your account's own device list at startup, which costs nothing and
                             starts no session; a device your account does not offer is refused here,
                             naming the ones it does. You name them rather than us publishing your whole
                             catalogue: an account offers around a hundred devices and a Devices screen
                             of a hundred rows is worse than one with the handful you use.
  --browserstack-app ID      the build to run: a bs://… id from an upload, or a custom_id you gave one
                             (or NTH_AGENT_BROWSERSTACK_APP). REQUIRED whenever the two above are set —
                             BrowserStack runs its own devices, so the build must already be in their
                             storage; there is no adb and nothing for us to install. Not a secret.
  --browserstack-key KEY     your BrowserStack access key (or NTH_AGENT_BROWSERSTACK_KEY). TWO EXPOSURES,
                             and they are different: passing --browserstack-key puts the value in
                             argv, visible to every process on this machine (ps) for as long as the
                             agent runs; typing NTH_AGENT_BROWSERSTACK_KEY=... inline before the command
                             writes it verbatim into your shell's HISTORY FILE, which outlives the
                             process, the terminal and the reboot. The second is longer-lived, so
                             "prefer the variable" on its own would be poor advice.
                             This form avoids both, and says on screen what it is asking for so it
                             cannot be mistaken for your machine's login password:
printf 'BrowserStack access key: ' && read -rs NTH_AGENT_BROWSERSTACK_KEY && echo && export NTH_AGENT_BROWSERSTACK_KEY  # paste this line ALONE
                             THE COMMENT IS THE WARNING AND IT IS ON THE LINE: 'read' takes whatever
                             follows it as the value, so pasting this together with the next line
                             makes THAT LINE your key. Measured on a person, 2026-09-03. The prompt
                             comes from 'printf' rather than from 'read' because no one 'read' serves
                             both shells — zsh wants read -rs "VAR?...", which bash rejects as "not a
                             valid identifier", and bash wants read -rsp "..." VAR, which zsh rejects
                             with "-p: no coprocess".
                             The key is read here, merged into the session request this agent sends,
                             and never written to disk, never narrated, and never sent to the
                             platform — the platform supplies only a method, a path, a body and a
                             content type, so there is nowhere for it to go.
                             With BOTH set, the drive tunnel replays against BrowserStack's hub
                             instead of your local Appium. With neither, nothing changes. The devices
                             offered are the ones you name with --browserstack-device, above.

iOS real devices — THE SIGNATURE BELONGS TO THIS MACHINE:
  --ios-team-id TEAM          your Apple Developer Team id (or NTH_AGENT_IOS_TEAM_ID). A free Personal
                              Team works.
  --ios-wda-bundle-id ID      the WebDriverAgent bundle id registered to that team (or
                              NTH_AGENT_IOS_WDA_BUNDLE_ID), e.g. com.example.WebDriverAgentRunner.
  --ios-signing-id NAME       the signing identity name (or NTH_AGENT_IOS_SIGNING_ID). Defaults to
                              "Apple Development", which is what a Personal Team's certificate is called.

  --keep-wda-warm         after a run, WebDriverAgent keeps running on your phone (that is what makes the
                          next run start in about a fifth of the time, and why the phone shows "Automation
                          Running"). By DEFAULT this agent stops it once nothing has used it for five
                          minutes — never while a session is attached. Pass this to keep it up instead.
  --wda-idle-after DUR    change that window (e.g. 30s, 2m). Ignored with --keep-wda-warm.

  Driving a real iPhone means building and SIGNING WebDriverAgent on THIS machine, and the identity
  that does it belongs to this machine — not to the platform, which holds only its own. Declare both
  the team and the bundle id and your iPhones are offered for runs; declare neither and they are
  reported as attached and NOT offered, with the reason, instead of being offered as phones nothing
  can drive. Run '{{RUN}} doctor --ios-wda-bundle-id ID' to see whether the provisioning
  profile that build needs is in place.

setup-ios — the one-time build, so you do not have to assemble it from a log:
  {{RUN}} setup-ios --ios-team-id TEAM --ios-wda-bundle-id ID [--udid UDID]

  It builds Appium's OWN WebDriverAgent project for one attached iPhone with your team, using the
  xcodebuild flags Appium does not pass. It states exactly what it will do — including that Apple
  will REGISTER THAT PHONE against your developer account — prints the full command, and waits for
  you to type yes. Nothing is installed on this machine and nothing outside the build's output
  directory is written. On failure it shows xcodebuild's own error, not an exit code.

upload — how a build gets into your BrowserStack account, so connect has an id to name:
  {{RUN}} upload --file ./app/build/outputs/apk/release/app-release.apk

  It needs your BrowserStack credentials and nothing else: no farm, no workspace token. It prints the bs://
  id and the exact command to use it with, because an id nobody knows what to do with is half a fix.

  --file PATH      THIS AGENT reads the bytes and sends them. The build stays on your side of the network —
                   use this for anything not published on the public internet, which is every pre-release
                   build behind a NAT or a VPN. Accepted: .apk, .aab, .xapk, .ipa, up to 1 GB. The path,
                   the shape and the size are checked here, before the upload, so a mistake costs a second
                   rather than the whole transfer.
  --url URL        BROWSERSTACK fetches the address themselves. It must be reachable FROM THEIR NETWORK, not
                   from this machine — a build served by a local HTTP server on your laptop works in your
                   browser and fails here, which is exactly the assumption worth stating out loud.
  --custom-id NAME your own name for the build, echoed back and usable in --browserstack-app instead of the
                   hash. Uploading again with the same one repoints it at the new build, which is what makes
                   repeat runs traceable in their dashboard.

  Exactly one of --file and --url. Neither is not "upload nothing" — the agent says which one is missing.

  --udid UDID              which phone (only needed when more than one iPhone is attached)
  --wda-project PATH       a WebDriverAgent.xcodeproj elsewhere than Appium's default location
  --derived-data-path PATH where to build (default: beside this agent's own state)
  --yes                    answer the question in advance; without it you are asked

network capture — OFF until you turn it on here, set up each phone, and a scenario asks for it:
  {{RUN}} capture enable          says what capture does, asks you to type ENABLE, then makes this machine's CA
  {{RUN}} capture setup [SERIAL]  copies the CA's certificate to a test phone, says how to install it, and checks
  {{PAD}}                         that the phone's browser trusts it
  {{RUN}} capture disable         hands every phone back, deletes the CA's key and every capture file here
  During a capture run the WHOLE phone's traffic, not only the app's, goes through this machine — use a test phone.
  Only debug builds that trust user certificates can be read. Everything is masked on this machine before it is
  sent, and masked again when it reaches us; we keep what arrives with the run for one hour, and nothing raw is
  ever kept on this machine. The agent never sends the capture CA's key anywhere and keeps it out of backups, though
  a backup made before that, or by a tool that ignores it, may still hold a copy. The CA lasts one year. To turn it
  all off: {{RUN}} capture disable, then remove the certificate on the phone (on a Samsung, in both places).

  nth-agent never signs or re-signs your apps, and never touches your signing certificates or keychain. When you
  turn on network capture, it makes one certificate authority of its own, on this machine, used only to stand in
  for the servers the apps on your test phone talk to, so that their HTTPS can be read — and only in apps built to
  trust it. The agent never sends its private key anywhere and keeps it out of backups (a backup made before that,
  or by a tool that ignores it, may still hold a copy); it lasts one year, and you can remove it any time
  ({{RUN}} capture disable, then delete the certificate on the phone).

Exit code 0 means at least one probe established the truth (zero devices is a true answer).
Exit code 1 means no probe could answer — which is not the same as "nothing is attached".
`

// probeTimeout bounds EACH probe — one budget each, not one shared (see withDeadline). A hung daemon
// becomes a CouldNotAsk with a remedy rather than a command that never returns; on iOS the bound is
// on the WAIT rather than the call, and ios.Probe.listBounded says exactly what that does and does
// not stop.
const probeTimeout = 20 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run parses (answering questions and mistakes itself) and dispatches. Exit codes: 0 a question answered or
// work done; 1 the work failed; 2 a mistake on the command line.
// STDIN IS A PARAMETER, and that is not decoration: setup-ios asks a question and a question with a
// non-substitutable input cannot be sealed. Every other verb ignores it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	inv, code := parse(args, verbs, stdout, stderr)
	if inv == nil {
		return code
	}
	if inv.Verb == "setup-ios" {
		return runSetupIos(inv, stdin, stdout, stderr, execRunner)
	}
	if inv.Verb == "upload" {
		return runUpload(inv, stdout, stderr)
	}
	if inv.Verb == "connect" {
		return runConnect(inv, stdout, stderr)
	}
	if inv.Verb == "capture" {
		return runCapture(inv, stdin, stdout, stderr, realCaptureSeams())
	}
	if inv.Verb == "doctor" {
		return runDoctor(inv, stdout, stderr)
	}
	if inv.Verb == "notices" {
		return runNotices(stdout)
	}
	return runDevices(inv, stdout, stderr)
}

func runDevices(inv *invocation, stdout, stderr io.Writer) int {
	skip, err := connect.ParseSkip(inv.Values["--skip"])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMistake
	}

	// ONE DEADLINE EACH, not one shared between them. A single context created here was a single
	// budget: a slow adb spent the iOS probe's allowance, so the second probe could be cut short by
	// the first probe's bad day and report a timeout that was never its own.
	// ONE HOME for the probe list: connect.ProbeSet — the same wiring the connect loop uses, with the
	// same per-probe budgets. A probe list copied here would be the two-homes drift this replaced.
	offerUncabled := inv.Bools[inventory.OfferUncabledFlag]
	outcomes := connect.ProbeSet(skip, android.AdbRunner, ios.UsbmuxdListerFor(offerUncabled), probeTimeout, offerUncabled)(context.Background())
	// ONE LINE PER USABLE PHONE about its live view — the judgement the connect loop declares each phone's mirror port
	// from, over the same rule and the same loopback check, so `devices` and the farm cannot disagree about a phone.
	liveView := liveViewJudge(context.Background(), &liveview.Chooser{})

	if inv.Bools["--json"] {
		err = render.JSON(stdout, outcomes, liveView)
	} else {
		err = render.Human(stdout, outcomes, liveView)
	}
	if err != nil {
		fmt.Fprintf(stderr, "could not write the report: %v\n", err)
		return 1
	}
	return render.ExitCode(outcomes)
}

// liveViewJudge decides one phone's live view on this machine: the mirror port the chooser reserves for it, and what
// this machine's Appium drivers can do — read ONCE, and only when a phone is first asked about, because it runs
// `appium driver list --installed` (a read; it starts nothing). The one judgement `devices` and `connect` share.
func liveViewJudge(ctx context.Context, chooser *liveview.Chooser) func(platform inventory.Platform, udid string) liveview.Availability {
	drivers := sync.OnceValue(func() liveview.Drivers {
		bounded, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		return liveview.ReadDrivers(bounded, exec.LookPath, func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(out), err
		})
	})
	return func(platform inventory.Platform, udid string) liveview.Availability {
		return liveview.Judge(liveview.Facts{Platform: platform, Reservation: chooser.For(udid), Drivers: drivers()})
	}
}

// doctorTeamID resolves the declared team AND its source for the doctor command — the same rule the connect
// path uses, in one place so the two commands cannot disagree about which source won.
func doctorTeamID(inv *invocation) (string, string) {
	return fromFlagOrEnv(inv.first("--ios-team-id"), "--ios-team-id", os.Getenv("NTH_AGENT_IOS_TEAM_ID"), "NTH_AGENT_IOS_TEAM_ID")
}

// runDoctor answers what this machine needs to run somebody's tests. It probes the SAME inventory `devices` does
// — the answer is scoped to what this machine can actually see — and then asks what is missing around it.
//
// IT INSTALLS NOTHING, CHANGES NOTHING, STARTS NOTHING. See package doctor.
func runDoctor(inv *invocation, stdout, stderr io.Writer) int {
	skip, err := connect.ParseSkip(inv.Values["--skip"])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMistake
	}

	ctx := context.Background()
	offerUncabled := inv.Bools[inventory.OfferUncabledFlag]
	outcomes := connect.ProbeSet(skip, android.AdbRunner, ios.UsbmuxdListerFor(offerUncabled), probeTimeout, offerUncabled)(ctx)
	iosTeamID, iosTeamIDSource := doctorTeamID(inv)
	env := doctor.RealEnv(
		firstNonEmpty(inv.first("--appium"), os.Getenv("NTH_AGENT_APPIUM")),
		appiumProbe(probeTimeout, nil),
		// The phone's own Developer Mode gate. A READ of mobile_image_mounter — the same class of call the device
		// listing already makes — translated here into the doctor's own vocabulary so that package imports nothing.
		func(ctx context.Context) ([]doctor.DeveloperModeReading, error) {
			readings, err := ios.ReadDeveloperMode(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]doctor.DeveloperModeReading, 0, len(readings))
			for _, r := range readings {
				out = append(out, doctor.DeveloperModeReading{UDID: r.UDID, Enabled: r.Enabled, Err: r.Err})
			}
			return out, nil
		},
		// THE BUNDLE ID AND THE TEAM A PERSON WOULD SIGN WITH, neither guessed (same rule as --appium), the read
		// of this machine's provisioning profiles, and where Appium keeps its WebDriverAgent project. All seams,
		// so every check is testable without a Mac.
		firstNonEmpty(inv.first("--ios-wda-bundle-id"), os.Getenv("NTH_AGENT_IOS_WDA_BUNDLE_ID")),
		// …and WHERE the team was read from, carried beside it (item 1). A person told "CERT8KD3M1, from
		// NTH_AGENT_IOS_TEAM_ID" goes straight to the thing that is wrong.
		iosTeamID, iosTeamIDSource,
		func(ctx context.Context) ([]ios.Profile, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			return ios.Profiles(ctx, ios.ProvisioningProfileDirs(home))
		},
		// WHAT THIS MAC CAN SIGN AS — macOS's own `security`, a read, and the one command that can say "that is a
		// certificate's code, not a team" (item 3). We install nothing to ask it.
		ios.Identities,
		foundWdaProject(),
		// WHAT IS LISTENING ON ONE LOCAL PORT, in WebDriverAgent's own terms — so the collision check can tell a
		// stranger holding the port from Appium's own forward holding it for us.
		func(ctx context.Context, port uint16) wda.RunnerStatus {
			return wda.ReadStatus(ctx, &http.Client{Timeout: wda.ProbeTimeout}, fmt.Sprintf("http://127.0.0.1:%d", port))
		})
	report := doctor.Diagnose(ctx, env, outcomes)
	// NETWORK CAPTURE ON THIS MACHINE, read from its state folder alone — the certificate, never the key.
	if configDir, err := os.UserConfigDir(); err == nil {
		status := capture.ReadStatus(capture.StoreIn(configDir), time.Now())
		report.Capture = &status
	}

	if inv.Bools["--json"] {
		// UNAFFECTED BY THE DRESSING, deliberately: --json is the machine's answer and it already carries every
		// field, caveats included. No colour, no wrapping, no withholding — a consumer that parsed it yesterday
		// parses the identical bytes today.
		err = doctor.JSON(stdout, report)
	} else {
		// WHO DECIDES THE DRESSING: this call site, because it is the only thing that knows both the real stdout
		// (is it a terminal?) and what the person typed. The renderer is handed two answers and guesses neither.
		err = doctor.Human(stdout, report, doctor.Options{
			Color: doctor.ColorFor(stdout, inv.Bools["--no-color"]),
			Why:   inv.Bools[doctor.WhyFlag],
		})
	}
	if err != nil {
		fmt.Fprintf(stderr, "could not write the report: %v\n", err)
		return 1
	}
	return doctor.ExitCode(report)
}

// cloudRowsFrom flattens the customer's configured specs for the wire, TAKING EACH DEVICE'S PLATFORM FROM THE
// CATALOGUE ENTRY THAT MATCHED IT. The farm package deliberately does not import browserstack — a device row's
// shape must not depend on which vendor happens to host it — so the vendor's answer is translated here.
//
// A spec with no matching entry produces NO ROW rather than a guessed one; the caller has already refused the
// whole start in that case, so this is the second lock on the same door.
func cloudRowsFrom(specs []browserstack.DeviceSpec, catalogue []browserstack.CatalogueDevice) []farm.CloudDeviceSpec {
	rows := make([]farm.CloudDeviceSpec, 0, len(specs))
	for _, s := range specs {
		entry, ok := browserstack.Match(catalogue, s)
		if !ok {
			continue
		}
		platform := farm.WireAndroid
		if entry.IsIos() {
			platform = farm.WireIos
		}
		rows = append(rows, farm.CloudDeviceSpec{
			DeviceName: s.DeviceName, PlatformVersion: s.PlatformVersion, Wire: s.Wire(), Platform: platform,
		})
	}
	return rows
}

// runConnect wires the standing loop: real probes, real clock, the credential from the environment.
func runConnect(inv *invocation, stdout, stderr io.Writer) int {
	// FOLDED TO THE TERMINAL, AND ONLY IN ONE (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 4): everything connect says goes through
	// stdout, so this is the one place it is decided. A log or a pipe gets every line whole.
	stdout = termwrap.For(stdout)
	// Flags win over the environment; the environment is the preferred home for the token (see usage).
	farmURL := firstNonEmpty(inv.first("--farm"), os.Getenv("NTH_AGENT_FARM"))
	token := firstNonEmpty(inv.first("--token"), os.Getenv("NTH_AGENT_TOKEN"))
	appiumURL := firstNonEmpty(inv.first("--appium"), os.Getenv("NTH_AGENT_APPIUM"))
	// THIS MACHINE'S OWN SIGNING IDENTITY. Flags win over the environment, exactly like the three above. Neither
	// value is a secret and neither is echoed: they ride the same authenticated call the token does, and the
	// narration never prints them (sealed by the leak census).
	// The CONNECT path takes the value only: what rides from here is findings — a check name, a state word and a
	// stamp, no prose — so there is no sentence for a source to appear in. The doctor command, which renders
	// sentences, resolves both through doctorTeamID.
	iosTeamID := firstNonEmpty(inv.first("--ios-team-id"), os.Getenv("NTH_AGENT_IOS_TEAM_ID"))
	iosWdaBundleID := firstNonEmpty(inv.first("--ios-wda-bundle-id"), os.Getenv("NTH_AGENT_IOS_WDA_BUNDLE_ID"))
	iosSigningID := firstNonEmpty(inv.first("--ios-signing-id"), os.Getenv("NTH_AGENT_IOS_SIGNING_ID"))
	// THE CUSTOMER'S CLOUD ACCOUNT, same idiom as every other input: flag wins, environment twin behind it.
	browserStack := browserstack.Credentials{
		User: firstNonEmpty(inv.first("--browserstack-user"), os.Getenv("NTH_AGENT_BROWSERSTACK_USER")),
		Key:  firstNonEmpty(inv.first("--browserstack-key"), os.Getenv("NTH_AGENT_BROWSERSTACK_KEY")),
	}
	browserStackApp := firstNonEmpty(inv.first("--browserstack-app"), os.Getenv("NTH_AGENT_BROWSERSTACK_APP"))
	// THE DEVICES THE CUSTOMER WANTS OFFERED. Repeatable, like --skip; the environment twin takes one, which is
	// the ordinary case. WE DO NOT PUBLISH THE CATALOGUE: their account offers 110 device entries against a farm
	// holding 19 rows in total, so offering all of them would multiply the table roughly sixfold for one customer
	// and put 110 rows on their Devices screen. That is a PRODUCT judgement — a screen of devices nobody uses is
	// worse than a short list — and not a technical limit; nothing here would break if it published all 110.
	cloudSpecArgs := inv.Values["--browserstack-device"]
	if len(cloudSpecArgs) == 0 {
		if fromEnv := os.Getenv("NTH_AGENT_BROWSERSTACK_DEVICE"); fromEnv != "" {
			cloudSpecArgs = []string{fromEnv}
		}
	}
	cloudSpecs := make([]browserstack.DeviceSpec, 0, len(cloudSpecArgs))
	for _, raw := range cloudSpecArgs {
		spec, err := browserstack.ParseSpec(raw)
		if err != nil {
			fmt.Fprintf(stderr, "--browserstack-device: %v\n", err)
			return exitMistake
		}
		cloudSpecs = append(cloudSpecs, spec)
	}
	skip, err := connect.ParseSkip(inv.Values["--skip"])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMistake
	}
	if farmURL == "" || token == "" {
		fmt.Fprintln(stderr, "connect needs the farm URL (--farm or NTH_AGENT_FARM) and the workspace agent token (NTH_AGENT_TOKEN preferred; --token accepted).")
		return exitMistake
	}
	// HALF-CONFIGURED IS A MISTAKE, NOT A STATE. Reading one-of-two as "configured" sends up a session that
	// cannot authenticate; reading it as "absent" silently drives the local Appium while the person believes
	// they are on BrowserStack. Both readings hide the same typo, so neither is taken.
	// A DEVICE WITH NO ACCOUNT IS THE SAME MISTAKE AS HALF A CREDENTIAL, and it gets the same treatment: the
	// agent would silently publish nothing and the person would watch an empty Devices screen.
	if len(cloudSpecs) > 0 && !browserStack.Configured() {
		fmt.Fprintln(stderr, "--browserstack-device needs a BrowserStack account: supply --browserstack-user and "+
			"--browserstack-key (or NTH_AGENT_BROWSERSTACK_USER and NTH_AGENT_BROWSERSTACK_KEY) as well.")
		return exitMistake
	}
	if browserStack.Partial() {
		fmt.Fprintln(stderr, "BrowserStack needs BOTH halves: --browserstack-user and --browserstack-key (or NTH_AGENT_BROWSERSTACK_USER and NTH_AGENT_BROWSERSTACK_KEY). Only one was supplied.")
		return exitMistake
	}

	// VALIDATED AGAINST THE ACCOUNT'S OWN LIST, AT STARTUP, ON THE CUSTOMER'S MACHINE — and it is free: a REST
	// read that starts no session and spends no device-minute. The hub's own answer for a device an account does
	// not offer is "Platform and Browser not valid.", which names a platform and a browser that are both fine and
	// never mentions the device list; this names the list.
	var cloudRows []farm.CloudDeviceSpec
	if len(cloudSpecs) > 0 {
		catalogue, err := browserstack.Uploader{Client: &http.Client{Timeout: 30 * time.Second}}.
			Catalogue(context.Background(), browserStack)
		switch {
		case err != nil:
			// ⚠️ NO ROWS, AND THIS IS THE SAME RULE APPLIED CONSISTENTLY. The rule was already chosen: a spec is
			// NOT REFUSED on evidence we could not get, because "your account does not offer this" would be a
			// claim about a list nobody read. Publishing the rows anyway is the same mistake pointed the other
			// way — a row states a PLATFORM, and the only place that fact exists is the list we just failed to
			// read. Refusing on an unread list and asserting on an unread list are one error with two signs.
			//
			// So nothing is published and the person is told, in words they can act on. The local path is
			// untouched: an agent with a phone on the desk keeps offering it.
			fmt.Fprintf(stdout,
				"could not read your BrowserStack device list, so no cloud devices are being offered this run: %v\n"+
					"  Nothing is wrong with the devices you configured — they were not checked. The agent will "+
					"keep running and any local devices are unaffected; restart it once the list is reachable.\n", err)
		default:
			if refusal := browserstack.RefuseUnofferedSpecs(cloudSpecs, catalogue); refusal != "" {
				fmt.Fprintln(stderr, refusal)
				return exitMistake
			}
			fmt.Fprintf(stdout, "%d BrowserStack device(s) checked against the account's list of %d and offered\n",
				len(cloudSpecs), len(catalogue))
		}
		// ONE CALL, BOTH PATHS, AND THE DEGRADED ONE IS DERIVED RATHER THAN BRANCHED. On the failure path
		// `catalogue` is nil, so no spec matches an entry and no row is built — the "publish nothing when the
		// list could not be read" rule is a CONSEQUENCE of taking each row's platform from the list, not a
		// second decision that could drift from it. A branch here would be a place for the two to disagree.
		cloudRows = cloudRowsFrom(cloudSpecs, catalogue)
	}

	offerUncabled := inv.Bools[inventory.OfferUncabledFlag]
	var configDir string
	configDir, err = os.UserConfigDir()
	if err != nil {
		fmt.Fprintf(stderr, "cannot locate the user config directory: %v\n", err)
		return 1
	}
	// ONE connect PER STATE FOLDER (A_MACHINE_THAT_STAYS_THE_CUSTOMERS): held for as long as this one runs, and
	// taken before the identity is read, so a second one is refused before it registers anything.
	releaseFolder, err := state.HoldConnect(configDir)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer releaseFolder()
	nodeID, err := state.LoadOrCreateNodeID(configDir)
	if err != nil {
		fmt.Fprintf(stderr, "cannot establish a stable node identity: %v\n", err)
		return 1
	}
	hostName, _ := os.Hostname()

	// THE AGENT STOPS ON Ctrl-C, SIGTERM OR SIGHUP, and gives its phones back on the way out: a capture's phone left pointing at a
	// proxy that is about to vanish would have no internet until the next start (internal/capture). A second signal kills
	// at once, as before — unless it comes within duplicateSignalWindow, when it is the first one delivered again.
	ctx, interrupted := withSignals()

	// NETWORK CAPTURE ON THIS MACHINE — always wired: with capture off it declares Disabled, refuses every start in words,
	// and still answers the farm's handback question and hands back what an earlier run left on a phone.
	captureReachesLocal := inv.Bools[netcap.AllowLocalFlag]
	if captureReachesLocal {
		fmt.Fprintln(stdout, capture.LocalNetworkOpen)
	}
	captureController := capture.NewController(capture.Config{
		Store: capture.StoreIn(configDir), Adb: capture.RealAdb, Out: stdout, AgentVersion: version,
		ReachLocal: captureReachesLocal,
	})
	// GIVING THE PHONES BACK ON EVERY WAY OUT — a signal, the terminal closing, or the loop ending on its own (a refused
	// token): deferred here, so no return below can skip it.
	defer handBackOnExit(captureController, stdout)
	go captureController.Run(ctx)

	var driver *connect.Driver
	var onRegistered func()
	// THE LIVE VIEW'S TWO HALVES, one of each per process: the mirrors the drive tunnel may stream (kept by the connect
	// loop from the rows it publishes), and the tunnel's own state, which the heartbeat reads to say TunnelDown on
	// evidence and writes to wake the reconnect when the farm answers.
	mirrors := &liveview.Offered{}
	// …and the phones a session may be started on, kept by the same loop from the same rows (internal/allowed).
	phones := &allowed.Phones{}
	tunnelState := tunnel.NewState()
	// THE TUNNEL IS WHAT MOVED, AND ONLY THE TUNNEL. It used to be dialled inside `appiumURL != ""` because a
	// driver was the only thing that ever needed one. A cloud row is reached through the same tunnel and needs
	// no local Appium, so the CONDITION widens by exactly one disjunct; everything inside that depends on a
	// local Appium stays behind its own check below.
	if appiumURL != "" || len(cloudSpecs) > 0 {
		// REFUSED HERE, AND THE POSITION IS THE POINT. It used to sit beside the credential checks above, which
		// run whether or not this agent forwards anything — so an agent with BrowserStack credentials and no
		// --appium, which reports device health and drives nothing, was told it needed a build for a path it is
		// not on. A correct sentence delivered to somebody it does not apply to is the cheap end of the same
		// family this whole slice is about. The tunnel below is the only thing that ever forwards a session, so
		// this is where the requirement becomes real.
		if browserstack.MissingApp(browserStack, browserStackApp) {
			fmt.Fprintln(stderr, browserstack.MissingAppMessage)
			return exitMistake
		}
		drive := tunnel.New(tunnel.Config{
			FarmURL: farmURL, NodeID: nodeID, Token: token, AppiumURL: appiumURL,
			BrowserStack: browserStack, BrowserStackApp: browserStackApp,
			State: tunnelState, Mirrors: mirrors, Phones: phones, Capture: captureController,
			Sleep: func(ctx context.Context, d time.Duration) {
				timer := time.NewTimer(d)
				defer timer.Stop()
				select {
				case <-ctx.Done():
				case <-timer.C:
				}
			},
			Out: stdout,
		})
		// THE DIAL WAITS FOR THE FIRST REGISTRATION: /v1/nodes/{id}/tunnel for a node the farm has never seen is
		// a 404 — and "the drive tunnel dropped" for a socket that never existed was the first sentence a
		// customer read. connect.Run calls OnRegistered every time the farm accepts a registration.
		//
		// ⚠️ AND THE ONCE-NESS LIVES HERE, WITH THE PARTY THAT CAN STATE ITS REASON. It used to be a
		// package-level `var registeredOnce bool` inside internal/connect — a package that cannot see what this
		// closure does. THIS is where the reason is knowable: the closure starts Tunnel.Run, which is an
		// infinite reconnect loop (`for ctx.Err() == nil`, backoff with jitter, its own three sentences), so one
		// goroutine is enough and a second would be a second tunnel for one node. connect re-registers whenever
		// the farm says it has forgotten this node, and that path fires the hook again; the sync.Once is what
		// makes that harmless without connect having to know why.
		//
		// THE TUNNEL RUNS UNDER THE AGENT'S OWN CONTEXT, which a signal now cancels (withSignals): it used to be
		// context.Background(), recorded here as the line to change the day the agent grew one, and that day came with
		// network capture, whose phones are handed back on the way out.
		probeAppium := appiumProbe(5*time.Second, nil)
		var dialOnce sync.Once
		onRegistered = func() { dialOnce.Do(func() { go drive.Run(ctx) }) }
		driver = &connect.Driver{
			AppiumURL: appiumURL,
			// A CLOUD-ONLY AGENT HAS NOTHING TO PROBE and must not be asked to. The probe answers "is the local
			// Appium up"; with no --appium there is no local Appium, and a probe against "" would dial this
			// machine's own loopback — the exact wrong-device defect the farm's blank-address rule cites.
			Probe:        func(ctx context.Context) error { return probeAppium(ctx, appiumURL) },
			TunnelUp:     drive.Connected,
			TunnelEverUp: drive.EverConnected,
			Evidence:     tunnelState,
		}
	}

	// PUTTING THE PHONE BACK THE WAY WE FOUND IT. The probe and the stop are wired here, where the real device is
	// reachable; the loop holds only the decision. Both talk to WebDriverAgent's own HTTP API over a usbmuxd
	// forward that lives exactly as long as the call — see internal/ios.WithWdaPort for why that, and not the
	// CoreDevice tunnel or a reconfigured Appium.
	wdaClient := &http.Client{Timeout: wda.ProbeTimeout}
	// ONE CHOOSER, TWO READERS: it decides the number this machine DECLARES for each phone, and both the
	// registration and the idle probe ask it. A second derivation anywhere is a second chance to be wrong about
	// the same thing, which is the whole reason this slice exists.
	chooser := &wda.PortChooser{
		Probe: func(ctx context.Context, port uint16) wda.RunnerStatus {
			return wda.ReadStatus(ctx, wdaClient, fmt.Sprintf("http://127.0.0.1:%d", port))
		},
	}
	probeWda := func(ctx context.Context, udid string, port uint16) wda.RunnerStatus {
		var status wda.RunnerStatus
		if err := ios.WithWdaPort(udid, port, func(baseURL string) error {
			status = wda.ReadStatus(ctx, wdaClient, baseURL)
			return nil
		}); err != nil {
			// A forward we could not open is a question we could not ask — never an idle phone.
			return wda.RunnerStatus{State: wda.RunnerUnknown, Reason: err.Error()}
		}
		return status
	}
	stopWda := func(ctx context.Context, udid string, port uint16) error {
		return ios.WithWdaPort(udid, port, func(baseURL string) error { return wda.Shutdown(ctx, wdaClient, baseURL) })
	}

	idleAfter := connect.DefaultWdaIdleAfter
	if raw := inv.first("--wda-idle-after"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			fmt.Fprintf(stderr, "--wda-idle-after wants a positive duration like 30s or 2m, not %q\n", raw)
			return exitMistake
		}
		idleAfter = parsed
	}

	// WHETHER THE FARM KNOWS THIS NODE FROM THIS RUN: a goodbye is said only for a node this run registered.
	var registered atomic.Bool
	cfg := connect.Config{
		// THE SAME STRING `--version` PRINTS, and the only place either reads it. A published binary carries its
		// tag (stamped by release.yml's -ldflags); one somebody built themselves carries `dev`, which is the true
		// answer and is sent as such.
		AgentVersion:   version,
		Client:         &farm.Client{BaseURL: farmURL, Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}},
		NodeID:         nodeID,
		HostName:       hostName,
		Probes:         connect.ProbeSet(skip, android.AdbRunner, ios.UsbmuxdListerFor(offerUncabled), probeTimeout, offerUncabled),
		CloudDevices:   cloudRows,
		Interval:       connect.DefaultInterval,
		BackoffBase:    time.Second,
		BackoffCeiling: time.Minute,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out:    stdout,
		Driver: driver,
		OnRegistered: func() {
			registered.Store(true)
			if onRegistered != nil {
				onRegistered()
			}
		},

		IosTeamID:      iosTeamID,
		IosWdaBundleID: iosWdaBundleID,
		IosSigningID:   iosSigningID,

		KeepWdaWarm:  inv.Bools["--keep-wda-warm"],
		WdaIdleAfter: idleAfter,
		WdaProbe:     probeWda,
		WdaStop:      stopWda,
		WdaPortFor:   chooser.For,

		// THE CHECK THAT ALREADY KNEW: the doctor's riding answers, computed here and refreshed by the loop on
		// its own cadence (Config.FindingsMaxAge), so a session failure can name the cause the machine already
		// found instead of listing four possibilities.
		DeviceFindings: deviceFindingsProvider(iosTeamID, iosWdaBundleID, chooser.For),

		// THE LIVE VIEW: judged per phone, used by connect only when a local Appium exists — a mirror needs something
		// here to start it, and the judge reads Appium's driver list only when first asked, so an agent without one
		// never runs it. Without one the node says it cannot stream and no phone carries a mirror port.
		LiveView: liveViewJudge(context.Background(), &liveview.Chooser{}),
		Mirrors:  mirrors,
		Phones:   phones,

		// WHETHER THIS MACHINE CAPTURES is a registration fact; the loop registers anew when it changes.
		Capture: captureController,

		// HOW THIS AGENT WAS STARTED, which decides how it says a newer release is installed (AN_AGENT_THAT_KNOWS_IT_IS_OLD).
		Install: connect.HowStarted(ownProgram(), os.Getenv, isFile),
	}
	runErr := connect.Run(ctx, cfg)
	if code := interrupted(); code != 0 {
		// A STOP SAYS GOODBYE (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 2), now that the loop has stopped: a registration sent
		// after it would bring the node back. A loop that ended on its own — a refused token, a refused identity, an unsafe
		// address — says none: the farm refuses the goodbye for the same reason, and nothing of this machine is on offer.
		if runErr == nil && registered.Load() {
			goodbye(captureController, cfg.Client, nodeID, stdout, leaveBound)
		}
		return code
	}
	if runErr != nil {
		return 1
	}
	return 0
}

// capturing is what handBackOnExit needs of the capture controller.
type capturing interface {
	Running() int
	StopAll(ctx context.Context)
}

// handBackOnExit gives the phones back on the way out, bounded: a phone out of reach keeps its record, and the next start
// hands it back.
func handBackOnExit(c capturing, stdout io.Writer) {
	if c.Running() == 0 {
		return
	}
	fmt.Fprintln(stdout, "stopping — handing the phones this machine is capturing back first")
	back, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c.StopAll(back)
}

// leaveBound is the longest the goodbye waits for the farm. A second Ctrl-C still ends the agent at once (watchSignals).
const leaveBound = 3 * time.Second

// leaver is what goodbye needs of the farm client.
type leaver interface {
	Leave(ctx context.Context, nodeID string) (farm.CallStatus, error)
}

// goodbye gives the phones back, then tells the farm this node is leaving, so its phones go off offer now rather than when
// the farm's heartbeat window lapses (30-40 s). A goodbye the farm does not take is said in one line, and the window still
// covers it.
func goodbye(c capturing, farmClient leaver, nodeID string, stdout io.Writer, bound time.Duration) {
	handBackOnExit(c, stdout)
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	status, err := farmClient.Leave(ctx, nodeID)
	if status == farm.CallOK {
		fmt.Fprintln(stdout, "told the farm this machine is leaving — its phones are off offer there now")
		return
	}
	// THE FARM'S SENTENCE WHEN IT GAVE ONE (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO), already safe to print; the guesses below
	// only when it gave none — a 404 was "an older farm" even when today's farm said the node was not this workspace's.
	answered, reason, _ := farm.WhatTheFarmSaid(err)
	// BUT NOT A 404'S (NTH_AGENT_0_4_2): today's farm says "Unknown node '…'. Re-register.", and at a stop nothing will
	// register. That it did not know this node is the whole of it. A 404 with no sentence is an older farm's, below.
	if status == farm.CallNotFound && reason != "" {
		fmt.Fprintln(stdout, "the farm did not know this node (404), so it took no goodbye — this machine's phones go off offer within a minute")
		return
	}
	if reason != "" {
		fmt.Fprintf(stdout, "the farm did not take this machine's goodbye (%d). It says: “%s” — it takes the phones off offer within a minute instead\n", answered, reason)
		return
	}
	why := "no reason given"
	switch {
	case ctx.Err() != nil:
		why = fmt.Sprintf("no answer within %s", bound)
	case status == farm.CallNotFound:
		why = "it answered 404, which an older farm does"
	case status == farm.CallUnauthorized:
		why = "it refused this agent's token"
	case status == farm.CallBusy:
		why = "it was busy"
	case err != nil:
		why = farm.OneLineForTerminal(err.Error())
	}
	fmt.Fprintln(stdout, goodbyeNotTaken(why, err))
}

// ownProgram is where this agent's program is, links followed, or "" when the system cannot say.
func ownProgram() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// isFile answers whether path is a file this account can see.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// appiumProbe is the one way connect and doctor ask the customer's Appium whether it is up: on this machine's loopback, and
// only of a program this account runs (internal/localconn; A_MACHINE_THAT_STAYS_THE_CUSTOMERS). Its answer decides
// whether this machine's phones are offered, so another account's listener on the port must not be able to give it.
func appiumProbe(timeout time.Duration, owner localconn.Owner) func(ctx context.Context, baseURL string) error {
	client := localconn.Client("your Appium", timeout, owner)
	return func(ctx context.Context, baseURL string) error { return appium.Status(ctx, client, baseURL) }
}

// withSignals is the agent's own context, cancelled by the first SIGINT, SIGTERM or SIGHUP. interrupted reports the exit
// code that signal stands for (130, 143, 129), or 0 when none came. After the first, and after duplicateSignalWindow, the system's
// own handling is back, so a second Ctrl-C ends the process at once.
func withSignals() (context.Context, func() int) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, exitSignals...)
	var code atomic.Int32
	go watchSignals(signals, cancel, func() { signal.Stop(signals) }, &code,
		func() <-chan time.Time { return time.After(duplicateSignalWindow) })
	return ctx, func() int { return int(code.Load()) }
}

// exitSignals are the signals that end connect, handing its phones back on the way out.
// SIGHUP IS ONE OF THEM: closing the terminal connect runs in sends it, and it used to end the agent at once and leave
// a phone under capture pointing at a proxy that no longer existed (A_MACHINE_THAT_STAYS_THE_CUSTOMERS).
var exitSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// duplicateSignalWindow is how long after the first SIGINT or SIGTERM a repeat is that same signal delivered again. Ctrl-C
// reaches every process in the terminal's group, so under `npx` this agent hears it from the terminal and then again from
// the npm shim, which forwards what it hears (publish/npm/index.js), milliseconds later. Read as a second Ctrl-C, that copy
// would kill the agent while it hands its phones back. A person's own second Ctrl-C comes after this, and still ends it.
const duplicateSignalWindow = 2 * time.Second

// watchSignals waits for the first signal, records its exit code and cancels; it then absorbs every repeat until the
// window it starts at that moment ends, and only then restores the system's own handling (stop).
func watchSignals(signals <-chan os.Signal, cancel func(), stop func(), code *atomic.Int32, window func() <-chan time.Time) {
	received, ok := <-signals
	if !ok {
		return
	}
	switch received {
	case syscall.SIGTERM:
		code.Store(143)
	case syscall.SIGHUP:
		code.Store(129)
	default:
		code.Store(130)
	}
	cancel()
	ended := window()
	for {
		select {
		case <-signals:
		case <-ended:
			stop()
			return
		}
	}
}

// deviceFindingsProvider wires the doctor's riding checks into the connect loop: the SAME Env seams the doctor
// command reads (the phone's Developer Mode, this machine's provisioning profiles, Appium's WebDriverAgent
// project), reduced by doctor.DeviceFindings to per-udid wire findings — check names, state words and their
// computation stamp, never the doctor's prose. The loop owns the refresh cadence; every finding carries its own
// CheckedAtUtc, so the consumer can apply its trust rule whatever this process did.
func deviceFindingsProvider(iosTeamID, iosWdaBundleID string, wdaPortFor func(ctx context.Context, udid string) uint16) func(ctx context.Context, outcomes []inventory.Outcome) map[string][]farm.WireFinding {
	env := doctor.Env{
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(out), err
		},
		IosWdaBundleID: iosWdaBundleID,
		IosTeamID:      iosTeamID,
		IosProfiles: func(ctx context.Context) ([]ios.Profile, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			return ios.Profiles(ctx, ios.ProvisioningProfileDirs(home))
		},
		WdaProjectPath: foundWdaProject(),
		// THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE: the two phone-side reads the sequence inference needs — does
		// WebDriverAgent answer on the port the driver uses, and is it installed at all. Both over usbmuxd; the
		// probe is the same one the idle tracker trusts, on the same derived port.
		IosWdaOnPhone: func(ctx context.Context, udid string) wda.RunnerStatus {
			// THE DECLARED PORT, from the one chooser the registration publishes from — never a derivation of
			// this closure's own (the port census holds every forward in this file to that).
			port := wdaPortFor(ctx, udid)
			var status wda.RunnerStatus
			if err := ios.WithWdaPort(udid, port, func(baseURL string) error {
				status = wda.ReadStatus(ctx, &http.Client{Timeout: wda.ProbeTimeout}, baseURL)
				return nil
			}); err != nil {
				return wda.RunnerStatus{State: wda.RunnerUnknown, Reason: err.Error()}
			}
			return status
		},
		IosInstalledOnPhone: func(ctx context.Context, udid, bundleID string) (bool, error) {
			return ios.AppInstalled(udid, bundleID)
		},
		IosDeveloperMode: func(ctx context.Context) ([]doctor.DeveloperModeReading, error) {
			readings, err := ios.ReadDeveloperMode(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]doctor.DeveloperModeReading, 0, len(readings))
			for _, r := range readings {
				out = append(out, doctor.DeveloperModeReading{UDID: r.UDID, Enabled: r.Enabled, Err: r.Err})
			}
			return out, nil
		},
	}
	return func(ctx context.Context, outcomes []inventory.Outcome) map[string][]farm.WireFinding {
		raw := doctor.DeviceFindings(ctx, env, outcomes, time.Now().UTC())
		if len(raw) == 0 {
			return nil
		}
		out := make(map[string][]farm.WireFinding, len(raw))
		for udid, perDevice := range raw {
			wire := make([]farm.WireFinding, 0, len(perDevice))
			for _, f := range perDevice {
				wire = append(wire, farm.WireFinding{
					Check: f.Check, State: string(f.State), CheckedAtUtc: f.CheckedAtUtc,
					// …and WHEN IT STOPS BEING TRUE, when the check has such a date (item 5). Field-by-field like
					// everything on this wire: nothing is copied by struct assignment, so a field added to one
					// side is a compile-visible decision on the other rather than a silent ride.
					ExpiresAtUtc: f.ExpiresAtUtc,
				})
			}
			out[udid] = wire
		}
		return out
	}
}

// foundWdaProject is Appium's WebDriverAgent project on this machine, or "" when it is not where Appium puts it.
// EMPTY IS AN ANSWER the check knows how to say; a guessed path would make it answer about a project nobody builds.
func foundWdaProject() string {
	home, _ := os.UserHomeDir()
	found, _ := wda.FindProject(wda.ProjectSearchPaths(home, os.Getenv("APPIUM_HOME")))
	return found
}

// fromFlagOrEnv resolves one value the way firstNonEmpty does — the flag wins — AND says which source won.
//
// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 1: firstNonEmpty knows the answer for exactly one instruction and
// then throws it away, so every sentence downstream could say WHAT was declared and none could say WHERE it was
// read. An evening went on a team id that was wrong in NTH_AGENT_IOS_TEAM_ID while the doctor's remedy named a
// different variable entirely. The source is the flag's own name or the variable's name — never the value, which
// the caller already has, and never a guess: neither set returns two empty strings.
func fromFlagOrEnv(flagValue, flagName, envValue, envName string) (value, source string) {
	if flagValue != "" {
		return flagValue, flagName
	}
	if envValue != "" {
		return envValue, envName
	}
	return "", ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
