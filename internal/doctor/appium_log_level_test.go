package doctor_test

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// THE_HEADER_THAT_REACHES_APPIUM, item 3 — WHAT ONLY APPIUM'S OWN LOG LEVEL CAN CLOSE.
//
// A request that types a value now carries X-Appium-Is-Sensitive to the Appium on this computer, and Appium masks that
// request's body. Some of Appium's own lines no header masks, and all of them are written at debug — Appium's default: a
// driver's answer (a field's text read back), "Responding to client with … result", "Encountered internal error", and on
// iOS the web view's "Executing atom … with 'args=[…]'", a saved login among them. This agent never starts Appium; it is
// given one (--appium), and Appium tells no client the level it logs at. So the doctor says what it can: that it cannot read
// the level, that Appium starts at debug, what debug writes, and the command that starts it at info.

// readyAppium is a machine whose Appium answers at --appium, and nothing else: no adb, no Xcode, no phone.
func readyAppium(runs *[]string, statuses *[]string) doctor.Env {
	return doctor.Env{
		LookPath:    func(string) (string, error) { return "", context.Canceled },
		DialTimeout: func(string, string, time.Duration) (net.Conn, error) { return nil, context.Canceled },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			*runs = append(*runs, strings.TrimSpace(name+" "+strings.Join(args, " ")))
			return "", context.Canceled
		},
		AppiumURL: "http://127.0.0.1:4723",
		AppiumStatus: func(_ context.Context, baseURL string) error {
			*statuses = append(*statuses, baseURL)
			return nil
		},
	}
}

// RED AT HEAD. The doctor names Appium's log level as what it is: not read, debug unless Appium was told otherwise, and it
// stops no run.
func TestTheDoctorSaysItCannotReadAppiumsLogLevelAndThatItStartsAtDebug(t *testing.T) {
	var runs, statuses []string
	report := doctor.Diagnose(context.Background(), readyAppium(&runs, &statuses), nil)
	c := checkNamed(t, report, "Appium log level")

	if c.State != doctor.Unknown || !c.Advisory || c.Blocks() {
		t.Fatalf("the level is not established, and it stops no run: state %q advisory %v", c.State, c.Advisory)
	}
	for _, want := range []string{"does not tell", "debug"} {
		if !strings.Contains(c.Detail, want) {
			t.Fatalf("what was found must say %q: %q", want, c.Detail)
		}
	}
	// AMENDED (N1): the command keeps Appium on this computer's own address too. WAS: "appium --log-level info".
	if !strings.Contains(c.Remedy, "appium --address 127.0.0.1 --log-level info") {
		t.Fatalf("what to do is the command that starts Appium at info: %q", c.Remedy)
	}
	if !strings.Contains(c.Limits, "config") {
		t.Fatalf("what the check cannot see is said — a level set in Appium's config file: %q", c.Limits)
	}
}

// RED AT HEAD. In the doctor's three parts: what is wrong, why it matters (what debug writes), and what to do — under a
// headline that still says nothing stops a run, because nothing does.
func TestTheDoctorSaysWhatDebugWritesInItsThreeParts(t *testing.T) {
	var runs, statuses []string
	logLevel := checkNamed(t, doctor.Diagnose(context.Background(), readyAppium(&runs, &statuses), nil), "Appium log level")
	report := doctor.Report{Checks: []doctor.Check{{Name: "Appium endpoint", State: doctor.Satisfied, Detail: "http://127.0.0.1:4723"}, logLevel}}

	var out bytes.Buffer
	if err := doctor.Human(&out, report, doctor.Options{Mode: ui.Mode{ASCII: true}}); err != nil {
		t.Fatal(err)
	}
	// Read as words: the screen folds its lines at its own width.
	text := strings.Join(strings.Fields(out.String()), " ")
	why := doctor.WhatItStops(logLevel)
	// AMENDED (N1): the command says --address 127.0.0.1 before the level. WAS: "appium --log-level info".
	for _, want := range []string{"Ready: nothing here stops a run", "Appium log level", strings.Join(strings.Fields(why), " "), "appium --address 127.0.0.1 --log-level info"} {
		if want == "" || !strings.Contains(text, want) {
			t.Fatalf("the doctor must say %q:\n%s", want, text)
		}
	}
	for _, part := range []string{"a saved login", "a field's text", "info"} {
		if !strings.Contains(why, part) {
			t.Fatalf("why it matters must say what debug writes (%q): %q", part, why)
		}
	}
	if strings.Contains(text, "could not be established") {
		t.Fatalf("a level the agent cannot read stops nothing, and the headline must not say otherwise:\n%s", text)
	}
}

// CONTROL — green at HEAD and now. The doctor changes nothing about an Appium it did not start: it asks it its status and
// nothing else, and runs no command that starts Appium or sets its level.
func TestTheDoctorLeavesAnAppiumItDidNotStartAsItIs(t *testing.T) {
	var runs, statuses []string
	doctor.Diagnose(context.Background(), readyAppium(&runs, &statuses), nil)

	if len(statuses) != 1 || statuses[0] != "http://127.0.0.1:4723" {
		t.Fatalf("the doctor asks the Appium it was given for its status, once: %v", statuses)
	}
	for _, run := range runs {
		if !strings.HasPrefix(run, "appium ") {
			continue
		}
		if run != "appium --version" && !strings.HasPrefix(run, "appium driver list") {
			t.Fatalf("the doctor ran an Appium command that is not a read: %q", run)
		}
		if strings.Contains(run, "log") {
			t.Fatalf("the doctor touched Appium's log level: %q", run)
		}
	}
}
