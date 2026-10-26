package doctor_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// A MACHINE THAT CANNOT RUN A TEST SHOULD SAY WHAT IT IS MISSING.
//
// We do not install anything on somebody else's computer, and that is precisely why the report has to be good.

func androidSeen() []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1"}}, nil),
		inventory.Answered(inventory.IOS, nil, nil),
	}
}

// env builds a machine to diagnose. Every knob is something the real Env reads and nothing it changes.
type machine struct {
	onPath      map[string]string
	run         map[string]string
	runErr      map[string]error
	listening   map[string]bool
	appiumURL   string
	appiumError error
	// devMode is what the PHONES answer. nil ⇒ this machine has no way to ask, which is Unknown and not a refusal.
	devMode    []doctor.DeveloperModeReading
	devModeErr error
}

func (m machine) env() doctor.Env {
	return doctor.Env{
		LookPath: func(name string) (string, error) {
			if p, ok := m.onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("executable file not found in $PATH")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			key := strings.TrimSpace(name + " " + strings.Join(args, " "))
			if err, ok := m.runErr[key]; ok {
				return m.run[key], err
			}
			return m.run[key], nil
		},
		DialTimeout: func(_, address string, _ time.Duration) (net.Conn, error) {
			if m.listening[address] {
				server, client := net.Pipe()
				_ = server.Close()
				return client, nil
			}
			return nil, errors.New("connect: connection refused")
		},
		// The test's own machine runs its adb server as this account (a_machine_that_stays_the_customers_test.go plants
		// the other answers).
		AdbServerOwner: func(net.Conn) (int, bool) { return os.Geteuid(), true },
		AppiumStatus:   func(context.Context, string) error { return m.appiumError },
		AppiumURL:      m.appiumURL,
		IosDeveloperMode: func(context.Context) ([]doctor.DeveloperModeReading, error) {
			if m.devMode == nil && m.devModeErr == nil {
				return nil, errors.New("usbmuxd could not be asked: no such file or directory")
			}
			return m.devMode, m.devModeErr
		},
	}
}

func find(t *testing.T, r doctor.Report, name string) doctor.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, r.Checks)
	return doctor.Check{}
}

// ── THE THREE STATES ───────────────────────────────────────────────────────────────────────────────────────

// INSTALLED IS NOT RUNNING. The operator's Appium is installed; that told us nothing about whether it answers,
// and reporting the two as one value is the confusion this whole command exists to end.
func TestAnInstalledButNotRunningDependencyIsNeitherSatisfiedNorMissing(t *testing.T) {
	m := machine{
		onPath:      map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium"},
		appiumURL:   "http://127.0.0.1:4723",
		appiumError: errors.New("dial tcp 127.0.0.1:4723: connect: connection refused"),
	}
	report := doctor.Diagnose(context.Background(), m.env(), androidSeen())

	binary := find(t, report, "Appium")
	endpoint := find(t, report, "Appium endpoint")

	if binary.State != doctor.Satisfied {
		t.Fatalf("Appium IS installed here — that half is satisfied, got %q", binary.State)
	}
	if endpoint.State != doctor.NotRunning {
		t.Fatalf("installed-but-not-running is its own state, got %q", endpoint.State)
	}
	if endpoint.State == doctor.Satisfied || endpoint.State == doctor.Missing {
		t.Fatal("the two states this command exists to separate must not collapse into either of the others")
	}
	if !endpoint.State.Blocking() {
		t.Fatal("a run cannot happen without a live Appium — this state blocks")
	}
	if endpoint.Remedy == "" {
		t.Fatal("a blocking state must say what a person does about it")
	}
}

