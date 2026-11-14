package android_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ANOTHER ACCOUNT ON THE SAME COMPUTER (ANOTHER_ACCOUNT_AND_LIMITS). adb's server decides which phones this machine
// has, and an emulator it lists is driven as this machine's phone; both are held to who runs them, asked of the
// kernel's own tables.

func listenersSay(byPort map[int][]netcap.Listener) func(int) ([]netcap.Listener, bool) {
	return func(port int) ([]netcap.Listener, bool) { return byPort[port], true }
}

func aFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// adb's client reaches localhost on ::1 as well as 127.0.0.1; a server another account holds on ::1 alone, while
// nothing listens on 127.0.0.1, is still another account's.
func TestAnAdbServerAnotherAccountHoldsOnIPv6LoopbackIsNeverAsked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake adb here is a #!/bin/sh script")
	}
	mark := adbStub(t)
	port := aFreePort(t)
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(port))
	guard := android.ServerGuard{Listeners: listenersSay(map[int][]netcap.Listener{port: {{IP: net.IPv6loopback, UID: 1002, Known: true}}})}
	outcome := android.Probe{Run: android.NewGuardedAdbRunner(guard), Enabled: true}.Outcome(context.Background())
	if !outcome.IsCouldNotAsk() || ran(mark) {
		t.Fatalf("a server another account holds on [::1] must not be asked: %s (ran %v)", outcome.KindString(), ran(mark))
	}
	if !strings.Contains(outcome.Reason(), "another account") || !strings.Contains(outcome.Reason(), "[::1]") {
		t.Fatalf("the reason names the address and the account: %q", outcome.Reason())
	}
}

// A listener that is there and does not take the agent's connection (a full queue, a dial cut short) is not
// "nothing is listening": only a listener the kernel shows nowhere is.
func TestAListenerThatDoesNotTakeTheConnectionIsNotNothing(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	getenv := func(name string) string {
		if name == "ANDROID_ADB_SERVER_PORT" {
			return port
		}
		return ""
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (android.ServerGuard{}).Before(ctx, getenv); err == nil {
		t.Fatal("a server whose connection could not be made, while the kernel shows it listening, must not be taken as no server")
	}
}

// The server the environment names is the one asked: adb dials 127.0.0.2 when told to, so that is where the check
// looks.
func TestTheAdbServerTheEnvironmentNamesIsTheOneAsked(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"ANDROID_ADB_SERVER_ADDRESS": "127.0.0.2", "ANDROID_ADB_SERVER_PORT": "5038"}, "127.0.0.2:5038"},
		{map[string]string{"ADB_SERVER_SOCKET": "tcp:127.0.0.3:5039"}, "127.0.0.3:5039"},
		{map[string]string{"ADB_SERVER_SOCKET": "tcp:localhost:5040"}, "127.0.0.1:5040"},
		{map[string]string{}, "127.0.0.1:5037"},
	} {
		address, refusal := android.ServerAddress(func(name string) string { return c.env[name] })
		if refusal != "" || address != c.want {
			t.Errorf("%v: got %q %q, want %q", c.env, address, refusal, c.want)
		}
	}
}

// After adb has answered, a server of this account's must be the one listening: an answer given by a listener that
// has gone again is no server's this agent can name, and is not used.
func TestAnAnswerNoServerOfThisAccountsGaveIsNotUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake adb here is a #!/bin/sh script")
	}
	mark := adbStub(t)
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(aFreePort(t)))
	guard := android.ServerGuard{Listeners: listenersSay(nil)}
	outcome := android.Probe{Run: android.NewGuardedAdbRunner(guard), Enabled: true}.Outcome(context.Background())
	if !ran(mark) {
		t.Fatal("adb is run when no server listens yet: it starts its own")
	}
	if !outcome.IsCouldNotAsk() || len(outcome.Offerable()) != 0 {
		t.Fatalf("an answer no listener of this account's gave must not be used: %s %v", outcome.KindString(), outcome.Offerable())
	}
	if !strings.Contains(outcome.Reason(), "not used") {
		t.Fatalf("the reason says the answer was not used: %q", outcome.Reason())
	}
}

// adb's answer is read up to a bound: no command the agent runs is answered with more than a few kilobytes, and an
// answer past the bound is not kept.
func TestAnAdbAnswerPastItsBoundIsNotKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake adb here is a #!/bin/sh script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nhead -c 3145728 /dev/zero | tr '\\0' A\n"
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ADB_SERVER_SOCKET", "")
	t.Setenv("ANDROID_ADB_SERVER_ADDRESS", "")
	adbServerHeldByThisTest(t)
	out, err := android.NewAdbRunner(nil)(context.Background(), "-s", "emulator-5554", "shell", "getprop", "ro.product.model")
	if err == nil || len(out) != 0 {
		t.Fatalf("an answer of 3 MiB must not be kept: got %d bytes, err %v", len(out), err)
	}
	if !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("the error says what bound was passed: %v", err)
	}
}

