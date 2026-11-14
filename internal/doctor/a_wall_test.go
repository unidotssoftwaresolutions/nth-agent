package doctor_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// A_WALL_THE_CUSTOMER_SHOULD_NOT_HAVE_TO_CLIMB item 2 — the checks that were silent, and the one that claimed
// more than it established.
//
// MEASURED ON A REAL MACHINE, 2026-08-24, and the numbers below are that machine's: the one signing identity is
// `Apple Development: <email> (CERT8KD3M1)` and its certificate's subject carries `OU=TEAM7QZ4X2`; both
// provisioning profiles say `TeamIdentifier: TEAM7QZ4X2`; the deployment's IOS_XCODE_ORG_ID said CERT8KD3M1. With
// that team, xcodebuild answered "No profiles for 'com.example.ada.WebDriverAgentRunner.xctrunner' were found"
// — about a bundle id whose profile was sitting on the disk — and `doctor` said "satisfied".

func iphoneSeen() []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008140-0000C0FFEE0000A1", Manufacturer: "Apple"}}, nil),
	}
}

// The profiles as they really are on that Mac: the team is TEAM7QZ4X2, not the certificate-name ten characters.
func realProfiles() []ios.Profile {
	return []ios.Profile{
		{
			Name:    "iOS Team Provisioning Profile: com.example.ada.WebDriverAgentRunner.xctrunner",
			AppID:   "TEAM7QZ4X2.com.example.ada.WebDriverAgentRunner.xctrunner",
			TeamID:  "TEAM7QZ4X2",
			Expires: "2026-08-31T14:41:44Z",
		},
	}
}

func envFor(teamID string, profiles []ios.Profile, targetTeams map[string]string) doctor.Env {
	return doctor.Env{
		LookPath:    func(string) (string, error) { return "", context.Canceled },
		DialTimeout: func(string, string, time.Duration) (net.Conn, error) { return nil, context.Canceled },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			if name != "xcodebuild" {
				return "", nil
			}
			for i, a := range args {
				if a == "-target" && i+1 < len(args) {
					return "    DEVELOPMENT_TEAM = " + targetTeams[args[i+1]] + "\n", nil
				}
			}
			return "", nil
		},
		IosWdaBundleID: "com.example.ada.WebDriverAgentRunner",
		IosTeamID:      teamID,
		IosProfiles:    func(context.Context) ([]ios.Profile, error) { return profiles, nil },
		WdaProjectPath: "/appium/WebDriverAgent.xcodeproj",
	}
}

func checkNamed(t *testing.T, report doctor.Report, name string) doctor.Check {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q", name)
	return doctor.Check{}
}

// THE OVER-CLAIM, CLOSED. A profile whose team is not the team you build as is not a profile you can build with,
// and the check must say so — with the team id the machine actually has, which is the sentence that turns a
// failed build into a fixed one.
func TestAProfileForAnotherTeamIsNotSatisfied(t *testing.T) {
	teams := map[string]string{"WebDriverAgentLib": "TEAM7QZ4X2", "WebDriverAgentRunner": "TEAM7QZ4X2"}
	report := doctor.Diagnose(context.Background(), envFor("CERT8KD3M1", realProfiles(), teams), iphoneSeen())
	c := checkNamed(t, report, "iOS provisioning profile")

	if c.State != doctor.Missing {
		t.Fatalf("a profile belonging to another team must not be satisfied, got %s — %s", c.State, c.Detail)
	}
	if !strings.Contains(c.Remedy, "TEAM7QZ4X2") {
		t.Fatalf("the remedy must NAME the team this machine actually has; got %q", c.Remedy)
	}
	if !strings.Contains(c.Detail, "CERT8KD3M1") {
		t.Fatalf("and say what was declared instead; got %q", c.Detail)
	}
}

func TestTheRightTeamIsSatisfiedAndStillSaysWhatItDidNotEstablish(t *testing.T) {
	teams := map[string]string{"WebDriverAgentLib": "TEAM7QZ4X2", "WebDriverAgentRunner": "TEAM7QZ4X2"}
	report := doctor.Diagnose(context.Background(), envFor("TEAM7QZ4X2", realProfiles(), teams), iphoneSeen())
	c := checkNamed(t, report, "iOS provisioning profile")

	if c.State != doctor.Satisfied {
		t.Fatalf("the declared team matches the profile's — expected satisfied, got %s (%s)", c.State, c.Detail)
	}
	if !strings.Contains(c.Detail, "2026-08-31") {
		t.Fatalf("a Personal Team's profile lasts SEVEN DAYS; the date is the fact, so it must be shown: %q", c.Detail)
	}
	// THE MODEL SENTENCE. The answer that cost an afternoon was a GREEN one, so a green answer is exactly where
	// "what this did not establish" has to appear.
	if c.Limits == "" {
		t.Fatal("a satisfied check must still say what it did not establish")
	}
	if !strings.Contains(c.Limits, "trusted on the phone") {
		t.Fatalf("the phone-trust blind spot is the one this cannot read — it must be named: %q", c.Limits)
	}
}

