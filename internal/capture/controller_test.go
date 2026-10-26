package capture

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── THE GATES: a start that fails one touches nothing, and its reason is the run's answer ─────────────────────────

func TestEveryGateRefusesWithoutTouchingThePhone(t *testing.T) {
	const serial = "R5CT30ABCDE"
	for _, c := range []struct {
		name    string
		serial  string
		arrange func(t *testing.T, m *machine, cfg *Config)
		want    string
	}{
		{name: "an iPhone", serial: "00008030-001A2B3C4D5E6F70", want: refusedNotAndroid},
		{name: "a serial adb would never print", serial: "R5CT 30", want: refusedNotAndroid},
		{name: "capture off on this machine", serial: serial, want: refusedOff(),
			arrange: func(t *testing.T, m *machine, _ *Config) { must(t, m.store.removeMark()) }},
		{name: "a system capture does not run on", serial: serial, want: refusedOff(),
			arrange: func(_ *testing.T, _ *machine, cfg *Config) { cfg.GOOS = "plan9" }},
		{name: "a CA key other users can read", serial: serial, want: refusedCA(),
			arrange: func(t *testing.T, m *machine, _ *Config) {
				must(t, os.Chmod(netcap.KeyPathIn(m.store.CADir()), 0o644))
			}},
		{name: "a CA past its year", serial: serial, want: refusedCA(),
			arrange: func(_ *testing.T, m *machine, _ *Config) { m.clock.Advance(367 * 24 * time.Hour) }},
		{name: "a phone not set up", serial: "R5CT30NOTSET", want: refusedNotSetUp("R5CT30NOTSET")},
		{name: "a phone set up for an earlier CA", serial: serial, want: refusedOldCA(serial),
			arrange: func(t *testing.T, m *machine, _ *Config) {
				must(t, m.store.setPhone(Phone{Serial: serial, CAFingerprint: strings.Repeat("0", 64), SetUpAtUtc: m.clock.Now()}))
			}},
		{name: "a phone another nth-agent is changing", serial: serial, want: refusedBusy(serial),
			arrange: func(t *testing.T, m *machine, _ *Config) {
				held, err := m.store.tryLockPhone(serial)
				must(t, err)
				t.Cleanup(held.unlock)
			}},
		{name: "no free port", serial: serial, want: refusedNoPort,
			arrange: func(_ *testing.T, _ *machine, cfg *Config) { cfg.PortFree = func(uint16) bool { return false } }},
		// AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE rule 2: the proxy lets in only this account, so a machine whose kernel cannot
		// say who connects would leave the phone pointed at a proxy that refuses its every connection.
		{name: "a machine that cannot tell which account connects", serial: serial, want: refusedPeerCheck,
			arrange: func(_ *testing.T, _ *machine, cfg *Config) {
				cfg.PeerCheck = func() error { return netcap.ErrPeerCheck }
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newMachine(t, samsung(serial))
			m.attach(samsung("R5CT30NOTSET")) // plugged in and ready — and never set up
			ctl := m.controller(func(cfg *Config) {
				if c.arrange != nil {
					c.arrange(t, m, cfg)
				}
			})
			ctl.Start(context.Background(), leaseA, c.serial)

			if touched := m.bench.commands(); len(touched) != 0 {
				t.Fatalf("a refused start touched a phone: %q", touched)
			}
			data, sentence := ctl.Pull(leaseA)
			if data != nil || sentence != c.want {
				t.Fatalf("the run must be told why, in the machine's words:\n got %q\nwant %q", sentence, c.want)
			}
			if !strings.Contains(m.out.String(), "was not started — "+c.want) {
				t.Fatalf("the console must say it, once: %q", m.out.String())
			}
			if records, _ := m.store.Records(); len(records) != 0 {
				t.Fatalf("a refused start left a record: %+v", records)
			}
			if entries, _ := os.ReadDir(m.store.FilesDir()); len(entries) != 0 {
				t.Fatalf("a refused start left a capture file: %v", entries)
			}
		})
	}
}

