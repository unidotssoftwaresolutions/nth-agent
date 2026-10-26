package android_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// THE SEAM, DRIVEN END TO END — and the reason this file exists apart from probe_test.go.
//
// The old seal handed the probe the captured fixture as stdout AND a hand-written
// errors.New("ADB server didn't ACK") as the error, then asserted the reason contained "ACK": it
// asserted its own input. The seam between the RUNNER's error construction (main.go's adbRunner
// wraps the whole combined output) and the PROBE's line selection was never exercised, and the two
// halves together printed a PROGRESS line as the cause.
//
// productionRunnerShape drives the REAL runner over a captured fixture.
//
// It used to REPLICATE adbRunner's wrapping in this file, while claiming "a change to either half moves this
// test" — a change to the runner moved nothing, because the runner was in package main and this file could not
// reach it. Now android.AdbRunner is in this package and the fixture is played back THROUGH it: a shell that
// exits non-zero after printing the capture, so the error this probe sees is the one production builds.
func productionRunnerShape(t *testing.T, fixture string) android.Runner {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("resolving fixture %s: %v", fixture, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reading fixture %s: %v", fixture, err)
	}
	return func(ctx context.Context, _ ...string) (string, error) {
		// AdbRunner shells out to `adb`; PATH is pointed at a stub that prints the capture and exits 1, so the
		// REAL construction (CombinedOutput + the fmt.Errorf wrapping) is what produces the error under test.
		return android.AdbRunner(withStubAdb(t, path), "devices", "-l")
	}
}

// withStubAdb puts a one-line `adb` on PATH that cats the fixture and exits non-zero, so AdbRunner's own
// failure path runs. No real adb is involved and no adb server is contacted.
func withStubAdb(t *testing.T, fixturePath string) context.Context {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat " + shellQuote(fixturePath) + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the adb stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// …and adb's server is a port nothing holds, so the runner's own check of who runs that server (adb_server_test.go)
	// asks this test's machine nothing either.
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(free.Addr().(*net.TCPAddr).Port))
	_ = free.Close()
	t.Setenv("ADB_SERVER_SOCKET", "")
	t.Setenv("ANDROID_ADB_SERVER_ADDRESS", "")
	return context.Background()
}

func shellQuote(path string) string { return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'" }

// A customer reads this sentence. It must name the CAUSE, not adb's progress narration.
func TestTheReasonNamesTheCauseNotAProgressLine(t *testing.T) {
	outcome := android.Probe{Run: productionRunnerShape(t, "server-not-acking.txt"), Enabled: true}.
		Outcome(context.Background())

	if !outcome.IsCouldNotAsk() {
		t.Fatalf("a server that will not start is COULD-NOT-ASK, got %s", outcome.KindString())
	}
	reason := outcome.Reason()
	t.Logf("production reason string: %q", reason)

	if strings.Contains(reason, "daemon not running; starting now") {
		t.Fatalf("the reason presents adb's PROGRESS line as the cause: %q", reason)
	}
	// PRECISELY what is forbidden: a progress LINE, which adb starts with `*`. An earlier draft of
	// this assertion forbade the substring "* " anywhere and went red on the CORRECT output, because
	// adb's own diagnosis names the socket `*smartsocket*` — a seal must forbid the defect, not a
	// character the right answer also contains.
	for _, part := range strings.Split(reason, " — ") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "adb could not be asked: ")
		if strings.HasPrefix(part, "*") {
			t.Fatalf("a `*` progress line is adb's narration, not the cause (Parse already skips them): %q", reason)
		}
	}
	// The cause adb actually reported, in its own words. Both halves of the real failure are named:
	// what could not be done, and the diagnosis underneath it.
	if !strings.Contains(reason, "ADB server didn't ACK") {
		t.Fatalf("the reason must carry what adb said went wrong, got %q", reason)
	}
	if !strings.Contains(reason, "smartsocket") {
		t.Fatalf("…and the diagnosis it printed further down, which is what a reader acts on, got %q", reason)
	}
}

// The happy-path shape of the same seam: an adb that exits ZERO must not be read as a failure just
// because its output happens to contain a `*` line.
func TestASuccessfulAdbIsNeverTurnedIntoAFailure(t *testing.T) {
	outcome := android.Probe{
		Run: func(context.Context, ...string) (string, error) {
			return "* daemon not running; starting now at tcp:5037\n* daemon started successfully\nList of devices attached\n\n", nil
		},
		Enabled: true,
	}.Outcome(context.Background())

	if !outcome.IsAnswered() {
		t.Fatalf("a zero-exit adb is an ANSWER whatever it narrated, got %s", outcome.KindString())
	}
	if len(outcome.Offerable()) != 0 || len(outcome.Unusable()) != 0 {
		t.Fatalf("…with nothing attached: %+v / %+v", outcome.Offerable(), outcome.Unusable())
	}
}

// THE MARKER SEAL for this probe — the guard that can actually fail.
//
// Asserting `TrimSpace(remedy) != ""` proves nothing: inventory.CouldNotAsk substitutes
// MissingRemedyMarker for a blank one, so the constructor supplies the answer the check tests. What
// can fail is the marker's ABSENCE, driven through this probe's own seam: every way this probe can
// reach a CouldNotAsk must hand the reader a real remedy.
func TestEveryAndroidCouldNotAskCarriesARealRemedy(t *testing.T) {
	ways := map[string]android.Runner{
		"adb missing from PATH": func(context.Context, ...string) (string, error) {
			return "", fmt.Errorf(`exec: "adb": executable file not found in $PATH`)
		},
		"server will not start": productionRunnerShape(t, "server-not-acking.txt"),
		"deadline exceeded": func(ctx context.Context, _ ...string) (string, error) {
			return "", context.DeadlineExceeded
		},
		"empty output and a bare error": func(context.Context, ...string) (string, error) {
			return "", fmt.Errorf("")
		},
	}

	for name, run := range ways {
		outcome := android.Probe{Run: run, Enabled: true}.Outcome(context.Background())
		if !outcome.IsCouldNotAsk() {
			t.Fatalf("%s: expected COULD-NOT-ASK, got %s", name, outcome.KindString())
		}
		if outcome.Remedy() == inventory.MissingRemedyMarker {
			t.Fatalf("%s: the reader got the marker — this probe ships a CouldNotAsk with no remedy", name)
		}
		if strings.Contains(outcome.Remedy(), "no remedy provided") {
			t.Fatalf("%s: the marker's text reached the reader by another route: %q", name, outcome.Remedy())
		}
	}
}