// SOMETHING ANSWERED, JUST NOT AS APPIUM — a third thing again, and not the same remedy.
func TestSomethingThatAnswersWrongIsUnreachableRatherThanNotRunning(t *testing.T) {
	m := machine{
		onPath:      map[string]string{"appium": "/usr/local/bin/appium"},
		appiumURL:   "http://10.0.0.9:4723",
		appiumError: errors.New("the Appium status route answered 404"),
	}
	endpoint := find(t, doctor.Diagnose(context.Background(), m.env(), androidSeen()), "Appium endpoint")

	if endpoint.State != doctor.Unreachable {
		t.Fatalf("an answer that is not Appium is not a stopped server, got %q", endpoint.State)
	}
	if !strings.Contains(endpoint.Remedy, "check the URL") {
		t.Fatalf("the remedy must send a person somewhere different from 'start it', got %q", endpoint.Remedy)
	}
}

func TestAMissingBinaryIsMissingAndSaysHowToGetIt(t *testing.T) {
	report := doctor.Diagnose(context.Background(), machine{}.env(), androidSeen())

	adb := find(t, report, "adb")
	if adb.State != doctor.Missing {
		t.Fatalf("adb is not on this PATH, got %q", adb.State)
	}
	if !strings.Contains(adb.Remedy, "platform-tools") {
		t.Fatalf("the remedy must name what to install, got %q", adb.Remedy)
	}
	if doctor.ExitCode(report) != 1 {
		t.Fatal("a machine that cannot run a test exits non-zero")
	}
}

// ── COULD NOT ESTABLISH IS NOT A FAILURE ───────────────────────────────────────────────────────────────────

// The rule the inventory vocabulary already turns on, read onto this command: an UNKNOWN is neither a pass nor a
// fault. Reporting one as a failure would train a reader to ignore the exit code.
func TestACheckThatCouldNotEstablishItsAnswerIsNotAFailure(t *testing.T) {
	// Everything is present; nobody said where Appium is. That is not a broken machine.
	m := machine{
		onPath:    map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium"},
		run:       map[string]string{"appium driver list --installed": "- uiautomator2@4.2.3 [installed (npm)]"},
		listening: map[string]bool{"127.0.0.1:5037": true},
	}
	report := doctor.Diagnose(context.Background(), m.env(), androidSeen())

	endpoint := find(t, report, "Appium endpoint")
	if endpoint.State != doctor.Unknown {
		t.Fatalf("no endpoint was given, so nothing was established, got %q", endpoint.State)
	}
	if endpoint.State.Blocking() {
		t.Fatal("'I could not tell' must never block — it is not a fault")
	}
	if doctor.ExitCode(report) != 0 {
		t.Fatal("an unknown is not a failure: the exit code stays 0 and the output carries the uncertainty")
	}
	if !strings.Contains(endpoint.Detail, "no endpoint was given") {
		t.Fatalf("the output must say WHAT could not be established, got %q", endpoint.Detail)
	}
}

// WE DO NOT GO LOOKING FOR AN APPIUM WE WERE NOT TOLD ABOUT. Guessing an endpoint and dialling it is a different
// decision from reporting one.
func TestNoEndpointIsGuessedWhenNoneWasGiven(t *testing.T) {
	var dialled []string
	env := machine{onPath: map[string]string{"appium": "/usr/local/bin/appium"}}.env()
	inner := env.AppiumStatus
	env.AppiumStatus = func(ctx context.Context, url string) error {
		dialled = append(dialled, url)
		return inner(ctx, url)
	}

	doctor.Diagnose(context.Background(), env, androidSeen())

	if len(dialled) != 0 {
		t.Fatalf("no endpoint was configured, so none may be probed — dialled %v", dialled)
	}
}

// adb's SERVER cannot be established without starting one, and this command starts nothing.
func TestTheAdbServerIsUnknownRatherThanFailedWhenNothingIsListening(t *testing.T) {
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb"}}
	server := find(t, doctor.Diagnose(context.Background(), m.env(), androidSeen()), "adb server")

	if server.State != doctor.Unknown {
		t.Fatalf("adb starts its own server on first use — not listening is not a fault, got %q", server.State)
	}
	if server.State.Blocking() {
		t.Fatal("an unknown adb server must not block a run")
	}
}

// ── SCOPED TO WHAT THIS MACHINE CAN DO ─────────────────────────────────────────────────────────────────────

