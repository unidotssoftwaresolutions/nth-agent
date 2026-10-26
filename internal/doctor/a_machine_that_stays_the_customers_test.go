package doctor_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — the doctor says whose program answered, and a program another account runs is
// never reported as this machine's working adb server or Appium.

func TestAnAdbServerAnotherAccountRunsIsNotReportedAsWorking(t *testing.T) {
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb"}, listening: map[string]bool{"127.0.0.1:5037": true}}
	env := m.env()
	env.AdbServerOwner = func(net.Conn) (int, bool) { return 1002, true }
	server := find(t, doctor.Diagnose(context.Background(), env, androidSeen()), "adb server")
	if server.State == doctor.Satisfied || !server.State.Blocking() {
		t.Fatalf("another account's adb server stops this machine's Android phones being asked about, got %q: %s", server.State, server.Detail)
	}
	if !strings.Contains(server.Detail, "another account on this machine (uid 1002)") || !strings.Contains(server.Remedy, "adb start-server") {
		t.Fatalf("the check says whose it is and what to do: %q / %q", server.Detail, server.Remedy)
	}
}

func TestAnAdbServerWhoseAccountCannotBeFoundIsUnknown(t *testing.T) {
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb"}, listening: map[string]bool{"127.0.0.1:5037": true}}
	env := m.env()
	env.AdbServerOwner = nil
	server := find(t, doctor.Diagnose(context.Background(), env, androidSeen()), "adb server")
	if server.State != doctor.Unknown {
		t.Fatalf("a server whose account nobody established is Unknown, never Satisfied, got %q", server.State)
	}
}

func TestThisAccountsAdbServerIsSatisfiedAndSaysWhose(t *testing.T) {
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb"}, listening: map[string]bool{"127.0.0.1:5037": true}}
	server := find(t, doctor.Diagnose(context.Background(), m.env(), androidSeen()), "adb server")
	if server.State != doctor.Satisfied || !strings.Contains(server.Detail, "run by you") {
		t.Fatalf("this account's own server: %q %q", server.State, server.Detail)
	}
}

func TestAnAdbServerOffThisMachineIsNotUsed(t *testing.T) {
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb"}}
	env := m.env()
	env.AdbServer = func() (string, string) {
		return "", "ADB_SERVER_SOCKET points adb at a server that is not on this machine's 127.0.0.1"
	}
	server := find(t, doctor.Diagnose(context.Background(), env, androidSeen()), "adb server")
	if !server.State.Blocking() || !strings.Contains(server.Detail, "ADB_SERVER_SOCKET") {
		t.Fatalf("a server elsewhere is not used, and the check says so: %q %q", server.State, server.Detail)
	}
}

// the Appium endpoint check names a program another account runs for what it is — not "something answered, but
// not as Appium", which sent a person to check a URL that was right.
func TestAnAppiumAnotherAccountRunsIsNamedForWhatItIs(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		detail string
		remedy string
	}{
		{"another account", &localconn.NotThisAccount{What: "your Appium", Address: "127.0.0.1:4723", UID: 1002, Found: true},
			"runs as another account on this machine (uid 1002)", "start Appium yourself"},
		{"not on this machine", &localconn.NotOnThisMachine{What: "your Appium", Host: "10.0.0.9"},
			"only on this machine", "--appium http://127.0.0.1:4723"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := machine{onPath: map[string]string{"appium": "/usr/local/bin/appium"}, appiumURL: "http://127.0.0.1:4723",
				appiumError: fmt.Errorf("Get %q: %w", "http://127.0.0.1:4723/status", c.err)}
			endpoint := find(t, doctor.Diagnose(context.Background(), m.env(), androidSeen()), "Appium endpoint")
			if !endpoint.State.Blocking() || !strings.Contains(endpoint.Detail, c.detail) || !strings.Contains(endpoint.Remedy, c.remedy) {
				t.Fatalf("got %q %q / %q", endpoint.State, endpoint.Detail, endpoint.Remedy)
			}
			if strings.Contains(endpoint.Remedy, "check the URL") {
				t.Fatalf("the URL is right; the program behind it is the problem: %q", endpoint.Remedy)
			}
		})
	}
}

// DECISION 1: doctor names each phone left out for not being on the cable, how it is reached, and the flag — and it is a
// note, not a fault: the machine's cabled phones still run.
func TestDoctorNamesTheLeftOutPhonesAndTheFlag(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.WithLeftOut(inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT30ABCDE"}}, nil),
			[]inventory.LeftOut{{ID: "emulator-5554", How: "an emulator"}}),
		inventory.WithLeftOut(inventory.Answered(inventory.IOS, nil, nil),
			[]inventory.LeftOut{{ID: "00008030-WIFI", How: "reached over Wi-Fi or the network"}}),
	}
	m := machine{onPath: map[string]string{"adb": "/usr/bin/adb"}}
	c := find(t, doctor.Diagnose(context.Background(), m.env(), outcomes), "devices left out")
	if c.State.Blocking() && !c.Advisory {
		t.Fatalf("a left-out phone is a note, not a fault: %q", c.State)
	}
	for _, want := range []string{"emulator-5554 (android) is an emulator", "00008030-WIFI (ios) is reached over Wi-Fi or the network", "only phones on this machine's USB cable"} {
		if !strings.Contains(c.Detail, want) {
			t.Fatalf("the detail says %q, got %q", want, c.Detail)
		}
	}
	if !strings.Contains(c.Remedy, "npx nthcase-agent@latest connect … --offer-uncabled-devices") {
		t.Fatalf("the remedy names the flag as a command, got %q", c.Remedy)
	}
	none := find(t, doctor.Diagnose(context.Background(), m.env(), androidSeen()), "devices left out")
	if none.State != doctor.NotRequired {
		t.Fatalf("with nothing left out there is nothing to say: %q", none.State)
	}
}
