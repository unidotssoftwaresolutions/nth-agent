package doctor

import (
	"context"
	"path/filepath"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── WHAT APPIUM'S ANDROID DRIVER NEEDS ON THIS MACHINE (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE) ───────────────────
//
// The agent does not install Appium or anything Appium needs: it is the person's machine. It says what is missing, in
// the commands of THIS system — a Windows machine is told `setx`, a Mac `export` for zsh, Linux `export` for bash — and
// each check is scoped like the uiautomator2 driver's: needed when an Android phone or emulator is seen here.
//
// WHAT THESE CHECKS READ: this agent's own environment. Appium reads the environment of the terminal it was started in,
// so the two agree only when both are started from the same kind of terminal — which every remedy below says.

// nodeJS: Appium is a Node.js program.
func nodeJS(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "Node.js",
		Need: "Appium is a Node.js program, and npm installs it and its drivers",
	}
	if !platformIsInPlay(inventory.Android, outcomes) {
		c.State = NotRequired
		c.Detail = notSeen(inventory.Android, outcomes)
		return c
	}
	path, err := env.LookPath("node")
	if err != nil {
		if _, appium := appiumOnPath(env); appium {
			// Appium answered from PATH and node did not: this build cannot tell what runs it, which is not a missing node.
			c.State = Unknown
			c.Detail = "node is not on PATH, and Appium is"
			c.Remedy = "nothing, probably — run `node --version` in the terminal you start Appium from to see which one it uses"
			return c
		}
		c.State = Missing
		c.Detail = "node is not on PATH"
		c.Remedy = NodeInstallRemedy(env.System)
		return c
	}
	c.State = Satisfied
	c.Detail = path
	if out, err := env.Run(ctx, "node", "--version"); err == nil {
		if line := firstLine(out); line != "" {
			c.Detail = line + " (" + path + ")"
		}
	}
	return c
}

// NodeInstallRemedy is how to install Node.js on system.
func NodeInstallRemedy(system string) string {
	switch system {
	case "windows":
		return "winget install OpenJS.NodeJS.LTS — then open a new terminal, and run: npm i -g appium"
	case "darwin":
		return "brew install node — then run: npm i -g appium"
	case "linux":
		return "install Node.js 20 or newer from https://nodejs.org/en/download (or your distribution's nodejs package), then run: npm i -g appium"
	}
	return "install Node.js 20 or newer from https://nodejs.org/en/download, then run: npm i -g appium"
}

// appiumOnPath finds Appium the way a shell does: `appium`, and on Windows npm's `appium.cmd` too.
func appiumOnPath(env Env) (string, bool) {
	names := []string{"appium"}
	if env.System == "windows" {
		names = append(names, "appium.cmd")
	}
	for _, name := range names {
		if path, err := env.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

// androidSDK: Appium's uiautomator2 driver finds adb and the SDK's tools through ANDROID_HOME (or ANDROID_SDK_ROOT).
func androidSDK(env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "Android SDK",
		Need: "Appium's uiautomator2 driver finds adb and the SDK's build tools through ANDROID_HOME (or ANDROID_SDK_ROOT); " +
			"without it no Android session starts",
		Limits: "this reads the agent's own environment; Appium reads the one it was started in, so start both from the same kind of terminal",
	}
	if !platformIsInPlay(inventory.Android, outcomes) {
		c.State = NotRequired
		c.Detail = notSeen(inventory.Android, outcomes)
		return c
	}
	if env.SDK == nil {
		c.State = Unknown
		c.Detail = "this build cannot read ANDROID_HOME"
		c.Remedy = "nothing to do here — run `appium driver doctor uiautomator2` to ask Appium itself"
		return c
	}
	where := *env.SDK
	for _, name := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		value := where.Getenv(name)
		if value == "" {
			continue
		}
		if isSDK(where, value) {
			c.State = Satisfied
			c.Detail = name + " is " + value
			return c
		}
		c.State = Missing
		c.Detail = name + " is " + value + ", which holds no " + filepath.Join("platform-tools", adbFile(where.System))
		c.Remedy = SDKRemedy(where)
		return c
	}
	c.State = Missing
	c.Detail = "ANDROID_HOME is not set"
	c.Remedy = SDKRemedy(where)
	return c
}

// isSDK: folder holds platform-tools/adb — the one part of an SDK every Android session uses.
func isSDK(where android.Where, folder string) bool {
	return where.IsFile(filepath.Join(folder, "platform-tools", adbFile(where.System)))
}

func adbFile(system string) string {
	if system == "windows" {
		return "adb.exe"
	}
	return "adb"
}

