package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── TURNING CAPTURE ON: the CA first, then the mark, and nothing on a system that cannot keep the key ─────────────

func TestEnableMakesTheCAThenTheMarkAndNeverReplacesACA(t *testing.T) {
	store := StoreIn(t.TempDir())
	now := time.Now()
	ca, err := Enable(store, "bench-mac", "v-test", now, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, on := store.Enabled(); !on {
		t.Fatal("enable must leave the mark")
	}
	for path, want := range map[string]os.FileMode{store.CADir(): 0o700, netcap.KeyPathIn(store.CADir()): 0o600} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s must be %04o: %v %v", path, want, info.Mode().Perm(), err)
		}
	}
	if _, err := Enable(store, "bench-mac", "v-test", now, "linux"); !errors.Is(err, ErrAlreadyOn) {
		t.Fatalf("a second enable must not replace the CA: %v", err)
	}
	if again, _ := netcap.ReadCertificate(store.CADir()); netcap.Fingerprint(again) != ca.Fingerprint() {
		t.Fatal("the CA was replaced")
	}
}

func TestEnableOnASystemThatCannotKeepTheKeyMakesNothing(t *testing.T) {
	root := t.TempDir()
	store := StoreIn(root)
	if _, err := Enable(store, "bench-mac", "v-test", time.Now(), "plan9"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Lstat(store.Root); !os.IsNotExist(err) {
		t.Fatalf("nothing may be made: %v", err)
	}
}

func TestAHalfMadeCAWithoutTheMarkIsReplaced(t *testing.T) {
	store := StoreIn(t.TempDir())
	first, err := Enable(store, "bench-mac", "v-test", time.Now(), "darwin")
	if err != nil {
		t.Fatal(err)
	}
	must(t, store.removeMark()) // an enable that died between the CA and the mark
	second, err := Enable(store, "bench-mac", "v-test", time.Now(), "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() == second.Fingerprint() {
		t.Fatal("a CA without its mark is this agent's own leftover, and is replaced")
	}
}

// ── TURNING IT OFF: the mark, every phone, every file, the key ──────────────────────────────────────────────────

func TestDisableRemovesTheMarkTheKeyTheFilesAndThePhonesAndOnlyThose(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	leftBehind(t, m, phone.serial, leaseA)
	must(t, os.MkdirAll(m.store.FilesDir(), 0o700))
	capture := filepath.Join(m.store.FilesDir(), leaseB+".har")
	must(t, os.WriteFile(capture, []byte(`{"log":{"entries":[]}}`), 0o600))
	// NOT ITS OWN: a file of another shape, a folder of a lease's name and a link of a lease's name all stay.
	other := filepath.Join(m.store.FilesDir(), "notes.txt")
	must(t, os.WriteFile(other, []byte("mine"), 0o600))
	must(t, os.Mkdir(filepath.Join(m.store.FilesDir(), leaseC+".har"), 0o700))
	target := filepath.Join(t.TempDir(), "elsewhere.har")
	must(t, os.WriteFile(target, []byte("not the agent's"), 0o600))
	must(t, os.Symlink(target, filepath.Join(m.store.FilesDir(), "1a2b3c4d-0000-4000-8000-000000000009.har")))

	result := Disable(context.Background(), m.store, m.bench.adb, m.clock.Now, time.Second)
	if result.Failed() {
		t.Fatalf("nothing should be left undone: %+v", result)
	}
	if _, on := m.store.Enabled(); on {
		t.Fatal("the mark must go")
	}
	for _, gone := range []string{netcap.KeyPathIn(m.store.CADir()), netcap.CertificatePath(m.store.CADir()), capture, m.store.phonesPath()} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s must be deleted: %v", gone, err)
		}
	}
	for _, kept := range []string{other, target, filepath.Join(m.store.FilesDir(), leaseC+".har")} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("%s is not a capture file of this agent's, and must stay: %v", kept, err)
		}
	}
	if len(result.HandedBack) != 1 || result.HandedBack[0] != phone.serial || m.bench.proxyOf(phone.serial) != ":0" {
		t.Fatalf("the phone must be handed back: %+v %q", result.HandedBack, m.bench.proxyOf(phone.serial))
	}
	if result.FilesDeleted != 1 || result.CAName != "NthCase agent CA — bench-mac" {
		t.Fatalf("got %+v", result)
	}

	summary := DisableSummary(result, m.store.CADir())
	for _, want := range []string{
		"Network capture is off for this machine.",
		"Handed back, with the proxy setting cleared: R5CT30ABCDE.",
		"The capture CA's key and certificate are deleted from this machine.",
		"Every capture file on this machine is deleted (1).",
		`Samsung — it is listed in two places; remove it in both:`,
		`View security certificates → User: delete "NthCase agent CA — bench-mac"`,
		`User certificates: delete it there too.`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("the summary must say %q:\n%s", want, summary)
		}
	}
}

