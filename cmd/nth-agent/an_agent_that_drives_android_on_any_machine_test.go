package main

import (
	"context"
	"strings"
	"testing"
)

// The setup-ios seals are about the Mac's flow, so they run as on a Mac on every system — the Windows runner included —
// and the one below names Windows itself (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE).
func init() { setupSystem = "darwin" }

// ON WINDOWS, setup-ios SAYS PLAINLY THAT IPHONES NEED A MAC, in the three parts, before it looks for anything or builds.
func TestOnWindowsSetupIosSaysIPhonesNeedAMac(t *testing.T) {
	setupSystem = "windows"
	defer func() { setupSystem = "darwin" }()
	built := false
	build := func(context.Context, string, ...string) (string, int, error) { built = true; return "", 0, nil }
	var out, errOut strings.Builder
	code := runSetupIos(setupInvocation("--yes"), strings.NewReader(""), &out, &errOut, build)
	text := strings.Join(strings.Fields(errOut.String()), " ") // folded at the report's width; compared unfolded
	for _, want := range []string{
		"✗ setup-ios builds WebDriverAgent with Xcode, and this machine runs Windows " +
			"iPhones need a Mac: only Xcode builds and signs WebDriverAgent, and it runs only on macOS. " +
			"→ run setup-ios on a Mac — this machine serves Android phones and emulators: npx nthcase-agent@latest doctor",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("setup-ios on Windows says %q:\n%s", want, text)
		}
	}
	if code != 1 || built || out.String() != "" {
		t.Fatalf("it stops there: exit %d, built %v, stdout %q", code, built, out.String())
	}
}