// SDKRemedy is how to point ANDROID_HOME at the SDK on this system — the line for this system's shell, after installing
// the SDK first when Android Studio's folder has none.
func SDKRemedy(where android.Where) string {
	var set string
	switch where.System {
	case "windows":
		set = `setx ANDROID_HOME "%LOCALAPPDATA%\Android\Sdk"` + " — then open a new terminal and start Appium from it"
	case "darwin":
		set = `export ANDROID_HOME="$HOME/Library/Android/sdk"` + " — add that line to ~/.zshrc, then open a new terminal and start Appium from it"
	case "linux":
		set = `export ANDROID_HOME="$HOME/Android/Sdk"` + " — add that line to ~/.bashrc, then open a new terminal and start Appium from it"
	default:
		return "set ANDROID_HOME to the folder that holds your Android SDK's platform-tools, then start Appium from a new terminal"
	}
	if studio := android.StudioSDK(where); studio == "" || !isSDK(where, studio) {
		return "install Android Studio from https://developer.android.com/studio and let it install the SDK, then run: " + set
	}
	return set
}

// java: Appium's uiautomator2 driver signs and installs its server on the phone with the SDK's Java tools.
func java(ctx context.Context, env Env, outcomes []inventory.Outcome) Check {
	c := Check{
		Name: "Java",
		Need: "Appium's uiautomator2 driver runs the SDK's Java tools (apksigner) when it installs its server on a phone, " +
			"and finds Java through JAVA_HOME",
		Limits: "this reads the agent's own environment; Appium reads the one it was started in, so start both from the same kind of terminal",
	}
	if !platformIsInPlay(inventory.Android, outcomes) {
		c.State = NotRequired
		c.Detail = notSeen(inventory.Android, outcomes)
		return c
	}
	if env.SDK == nil {
		c.State = Unknown
		c.Detail = "this build cannot read JAVA_HOME"
		c.Remedy = "nothing to do here — run `appium driver doctor uiautomator2` to ask Appium itself"
		return c
	}
	where := *env.SDK
	program := "java"
	if where.System == "windows" {
		program = "java.exe"
	}
	if home := where.Getenv("JAVA_HOME"); home != "" {
		javaPath := filepath.Join(home, "bin", program)
		if where.IsFile(javaPath) {
			c.State = Satisfied
			c.Detail = "JAVA_HOME is " + home
			if out, err := env.Run(ctx, javaPath, "-version"); err == nil {
				if line := firstLine(out); line != "" {
					c.Detail = line + " (JAVA_HOME is " + home + ")"
				}
			}
			return c
		}
		c.State = Missing
		c.Detail = "JAVA_HOME is " + home + ", which holds no " + filepath.Join("bin", program)
		c.Remedy = JavaRemedy(where)
		return c
	}
	c.State = Missing
	c.Detail = "JAVA_HOME is not set"
	if path, err := env.LookPath("java"); err == nil {
		c.Detail += " (java is on PATH at " + path + ", and the driver reads JAVA_HOME)"
	}
	c.Remedy = JavaRemedy(where)
	return c
}

// studioJava is the Java that ships inside Android Studio on each system — the one the driver can use with no other
// install.
var studioJava = map[string]string{
	"windows": `C:\Program Files\Android\Android Studio\jbr`,
	"darwin":  "/Applications/Android Studio.app/Contents/jbr/Contents/Home",
	"linux":   "",
}

// JavaRemedy is how to give the driver a Java on this system: Android Studio's own, when it is there; otherwise one to
// install.
func JavaRemedy(where android.Where) string {
	studio := studioJava[where.System]
	program := "java"
	if where.System == "windows" {
		program = "java.exe"
	}
	hasStudio := studio != "" && where.IsFile(filepath.Join(studio, "bin", program))
	switch where.System {
	case "windows":
		if hasStudio {
			return `setx JAVA_HOME "` + studio + `"` + " — Android Studio's own Java; then open a new terminal and start Appium from it"
		}
		return "winget install EclipseAdoptium.Temurin.17.JDK — its installer can set JAVA_HOME; then open a new terminal and start Appium from it"
	case "darwin":
		if hasStudio {
			return `export JAVA_HOME="` + studio + `"` + " — Android Studio's own Java; add that line to ~/.zshrc, then open a new terminal and start Appium from it"
		}
		return `brew install --cask temurin@17, then: export JAVA_HOME="$(/usr/libexec/java_home -v 17)"` +
			" — add that line to ~/.zshrc, then open a new terminal and start Appium from it"
	case "linux":
		return "install a JDK 17 (on Debian or Ubuntu: sudo apt install openjdk-17-jdk), then: " +
			`export JAVA_HOME="$(dirname "$(dirname "$(readlink -f "$(command -v javac)")")")"` +
			" — add that line to ~/.bashrc, then open a new terminal and start Appium from it"
	}
	return "install a JDK 17 and set JAVA_HOME to it, then start Appium from a new terminal"
}

// appiumMissingDetail is the Appium check's detail when it is not found: on Windows npm installs `appium.cmd`, and the
// sentence says both were looked for.
func appiumMissingDetail(system string) string {
	if system == "windows" {
		return "not on PATH (looked for appium and appium.cmd)"
	}
	return "not on PATH"
}
