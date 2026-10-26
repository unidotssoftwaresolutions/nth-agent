package android_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — adb's server decides which phones this machine has, so the agent uses adb
// only through a server this account runs on this machine.
//
// Every case here points adb at a port THIS TEST holds (ANDROID_ADB_SERVER_PORT) and puts a stub `adb` on PATH that
// leaves a mark when it runs: no real adb runs and no real adb server is contacted.

const cabledListing = "List of devices attached\nR5CT64M346B device usb:2-1.1 product:m34x model:SM_M346B1 device:m34x transport_id:1\n"

// adbStub puts an `adb` on PATH that prints the listing and leaves a mark, and returns where the mark goes.
func adbStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	script := "#!/bin/sh\ntouch " + shellQuote(mark) + "\nprintf '%s' " + shellQuote(cabledListing) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ADB_SERVER_SOCKET", "")
	t.Setenv("ANDROID_ADB_SERVER_ADDRESS", "")
	return mark
}

// adbServerHeldByThisTest opens a listener and points adb at it.
func adbServerHeldByThisTest(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
}

func ran(mark string) bool { _, err := os.Stat(mark); return err == nil }

func anotherAccount(net.Conn) (int, bool) { return 1002, true }

func TestAnAdbServerAnotherAccountRunsIsNeverAsked(t *testing.T) {
	mark := adbStub(t)
	adbServerHeldByThisTest(t)

	outcome := android.Probe{Run: android.NewAdbRunner(anotherAccount), Enabled: true}.Outcome(context.Background())
	if !outcome.IsCouldNotAsk() {
		t.Fatalf("a machine whose adb server another account runs could not be asked about its phones, got %s with %v",
			outcome.KindString(), outcome.Offerable())
	}
	if ran(mark) {
		t.Fatal("adb was run against a server another account runs")
	}
	if want := "runs as another account on this machine (uid 1002), so the agent will not use it as adb's server"; !strings.Contains(outcome.Reason(), want) {
		t.Fatalf("the reason says why in plain words (%q), got %q", want, outcome.Reason())
	}
	if !strings.Contains(outcome.Remedy(), "adb start-server") {
		t.Fatalf("the remedy says what to do, got %q", outcome.Remedy())
	}
}

func TestThisAccountsOwnAdbServerIsUsed(t *testing.T) {
	mark := adbStub(t)
	adbServerHeldByThisTest(t)

	outcome := android.Probe{Run: android.NewAdbRunner(nil), Enabled: true}.Outcome(context.Background())
	if !outcome.IsAnswered() || len(outcome.Offerable()) != 1 || !ran(mark) {
		t.Fatalf("this account's own server, asked of the real kernel, is used: %s %v (ran %v) %q",
			outcome.KindString(), outcome.Offerable(), ran(mark), outcome.Reason())
	}
}

// NOTHING LISTENING is the ordinary first run: adb starts its own server, as this account.
func TestNoAdbServerYetIsNotARefusal(t *testing.T) {
	mark := adbStub(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(port))

	outcome := android.Probe{Run: android.NewAdbRunner(anotherAccount), Enabled: true}.Outcome(context.Background())
	if !outcome.IsAnswered() || !ran(mark) {
		t.Fatalf("with no server listening adb starts this account's own: %s %q", outcome.KindString(), outcome.Reason())
	}
}

// A SERVER ELSEWHERE cannot be checked, so it is not used — and nothing is dialled to find that out.
func TestAnAdbServerOffThisMachineIsNotUsed(t *testing.T) {
	for _, c := range []struct{ name, value string }{
		{"ADB_SERVER_SOCKET", "tcp:192.0.2.1:5037"},
		{"ADB_SERVER_SOCKET", "localfilesystem:/tmp/adb.sock"},
		{"ANDROID_ADB_SERVER_ADDRESS", "192.0.2.1"},
	} {
		t.Run(c.name+"="+c.value, func(t *testing.T) {
			mark := adbStub(t)
			t.Setenv(c.name, c.value)
			outcome := android.Probe{Run: android.NewAdbRunner(nil), Enabled: true}.Outcome(context.Background())
			if !outcome.IsCouldNotAsk() || ran(mark) {
				t.Fatalf("an adb server off this machine is not used: %s (ran %v)", outcome.KindString(), ran(mark))
			}
			if !strings.Contains(outcome.Reason(), c.name) {
				t.Fatalf("the reason names the variable that points adb elsewhere, got %q", outcome.Reason())
			}
		})
	}
}