// A machine with no iPhone attached is not broken; it is an Android machine.
//
// THIS ASSERTION MOVED RATHER THAN WENT. It named one check, "iOS driving", which was a note that returned Unknown
// and declined. iOS now has the same three-check shape Android has, so the property it was protecting — an Android
// machine is never told it is broken for not being a Mac with an iPhone — is asserted across ALL of them.
func TestAnIosGapOnAnAndroidMachineIsANoteNotAFailure(t *testing.T) {
	m := machine{
		onPath:    map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium"},
		run:       map[string]string{"appium driver list --installed": "- uiautomator2@4.2.3 [installed (npm)]"},
		listening: map[string]bool{"127.0.0.1:5037": true},
		appiumURL: "http://127.0.0.1:4723",
	}
	report := doctor.Diagnose(context.Background(), m.env(), androidSeen())

	for _, name := range []string{"Appium driver xcuitest", "Xcode command-line tools", "iOS Developer Mode"} {
		c := find(t, report, name)
		if c.State != doctor.NotRequired {
			t.Fatalf("no iOS device was seen, so %q is not required here, got %q", name, c.State)
		}
		if c.State.Blocking() {
			t.Fatalf("%q must not tell an Android machine it is broken", name)
		}
	}
	if doctor.ExitCode(report) != 0 {
		t.Fatal("this machine can run a test — nothing blocks")
	}
}

// …and when an iPhone IS attached, the machine is TOLD WHAT IT NEEDS.
//
// THIS ASSERTION MOVED TOO, and its premise is what this slice changed. It required the remedy to say
// "not yet supported" — a sentence that also claimed the phone "is not offered for runs" while this agent
// publishes iOS offerables and the product offers them. The honest answer is no longer a decline: iOS gets the
// checks Android has, and each one is a thing a person can act on.
func TestAnAttachedIphoneIsToldWhatItNeeds(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030", Model: "iPhone 15"}}, nil),
	}
	// A bare machine: no Appium, no Xcode, no way to ask the phone.
	report := doctor.Diagnose(context.Background(), machine{}.env(), outcomes)

	driver := find(t, report, "Appium driver xcuitest")
	if driver.State != doctor.Unknown {
		t.Fatalf("with no Appium on PATH its driver list cannot be read, got %q", driver.State)
	}
	xcode := find(t, report, "Xcode command-line tools")
	if xcode.State != doctor.Missing {
		t.Fatalf("an attached iPhone with no xcodebuild is a real, actionable gap, got %q", xcode.State)
	}
	if xcode.Remedy == "" {
		t.Fatal("every gap this agent names must be a sentence a person can act on")
	}
	if !strings.Contains(xcode.Need, "xcuitest driver") {
		t.Fatalf("whose requirement it is must be stated — it is Appium's driver's, not this agent's: %q", xcode.Need)
	}
	// NOTHING here may claim the phone is not offered: this agent publishes it.
	for _, c := range report.Checks {
		for _, sentence := range []string{c.Need, c.Detail, c.Remedy} {
			if strings.Contains(strings.ToLower(sentence), "not offered") {
				t.Fatalf("a doctor sentence claims iOS devices are not offered while the agent offers them: %q", sentence)
			}
		}
	}
}

// A driver is only required for a platform this machine actually sees.
func TestTheAndroidDriverIsNotRequiredOnAMachineWithNoAndroidDevice(t *testing.T) {
	outcomes := []inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)}
	driver := find(t, doctor.Diagnose(context.Background(), machine{}.env(), outcomes), "Appium driver uiautomator2")

	if driver.State != doctor.NotRequired {
		t.Fatalf("no Android device is here, so its driver is not a gap, got %q", driver.State)
	}
}

