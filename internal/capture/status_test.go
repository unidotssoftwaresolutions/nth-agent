package capture

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── WHAT `doctor` SAYS ABOUT CAPTURE, read from the state folder alone ───────────────────────────────────────────

func lines(s Status) string { return strings.Join(s.Lines, "\n") }

func TestDoctorSaysCaptureIsOffAndHowToTurnItOn(t *testing.T) {
	status := ReadStatus(StoreIn(t.TempDir()), time.Now())
	if status.On || status.Lines[0] != "network capture: off — npx nthcase-agent@latest capture enable turns it on." {
		t.Fatalf("got %+v", status)
	}
	// The promise, up to where it would repeat SixThings (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 3) — and the cut is real.
	own, _, found := strings.Cut(ThePromise(), promiseRepeatsFrom)
	if !found || !strings.Contains(lines(status), SixThings()) || !strings.Contains(lines(status), own) {
		t.Fatalf("doctor says the six things and the promise even when capture is off:\n%s", lines(status))
	}
}

func TestDoctorStatesTheCAsEndAndHowToRenewItFromThirtyDaysBefore(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	cert := m.ca.Certificate()
	ends := cert.NotAfter.UTC().Format(time.DateOnly)

	fresh := ReadStatus(m.store, m.clock.Now())
	if !fresh.On || fresh.CAEnds == nil || !strings.Contains(fresh.Lines[0], "ends "+ends) ||
		!strings.Contains(fresh.Lines[0], `"NthCase agent CA — bench-mac"`) || !strings.Contains(lines(fresh), SixThings()) {
		t.Fatalf("doctor must name the CA and the day it ends:\n%s", lines(fresh))
	}
	if strings.Contains(lines(fresh), "To renew it") {
		t.Fatalf("a CA with most of its year left needs no renewal line:\n%s", lines(fresh))
	}
	if !strings.Contains(lines(fresh), "phone set up: R5CT30ABCDE (SM-A546B) — its browser trusted the capture certificate on") {
		t.Fatalf("doctor names each phone set up:\n%s", lines(fresh))
	}

	soon := ReadStatus(m.store, cert.NotAfter.Add(-29*24*time.Hour))
	if !strings.Contains(lines(soon), "the capture CA ends within 30 days. To renew it: npx nthcase-agent@latest capture disable, then npx nthcase-agent@latest capture enable, then set up each phone") {
		t.Fatalf("thirty days before its end, doctor says how to renew it:\n%s", lines(soon))
	}
	if !strings.Contains(soon.Lines[0], "(29 days from now)") {
		t.Fatalf("got %q", soon.Lines[0])
	}

	ended := ReadStatus(m.store, cert.NotAfter.Add(time.Hour))
	if ended.CAProblem != "the capture CA has ended" || !strings.Contains(lines(ended), "the capture CA has ended, so nothing is captured on this machine. To renew it") {
		t.Fatalf("got %+v", ended)
	}
}

func TestDoctorTellsAPhoneSetUpForAnEarlierCAFromOneSetUpForThisOne(t *testing.T) {
	m := newMachine(t, samsung("R5CT30ABCDE"))
	must(t, m.store.setPhone(Phone{Serial: "R5CT30OLDCA", Model: "Pixel 8", CAFingerprint: strings.Repeat("ab", 32), SetUpAtUtc: m.clock.Now()}))
	status := ReadStatus(m.store, m.clock.Now())
	if !strings.Contains(lines(status), "phone R5CT30OLDCA (Pixel 8) was set up for an earlier capture CA, so it is not captured: npx nthcase-agent@latest capture setup R5CT30OLDCA") {
		t.Fatalf("got:\n%s", lines(status))
	}
	if len(status.Phones) != 2 || status.Phones[0].ForThisCA == status.Phones[1].ForThisCA {
		t.Fatalf("got %+v", status.Phones)
	}
}

func TestDoctorSaysWhenTheCACannotBeRead(t *testing.T) {
	m := newMachine(t)
	must(t, os.Remove(netcap.CertificatePath(m.store.CADir())))
	status := ReadStatus(m.store, m.clock.Now())
	if status.CAProblem == "" || !strings.HasPrefix(status.Lines[0], "network capture: ON, and its CA cannot be read — turn capture off and on") {
		t.Fatalf("got %+v", status)
	}
}

func TestDoctorReadsTheCertificateAndNeverTheKey(t *testing.T) {
	m := newMachine(t)
	must(t, os.Chmod(netcap.KeyPathIn(m.store.CADir()), 0o000)) // a key nobody can read
	t.Cleanup(func() { _ = os.Chmod(netcap.KeyPathIn(m.store.CADir()), 0o600) })
	if status := ReadStatus(m.store, m.clock.Now()); status.CAProblem != "" || status.CAEnds == nil {
		t.Fatalf("doctor must answer from the certificate alone: %+v", status)
	}
}