func TestAPhoneTheRunAlreadyCapturesIsNotCapturedTwice(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil)
	ctl.Start(context.Background(), leaseA, phone.serial)
	t.Cleanup(func() { ctl.StopAll(context.Background()) })
	before := len(m.bench.commands())

	ctl.Start(context.Background(), leaseA, phone.serial) // the same start, sent twice, is one start
	ctl.Start(context.Background(), leaseB, phone.serial) // another lease on the same phone is refused
	if after := len(m.bench.commands()); after != before {
		t.Fatalf("the phone was touched again: %q", m.bench.commands()[before:])
	}
	if _, sentence := ctl.Pull(leaseB); sentence != refusedLive(phone.serial) {
		t.Fatalf("got %q", sentence)
	}
}

func TestAMachineCapturesFourPhonesAtOnceAndRefusesTheFifth(t *testing.T) {
	serials := []string{"R5CT30AAAA1", "R5CT30AAAA2", "R5CT30AAAA3", "R5CT30AAAA4", "R5CT30AAAA5"}
	var phones []*fakePhone
	for _, s := range serials {
		phones = append(phones, pixel(s))
	}
	m := newMachine(t, phones...)
	ctl := m.controller(nil)
	t.Cleanup(func() { ctl.StopAll(context.Background()) })
	leases := []string{leaseA, leaseB, leaseC, "1a2b3c4d-0000-4000-8000-000000000004", "1a2b3c4d-0000-4000-8000-000000000005"}
	for i := 0; i < 4; i++ {
		ctl.Start(context.Background(), leases[i], serials[i])
	}
	if ctl.Running() != MaxConcurrent {
		t.Fatalf("four captures must run, %d do:\n%s", ctl.Running(), m.out.String())
	}
	ctl.Start(context.Background(), leases[4], serials[4])
	if _, sentence := ctl.Pull(leases[4]); sentence != refusedAt4 {
		t.Fatalf("got %q", sentence)
	}
	if touched := m.bench.commandsTo(serials[4]); len(touched) != 0 {
		t.Fatalf("the fifth phone was touched: %q", touched)
	}
	// Four captures, four different ports — each phone's own.
	ports := map[string]bool{}
	for _, s := range serials[:4] {
		ports[m.bench.proxyOf(s)] = true
	}
	if len(ports) != 4 {
		t.Fatalf("four phones share ports: %v", ports)
	}
}

func TestAPhoneThatRefusesPartWayIsHandedBackAndTheRunToldWhy(t *testing.T) {
	for _, c := range []struct {
		refuses string
		want    string
	}{
		{"reverse", refusedReverse},
		{"proxy-set", refusedProxy},
	} {
		t.Run(c.refuses, func(t *testing.T) {
			phone := samsung("R5CT30ABCDE")
			phone.refuses = map[string]bool{c.refuses: true}
			m := newMachine(t, phone)
			ctl := m.controller(nil)
			ctl.Start(context.Background(), leaseA, phone.serial)

			if _, sentence := ctl.Pull(leaseA); sentence != c.want {
				t.Fatalf("got %q, want %q", sentence, c.want)
			}
			if proxy := m.bench.proxyOf(phone.serial); proxy != "" && proxy != ":0" {
				t.Fatalf("the phone was left pointing at the capture: %q", proxy)
			}
			if m.bench.reversesOf(phone.serial) != 0 {
				t.Fatal("the phone was left with the capture's reverse")
			}
			if records, _ := m.store.Records(); len(records) != 0 {
				t.Fatalf("a phone read back clean keeps no record: %+v", records)
			}
			if entries, _ := os.ReadDir(m.store.FilesDir()); len(entries) != 0 {
				t.Fatalf("a capture that never ran left a file: %v", entries)
			}
			if ctl.Running() != 0 {
				t.Fatal("a refused start is still counted as running")
			}
		})
	}
}

