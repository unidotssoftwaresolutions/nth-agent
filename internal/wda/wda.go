// Package wda is the ONE-TIME iOS SETUP A PERSON SHOULD NOT HAVE TO ASSEMBLE FROM A LOG.
//
// Driving a real iPhone means Appium building and SIGNING WebDriverAgent on this machine first. The operator got
// his first session up in seven interventions, and four of them were this build: a team that does not match the
// profiles, an xcodebuild flag Appium never passes, an Xcode menu item nobody names, and a tool that answers
// "exit 65" while hiding the sentence that says what actually went wrong. Every one of those is one command away,
// and this package is that command.
//
// WHAT IT DOES NOT DO. It installs nothing, it changes no file outside the derived-data directory it is given, and
// it never runs without being told yes. It does ask Apple to REGISTER THE PHONE against the customer's own
// developer account — that is what -allowProvisioningDeviceRegistration means — so the plan is stated in full and
// an explicit yes is required. Building a project Appium already ships, on the customer's own machine, at their
// explicit instruction, is not us installing something of ours; registering their device with Apple is still
// theirs to authorise.
package wda

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scheme and the two targets this package cares about. The scheme is what xcodebuild is pointed at; the targets
// are what a signing check reads, because DEVELOPMENT_TEAM is stored per target and the library is where a
// hand-configured project usually still carries somebody else's.
const (
	Scheme    = "WebDriverAgentRunner"
	LibTarget = "WebDriverAgentLib"
	RunTarget = "WebDriverAgentRunner"
)

// Plan is everything the build will use, resolved before anything runs — so it can be STATED before it is done.
type Plan struct {
	ProjectPath     string // …/appium-webdriveragent/WebDriverAgent.xcodeproj
	UDID            string // the phone this build is signed for and registered against
	DeviceName      string // what to call that phone in the statement (may be empty)
	TeamID          string // the APPLE TEAM id — see TeamID's own note in doctor's checks
	BundleID        string // the WebDriverAgent bundle id registered to that team
	SigningID       string // "Apple Development" for a free Personal Team
	DerivedDataPath string // where the build's products land; the ONLY path this package writes to
}

// ProjectSearchPaths are where Appium's WebDriverAgent copy lives, in the order they are tried. APPIUM_HOME wins
// because it is what Appium itself reads; ~/.appium is its default. NAMED, NOT GUESSED: when none exists the
// caller says which paths it looked in, so a person with a non-standard install can point at theirs.
func ProjectSearchPaths(home, appiumHome string) []string {
	var roots []string
	if appiumHome != "" {
		roots = append(roots, appiumHome)
	}
	if home != "" {
		roots = append(roots, filepath.Join(home, ".appium"))
	}
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, filepath.Join(root,
			"node_modules", "appium-xcuitest-driver", "node_modules", "appium-webdriveragent", "WebDriverAgent.xcodeproj"))
	}
	return paths
}

// FindProject returns the first search path that exists, or every path it tried.
func FindProject(paths []string) (string, []string) {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", paths
}

// DefaultDerivedDataPath is where this package builds to: the agent's own directory, beside the node id. NOT
// Xcode's shared DerivedData — a build the agent owns must be one the agent can describe by path, and one a person
// can delete without wondering what else went with it. It is also the path a prebuilt-WDA session is pointed at.
func DefaultDerivedDataPath(configDir string) string {
	return filepath.Join(configDir, "nth-agent", "wda-derived-data")
}

// Command is the EXACT command this package runs — returned rather than hidden, so a person who would rather run
// it themselves can, and so a failure can be reproduced by hand. Every flag is here for a measured reason:
//
//	build-for-testing                       produces the .xctrunner a test session needs; a plain Build does not,
//	                                        which is the menu item the old remedy sent people to.
//	-destination id=<udid>                  signs for THIS phone and registers THIS phone.
//	-allowProvisioningUpdates               lets Xcode create the profile. APPIUM NEVER PASSES THIS: it passes it
//	                                        only when appium:allowProvisioningDeviceRegistration is set, which has
//	                                        no default (appium-webdriveragent lib/xcodebuild.ts:367,
//	                                        appium-xcuitest-driver lib/desired-caps.ts:321). Without it xcodebuild
//	                                        says "Automatic signing is disabled and unable to generate a profile".
//	-allowProvisioningDeviceRegistration    registers the phone with the customer's developer account — the part
//	                                        that needs their yes.
//	DEVELOPMENT_TEAM=<team>                 on the COMMAND LINE, which applies to EVERY target. Setting it in Xcode
//	                                        on one target leaves the others on whatever the project shipped with,
//	                                        and the library target is where that bites.
func (p Plan) Command() (string, []string) {
	return "xcodebuild", []string{
		"build-for-testing",
		"-project", p.ProjectPath,
		"-scheme", Scheme,
		"-configuration", "Debug",
		"-destination", "id=" + p.UDID,
		"-derivedDataPath", p.DerivedDataPath,
		"-allowProvisioningUpdates",
		"-allowProvisioningDeviceRegistration",
		"DEVELOPMENT_TEAM=" + p.TeamID,
		"PRODUCT_BUNDLE_IDENTIFIER=" + p.BundleID,
		"CODE_SIGN_IDENTITY=" + p.SigningID,
	}
}

