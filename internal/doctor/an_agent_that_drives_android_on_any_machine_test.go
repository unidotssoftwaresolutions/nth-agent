package doctor_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── WHAT APPIUM'S ANDROID DRIVER NEEDS, SAID IN EACH SYSTEM'S OWN COMMANDS (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE) ──

// sdkMachine is a machine on system with this environment and these files, an Android phone attached, and Appium and
// node on PATH unless the test removes them.
func sdkMachine(system string, env map[string]string, files ...string) (doctor.Env, []inventory.Outcome) {
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb", "appium": "/usr/local/bin/appium", "node": "/usr/local/bin/node"}}
	e := m.env()
	e.System = system
	exists := map[string]bool{}
	for _, f := range files {
		exists[f] = true
	}
	e.SDK = &android.Where{System: system, Home: "/home/ada", Getenv: func(name string) string { return env[name] },
		IsFile: func(path string) bool { return exists[path] }}
	return e, androidSeen()
}

var local = filepath.Join("C:", "Users", "ada", "AppData", "Local")

func TestTheAndroidSdkCheckGivesThisSystemsCommand(t *testing.T) {
	windowsStudio := filepath.Join(local, "Android", "Sdk", "platform-tools", "adb.exe")
	macStudio := filepath.Join("/home/ada", "Library", "Android", "sdk", "platform-tools", "adb")
	linuxStudio := filepath.Join("/home/ada", "Android", "Sdk", "platform-tools", "adb")
	for _, c := range []struct {
		name, system string
		env          map[string]string
		files        []string
		state        doctor.State
		detail       string
		remedy       string
	}{
		{"Windows, Android Studio's SDK there and ANDROID_HOME not set", "windows", map[string]string{"LOCALAPPDATA": local}, []string{windowsStudio},
			doctor.Missing, "ANDROID_HOME is not set",
			`setx ANDROID_HOME "%LOCALAPPDATA%\Android\Sdk" — then open a new terminal and start Appium from it`},
		{"Windows, no SDK at all", "windows", map[string]string{"LOCALAPPDATA": local}, nil,
			doctor.Missing, "ANDROID_HOME is not set",
			`install Android Studio from https://developer.android.com/studio and let it install the SDK, then run: ` +
				`setx ANDROID_HOME "%LOCALAPPDATA%\Android\Sdk" — then open a new terminal and start Appium from it`},
		{"macOS, ANDROID_HOME not set", "darwin", nil, []string{macStudio},
			doctor.Missing, "ANDROID_HOME is not set",
			`export ANDROID_HOME="$HOME/Library/Android/sdk" — add that line to ~/.zshrc, then open a new terminal and start Appium from it`},
		{"Linux, ANDROID_HOME not set", "linux", nil, []string{linuxStudio},
			doctor.Missing, "ANDROID_HOME is not set",
			`export ANDROID_HOME="$HOME/Android/Sdk" — add that line to ~/.bashrc, then open a new terminal and start Appium from it`},
		{"Linux, ANDROID_HOME names a folder with no SDK", "linux", map[string]string{"ANDROID_HOME": "/opt/nothing"}, []string{linuxStudio},
			doctor.Missing, "ANDROID_HOME is /opt/nothing, which holds no " + filepath.Join("platform-tools", "adb"),
			`export ANDROID_HOME="$HOME/Android/Sdk" — add that line to ~/.bashrc, then open a new terminal and start Appium from it`},
		{"Windows, ANDROID_HOME names the SDK", "windows", map[string]string{"ANDROID_HOME": filepath.Join(local, "Android", "Sdk")}, []string{windowsStudio},
			doctor.Satisfied, "ANDROID_HOME is " + filepath.Join(local, "Android", "Sdk"), ""},
		{"macOS, ANDROID_SDK_ROOT names the SDK", "darwin", map[string]string{"ANDROID_SDK_ROOT": filepath.Join("/home/ada", "Library", "Android", "sdk")}, []string{macStudio},
			doctor.Satisfied, "ANDROID_SDK_ROOT is " + filepath.Join("/home/ada", "Library", "Android", "sdk"), ""},
	} {
		env, outcomes := sdkMachine(c.system, c.env, c.files...)
		got := find(t, doctor.Diagnose(context.Background(), env, outcomes), "Android SDK")
		if got.State != c.state || got.Detail != c.detail || got.Remedy != c.remedy {
			t.Errorf("%s:\n got %s / %q / %q\nwant %s / %q / %q", c.name, got.State, got.Detail, got.Remedy, c.state, c.detail, c.remedy)
		}
	}
}

