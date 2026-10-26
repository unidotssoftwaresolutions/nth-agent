package doctor_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// THE_CHECK_THAT_ALREADY_KNEW — the findings the machine rides to the farm: state words + timestamps, no prose.

func iphone(udid string) []inventory.Outcome {
	return []inventory.Outcome{inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: udid}}, nil)}
}

func settings(team string) string {
	return "    DEVELOPMENT_TEAM = " + team + "\n"
}

func TestEveryFindingCarriesItsComputationStamp(t *testing.T) {
	// SEAL 1's producer half: a finding with no stated freshness must not be mintable. The stamp is a required
	// parameter of the computation and lands on every finding — including the Unknowns, which are exactly the
	// ones a lazy implementation would leave unstamped.
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	found := doctor.DeviceFindings(context.Background(), doctor.Env{}, iphone("00008140-X"), now)

	perDevice := found["00008140-X"]
	// CHANGED 2026-08-25 (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE): four ride now — the WDA-on-phone answer joined
	// the three, because a fact you cannot ask for may still be entailed by the order in which things failed.
	if len(perDevice) != 4 {
		t.Fatalf("four checks ride (project, profile, developer mode, wda-on-phone), got %d: %+v", len(perDevice), perDevice)
	}
	for _, f := range perDevice {
		if !f.CheckedAtUtc.Equal(now) {
			t.Fatalf("finding %s rides without its computation stamp: %+v", f.Check, f)
		}
		if f.State != doctor.Unknown {
			t.Fatalf("an empty Env can establish nothing; %s must be unknown, got %s", f.Check, f.State)
		}
	}
}

func TestTheProjectFindingReducesTheDoctorsOwnCheck(t *testing.T) {
	now := time.Now().UTC()
	env := doctor.Env{
		WdaProjectPath: "/tmp/WebDriverAgent.xcodeproj",
		IosTeamID:      "TEAM7QZ4X2",
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			return settings(""), nil // no team on a target — the build fails with code 65 before the phone is involved
		},
	}
	found := doctor.DeviceFindings(context.Background(), env, iphone("u1"), now)
	if got := stateOf(t, found["u1"], doctor.FindingWdaProject); got != doctor.Missing {
		t.Fatalf("a target with no team is the operator's xcodebuild-code-65 — must ride Missing, got %s", got)
	}

	env.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		return settings("TEAM7QZ4X2"), nil
	}
	found = doctor.DeviceFindings(context.Background(), env, iphone("u1"), now)
	if got := stateOf(t, found["u1"], doctor.FindingWdaProject); got != doctor.Satisfied {
		t.Fatalf("every target carrying the declared team is Satisfied, got %s", got)
	}
}

func TestTheProfileFindingAppliesTheTeamRuleNotJustTheBundle(t *testing.T) {
	// The doctor's own hard-won rule (measured 2026-08-24): right bundle + wrong team fails the build exactly as
	// no profile does. The finding must say Missing, not the half-answer.
	now := time.Now().UTC()
	env := doctor.Env{
		IosWdaBundleID: "com.example.WebDriverAgentRunner",
		IosTeamID:      "CERT8KD3M1",
		IosProfiles: func(ctx context.Context) ([]ios.Profile, error) {
			return []ios.Profile{{Name: "wildcard", AppID: "TEAM7QZ4X2.*", TeamID: "TEAM7QZ4X2"}}, nil
		},
	}
	found := doctor.DeviceFindings(context.Background(), env, iphone("u1"), now)
	if got := stateOf(t, found["u1"], doctor.FindingProvisioning); got != doctor.Missing {
		t.Fatalf("a profile for another team cannot sign this build — must ride Missing, got %s", got)
	}
}