// Shell renders the command as a person would paste it. Quoting is deliberately simple and only where needed:
// this is a line to read and re-run, not a shell-injection surface — nothing here is attacker-supplied.
func (p Plan) Shell() string {
	name, args := p.Command()
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, name)
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = "'" + a + "'"
		}
		quoted = append(quoted, a)
	}
	return strings.Join(quoted, " ")
}

// Describe is WHAT WILL HAPPEN, in the order a person cares about, ending with the part that needs their yes.
// It names the project, the target, the phone, the team, and the registration — the constraint list, spelled out.
func (p Plan) Describe() string {
	phone := p.UDID
	if p.DeviceName != "" {
		phone = fmt.Sprintf("%s (%s)", p.DeviceName, p.UDID)
	}
	var b strings.Builder
	fmt.Fprintln(&b, "This will build and sign WebDriverAgent for one iPhone. Here is exactly what it does:")
	fmt.Fprintf(&b, "  project   %s\n", p.ProjectPath)
	fmt.Fprintf(&b, "            (Appium's own copy — nothing of ours is installed on this machine)\n")
	fmt.Fprintf(&b, "  scheme    %s, built for testing (this is what produces the .xctrunner a session needs)\n", Scheme)
	fmt.Fprintf(&b, "  phone     %s\n", phone)
	fmt.Fprintf(&b, "  team      %s, signing as %q\n", p.TeamID, p.SigningID)
	fmt.Fprintf(&b, "  bundle id %s\n", p.BundleID)
	fmt.Fprintf(&b, "  output    %s  (the only path this writes to)\n", p.DerivedDataPath)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  APPLE WILL REGISTER THIS PHONE against your developer account, and may create a")
	fmt.Fprintln(&b, "  provisioning profile for it. That is your account and your device, so it is your call.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "The command, in full — run it yourself instead if you prefer:")
	fmt.Fprintf(&b, "  %s\n", p.Shell())
	return b.String()
}

// Result is what one build amounts to. Errors are xcodebuild's OWN LINES, never a code: "exit 65" is what Appium
// prints and it is the reason wall 5 cost an afternoon.
type Result struct {
	Ok       bool
	ExitCode int
	Errors   []string // xcodebuild's `error:` lines, in order
	Output   string   // everything it wrote, for the caller to keep or show
}

// ErrorLines pulls xcodebuild's own complaints out of its output. It looks for the `error:` marker xcodebuild
// writes and returns the whole line, trimmed — the sentence, not a summary of it.
func ErrorLines(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "error:") {
			out = append(out, trimmed)
		}
	}
	return out
}

// Runner is the process seam: production runs xcodebuild, tests do not.
type Runner func(ctx context.Context, name string, args ...string) (output string, exitCode int, err error)

// Build runs the plan and keeps the tool's own words.
func Build(ctx context.Context, plan Plan, run Runner) Result {
	name, args := plan.Command()
	output, code, err := run(ctx, name, args...)
	result := Result{Ok: err == nil && code == 0, ExitCode: code, Output: output, Errors: ErrorLines(output)}
	if !result.Ok && len(result.Errors) == 0 && err != nil {
		// The tool said nothing recognisable; say what we have rather than inventing a diagnosis.
		result.Errors = []string{strings.TrimSpace(err.Error())}
	}
	return result
}

// ProductPath is where a successful build's real-device product lands — the path a prebuilt-WDA session is
// pointed at, and what a check looks for to answer "was this ever built here".
func ProductPath(derivedDataPath string) string {
	return filepath.Join(derivedDataPath, "Build", "Products", "Debug-iphoneos")
}