func TestTheJavaCheckGivesThisSystemsCommand(t *testing.T) {
	studioJavaWindows := filepath.Join(`C:\Program Files\Android\Android Studio\jbr`, "bin", "java.exe")
	studioJavaMac := filepath.Join("/Applications/Android Studio.app/Contents/jbr/Contents/Home", "bin", "java")
	for _, c := range []struct {
		name, system string
		env          map[string]string
		files        []string
		state        doctor.State
		remedy       string
	}{
		{"Windows with Android Studio", "windows", nil, []string{studioJavaWindows}, doctor.Missing,
			`setx JAVA_HOME "C:\Program Files\Android\Android Studio\jbr" — Android Studio's own Java; then open a new terminal and start Appium from it`},
		{"Windows without it", "windows", nil, nil, doctor.Missing,
			"winget install EclipseAdoptium.Temurin.17.JDK — its installer can set JAVA_HOME; then open a new terminal and start Appium from it"},
		{"macOS with Android Studio", "darwin", nil, []string{studioJavaMac}, doctor.Missing,
			`export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home" — Android Studio's own Java; add that line to ~/.zshrc, then open a new terminal and start Appium from it`},
		{"macOS without it", "darwin", nil, nil, doctor.Missing,
			`brew install --cask temurin@17, then: export JAVA_HOME="$(/usr/libexec/java_home -v 17)" — add that line to ~/.zshrc, then open a new terminal and start Appium from it`},
		{"Linux", "linux", nil, nil, doctor.Missing,
			`install a JDK 17 (on Debian or Ubuntu: sudo apt install openjdk-17-jdk), then: export JAVA_HOME="$(dirname "$(dirname "$(readlink -f "$(command -v javac)")")")" — add that line to ~/.bashrc, then open a new terminal and start Appium from it`},
		{"JAVA_HOME names a Java", "linux", map[string]string{"JAVA_HOME": "/usr/lib/jvm/java-17"}, []string{filepath.Join("/usr/lib/jvm/java-17", "bin", "java")}, doctor.Satisfied, ""},
	} {
		env, outcomes := sdkMachine(c.system, c.env, c.files...)
		got := find(t, doctor.Diagnose(context.Background(), env, outcomes), "Java")
		if got.State != c.state || got.Remedy != c.remedy {
			t.Errorf("%s:\n got %s / %q\nwant %s / %q", c.name, got.State, got.Remedy, c.state, c.remedy)
		}
	}
}

func TestTheNodeAndAppiumChecksGiveThisSystemsCommand(t *testing.T) {
	for system, remedy := range map[string]string{
		"windows": "winget install OpenJS.NodeJS.LTS — then open a new terminal, and run: npm i -g appium",
		"darwin":  "brew install node — then run: npm i -g appium",
		"linux":   "install Node.js 20 or newer from https://nodejs.org/en/download (or your distribution's nodejs package), then run: npm i -g appium",
	} {
		env, outcomes := sdkMachine(system, nil)
		lookPath := env.LookPath
		env.LookPath = func(name string) (string, error) {
			if name == "node" || name == "appium" || name == "appium.cmd" {
				return lookPath("nothing-by-that-name")
			}
			return lookPath(name)
		}
		report := doctor.Diagnose(context.Background(), env, outcomes)
		if got := find(t, report, "Node.js"); got.State != doctor.Missing || got.Remedy != remedy {
			t.Errorf("%s: Node.js got %s / %q, want missing / %q", system, got.State, got.Remedy, remedy)
		}
		appium := find(t, report, "Appium")
		if appium.State != doctor.Missing || appium.Remedy != "install Appium 2 (npm i -g appium) — we do not install it for you: it is your machine" {
			t.Errorf("%s: Appium got %s / %q", system, appium.State, appium.Remedy)
		}
		if system == "windows" && appium.Detail != "not on PATH (looked for appium and appium.cmd)" {
			t.Errorf("on Windows npm installs appium.cmd, and the detail says both were looked for: %q", appium.Detail)
		}
	}

	// On Windows, npm's appium.cmd is Appium.
	env, outcomes := sdkMachine("windows", nil)
	lookPath := env.LookPath
	env.LookPath = func(name string) (string, error) {
		if name == "appium" {
			return lookPath("nothing-by-that-name")
		}
		if name == "appium.cmd" {
			return `C:\Users\ada\AppData\Roaming\npm\appium.cmd`, nil
		}
		return lookPath(name)
	}
	if got := find(t, doctor.Diagnose(context.Background(), env, outcomes), "Appium"); got.State != doctor.Satisfied {
		t.Errorf("appium.cmd on PATH is Appium on Windows: %+v", got)
	}

	// Appium on PATH and node not: this build cannot tell what runs it — never a missing node.
	env, outcomes = sdkMachine("linux", nil)
	lookPath = env.LookPath
	env.LookPath = func(name string) (string, error) {
		if name == "node" {
			return lookPath("nothing-by-that-name")
		}
		return lookPath(name)
	}
	if got := find(t, doctor.Diagnose(context.Background(), env, outcomes), "Node.js"); got.State != doctor.Unknown {
		t.Errorf("Appium found and node not is unknown, not missing: %+v", got)
	}
}