// A PLATFORM THAT COULD NOT BE ASKED IS STILL IN PLAY — "I could not ask" must never read as "there is nothing
// there", which is the rule the whole inventory vocabulary turns on.
func TestAPlatformThatCouldNotBeAskedIsStillInPlay(t *testing.T) {
	outcomes := []inventory.Outcome{inventory.CouldNotAsk(inventory.Android, "adb is not on PATH", "install platform-tools")}
	driver := find(t, doctor.Diagnose(context.Background(), machine{}.env(), outcomes), "Appium driver uiautomator2")

	if driver.State == doctor.NotRequired {
		t.Fatal("a platform nobody could ask about must not be written off as absent")
	}
}

// ── WHAT IT PRINTS ─────────────────────────────────────────────────────────────────────────────────────────

// plainView is the DEFAULT rendering: no colour, no --why. CHANGED 2026-08-26
// (NINE_GREENS_AND_THE_TWO_THAT_MATTER): Human gained an Options argument because the caller — not the renderer —
// is the only thing that knows whether stdout is a terminal and what the person typed. These tests assert what
// the output SAYS, and every one of their assertions is about text that colour never touches, so the plain view
// is the right view for them; the dressing has its own seals in nine_greens_test.go.
var plainView = doctor.Options{}

func TestTheHumanOutputSeparatesTheThreeStatesInWords(t *testing.T) {
	m := machine{
		onPath:      map[string]string{"appium": "/usr/local/bin/appium"},
		appiumURL:   "http://127.0.0.1:4723",
		appiumError: errors.New("dial tcp 127.0.0.1:4723: connect: connection refused"),
	}
	var out strings.Builder
	if err := doctor.Human(&out, doctor.Diagnose(context.Background(), m.env(), androidSeen()), plainView); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	for _, want := range []string{"INSTALLED, NOT RUNNING", "MISSING", "could not establish", "We do not install anything on your machine"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the output must contain %q, got:\n%s", want, text)
		}
	}
}

func TestAHealthyMachineDoesNotClaimEverythingIsFine(t *testing.T) {
	m := machine{
		onPath:    map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium"},
		run:       map[string]string{"appium driver list --installed": "- uiautomator2@4.2.3 [installed (npm)]"},
		listening: map[string]bool{"127.0.0.1:5037": true},
	}
	var out strings.Builder
	_ = doctor.Human(&out, doctor.Diagnose(context.Background(), m.env(), androidSeen()), plainView)

	if !strings.Contains(out.String(), "unknown, not clear") {
		t.Fatalf("some checks are unknown here; the summary must not read as an all-clear, got:\n%s", out.String())
	}
}

// ── THE SUMMARY LINE IS A SENTENCE A PERSON READS AT THE WORST MOMENT ──────────────────────────────────────────
//
// It used to print "1 thing(s) stop a run happening on this machine:". Two defects in one line: a parenthesised
// plural, which is a program telling the reader it could not be bothered to agree with itself, and "stop" for a
// count of one. Nothing pinned it, which is how it survived — so this is the pin as well as the fix.
func TestTheSummaryLineAgreesWithItsOwnCount(t *testing.T) {
	blocked := func(n int) doctor.Report {
		var r doctor.Report
		for i := 0; i < n; i++ {
			r.Checks = append(r.Checks, doctor.Check{
				Name:   fmt.Sprintf("requirement-%d", i),
				State:  doctor.Missing,
				Remedy: "install it",
			})
		}
		return r
	}

	var one strings.Builder
	if err := doctor.Human(&one, blocked(1), plainView); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(one.String(), "One thing stops a run happening on this machine:") {
		t.Fatalf("one blocking check must read as a sentence, got:\n%s", one.String())
	}

	var two strings.Builder
	if err := doctor.Human(&two, blocked(2), plainView); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(two.String(), "2 things stop a run happening on this machine:") {
		t.Fatalf("two blocking checks must read as a sentence, got:\n%s", two.String())
	}

	// AND THE PARENTHESISED PLURAL IS GONE FROM BOTH, which is the defect rather than a side effect of the wording.
	for _, out := range []string{one.String(), two.String()} {
		if strings.Contains(out, "thing(s)") {
			t.Fatalf("a parenthesised plural is a customer-facing shrug, got:\n%s", out)
		}
	}
}
