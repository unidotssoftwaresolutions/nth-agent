package android

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ── WHICH adb: PATH FIRST, THEN THE ANDROID SDK ─────────────────────────────────────────────────────────────────────
//
// adb used to be run by its bare name, so it had to be on PATH. On a Mac or a Linux machine that is usually true; on
// Windows it rarely is: Android Studio installs the SDK under %LOCALAPPDATA%\Android\Sdk and puts nothing on PATH. So adb
// is looked for, in this order, and the first one that is a file is the one this agent runs:
//
//  1. adb on PATH — what the person's own shell runs, so it wins;
//  2. platform-tools in the SDK that ANDROID_HOME names, then the one ANDROID_SDK_ROOT names (the SDK's own variables,
//     which Android Studio's guide and Appium's uiautomator2 driver both read);
//  3. platform-tools in the folder Android Studio installs the SDK to: %LOCALAPPDATA%\Android\Sdk on Windows,
//     ~/Library/Android/sdk on macOS, ~/Android/Sdk on Linux.
//
// On Windows the program is adb.exe. Nothing is found → the bare name, and running it fails the way it always did, with
// the sentence that says to install platform-tools.

// Where is what the search reads. A TEST SEAM: production is ThisMachine.
type Where struct {
	System   string // runtime.GOOS
	Getenv   func(string) string
	LookPath func(string) (string, error)
	IsFile   func(string) bool
	Home     string
}

// Found is the adb the search chose: its path, and where it was found, in the words a sentence about it uses ("on
// PATH", "in ANDROID_HOME", "in Android Studio's SDK folder").
type Found struct {
	Path string
	From string
}

// ThisMachine is this process's environment, PATH and file system.
func ThisMachine() Where {
	home, _ := os.UserHomeDir()
	return Where{System: runtime.GOOS, Getenv: sdkEnv, LookPath: exec.LookPath, Home: home, IsFile: func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular()
	}}
}

// sdkEnv reads, from this process's environment, each variable the search reads — BY NAME, so the README's census of the
// variables this agent reads finds every one.
func sdkEnv(name string) string {
	switch name {
	case "ANDROID_HOME":
		return os.Getenv("ANDROID_HOME")
	case "ANDROID_SDK_ROOT":
		return os.Getenv("ANDROID_SDK_ROOT")
	case "LOCALAPPDATA":
		return os.Getenv("LOCALAPPDATA")
	case "JAVA_HOME":
		// Not the search's: the doctor's Java check reads it through the same seam, because Appium's uiautomator2 driver
		// reads it beside ANDROID_HOME.
		return os.Getenv("JAVA_HOME")
	}
	return ""
}

// FindAdb looks for adb in the order above. found is false when no file was found anywhere.
func FindAdb(w Where) (Found, bool) {
	program := "adb"
	if w.System == "windows" {
		program = "adb.exe"
	}
	if path, err := w.LookPath(program); err == nil && path != "" {
		return Found{Path: path, From: "on PATH"}, true
	}
	for _, sdk := range SDKFolders(w) {
		path := filepath.Join(sdk.Path, "platform-tools", program)
		if w.IsFile(path) {
			return Found{Path: path, From: sdk.From}, true
		}
	}
	return Found{}, false
}

// SDKFolders is every place an Android SDK may be, in the order the search reads them: ANDROID_HOME, ANDROID_SDK_ROOT, then
// Android Studio's own folder for this system. A variable that is not set is left out.
func SDKFolders(w Where) []Found {
	var folders []Found
	for _, name := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if value := w.Getenv(name); value != "" {
			folders = append(folders, Found{Path: value, From: "in " + name})
		}
	}
	if studio := StudioSDK(w); studio != "" {
		folders = append(folders, Found{Path: studio, From: "in Android Studio's SDK folder"})
	}
	return folders
}

// StudioSDK is the folder Android Studio installs the SDK to on this system, or "" when it cannot be named.
func StudioSDK(w Where) string {
	switch w.System {
	case "windows":
		if local := w.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Android", "Sdk")
		}
	case "darwin":
		if w.Home != "" {
			return filepath.Join(w.Home, "Library", "Android", "sdk")
		}
	case "linux":
		if w.Home != "" {
			return filepath.Join(w.Home, "Android", "Sdk")
		}
	}
	return ""
}

// Program is the adb each command is run with (FindAdb), or the bare name when none was found. It is looked for again for
// every command — a PATH lookup and at most three file checks — so an SDK installed while the agent runs is found.
func Program() string {
	if found, ok := FindAdb(ThisMachine()); ok {
		return found.Path
	}
	return "adb"
}

// LookPath is exec.LookPath, except that adb is looked for where FindAdb looks.
func LookPath(name string) (string, error) {
	if name == "adb" {
		if found, ok := FindAdb(ThisMachine()); ok {
			return found.Path, nil
		}
	}
	return exec.LookPath(name)
}
