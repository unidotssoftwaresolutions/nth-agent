package doctor_test

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// AN APPIUM ONLY THIS COMPUTER CAN REACH. An Appium that answers /status, runs as this account and listens on every address
// of the computer used to read "ready" — and anything on the network that reaches it drives the phones past every check the
// agent makes. The doctor reads where the program on Appium's port is bound, from the kernel's own table (a seam here), and
// says so when it is anywhere but loopback: a look, not a stop, since connect still drives it; with the command that fixes it.

func boundTo(addresses ...string) func(int) ([]net.IP, bool) {
	return func(port int) ([]net.IP, bool) {
		if port != 4723 {
			return nil, true
		}
		var ips []net.IP
		for _, a := range addresses {
			ips = append(ips, net.ParseIP(a))
		}
		return ips, true
	}
}

func TestTheDoctorSaysWhenAppiumAlsoListensBeyondThisComputer(t *testing.T) {
	for _, c := range []struct {
		name, bound string
		want        []string
	}{
		{"every address of IPv4", "0.0.0.0", []string{"0.0.0.0 (every address of this computer)"}},
		{"every address of both", "::", []string{":: (every address of this computer)"}},
		{"a network address beside loopback", "127.0.0.1 192.168.1.20", []string{"192.168.1.20"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var runs, statuses []string
			env := readyAppium(&runs, &statuses)
			env.AppiumListeners = boundTo(strings.Fields(c.bound)...)
			check := checkNamed(t, doctor.Diagnose(context.Background(), env, nil), "Appium endpoint")
			if check.State != doctor.Satisfied || !check.Advisory || check.Blocks() {
				t.Fatalf("it answers and runs are driven, and it needs a look: state %q advisory %v", check.State, check.Advisory)
			}
			for _, want := range c.want {
				if !strings.Contains(check.Detail, want) {
					t.Errorf("what was found must name %q: %q", want, check.Detail)
				}
			}
			if !strings.Contains(check.Remedy, "appium --address 127.0.0.1") {
				t.Errorf("what to do is the command that keeps Appium on this computer: %q", check.Remedy)
			}
			if why := doctor.WhatItStops(check); !strings.Contains(why, "another computer") {
				t.Errorf("why it matters is said: %q", why)
			}
			var out bytes.Buffer
			if err := doctor.Human(&out, doctor.Diagnose(context.Background(), env, nil), doctor.Options{Mode: ui.Mode{Columns: 200}}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "!! Appium endpoint") && !strings.Contains(out.String(), "! Appium endpoint") {
				t.Errorf("the report marks it as needing a look:\n%s", out.String())
			}
		})
	}
}

func TestTheDoctorSaysNothingMoreForAnAppiumOnLoopback(t *testing.T) {
	for _, bound := range []string{"127.0.0.1", "::1", "127.0.0.1 ::1"} {
		var runs, statuses []string
		env := readyAppium(&runs, &statuses)
		env.AppiumListeners = boundTo(strings.Fields(bound)...)
		check := checkNamed(t, doctor.Diagnose(context.Background(), env, nil), "Appium endpoint")
		if check.State != doctor.Satisfied || check.Advisory || check.Remedy != "" {
			t.Errorf("%s: an Appium on loopback alone is ready, with nothing to do: %+v", bound, check)
		}
	}
	// A kernel table that could not be read is said in what the check did not establish, and changes nothing else.
	var runs, statuses []string
	env := readyAppium(&runs, &statuses)
	env.AppiumListeners = func(int) ([]net.IP, bool) { return nil, false }
	check := checkNamed(t, doctor.Diagnose(context.Background(), env, nil), "Appium endpoint")
	if check.State != doctor.Satisfied || check.Advisory || !strings.Contains(check.Limits, "where Appium listens") {
		t.Errorf("an unread table is a limit, not a finding: %+v", check)
	}
}