func TestDeveloperModeRidesPerDevice(t *testing.T) {
	now := time.Now().UTC()
	env := doctor.Env{
		IosDeveloperMode: func(ctx context.Context) ([]doctor.DeveloperModeReading, error) {
			return []doctor.DeveloperModeReading{
				{UDID: "on-phone", Enabled: true},
				{UDID: "off-phone", Enabled: false},
				{UDID: "shy-phone", Err: "could not ask"},
			}, nil
		},
	}
	outcomes := []inventory.Outcome{inventory.Answered(inventory.IOS,
		[]inventory.Offerable{{ID: "on-phone"}, {ID: "off-phone"}, {ID: "shy-phone"}, {ID: "unseen-phone"}}, nil)}
	found := doctor.DeviceFindings(context.Background(), env, outcomes, now)

	if got := stateOf(t, found["on-phone"], doctor.FindingDevMode); got != doctor.Satisfied {
		t.Fatalf("on-phone: %s", got)
	}
	if got := stateOf(t, found["off-phone"], doctor.FindingDevMode); got != doctor.Missing {
		t.Fatalf("off-phone: %s", got)
	}
	if got := stateOf(t, found["shy-phone"], doctor.FindingDevMode); got != doctor.Unknown {
		t.Fatalf("a phone that could not be asked is not a phone that said no: %s", got)
	}
	// The probe saw it, the read did not — a disagreement between two reads of one machine is UNKNOWN.
	if got := stateOf(t, found["unseen-phone"], doctor.FindingDevMode); got != doctor.Unknown {
		t.Fatalf("unseen-phone: %s", got)
	}
}

func TestNoFindingsForAndroidOrUnansweredOutcomes(t *testing.T) {
	now := time.Now().UTC()
	android := []inventory.Outcome{inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "RF..."}}, nil)}
	if found := doctor.DeviceFindings(context.Background(), doctor.Env{}, android, now); found != nil {
		t.Fatalf("an Android phone carries no iOS findings — 'not checked' must stay absent, got %+v", found)
	}
}

func stateOf(t *testing.T, findings []doctor.DeviceFinding, check string) doctor.State {
	t.Helper()
	for _, f := range findings {
		if f.Check == check {
			return f.State
		}
	}
	t.Fatalf("no %s finding in %+v", check, findings)
	return ""
}

// ── THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE ───────────────────────────────────────────────────────────────────

// theEveningsProfile is the operator's decoded profile, reduced to the fixture: it covers Malta and NOT the
// second phone — the one Apple's own refusal named.
func theEveningsProfile() []ios.Profile {
	return []ios.Profile{{
		Name:               "iOS Team Provisioning Profile: com.example.ada.WebDriverAgentRunner.xctrunner",
		AppID:              "TEAM7QZ4X2.com.example.ada.WebDriverAgentRunner.xctrunner",
		TeamID:             "TEAM7QZ4X2",
		ProvisionedDevices: []string{"00008140-0000C0FFEE0000A1"},
	}}
}

func profileEnv() doctor.Env {
	return doctor.Env{
		// Harmless seams for the checks Diagnose runs before the one under test — a nil seam is a panic, and
		// this fixture is about the provisioning check, not the toolchain.
		LookPath:     func(string) (string, error) { return "", errors.New("not on this fixture") },
		Run:          func(ctx context.Context, name string, args ...string) (string, error) { return "", errors.New("no") },
		DialTimeout:  func(network, address string, timeout time.Duration) (net.Conn, error) { return nil, errors.New("no") },
		AppiumStatus: func(ctx context.Context, baseURL string) error { return errors.New("down") },

		IosWdaBundleID: "com.example.ada.WebDriverAgentRunner",
		IosTeamID:      "TEAM7QZ4X2",
		IosProfiles:    func(ctx context.Context) ([]ios.Profile, error) { return theEveningsProfile(), nil },
	}
}

