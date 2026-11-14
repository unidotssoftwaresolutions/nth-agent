package android_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── adb is found where Android Studio puts it (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE) ─────────────────────────────

// where is a machine the test makes up: its system, its environment, what is on PATH and which files exist.
func where(system string, env map[string]string, onPath map[string]string, files ...string) android.Where {
	exists := map[string]bool{}
	for _, f := range files {
		exists[f] = true
	}
	return android.Where{
		System: system,
		Home:   "/home/ada",
		Getenv: func(name string) string { return env[name] },
		LookPath: func(name string) (string, error) {
			if path, ok := onPath[name]; ok {
				return path, nil
			}
			return "", errors.New("not on PATH")
		},
		IsFile: func(path string) bool { return exists[path] },
	}
}

func TestAdbIsFoundOnPathThenInTheSdkThenInAndroidStudiosFolder(t *testing.T) {
	local := filepath.Join("C:", "Users", "ada", "AppData", "Local")
	studioWindows := filepath.Join(local, "Android", "Sdk", "platform-tools", "adb.exe")
	home := filepath.Join("/opt", "android-sdk", "platform-tools", "adb")
	root := filepath.Join("/opt", "sdk-root", "platform-tools", "adb")
	for _, c := range []struct {
		name  string
		w     android.Where
		want  android.Found
		found bool
	}{
		{"Windows, adb on PATH wins over the SDK",
			where("windows", map[string]string{"LOCALAPPDATA": local}, map[string]string{"adb.exe": `C:\tools\adb.exe`}, studioWindows),
			android.Found{Path: `C:\tools\adb.exe`, From: "on PATH"}, true},
		{"Windows, only Android Studio's folder: %LOCALAPPDATA%\\Android\\Sdk, and the program is adb.exe",
			where("windows", map[string]string{"LOCALAPPDATA": local}, nil, studioWindows),
			android.Found{Path: studioWindows, From: "in Android Studio's SDK folder"}, true},
		{"Windows, ANDROID_HOME before Android Studio's folder",
			where("windows", map[string]string{"LOCALAPPDATA": local, "ANDROID_HOME": filepath.Join("D:", "sdk")}, nil,
				filepath.Join("D:", "sdk", "platform-tools", "adb.exe"), studioWindows),
			android.Found{Path: filepath.Join("D:", "sdk", "platform-tools", "adb.exe"), From: "in ANDROID_HOME"}, true},
		{"macOS, ANDROID_HOME",
			where("darwin", map[string]string{"ANDROID_HOME": filepath.Join("/opt", "android-sdk")}, nil, home),
			android.Found{Path: home, From: "in ANDROID_HOME"}, true},
		{"macOS, ANDROID_SDK_ROOT when ANDROID_HOME holds no adb",
			where("darwin", map[string]string{"ANDROID_HOME": "/nowhere", "ANDROID_SDK_ROOT": filepath.Join("/opt", "sdk-root")}, nil, root),
			android.Found{Path: root, From: "in ANDROID_SDK_ROOT"}, true},
		{"macOS, ~/Library/Android/sdk",
			where("darwin", nil, nil, filepath.Join("/home/ada", "Library", "Android", "sdk", "platform-tools", "adb")),
			android.Found{Path: filepath.Join("/home/ada", "Library", "Android", "sdk", "platform-tools", "adb"), From: "in Android Studio's SDK folder"}, true},
		{"Linux, ~/Android/Sdk",
			where("linux", nil, nil, filepath.Join("/home/ada", "Android", "Sdk", "platform-tools", "adb")),
			android.Found{Path: filepath.Join("/home/ada", "Android", "Sdk", "platform-tools", "adb"), From: "in Android Studio's SDK folder"}, true},
		{"Linux, adb on PATH",
			where("linux", nil, map[string]string{"adb": "/usr/bin/adb"}),
			android.Found{Path: "/usr/bin/adb", From: "on PATH"}, true},
		{"Linux, a Windows name is not looked for",
			where("linux", nil, map[string]string{"adb.exe": "/mnt/c/adb.exe"}),
			android.Found{}, false},
		{"nowhere at all",
			where("windows", map[string]string{"LOCALAPPDATA": local}, nil),
			android.Found{}, false},
	} {
		got, found := android.FindAdb(c.w)
		if found != c.found || got != c.want {
			t.Errorf("%s: got %+v (%v), want %+v (%v)", c.name, got, found, c.want, c.found)
		}
	}
}

// ── an emulator on this machine is offered ───────────────────────────────────────────────────────────────────────────