// WALL 4, WHICH NOTHING CAUGHT. The profile is fine and the build still fails, because a target carries no team.
func TestATargetWithNoTeamIsCaughtEvenWhenTheProfileIsPerfect(t *testing.T) {
	teams := map[string]string{"WebDriverAgentLib": "", "WebDriverAgentRunner": "TEAM7QZ4X2"}
	report := doctor.Diagnose(context.Background(), envFor("TEAM7QZ4X2", realProfiles(), teams), iphoneSeen())

	if profile := checkNamed(t, report, "iOS provisioning profile"); profile.State != doctor.Satisfied {
		t.Fatalf("the profile really is fine — this test is about the OTHER check; got %s", profile.State)
	}
	c := checkNamed(t, report, "iOS WebDriverAgent project")
	if c.State != doctor.Missing {
		t.Fatalf("a target with no team must be caught, got %s — %s", c.State, c.Detail)
	}
	// THE WORDING IS PART OF THE SEAL. An unset team and a foreign one are different situations with different
	// fixes, and this check has a branch for each — the first draft of this test asserted only that the TARGET was
	// named, so removing the unset branch entirely left it green: the foreign branch absorbed the case and said
	// "WebDriverAgentLib is on team , and you declared …". Measured by injection 3, which is why it now pins the
	// sentence a person would act on.
	if !strings.Contains(c.Detail, "no team is set on WebDriverAgentLib") {
		t.Fatalf("an UNSET team must be reported as unset, naming the target a person did not click: %q", c.Detail)
	}
	if !strings.Contains(c.Remedy, "setup-ios") {
		t.Fatalf("the remedy must offer the command that removes the wall, not only the manual steps: %q", c.Remedy)
	}
	if !strings.Contains(c.Limits, "command line") {
		t.Fatalf("and say what it does not establish — a command-line override beats the stored team: %q", c.Limits)
	}
}

// A TEAM ON EVERY TARGET, BUT NOT YOURS. The other direction of the same check.
func TestATargetOnAnotherTeamIsCaught(t *testing.T) {
	teams := map[string]string{"WebDriverAgentLib": "SOMEONEELSE", "WebDriverAgentRunner": "TEAM7QZ4X2"}
	report := doctor.Diagnose(context.Background(), envFor("TEAM7QZ4X2", realProfiles(), teams), iphoneSeen())
	c := checkNamed(t, report, "iOS WebDriverAgent project")
	if c.State != doctor.Missing {
		t.Fatalf("a target on another team must be caught, got %s — %s", c.State, c.Detail)
	}
	if !strings.Contains(c.Detail, "SOMEONEELSE") {
		t.Fatalf("naming the team is what makes it fixable: %q", c.Detail)
	}
}

// THE CORRECTED REMEDY. The old one sent people to a plain Build (which produces no .xctrunner — the very thing
// the check looks for) and never mentioned the library target. A remedy that names the wrong menu item spends a
// person's trust as well as their time.
func TestTheRemedyNamesBuildForTestingAndBothTargets(t *testing.T) {
	teams := map[string]string{"WebDriverAgentLib": "TEAM7QZ4X2", "WebDriverAgentRunner": "TEAM7QZ4X2"}
	report := doctor.Diagnose(context.Background(), envFor("TEAM7QZ4X2", nil, teams), iphoneSeen())
	c := checkNamed(t, report, "iOS provisioning profile")

	if c.State != doctor.Missing {
		t.Fatalf("no profiles at all must be missing, got %s", c.State)
	}
	for _, must := range []string{"setup-ios", "Build For Testing", "WebDriverAgentLib", "WebDriverAgentRunner", ".xctrunner"} {
		if !strings.Contains(c.Remedy, must) {
			t.Fatalf("the remedy must say %q; got %q", must, c.Remedy)
		}
	}
	// REMOVAL BEFORE INSTRUCTION: the command comes first, the manual steps are the fallback.
	if strings.Index(c.Remedy, "setup-ios") > strings.Index(c.Remedy, "Xcode") {
		t.Fatalf("the one-command answer must come before the by-hand one: %q", c.Remedy)
	}
}