// fakeAdb answers the probe for one emulator and records what it was asked.
type fakeAdb struct {
	mu    sync.Mutex
	asked []string
}

func (f *fakeAdb) run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	f.asked = append(f.asked, strings.Join(args, " "))
	f.mu.Unlock()
	switch {
	case len(args) == 2 && args[0] == "devices":
		return "List of devices attached\nemulator-5554 device product:sdk_gphone64 model:sdk_gphone64 transport_id:3\n", nil
	case len(args) > 0 && args[len(args)-1] == "ro.boot.qemu.avd_name":
		return "", nil // an emulator too old to set it, or an endpoint that answers it empty
	case len(args) > 2 && args[2] == "emu":
		return "Pixel_8\nOK\n", nil
	}
	return "x", nil
}

func (f *fakeAdb) wasAsked(words string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.asked {
		if strings.Contains(a, words) {
			return true
		}
	}
	return false
}

// An emulator is offered only when this account runs it: the program on its adb port (and its console) is this
// account's. Another account's program on 127.0.0.1:5555 that this account's adb server took for an emulator is not,
// and it is named as not usable rather than left out, so no flag offers it.
func TestAnEmulatorIsOfferedOnlyWhenThisAccountRunsIt(t *testing.T) {
	me := netcap.ThisAccount()
	ours := func(port int) netcap.Listener {
		return netcap.Listener{IP: net.IPv4(127, 0, 0, 1), UID: me, Known: true}
	}
	for _, c := range []struct {
		name    string
		ports   map[int][]netcap.Listener
		offered bool
		says    string
	}{
		{"this account runs both its ports", map[int][]netcap.Listener{5554: {ours(5554)}, 5555: {ours(5555)}}, true, ""},
		{"another account holds its adb port", map[int][]netcap.Listener{5554: {ours(5554)}, 5555: {{IP: net.IPv4(127, 0, 0, 1), UID: 1002, Known: true}}}, false, "another account"},
		{"another account holds its console", map[int][]netcap.Listener{5554: {{IP: net.IPv4(127, 0, 0, 1), UID: 1002, Known: true}}, 5555: {ours(5555)}}, false, "another account"},
		{"nobody can say who holds its adb port", map[int][]netcap.Listener{5554: {ours(5554)}, 5555: {{IP: net.IPv4(127, 0, 0, 1)}}}, false, "could not tell"},
		{"nothing listens on its adb port", map[int][]netcap.Listener{5554: {ours(5554)}}, false, "nothing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			adb := &fakeAdb{}
			outcome := android.Probe{Run: adb.run, Enabled: true, EmulatorPorts: listenersSay(c.ports)}.Outcome(context.Background())
			offered := false
			for _, o := range outcome.Offerable() {
				offered = offered || o.ID == "emulator-5554"
			}
			if offered != c.offered {
				t.Fatalf("offered %v, want %v", offered, c.offered)
			}
			if c.offered {
				return
			}
			said := ""
			for _, u := range outcome.Unusable() {
				if u.ID == "emulator-5554" {
					said = u.State + " / " + u.Remedy
				}
			}
			if !strings.Contains(said, c.says) {
				t.Fatalf("the emulator is named as not usable, saying %q: got %q", c.says, said)
			}
			for _, l := range outcome.LeftOut() {
				if l.ID == "emulator-5554" {
					t.Fatal("it is not merely left out: --offer-uncabled-devices must never offer it")
				}
			}
			if adb.wasAsked("-s emulator-5554") {
				t.Fatal("an emulator that is not offered is not asked anything either")
			}
		})
	}
}

// The probe never asks an emulator's console for its name: adb's client sends this account's console token to
// whatever listens on the console's port, and the name is shown only in this machine's own table.
func TestTheProbeNeverAsksAnEmulatorsConsole(t *testing.T) {
	me := netcap.ThisAccount()
	ports := map[int][]netcap.Listener{
		5554: {{IP: net.IPv4(127, 0, 0, 1), UID: me, Known: true}},
		5555: {{IP: net.IPv4(127, 0, 0, 1), UID: me, Known: true}},
	}
	adb := &fakeAdb{}
	android.Probe{Run: adb.run, Enabled: true, EmulatorPorts: listenersSay(ports)}.Outcome(context.Background())
	if adb.wasAsked(" emu ") {
		t.Fatalf("the probe asked an emulator's console (adb emu): %v", adb.asked)
	}
}

// thisAccountRunsEveryEmulator is the kernel's answer on a machine where this account runs every emulator adb lists:
// both ports of each, on 127.0.0.1, are this account's. The older seals that offer an emulator read it.
func thisAccountRunsEveryEmulator(port int) ([]netcap.Listener, bool) {
	if port < 5554 || port > 5683 {
		return nil, true
	}
	return []netcap.Listener{{IP: net.IPv4(127, 0, 0, 1), UID: netcap.ThisAccount(), Known: true}}, true
}