// emulatorListing is one emulator on this machine and the two kinds of network phone, all in state "device". Invented to
// adb's documented shape.
const emulatorListing = "List of devices attached\n" +
	"emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1\n" +
	"192.168.1.20:5555      device product:p model:m device:d transport_id:2\n" +
	"adb-R5CT30ABCDE-AbCdEf._adb-tls-connect._tcp device product:p model:m device:d transport_id:3\n"

// emulatorRunner answers the listing, the emulator's properties, and — when asked — its console's name for itself.
func emulatorRunner(avdProp, consoleName string) android.Runner {
	return func(_ context.Context, args ...string) (string, error) {
		switch {
		case reflect.DeepEqual(args, []string{"devices", "-l"}):
			return emulatorListing, nil
		case reflect.DeepEqual(args, []string{"-s", "emulator-5554", "shell", "getprop", "ro.boot.qemu.avd_name"}):
			return avdProp + "\n", nil
		case reflect.DeepEqual(args, []string{"-s", "emulator-5554", "shell", "getprop", "ro.product.model"}):
			return "sdk_gphone64_arm64\n", nil
		case reflect.DeepEqual(args, []string{"-s", "emulator-5554", "emu", "avd", "name"}):
			if consoleName == "" {
				return "", errors.New("error: no emulator console")
			}
			return consoleName + "\r\nOK\r\n", nil
		}
		return "", nil
	}
}

func TestAnEmulatorOnThisMachineIsOfferedAsAnEmulatorByItsAvdName(t *testing.T) {
	outcome := android.Probe{Run: emulatorRunner("Pixel_8_API_35", ""), Enabled: true}.Outcome(context.Background())
	offered := outcome.Offerable()
	if len(offered) != 1 || offered[0].ID != "emulator-5554" || !offered[0].Emulator || offered[0].AVD != "Pixel_8_API_35" ||
		offered[0].Model != "sdk_gphone64_arm64" {
		t.Fatalf("emulator-5554 is offered, as an emulator, named Pixel_8_API_35: %+v", offered)
	}
	want := []inventory.LeftOut{
		{ID: "192.168.1.20:5555", How: "reached over the network (adb connect)"},
		{ID: "adb-R5CT30ABCDE-AbCdEf._adb-tls-connect._tcp", How: "reached over Wi-Fi (wireless debugging)"},
	}
	if got := outcome.LeftOut(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the network serials are still left out:\n got %+v\nwant %+v", got, want)
	}

	// An emulator too old to set the property says its name through its console.
	older := android.Probe{Run: emulatorRunner("", "Nexus_5_API_29"), Enabled: true}.Outcome(context.Background())
	if got := older.Offerable(); len(got) != 1 || got[0].AVD != "Nexus_5_API_29" {
		t.Fatalf("the console's answer names an older emulator: %+v", got)
	}
	// One that says neither is still offered, as an emulator, with no name.
	silent := android.Probe{Run: emulatorRunner("", ""), Enabled: true}.Outcome(context.Background())
	if got := silent.Offerable(); len(got) != 1 || !got[0].Emulator || got[0].AVD != "" {
		t.Fatalf("an emulator that names itself nowhere is still offered: %+v", got)
	}
}

func TestTheFlagStillOffersTheNetworkPhones(t *testing.T) {
	outcome := android.Probe{Run: emulatorRunner("Pixel_8_API_35", ""), Enabled: true, OfferUncabled: true}.Outcome(context.Background())
	if got := ids(outcome.Offerable()); !reflect.DeepEqual(got, []string{"emulator-5554", "192.168.1.20:5555", "adb-R5CT30ABCDE-AbCdEf._adb-tls-connect._tcp"}) ||
		len(outcome.LeftOut()) != 0 {
		t.Fatalf("with %s the network phones are offered too: %v, left out %v", inventory.OfferUncabledFlag, got, outcome.LeftOut())
	}
	for _, d := range outcome.Offerable()[1:] {
		if d.Emulator {
			t.Fatalf("a network phone is not an emulator: %+v", d)
		}
	}
}

func TestOnlyAnEmulatorsOwnConsoleSerialIsLocal(t *testing.T) {
	for serial, local := range map[string]bool{
		"emulator-5554":       true,
		"emulator-5556":       true,
		"emulator-5682":       true,
		"emulator-5555":       false, // an adb port, never a console's
		"emulator-5552":       false,
		"emulator-5684":       false,
		"emulator-":           false,
		"emulator-55x4":       false,
		"emulator-005554":     false,
		"192.168.1.20:5555":   false,
		"localhost:5555":      false,
		"R5CT30ABCDE":         false,
		"emulator-5554:5555":  false,
		"my-emulator-5554":    false,
		"adb-emulator-5554._": false,
	} {
		if got := android.LocalEmulator(serial); got != local {
			t.Errorf("LocalEmulator(%q) = %v, want %v", serial, got, local)
		}
	}
}