// ── A CAPTURE, END TO END ON THE BENCH ──────────────────────────────────────────────────────────────────────────

func TestACaptureIsWrittenDownFirstRecordsMaskedAndHandsThePhoneBack(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	phone.userCAs = []string{"NthCase agent CA — bench-mac"} // installed by the person; never the agent's to touch
	m := newMachine(t, phone)
	server, roots := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=s3cr3t-session-value; Path=/; HttpOnly")
		_, _ = w.Write([]byte(`{"access_token":"tok-9f8e7d6c5b4a","user":"ada"}`))
	})
	ctl := m.controller(func(cfg *Config) { cfg.UpstreamRoots = roots })

	// WRITTEN DOWN FIRST: when the phone is about to be changed, the record of what changes is already on disk.
	changes := 0
	m.bench.beforeChange = func(serial string, args []string) {
		records, _ := m.store.Records()
		if len(records) != 1 || records[0].Serial != serial || records[0].Lease != leaseA || records[0].Kind != KindCapture ||
			!strings.Contains(strings.Join(args, " "), strconv.Itoa(records[0].Port)) {
			t.Errorf("phone changed by %q before it was written down: %+v", args, records)
		}
		changes++
	}
	ctl.Start(context.Background(), leaseA, phone.serial)
	if changes != 2 || ctl.Running() != 1 {
		t.Fatalf("the start made %d changes and runs %d captures:\n%s", changes, ctl.Running(), m.out.String())
	}
	m.bench.beforeChange = nil
	proxy := m.bench.proxyOf(phone.serial)
	if !strings.Contains(m.out.String(), "every app's traffic on that phone goes through this machine ("+proxy+")") {
		t.Fatalf("the console must say what a capture means for the phone: %q", m.out.String())
	}

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/me?api_key=AIzaSyA1234567890abcdefghijklmnopqrstuv&page=2", nil)
	request.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhZGEifQ.c2lnbmF0dXJl")
	response, body := fetch(t, m.appsOn(phone.serial, m.trusts()), request)
	if response.StatusCode != 200 || string(body) != `{"access_token":"tok-9f8e7d6c5b4a","user":"ada"}` {
		t.Fatalf("the phone must get the server's own answer, untouched: %d %s", response.StatusCode, body)
	}
	eventually(t, "the exchange recorded", func() bool { data, _ := ctl.Pull(leaseA); return strings.Contains(string(data), "/me?") })

	ctl.Stop(context.Background(), leaseA)
	if got := m.bench.proxyOf(phone.serial); got != ":0" {
		t.Fatalf("the phone's proxy must be cleared, it reads %q", got)
	}
	if m.bench.reversesOf(phone.serial) != 0 {
		t.Fatal("the capture's reverse must be removed")
	}
	if records, _ := m.store.Records(); len(records) != 0 {
		t.Fatalf("a phone read back clean keeps no record: %+v", records)
	}
	if !strings.Contains(m.out.String(), "stopped: 1 request(s) recorded and masked; the phone was handed back") {
		t.Fatalf("the console must say the phone was handed back: %q", m.out.String())
	}
	m.bench.with(phone.serial, func(p *fakePhone) {
		if len(p.userCAs) != 1 || len(p.downloads) != 0 {
			t.Fatalf("the phone's certificates are the person's: %+v", p)
		}
	})

	data, sentence := ctl.Pull(leaseA)
	if sentence != "" {
		t.Fatalf("pull: %q", sentence)
	}
	secrets := []string{"eyJhbGciOiJIUzI1NiJ9", "AIzaSyA1234567890", "tok-9f8e7d6c5b4a", "s3cr3t-session-value"}
	for _, secret := range secrets {
		if strings.Contains(string(data), secret) {
			t.Fatalf("%q reached the pulled capture unmasked:\n%s", secret, data)
		}
	}
	// NOTHING RAW ON THE DISK: every file the capture's folder holds, read whole.
	written := 0
	must(t, filepath.WalkDir(m.store.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		for _, secret := range secrets {
			if strings.Contains(string(contents), secret) {
				t.Errorf("%s holds %q unmasked", path, secret)
			}
		}
		written++
		return err
	}))
	if written < 4 { // the mark, the CA's two files, the phones, the capture
		t.Fatalf("read %d files — the walk is not reading the capture's folder", written)
	}
	var har struct {
		Log struct {
			Entries []struct {
				Request struct {
					URL string `json:"url"`
				} `json:"request"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &har); err != nil || len(har.Log.Entries) != 1 {
		t.Fatalf("the pull must be a whole HAR with the one exchange: %v\n%s", err, data)
	}
	if u := har.Log.Entries[0].Request.URL; !strings.Contains(u, "page=2") || !strings.Contains(u, netcap.Mask) {
		t.Fatalf("the URL keeps what is not a credential and masks what is: %s", u)
	}

	// A PULL FOR ANOTHER LEASE gets nothing of this one.
	if other, sentence := ctl.Pull(leaseB); other != nil || sentence != pullNothingKnown {
		t.Fatalf("another lease's pull got %d bytes, %q", len(other), sentence)
	}

	// PULLED: the file is deleted, and the lease is told so.
	ctl.Pulled(leaseA)
	if _, err := os.Lstat(filepath.Join(m.store.FilesDir(), leaseA+".har")); !os.IsNotExist(err) {
		t.Fatalf("the pulled capture is still on the disk: %v", err)
	}
	if again, sentence := ctl.Pull(leaseA); again != nil || sentence != pullAlreadyTaken {
		t.Fatalf("a second pull got %d bytes, %q", len(again), sentence)
	}
}

// ── THE FILE'S HOUR, A RESTART INCLUDED ─────────────────────────────────────────────────────────────────────────

// capturedOnce runs one capture with one exchange on it — plain HTTP to a server the test opened — and stops it.
func capturedOnce(t *testing.T, m *machine, ctl *Controller, serial string) {
	t.Helper()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(plain.Close)
	ctl.Start(context.Background(), leaseA, serial)
	if ctl.Running() != 1 {
		t.Fatalf("the capture did not start:\n%s", m.out.String())
	}
	request, _ := http.NewRequest(http.MethodGet, plain.URL+"/plain", nil)
	fetch(t, m.appsOn(serial, m.trusts()), request)
	eventually(t, "the exchange recorded", func() bool { data, _ := ctl.Pull(leaseA); return strings.Contains(string(data), "/plain") })
	ctl.Stop(context.Background(), leaseA)
}

func TestAnUnpulledCaptureIsDeletedAnHourAfterItStops(t *testing.T) {
	phone := pixel("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil)
	capturedOnce(t, m, ctl, phone.serial)
	file := filepath.Join(m.store.FilesDir(), leaseA+".har")

	m.clock.Advance(59 * time.Minute)
	ctl.pass(context.Background())
	if _, err := os.Lstat(file); err != nil {
		t.Fatalf("deleted before its hour: %v", err)
	}
	m.clock.Advance(time.Minute)
	ctl.pass(context.Background())
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("still on the disk an hour after the capture stopped: %v", err)
	}
	if data, sentence := ctl.Pull(leaseA); data != nil || sentence != pullExpired {
		t.Fatalf("got %d bytes, %q", len(data), sentence)
	}
}

func TestARestartedAgentServesTheFileOnceAndStillKeepsTheHour(t *testing.T) {
	t.Run("pulled after the restart", func(t *testing.T) {
		phone := pixel("R5CT30ABCDE")
		m := newMachine(t, phone)
		capturedOnce(t, m, m.controller(nil), phone.serial)

		restarted := m.controller(nil) // a new process: it knows nothing of the capture but its file
		data, sentence := restarted.Pull(leaseA)
		if sentence != "" || !strings.Contains(string(data), "/plain") {
			t.Fatalf("the restarted agent must serve the capture it finds: %q %s", sentence, data)
		}
		restarted.Pulled(leaseA)
		if _, err := os.Lstat(filepath.Join(m.store.FilesDir(), leaseA+".har")); !os.IsNotExist(err) {
			t.Fatalf("pulled, and still on the disk: %v", err)
		}
	})
	t.Run("never pulled", func(t *testing.T) {
		phone := pixel("R5CT30ABCDE")
		m := newMachine(t, phone)
		capturedOnce(t, m, m.controller(nil), phone.serial)

		restarted := m.controller(nil)
		restarted.pass(context.Background()) // the sweep at start: the file is minutes old, so it stays
		if _, err := os.Lstat(filepath.Join(m.store.FilesDir(), leaseA+".har")); err != nil {
			t.Fatalf("the sweep at start deleted a file inside its hour: %v", err)
		}
		m.clock.Advance(61 * time.Minute) // the hour, counted from the file's last write
		restarted.pass(context.Background())
		if _, err := os.Lstat(filepath.Join(m.store.FilesDir(), leaseA+".har")); !os.IsNotExist(err) {
			t.Fatalf("the restarted agent kept a file past its hour: %v", err)
		}
	})
}

// ── EVERY HANDBACK PATH ─────────────────────────────────────────────────────────────────────────────────────────

// leftBehind is a record a previous process wrote and a phone still pointed at it — a crash in the middle of a capture.
func leftBehind(t *testing.T, m *machine, serial, lease string) Record {
	t.Helper()
	port := int(netcap.PortCandidates(serial)[0])
	r := Record{Serial: serial, Lease: lease, Kind: KindCapture, Port: port, Proxy: ProxyValue(port), Reverse: true, Written: m.clock.Now()}
	must(t, m.store.writeRecord(r))
	m.bench.with(serial, func(p *fakePhone) {
		p.proxy = r.Proxy
		p.reverses["tcp:"+strconv.Itoa(port)] = true
	})
	return r
}

func TestWhatAPreviousRunLeftIsHandedBackAtStart(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	leftBehind(t, m, phone.serial, leaseA)

	ctl := m.controller(func(cfg *Config) { cfg.MarkEvery, cfg.PassEvery = 20*time.Millisecond, 50*time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ctl.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the phone handed back at start", func() bool { return m.bench.proxyOf(phone.serial) == ":0" })
	eventually(t, "the record removed", func() bool { r, _ := m.store.Records(); return len(r) == 0 })
	if m.bench.reversesOf(phone.serial) != 0 {
		t.Fatal("the reverse a previous run left must go too")
	}
	if !strings.Contains(m.out.String(), "phone R5CT30ABCDE was handed back: the proxy setting a capture left on it is cleared") {
		t.Fatalf("the console must say so: %q", m.out.String())
	}
}

func TestAnUnreachablePhoneKeepsItsRecordIsSaidOnceAndIsHandedBackOnTheTimer(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	r := leftBehind(t, m, phone.serial, leaseA)
	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = true })
	ctl := m.controller(nil)

	for range 3 {
		ctl.pass(context.Background())
	}
	records, _ := m.store.Records()
	if len(records) != 1 || records[0].LastTried == nil || !strings.HasPrefix(records[0].LastObservation, "the phone was not reachable over adb") {
		t.Fatalf("an unreachable phone keeps its record, with what was seen: %+v", records)
	}
	message := strings.TrimRight(Unreachable(phone.serial, r.Proxy), "\n")
	if n := strings.Count(m.out.String(), "still has its network proxy pointed at this machine's capture"); n != 1 || !strings.Contains(m.out.String(), message) {
		t.Fatalf("the unreachable message is said once, whole (said %d times):\n%s", n, m.out.String())
	}
	// doctor says it too, from the record alone.
	status := ReadStatus(m.store, m.clock.Now())
	if len(status.Owed) != 1 || !strings.Contains(strings.Join(status.Lines, "\n"), "adb -s R5CT30ABCDE shell settings put global http_proxy :0") {
		t.Fatalf("doctor must name the phone and the way to clear it: %+v", status.Lines)
	}

	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = false }) // plugged back in
	ctl.pass(context.Background())
	if records, _ := m.store.Records(); len(records) != 0 || m.bench.proxyOf(phone.serial) != ":0" {
		t.Fatalf("the timer must hand it back once it can: %+v %q", records, m.bench.proxyOf(phone.serial))
	}
	if len(ReadStatus(m.store, m.clock.Now()).Owed) != 0 {
		t.Fatal("doctor must stop saying it once it is done")
	}
}

func TestTheFarmsQuestionIsAnsweredFromThePhone(t *testing.T) {
	t.Run("while it captures: stopped, handed back, clean — the CA the person installed is theirs", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		phone.userCAs = []string{"NthCase agent CA — bench-mac"}
		m := newMachine(t, phone)
		ctl := m.controller(nil)
		ctl.Start(context.Background(), leaseA, phone.serial)
		clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
		if !clean || observation != "" || ctl.Running() != 0 || m.bench.proxyOf(phone.serial) != ":0" {
			t.Fatalf("got %v %q, running %d, proxy %q", clean, observation, ctl.Running(), m.bench.proxyOf(phone.serial))
		}
	})
	t.Run("a phone out of reach: not clean, and why", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		m := newMachine(t, phone)
		ctl := m.controller(nil)
		ctl.Start(context.Background(), leaseA, phone.serial)
		m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = true })
		clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
		if clean || !strings.HasPrefix(observation, "the phone was not reachable over adb") {
			t.Fatalf("got %v %q", clean, observation)
		}
		if records, _ := m.store.Records(); len(records) != 1 {
			t.Fatalf("its record is kept for the next handback: %+v", records)
		}
		m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = false })
		if clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA); !clean {
			t.Fatalf("asked again once it is back: %v %q", clean, observation)
		}
	})
	t.Run("a proxy that does not clear: not clean, and what it reads", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		m := newMachine(t, phone)
		ctl := m.controller(nil)
		ctl.Start(context.Background(), leaseA, phone.serial)
		m.bench.with(phone.serial, func(p *fakePhone) { p.stuckProxy = true })
		clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
		if clean || !strings.Contains(observation, "still points at this machine's capture") {
			t.Fatalf("got %v %q", clean, observation)
		}
	})
	t.Run("another lease's capture on the phone is left running", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		m := newMachine(t, phone)
		ctl := m.controller(nil)
		ctl.Start(context.Background(), leaseA, phone.serial)
		t.Cleanup(func() { ctl.StopAll(context.Background()) })
		clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseB)
		if clean || !strings.HasPrefix(observation, "another capture is running on this phone") || ctl.Running() != 1 {
			t.Fatalf("got %v %q, running %d", clean, observation, ctl.Running())
		}
	})
	t.Run("a phone a previous run left proxied is handed back by the question", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		m := newMachine(t, phone)
		leftBehind(t, m, phone.serial, leaseA)
		ctl := m.controller(nil)
		if clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA); !clean {
			t.Fatalf("got %v %q", clean, observation)
		}
		if m.bench.proxyOf(phone.serial) != ":0" {
			t.Fatal("the phone must be cleared")
		}
	})
}

func TestCaptureTurnedOffStopsEveryCaptureAndHandsItsPhoneBack(t *testing.T) {
	a, b := samsung("R5CT30AAAA1"), pixel("R5CT30AAAA2")
	m := newMachine(t, a, b)
	ctl := m.controller(func(cfg *Config) { cfg.MarkEvery, cfg.PassEvery = 20*time.Millisecond, time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ctl.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	ctl.Start(context.Background(), leaseA, a.serial)
	ctl.Start(context.Background(), leaseB, b.serial)
	if ctl.Running() != 2 {
		t.Fatalf("two captures must run:\n%s", m.out.String())
	}

	must(t, m.store.removeMark()) // `nth-agent capture disable`, typed in another terminal
	eventually(t, "every capture stopped", func() bool { return ctl.Running() == 0 })
	for _, s := range []string{a.serial, b.serial} {
		if got := m.bench.proxyOf(s); got != ":0" {
			t.Fatalf("phone %s reads %q", s, got)
		}
	}
	if !strings.Contains(m.out.String(), "network capture was turned off on this machine — every capture is stopped and its phone handed back") {
		t.Fatalf("the console must say why: %q", m.out.String())
	}
}

func TestACaptureNobodyStopsIsStoppedAtItsBound(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil)
	ctl.Start(context.Background(), leaseA, phone.serial)
	m.clock.Advance(MaxCapture - time.Minute)
	ctl.stopOverdue(context.Background())
	if ctl.Running() != 1 {
		t.Fatal("stopped before its bound")
	}
	m.clock.Advance(time.Minute)
	ctl.stopOverdue(context.Background())
	if ctl.Running() != 0 || m.bench.proxyOf(phone.serial) != ":0" {
		t.Fatalf("a capture past its bound must be stopped and its phone handed back: running %d, proxy %q", ctl.Running(), m.bench.proxyOf(phone.serial))
	}
	if !strings.Contains(m.out.String(), "ran for 4h0m0s without being stopped, so this machine stopped it and handed the phone back") {
		t.Fatalf("the console must say why: %q", m.out.String())
	}
}

func TestStopAllHandsEveryPhoneBack(t *testing.T) {
	a, b := samsung("R5CT30AAAA1"), pixel("R5CT30AAAA2")
	m := newMachine(t, a, b)
	ctl := m.controller(nil)
	ctl.Start(context.Background(), leaseA, a.serial)
	ctl.Start(context.Background(), leaseB, b.serial)
	ctl.StopAll(context.Background())
	if ctl.Running() != 0 || m.bench.proxyOf(a.serial) != ":0" || m.bench.proxyOf(b.serial) != ":0" {
		t.Fatalf("running %d, proxies %q %q", ctl.Running(), m.bench.proxyOf(a.serial), m.bench.proxyOf(b.serial))
	}
	if records, _ := m.store.Records(); len(records) != 0 {
		t.Fatalf("records left: %+v", records)
	}
}

// ── THE DECLARATION ─────────────────────────────────────────────────────────────────────────────────────────────

func TestTheDeclarationIsTheMachinesAndNeverReadsTheKey(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T, m *machine, cfg *Config)
		want    string
	}{
		{"off", func(t *testing.T, m *machine, _ *Config) { must(t, m.store.removeMark()) }, farm.CaptureDisabled},
		{"on", nil, farm.CaptureAvailable},
		{"on, and no adb", func(_ *testing.T, _ *machine, cfg *Config) {
			cfg.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
		}, farm.CaptureUnavailable},
		{"on, and a key others can read", func(t *testing.T, m *machine, _ *Config) {
			must(t, os.Chmod(netcap.KeyPathIn(m.store.CADir()), 0o640))
		}, farm.CaptureUnavailable},
		{"on, and a CA past its year", func(_ *testing.T, m *machine, _ *Config) { m.clock.Advance(367 * 24 * time.Hour) }, farm.CaptureUnavailable},
		{"on, on a system capture does not run on", func(_ *testing.T, _ *machine, cfg *Config) { cfg.GOOS = "plan9" }, farm.CaptureUnavailable},
		// THE KEY IS NOT READ: a key this user cannot read — still owner-only — leaves the answer Available.
		{"on, and a key nobody can read", func(t *testing.T, m *machine, _ *Config) {
			must(t, os.Chmod(netcap.KeyPathIn(m.store.CADir()), 0o200))
		}, farm.CaptureAvailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newMachine(t)
			ctl := m.controller(func(cfg *Config) {
				if c.arrange != nil {
					c.arrange(t, m, cfg)
				}
			})
			if got := ctl.Availability(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
