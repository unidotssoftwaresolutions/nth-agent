package main

import (
	"context"
	"strings"
	"testing"
)

// A_WALL_THE_CUSTOMER_SHOULD_NOT_HAVE_TO_CLIMB — the one-time build, and the two promises it makes about itself:
// it says what it will do before it does it, and when it fails it shows the tool's own words.
//
// Both are here because both were broken by tools the operator met today. Appium builds WebDriverAgent without
// ever saying so and reports "exit 65" with the reason hidden; the reason was in xcodebuild's output all along.

func setupInvocation(extra ...string) *invocation {
	inv := &invocation{Verb: "setup-ios", Bools: map[string]bool{}, Values: map[string][]string{}}
	inv.Values["--udid"] = []string{"00008140-0000C0FFEE0000A1"}
	inv.Values["--ios-team-id"] = []string{"TEAM7QZ4X2"}
	inv.Values["--ios-wda-bundle-id"] = []string{"com.example.WebDriverAgentRunner"}
	inv.Values["--wda-project"] = []string{"/tmp/wda/WebDriverAgent.xcodeproj"}
	inv.Values["--derived-data-path"] = []string{"/tmp/dd"}
	for _, f := range extra {
		inv.Bools[f] = true
	}
	return inv
}

type recordingBuild struct {
	ran    bool
	args   []string
	output string
	code   int
	err    error
}

func (r *recordingBuild) run(_ context.Context, _ string, args ...string) (string, int, error) {
	r.ran = true
	r.args = args
	return r.output, r.code, r.err
}

// THE CONSENT SEAL. The build registers the customer's phone with Apple, so it does not happen because a reader
// was silent — and the plan is STATED before the question, not after it.
func TestTheSetupSaysWhatItWillDoAndDoesNothingWithoutAYes(t *testing.T) {
	for _, answer := range []string{"", "\n", "no\n", "y\n", "YES please\n"} {
		build := &recordingBuild{}
		var out, errOut strings.Builder
		code := runSetupIos(setupInvocation(), strings.NewReader(answer), &out, &errOut, build.run)

		if build.ran {
			t.Fatalf("answer %q must not build", answer)
		}
		if code == exitOK {
			t.Fatalf("answer %q must not report success, got %d", answer, code)
		}
		// …and the plan was on screen BEFORE the question, in full.
		said := out.String()
		for _, must := range []string{
			"WebDriver", "WebDriverAgent.xcodeproj", "00008140-0000C0FFEE0000A1", "TEAM7QZ4X2",
			"com.example.WebDriverAgentRunner", "/tmp/dd",
			"REGISTER THIS PHONE", "Type 'yes'",
		} {
			if !strings.Contains(said, must) {
				t.Fatalf("the plan must state %q before asking; got:\n%s", must, said)
			}
		}
		// THE EXACT COMMAND, so a person may run it themselves — and so a failure is reproducible by hand.
		for _, flag := range []string{"build-for-testing", "-allowProvisioningUpdates", "-allowProvisioningDeviceRegistration", "DEVELOPMENT_TEAM=TEAM7QZ4X2"} {
			if !strings.Contains(said, flag) {
				t.Fatalf("the printed command must carry %s; got:\n%s", flag, said)
			}
		}
		if !strings.Contains(said, "Nothing was done") {
			t.Fatalf("a refusal must say nothing happened; got:\n%s", said)
		}
	}
}

func TestAnExplicitYesBuildsWithTheFlagsAppiumDoesNotPass(t *testing.T) {
	build := &recordingBuild{output: "** TEST BUILD SUCCEEDED **"}
	var out, errOut strings.Builder
	if code := runSetupIos(setupInvocation(), strings.NewReader("yes\n"), &out, &errOut, build.run); code != exitOK {
		t.Fatalf("an explicit yes must build, got %d / %s", code, errOut.String())
	}
	if !build.ran {
		t.Fatal("the build must actually run")
	}
	joined := strings.Join(build.args, " ")
	for _, flag := range []string{
		"build-for-testing", "-allowProvisioningUpdates", "-allowProvisioningDeviceRegistration",
		"DEVELOPMENT_TEAM=TEAM7QZ4X2", "PRODUCT_BUNDLE_IDENTIFIER=com.example.WebDriverAgentRunner",
		"id=00008140-0000C0FFEE0000A1",
	} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("the build must pass %s — the whole point is the flags Appium omits; got %s", flag, joined)
		}
	}
	// build-for-testing, NOT build: a plain Build produces no .xctrunner, which is what the checks look for and
	// what a session needs. The old remedy sent people to the wrong menu item.
	if build.args[0] != "build-for-testing" {
		t.Fatalf("the action must be build-for-testing, got %q", build.args[0])
	}
}

// --yes is allowed and is NOT a default: it must be typed. The seal above proves the default asks; this proves the
// flag is the only way past it, and that the plan is printed either way.
func TestTheQuestionCanOnlyBeSkippedByTypingTheFlag(t *testing.T) {
	build := &recordingBuild{output: "ok"}
	var out, errOut strings.Builder
	if code := runSetupIos(setupInvocation("--yes"), strings.NewReader(""), &out, &errOut, build.run); code != exitOK {
		t.Fatalf("--yes must proceed, got %d", code)
	}
	if !build.ran {
		t.Fatal("--yes must build")
	}
	if !strings.Contains(out.String(), "REGISTER THIS PHONE") {
		t.Fatal("--yes skips the QUESTION, never the statement — a person scripting this still gets it in their log")
	}
	if !strings.Contains(out.String(), "--yes was given") {
		t.Fatal("and the output must say the question was not asked")
	}
}

// THE "SHOW THE TOOL'S OWN WORDS" SEAL. Wall 5 is what happens when a tool swallows its child's output: Appium
// prints "exit 65" and the sentence that says why sits unread in xcodebuild's log.
func TestAFailedBuildShowsXcodebuildsOwnErrorNotACode(t *testing.T) {
	const realOutput = `Command line invocation: xcodebuild build-for-testing …
/Users/x/.appium/…/WebDriverAgent.xcodeproj: error: No profiles for 'com.example.WebDriverAgentRunner.xctrunner' were found: Xcode couldn't find any iOS App Development provisioning profiles matching 'com.example.WebDriverAgentRunner.xctrunner'. Automatic signing is disabled and unable to generate a profile. To enable automatic signing, pass -allowProvisioningUpdates to xcodebuild. (in target 'WebDriverAgentRunner' from project 'WebDriverAgent')
** TEST BUILD FAILED **`
	build := &recordingBuild{output: realOutput, code: 65, err: errExit(65)}
	var out, errOut strings.Builder
	if code := runSetupIos(setupInvocation("--yes"), strings.NewReader(""), &out, &errOut, build.run); code == exitOK {
		t.Fatal("a failed build must not report success")
	}
	said := errOut.String()
	if !strings.Contains(said, "No profiles for 'com.example.WebDriverAgentRunner.xctrunner' were found") {
		t.Fatalf("xcodebuild's own sentence must reach the person; got:\n%s", said)
	}
	if !strings.Contains(said, "Automatic signing is disabled") {
		t.Fatalf("the whole line, not a summary of it; got:\n%s", said)
	}
	// The code may be shown — it just may never be the ONLY thing shown.
	if strings.TrimSpace(said) == "65" {
		t.Fatal("an exit code alone is the defect this verb exists to remove")
	}
}

type errExit int

func (e errExit) Error() string { return "exit status " + string(rune('0'+int(e)%10)) }
