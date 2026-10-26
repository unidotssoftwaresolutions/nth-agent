package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// buildTimeout bounds the WebDriverAgent build. A COLD real-device build compiles the whole project and talks to
// Apple for a profile; ten minutes is generous on purpose, because the alternative — a build killed at four
// minutes — teaches a person that the tool is broken when it was working.
const buildTimeout = 10 * time.Minute

// runSetupIos is the one-time build, stated before it is done.
//
// THE ORDER IS THE POINT: resolve everything first, so the plan can be printed in full and answered as one
// question. A tool that asks "is this ok?" and then discovers it does not know which phone has asked nothing.
// THE PROCESS SEAM IS A PARAMETER, not a package var: a seal about "does this ask before it builds" is worthless
// if the only way to run it is to actually build.
func runSetupIos(inv *invocation, stdin io.Reader, stdout, stderr io.Writer, build wda.Runner) int {
	teamID := firstNonEmpty(inv.first("--ios-team-id"), os.Getenv("NTH_AGENT_IOS_TEAM_ID"))
	bundleID := firstNonEmpty(inv.first("--ios-wda-bundle-id"), os.Getenv("NTH_AGENT_IOS_WDA_BUNDLE_ID"))
	signingID := firstNonEmpty(inv.first("--ios-signing-id"), os.Getenv("NTH_AGENT_IOS_SIGNING_ID"), "Apple Development")

	if teamID == "" || bundleID == "" {
		fmt.Fprintln(stderr, "setup-ios needs your Apple team id and the WebDriverAgent bundle id "+
			"(--ios-team-id / --ios-wda-bundle-id, or NTH_AGENT_IOS_TEAM_ID / NTH_AGENT_IOS_WDA_BUNDLE_ID).")
		fmt.Fprintln(stderr, "The TEAM ID is the 10-character code your signing certificate carries — run "+
			"'"+command.Line("doctor --ios-wda-bundle-id <id>")+"' and it will read it off this machine for you.")
		return exitMistake
	}

	project := inv.first("--wda-project")
	if project == "" {
		home, _ := os.UserHomeDir()
		paths := wda.ProjectSearchPaths(home, os.Getenv("APPIUM_HOME"))
		found, tried := wda.FindProject(paths)
		if found == "" {
			fmt.Fprintln(stderr, "could not find Appium's WebDriverAgent project. Looked in:")
			for _, p := range tried {
				fmt.Fprintf(stderr, "  %s\n", p)
			}
			fmt.Fprintln(stderr, "Install the xcuitest driver (appium driver install xcuitest), or point at yours with --wda-project.")
			return 1
		}
		project = found
	}

	udid, err := chooseIosDevice(inv.first("--udid"))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMistake
	}

	derived := inv.first("--derived-data-path")
	if derived == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintf(stderr, "cannot locate the user config directory: %v\n", err)
			return 1
		}
		derived = wda.DefaultDerivedDataPath(configDir)
	}

	plan := wda.Plan{
		ProjectPath: project, UDID: udid, TeamID: teamID, BundleID: bundleID,
		SigningID: signingID, DerivedDataPath: derived,
	}

	fmt.Fprint(stdout, plan.Describe())
	if !inv.Bools["--yes"] {
		ok, err := confirm(stdin, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "could not read your answer: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintln(stdout, "Nothing was done.")
			return exitMistake
		}
	} else {
		fmt.Fprintln(stdout, "\n(--yes was given, so this was not asked.)")
	}

	fmt.Fprintln(stdout, "\nBuilding. A cold build takes minutes — it compiles the project and asks Apple for a profile.")
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	result := wda.Build(ctx, plan, build)

	if !result.Ok {
		// XCODEBUILD'S OWN WORDS. "exit 65" is what Appium prints and it is why wall 5 cost an afternoon.
		fmt.Fprintf(stderr, "\nThe build failed (xcodebuild exited %d). What it said:\n", result.ExitCode)
		if len(result.Errors) == 0 {
			fmt.Fprintln(stderr, "  (xcodebuild printed no error line — the full output follows)")
			fmt.Fprintln(stderr, result.Output)
		}
		for _, line := range result.Errors {
			fmt.Fprintf(stderr, "  %s\n", line)
		}
		fmt.Fprintln(stderr, "\nThe exact command is above; run it yourself to see everything xcodebuild wrote.")
		return 1
	}

	fmt.Fprintln(stdout, "\nDone. WebDriverAgent is built and signed for that phone.")
	fmt.Fprintf(stdout, "  built to: %s\n", wda.ProductPath(plan.DerivedDataPath))
	fmt.Fprintln(stdout, "  Run '"+command.Line("doctor --ios-wda-bundle-id "+bundleID)+"' to see it confirmed,")
	fmt.Fprintln(stdout, "  then start the agent with --ios-team-id and --ios-wda-bundle-id and your iPhone is offered for runs.")
	fmt.Fprintln(stdout, "\nThe FIRST session on that phone may still ask you to trust the developer on the device")
	fmt.Fprintln(stdout, "(Settings > General > VPN & Device Management). Nothing off the phone can answer that for you.")
	return exitOK
}

// chooseIosDevice picks the phone to build for. ONE attached iPhone needs no flag; more than one does, and the
// error NAMES them rather than picking. Guessing which of somebody's phones to register with Apple is not a
// guess this program gets to make.
func chooseIosDevice(requested string) (string, error) {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested), nil
	}
	outcomes := connect.ProbeSet(map[inventory.Platform]bool{inventory.Android: true}, android.AdbRunner, ios.UsbmuxdLister, probeTimeout, false)(context.Background())
	var attached []string
	for _, o := range outcomes {
		if o.Platform() != inventory.IOS || !o.IsAnswered() {
			continue
		}
		for _, d := range o.Offerable() {
			attached = append(attached, d.ID)
		}
	}
	switch len(attached) {
	case 0:
		return "", fmt.Errorf("no iPhone is attached to this machine (or the probe could not ask) — plug one in, unlock it, and try again")
	case 1:
		return attached[0], nil
	default:
		return "", fmt.Errorf("%d iPhones are attached: %s\nsay which one with --udid", len(attached), strings.Join(attached, ", "))
	}
}

// confirm asks the one question and accepts only an explicit yes. Anything else — including empty, including
// EOF — is no: a build that registers somebody's phone with Apple does not happen because a reader was silent.
func confirm(stdin io.Reader, stdout io.Writer) (bool, error) {
	fmt.Fprint(stdout, "Type 'yes' to go ahead (anything else stops): ")
	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return false, nil // no answer is not a yes
		}
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

// execRunner is the production process seam: it keeps stdout and stderr TOGETHER, because xcodebuild writes its
// error lines to stdout and a runner that kept only stderr would reproduce the exact defect this verb exists to
// remove.
func execRunner(ctx context.Context, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	code := cmd.ProcessState.ExitCode()
	return string(out), code, err
}