// SEAL 1 — one fixture, two phones, opposite answers in the SAME walk: the covered sibling satisfied, the
// refused phone missing, each finding carrying its udid by construction (the findings map is keyed by it).
func TestAProfileThatDoesNotCoverThisDeviceReportsMissingForThisDevice(t *testing.T) {
	now := time.Now().UTC()
	outcomes := []inventory.Outcome{inventory.Answered(inventory.IOS,
		[]inventory.Offerable{{ID: "00008140-0000C0FFEE0000A1"}, {ID: "00008110-0000C0FFEE0000B2"}}, nil)}

	found := doctor.DeviceFindings(context.Background(), profileEnv(), outcomes, now)

	if got := stateOf(t, found["00008140-0000C0FFEE0000A1"], doctor.FindingProvisioning); got != doctor.Satisfied {
		t.Fatalf("Malta is in the ProvisionedDevices list: %s", got)
	}
	if got := stateOf(t, found["00008110-0000C0FFEE0000B2"], doctor.FindingProvisioning); got != doctor.Missing {
		t.Fatalf("the phone xcodebuild refused by name must ride missing FOR THIS DEVICE: %s", got)
	}
}

// SEAL 2 — the third limit is STATED: the check reports a per-device state and its Limits line names what
// device coverage does and does not establish. A limit unstated is not a limit observed.
func TestTheThirdLimitIsStatedOnTheProvisioningCheck(t *testing.T) {
	outcomes := []inventory.Outcome{inventory.Answered(inventory.IOS,
		[]inventory.Offerable{{ID: "00008110-0000C0FFEE0000B2"}}, nil)}

	report := doctor.Diagnose(context.Background(), profileEnv(), outcomes)
	for _, c := range report.Checks {
		if c.Name != "iOS provisioning profile" {
			continue
		}
		if !strings.Contains(c.Limits, doctor.ProfileDeviceCoverageLimit) {
			t.Fatalf("the per-device state ships WITH its limits line, got: %s", c.Limits)
		}
		// …and the per-device truth is no longer silenced: the uncovered attached phone makes the check Missing,
		// with the udid in the detail — the granularity a customer needs to act.
		if c.State != doctor.Missing || !strings.Contains(c.Detail, "00008110-0000C0FFEE0000B2") {
			t.Fatalf("an uncovered attached phone must turn the check Missing and be named: %s / %s", c.State, c.Detail)
		}
		return
	}
	t.Fatal("no provisioning check in the report")
}

// The WDA-on-phone finding: satisfied only when it answers; MISSING is two facts (installed AND silent); silence
// with no install answer — or a phone that could not be asked — is unknown, never the signature.
func TestWdaOnPhoneFindingStates(t *testing.T) {
	now := time.Now().UTC()
	env := doctor.Env{IosWdaBundleID: "com.x.WebDriverAgentRunner"}
	probe := func(state wda.RunnerState) func(ctx context.Context, udid string) wda.RunnerStatus {
		return func(ctx context.Context, udid string) wda.RunnerStatus { return wda.RunnerStatus{State: state} }
	}
	installed := func(answer bool, err error) func(ctx context.Context, udid, bundle string) (bool, error) {
		return func(ctx context.Context, udid, bundle string) (bool, error) { return answer, err }
	}
	state := func(e doctor.Env) doctor.State {
		t.Helper()
		found := doctor.DeviceFindings(context.Background(), e, iphone("u1"), now)
		return stateOf(t, found["u1"], doctor.FindingWdaOnPhone)
	}

	e := env
	e.IosWdaOnPhone = probe(wda.RunnerIdle)
	if got := state(e); got != doctor.Satisfied {
		t.Fatalf("an answering WDA is satisfied: %s", got)
	}
	e.IosWdaOnPhone = probe(wda.RunnerNotListening)
	e.IosInstalledOnPhone = installed(true, nil)
	if got := state(e); got != doctor.Missing {
		t.Fatalf("INSTALLED and silent is the signature and rides missing: %s", got)
	}
	e.IosInstalledOnPhone = installed(false, nil)
	if got := state(e); got != doctor.Unknown {
		t.Fatalf("not installed is an ordinary first run, never the signature: %s", got)
	}
	e.IosInstalledOnPhone = nil
	if got := state(e); got != doctor.Unknown {
		t.Fatalf("silence with no install answer is just silence: %s", got)
	}
	e.IosWdaOnPhone = probe(wda.RunnerUnknown)
	e.IosInstalledOnPhone = installed(true, nil)
	if got := state(e); got != doctor.Unknown {
		t.Fatalf("a phone that could not be asked answered nothing: %s", got)
	}
}