func TestDisableReportsAPhoneItCouldNotReachAndKeepsItsRecord(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	r := leftBehind(t, m, phone.serial, leaseA)
	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = true })

	result := Disable(context.Background(), m.store, m.bench.adb, m.clock.Now, time.Second)
	if !result.Failed() || len(result.StillOwed) != 1 || len(result.HandedBack) != 0 {
		t.Fatalf("got %+v", result)
	}
	if records, _ := m.store.Records(); len(records) != 1 {
		t.Fatalf("its record is kept, so the next connect hands it back: %+v", records)
	}
	summary := DisableSummary(result, m.store.CADir())
	if !strings.Contains(summary, "NOT HANDED BACK YET — "+unreachableBody(phone.serial, r.Proxy)) {
		t.Fatalf("the summary must say what the phone is left with and how to clear it:\n%s", summary)
	}
}

func TestDisableWaitsForTheRunningConnectToHandItsPhonesBack(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(func(cfg *Config) { cfg.MarkEvery, cfg.PassEvery = 20*time.Millisecond, time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ctl.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	ctl.Start(context.Background(), leaseA, phone.serial)
	if ctl.Running() != 1 {
		t.Fatalf("the capture did not start:\n%s", m.out.String())
	}

	// `capture disable` in another terminal: the phone's lock is held by the capture until connect, seeing the mark
	// gone, stops it.
	result := Disable(context.Background(), m.store, m.bench.adb, m.clock.Now, 10*time.Second)
	if result.Failed() || len(result.HandedBack) != 1 {
		t.Fatalf("got %+v\n%s", result, m.out.String())
	}
	if ctl.Running() != 0 || m.bench.proxyOf(phone.serial) != ":0" {
		t.Fatalf("running %d, proxy %q", ctl.Running(), m.bench.proxyOf(phone.serial))
	}
}

func TestDisableSaysPlainlyWhatItCouldNotDelete(t *testing.T) {
	r := DisableResult{
		CAName: "NthCase agent CA — bench-mac", MarkPath: "/x/enabled", MarkFailure: errors.New("permission denied"),
		CAFailure: errors.New("could not delete ca-key.pem in /x/ca"), FilesDeleted: 2, FilesFailed: []string{leaseA + ".har"},
	}
	summary := DisableSummary(r, "/x/ca")
	for _, want := range []string{
		"Network capture could NOT be turned off: its mark /x/enabled could not be deleted (permission denied).",
		"The capture CA's key could NOT be deleted",
		"delete the folder /x/ca by hand",
		"2 capture file(s) are deleted, and these could NOT be: " + leaseA + ".har",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("missing %q:\n%s", want, summary)
		}
	}
	for _, never := range []string{"Network capture is off for this machine.", "are deleted from this machine.", "Every capture file"} {
		if strings.Contains(summary, never) {
			t.Fatalf("it must not claim %q:\n%s", never, summary)
		}
	}
}