// NO TEAM GIVEN IS HALF A QUESTION, AND IT SAYS SO — the shape the old check should have had all along.
func TestWithNoTeamTheCheckAnswersTheHalfItWasAskedAndSaysWhichHalf(t *testing.T) {
	teams := map[string]string{"WebDriverAgentLib": "TEAM7QZ4X2", "WebDriverAgentRunner": "TEAM7QZ4X2"}
	report := doctor.Diagnose(context.Background(), envFor("", realProfiles(), teams), iphoneSeen())
	c := checkNamed(t, report, "iOS provisioning profile")

	if c.State != doctor.Satisfied {
		t.Fatalf("the bundle-id half is satisfied, got %s", c.State)
	}
	if !strings.Contains(c.Detail, "no team was given") {
		t.Fatalf("it must say which half it answered: %q", c.Detail)
	}
	if !strings.Contains(c.Limits, "--ios-team-id") {
		t.Fatalf("and what that leaves unestablished: %q", c.Limits)
	}
}

// ── THE_PORT_BELONGS_TO_THE_MACHINE_TOO: a collision is a STATED outcome ──────────────────────────────────────

func portEnv(local func(context.Context, uint16) wda.RunnerStatus) doctor.Env {
	env := envFor("TEAM7QZ4X2", realProfiles(), map[string]string{"WebDriverAgentLib": "TEAM7QZ4X2", "WebDriverAgentRunner": "TEAM7QZ4X2"})
	env.IosLocalPortStatus = local
	return env
}

func TestSomethingElseHoldingTheDerivedPortIsReportedWithTheNumber(t *testing.T) {
	// The port is DERIVED, so it is a fixed number for that phone — and a fixed number is one something else here
	// can already hold. "Something answered and it was not WebDriverAgent" is exactly that.
	report := doctor.Diagnose(context.Background(),
		portEnv(func(context.Context, uint16) wda.RunnerStatus {
			return wda.RunnerStatus{State: wda.RunnerUnknown, Reason: "not WebDriverAgent"}
		}), iphoneSeen())
	c := checkNamed(t, report, "iOS WebDriverAgent port")

	if c.State != doctor.Missing {
		t.Fatalf("a stranger on the derived port must be reported, got %s — %s", c.State, c.Detail)
	}
	want := wda.PortFor("00008140-0000C0FFEE0000A1")
	if !strings.Contains(c.Detail, itoaForTest(int(want))) {
		t.Fatalf("the detail must NAME the port a person has to free: %q (expected %d)", c.Detail, want)
	}
	if !strings.Contains(c.Detail, "00008140-0000C0FFEE0000A1") {
		t.Fatalf("…and which phone it is for: %q", c.Detail)
	}
	if c.Remedy == "" {
		t.Fatalf("a blocking state must say what to do")
	}
}

// THE OTHER DIRECTION, AND IT IS THE ONE THAT KEEPS THIS FROM CRYING WOLF: during and after a session Appium's own
// forward holds that port. WebDriverAgent answering there is ours, not a collision.
func TestWebDriverAgentItselfOnThePortIsNotACollision(t *testing.T) {
	for _, state := range []wda.RunnerState{wda.RunnerIdle, wda.RunnerInUse, wda.RunnerNotListening} {
		report := doctor.Diagnose(context.Background(),
			portEnv(func(context.Context, uint16) wda.RunnerStatus { return wda.RunnerStatus{State: state} }), iphoneSeen())
		c := checkNamed(t, report, "iOS WebDriverAgent port")
		if c.State != doctor.Satisfied {
			t.Fatalf("%v on the derived port is not a collision, got %s — %s", state, c.State, c.Detail)
		}
	}
}

func TestWithNoWayToAskTheCollisionCheckSaysUnknownRatherThanClear(t *testing.T) {
	report := doctor.Diagnose(context.Background(), portEnv(nil), iphoneSeen())
	if c := checkNamed(t, report, "iOS WebDriverAgent port"); c.State != doctor.Unknown {
		t.Fatalf("a question nobody could ask is unknown, never satisfied, got %s", c.State)
	}
}

func itoaForTest(n int) string { return strconv.Itoa(n) }
