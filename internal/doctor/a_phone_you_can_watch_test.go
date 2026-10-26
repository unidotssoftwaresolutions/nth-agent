package doctor_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/doctor"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
)

// AN_AGENT_THAT_SHOWS_ITS_PHONE — `doctor` and `devices` say, ONE LINE PER PHONE, whether a run on it can be watched,
// and they say it with the same sentence the connect loop decides the phone's mirror port from (liveview.Judge).

// reservations is a machine where the Samsung's mirror port is free and every candidate for the iPhone is held.
func reservations(udid string) liveview.Reservation {
	if udid == "R5CT64M346B" {
		return liveview.Reservation{Reserved: true, Port: 35118, Derived: 35118, Tried: 1}
	}
	return liveview.Reservation{Derived: 31002, Tried: liveview.DefaultTries}
}

func watchableMachine() doctor.Env {
	env := equippedMachine().env()
	env.MirrorPortFor = reservations
	return env
}

func liveViewChecks(r doctor.Report) map[string]doctor.Check {
	out := map[string]doctor.Check{}
	for _, c := range r.Checks {
		if strings.HasPrefix(c.Name, "live view") {
			out[c.Name] = c
		}
	}
	return out
}

func TestTheDoctorSaysEachPhonesLiveViewInOneLineAndNeverAsABlocker(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1"}},
			[]inventory.Unusable{inventory.NewUnusable("R5CT99ZZZZZ", "unauthorized", "Tap Allow on the device.")}),
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030", Model: "iPhone 15"}}, nil),
	}
	report := doctor.Diagnose(context.Background(), watchableMachine(), outcomes)
	checks := liveViewChecks(report)
	if len(checks) != 2 {
		t.Fatalf("one live-view line per USABLE phone, and the unusable one gets none: %v", checks)
	}
	samsung, iphone := checks["live view R5CT64M346B"], checks["live view 00008030"]
	if samsung.State != doctor.Satisfied || !strings.Contains(samsung.Detail, "127.0.0.1:35118") {
		t.Fatalf("a phone with a reserved port and a driver that opens it is ready: %+v", samsung)
	}
	if iphone.State != doctor.Missing || !iphone.Advisory || iphone.Blocks() ||
		!strings.Contains(iphone.Detail, "no mirror port was free") || !strings.Contains(iphone.Remedy, "free 31002") {
		t.Fatalf("a phone with no free port is MISSING, does not stop a run, and says what to free: %+v", iphone)
	}
	if doctor.ExitCode(report) != 0 {
		t.Fatal("no live view never stops a run — the phone is still driven")
	}

	var human bytes.Buffer
	doctor.Human(&human, report, doctor.Options{})
	for _, line := range []string{"live view R5CT64M346B: ready", "live view 00008030: missing (does not stop a run)"} {
		if !strings.Contains(human.String(), line) {
			t.Fatalf("the doctor's output must carry %q:\n%s", line, human.String())
		}
	}
}

func TestADriverThatCannotOpenTheMirrorIsTheReasonNotThePort(t *testing.T) {
	env := watchableMachine()
	env.MirrorPortFor = func(string) liveview.Reservation {
		return liveview.Reservation{Reserved: true, Port: 31002, Derived: 31002, Tried: 1}
	}
	machine := equippedMachine()
	machine.run["appium driver list --installed"] = "- uiautomator2@7.5.2 [installed (npm)]\n- xcuitest@7.35.1 [installed (npm)]"
	env.Run = machine.env().Run
	report := doctor.Diagnose(context.Background(), env,
		[]inventory.Outcome{inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030"}}, nil)})
	iphone := liveViewChecks(report)["live view 00008030"]
	if iphone.State != doctor.Missing || !strings.Contains(iphone.Detail, "xcuitest driver 7.35.1 does not forward") ||
		iphone.Remedy != "appium driver update xcuitest, then restart the agent" {
		t.Fatalf("an xcuitest before 8.1.0 cannot show a real iPhone, whatever port is free: %+v", iphone)
	}
}

func TestAMachineThatCannotCheckItsPortsSaysUnknown(t *testing.T) {
	env := equippedMachine().env() // no MirrorPortFor: a build that cannot ask
	report := doctor.Diagnose(context.Background(), env, bothPlatformsSeen())
	for name, c := range liveViewChecks(report) {
		if c.State != doctor.Unknown {
			t.Fatalf("%s: a machine that cannot check is unknown, never ready and never missing: %+v", name, c)
		}
	}
	none := doctor.Diagnose(context.Background(), watchableMachine(), []inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)})
	if c := liveViewChecks(none)["live view"]; c.State != doctor.NotRequired {
		t.Fatalf("no usable phone, no question: %+v", c)
	}
}

// ── TWO SURFACES, ONE PHONE, ONE SENTENCE ────────────────────────────────────────────────────────────────────────

func TestDevicesAndDoctorSayTheSameSentenceAboutTheSamePhone(t *testing.T) {
	outcomes := []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1"}},
			[]inventory.Unusable{inventory.NewUnusable("R5CT99ZZZZZ", "unauthorized", "Tap Allow on the device.")}),
		inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008030", Model: "iPhone 15"}}, nil),
	}
	env := watchableMachine()
	report := doctor.Diagnose(context.Background(), env, outcomes)

	drivers := liveview.Drivers{Read: true, Installed: liveview.ParseDriverList("- uiautomator2@7.5.2 [installed (npm)]\n- xcuitest@11.9.0 [installed (npm)]")}
	judge := func(platform inventory.Platform, udid string) liveview.Availability {
		return liveview.Judge(liveview.Facts{Platform: platform, Reservation: reservations(udid), Drivers: drivers})
	}
	var listing bytes.Buffer
	if err := render.Human(&listing, outcomes, judge); err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(listing.String(), "live view:")
	if lines != 2 {
		t.Fatalf("`devices` prints one live-view line per usable phone and none for the unusable one:\n%s", listing.String())
	}
	for name, c := range liveViewChecks(report) {
		if !strings.Contains(listing.String(), c.Detail) {
			t.Fatalf("%s: `doctor` says %q and `devices` does not say the same:\n%s", name, c.Detail, listing.String())
		}
	}
	if !strings.Contains(listing.String(), "live view: no — no mirror port was free on this machine") ||
		!strings.Contains(listing.String(), "live view: yes — its mirror is 127.0.0.1:35118") {
		t.Fatalf("each line says yes or no first:\n%s", listing.String())
	}

	var machineReadable bytes.Buffer
	if err := render.JSON(&machineReadable, outcomes, judge); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"available": true`, `"port": 35118`, `"available": false`, `"remedy": "free 31002`} {
		if !strings.Contains(machineReadable.String(), want) {
			t.Fatalf("`devices --json` carries the same facts (%s):\n%s", want, machineReadable.String())
		}
	}

	// A caller that established nothing about live views prints nothing about them — yesterday's bytes.
	var silent bytes.Buffer
	render.Human(&silent, outcomes, nil)
	if strings.Contains(silent.String(), "live view") {
		t.Fatalf("no judge, no line:\n%s", silent.String())
	}
}