func TestAdbInAndroidStudiosFolderIsFoundByTheDoctor(t *testing.T) {
	studio := filepath.Join(local, "Android", "Sdk", "platform-tools", "adb.exe")
	env, outcomes := sdkMachine("windows", map[string]string{"LOCALAPPDATA": local}, studio)
	lookPath := env.LookPath
	env.LookPath = func(name string) (string, error) {
		if name == "adb" || name == "adb.exe" {
			return lookPath("nothing-by-that-name")
		}
		return lookPath(name)
	}
	got := find(t, doctor.Diagnose(context.Background(), env, outcomes), "adb")
	if got.State != doctor.Satisfied || !strings.Contains(got.Detail, studio) || !strings.Contains(got.Detail, "in Android Studio's SDK folder") {
		t.Fatalf("adb in %%LOCALAPPDATA%%\\Android\\Sdk is found, and the detail says where: %+v", got)
	}
	if got := doctor.AdbInstallRemedy("windows"); got != `install Android Studio from https://developer.android.com/studio — its SDK puts adb in %LOCALAPPDATA%\Android\Sdk\platform-tools, where the agent finds it` {
		t.Fatalf("Windows is told to install Android Studio: %q", got)
	}
}

func TestTheAndroidChecksAreNotNeededWithNoAndroidDevice(t *testing.T) {
	env, _ := sdkMachine("windows", nil)
	report := doctor.Diagnose(context.Background(), env, []inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)})
	for _, name := range []string{"Node.js", "Android SDK", "Java"} {
		if got := find(t, report, name); got.State != doctor.NotRequired || got.Detail != "no Android device was seen on this machine" {
			t.Errorf("%s with no Android phone or emulator: %+v", name, got)
		}
	}
}

// ── iOS ON WINDOWS: n/a, never a fault ───────────────────────────────────────────────────────────────────────────────

func TestOnWindowsEveryIOSCheckIsNotApplicable(t *testing.T) {
	env, _ := sdkMachine("windows", nil)
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "emulator-5554", Emulator: true, AVD: "Pixel_8_API_35"}}, nil),
		inventory.NotHere(inventory.IOS, "needs a Mac"),
	}
	report := doctor.Diagnose(context.Background(), env, outcomes)
	seen := 0
	for _, c := range report.Checks {
		if !strings.HasPrefix(c.Name, "iOS") && c.Name != "Appium driver xcuitest" && c.Name != "Xcode command-line tools" {
			continue
		}
		seen++
		if c.State != doctor.NotRequired || c.Detail != "n/a on Windows: iOS needs a Mac" || c.Blocks() {
			t.Errorf("%s on Windows: %+v", c.Name, c)
		}
	}
	if seen < 7 {
		t.Fatalf("instrument guard: %d iOS checks read", seen)
	}
	var out strings.Builder
	if err := doctor.Human(&out, report, doctor.Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "· iOS Developer Mode: not needed on this machine — n/a on Windows: iOS needs a Mac") {
		t.Fatalf("doctor marks iOS n/a on Windows:\n%s", out.String())
	}
}
