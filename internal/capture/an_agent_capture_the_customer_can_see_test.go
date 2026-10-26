package capture

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE: what the farm can know before a run, and who may use a capture's proxy ─────

// Rule 1: ONE PHONE'S READINESS IS THE GATES A START READS — said per phone, in the farm's own words, so the farm can
// withhold the pull URL from a phone nobody set up and say why before the run starts.
func TestAPhonesReadinessIsWhatAStartWouldDecide(t *testing.T) {
	const serial = "R5CT30ABCDE"
	for _, c := range []struct {
		name    string
		serial  string
		arrange func(t *testing.T, m *machine, cfg *Config)
		want    string
	}{
		{name: "set up here, for this CA", serial: serial, want: farm.PhoneCaptureReady},
		{name: "never set up here", serial: "R5CT30NOTSET", want: farm.PhoneCaptureNotSetUp},
		{name: "set up for an earlier CA", serial: serial, want: farm.PhoneCaptureNotSetUp,
			arrange: func(t *testing.T, m *machine, _ *Config) {
				must(t, m.store.setPhone(Phone{Serial: serial, CAFingerprint: strings.Repeat("0", 64), SetUpAtUtc: m.clock.Now()}))
			}},
		// The machine's own word already says why nothing is captured — the phone's would only repeat it, less usefully.
		{name: "capture off on this machine", serial: serial, want: "",
			arrange: func(t *testing.T, m *machine, _ *Config) { must(t, m.store.removeMark()) }},
		{name: "a CA past its year", serial: serial, want: "",
			arrange: func(_ *testing.T, m *machine, _ *Config) { m.clock.Advance(367 * 24 * time.Hour) }},
		{name: "no adb on this machine", serial: serial, want: "",
			arrange: func(_ *testing.T, _ *machine, cfg *Config) {
				cfg.LookPath = func(string) (string, error) { return "", errors.New("not found") }
			}},
		{name: "an iPhone", serial: "00008030-001A2B3C4D5E6F70", want: ""},
		{name: "a serial adb would never print", serial: "R5CT 30", want: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newMachine(t, samsung(serial))
			ctl := m.controller(func(cfg *Config) {
				if c.arrange != nil {
					c.arrange(t, m, cfg)
				}
			})
			if got := ctl.Readiness(c.serial); got != c.want {
				t.Fatalf("readiness %q, want %q", got, c.want)
			}
			if touched := m.bench.commands(); len(touched) != 0 {
				t.Fatalf("saying a phone's readiness touched a phone: %q", touched)
			}
			// …and it is the same answer a start gives: Ready starts, NotSetUp is refused for the same reason.
			if c.want == farm.PhoneCaptureNotSetUp {
				ctl.Start(context.Background(), leaseA, c.serial)
				if _, sentence := ctl.Pull(leaseA); sentence != refusedNotSetUp(c.serial) && sentence != refusedOldCA(c.serial) {
					t.Fatalf("a phone said to be not set up must be refused as not set up, got %q", sentence)
				}
			}
		})
	}
}

// Rule 2: ANOTHER ACCOUNT'S CONNECTION TO A RUNNING CAPTURE'S PROXY is refused unread, said on the console once, and never
// reaches the record; the phone's own traffic (this account's adb) still goes through.
func TestAnotherAccountsConnectionToACaptureIsRefusedAndSaidOnce(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	other := os.Geteuid() + 1
	var fromAnotherAccount atomic.Bool
	ctl := m.controller(func(cfg *Config) {
		cfg.PeerOwner = func(c net.Conn) (int, bool) {
			if fromAnotherAccount.Load() {
				return other, true
			}
			return netcap.OwnerOfPeer(c)
		}
	})
	server, roots := upstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	ctl.cfg.UpstreamRoots = roots // before the start: the proxy reads it when it is made
	ctl.Start(context.Background(), leaseA, phone.serial)
	t.Cleanup(func() { ctl.StopAll(context.Background()) })
	if ctl.Running() != 1 {
		t.Fatalf("the capture must start:\n%s", m.out.String())
	}

	// The phone's own app, through this account's adb: served and recorded.
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/mine", nil)
	if response, _ := fetch(t, m.appsOn(phone.serial, m.trusts()), request); response.StatusCode != http.StatusOK {
		t.Fatalf("the phone's own traffic must go through, got %d", response.StatusCode)
	}
	eventually(t, "the phone's own exchange recorded", func() bool { data, _ := ctl.Pull(leaseA); return strings.Contains(string(data), "/mine") })

	// Another account on this machine, twice.
	fromAnotherAccount.Store(true)
	for i := 0; i < 2; i++ {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/theirs", nil)
		if response, err := m.appsOn(phone.serial, m.trusts()).Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("another account's connection was served (status %d)", response.StatusCode)
		}
	}
	want := "network capture on phone R5CT30ABCDE: a connection to its proxy from another account on this machine (uid " +
		strconv.Itoa(other) + ") was refused — only this account and the administrator may use it"
	if strings.Count(m.out.String(), want) != 1 {
		t.Fatalf("the console must say it once:\n%s", m.out.String())
	}

	fromAnotherAccount.Store(false)
	ctl.Stop(context.Background(), leaseA)
	data, sentence := ctl.Pull(leaseA)
	if data == nil {
		t.Fatalf("the capture must be pullable: %s", sentence)
	}
	if !strings.Contains(string(data), "/mine") || strings.Contains(string(data), "/theirs") {
		t.Fatalf("only the phone's own traffic may be in the record:\n%s", data)
	}
}

// Rule 2, for `capture setup`: on a machine that cannot tell which account connects, the check would refuse the phone's
// browser too — so setup says so before a phone is touched at all.
func TestSetupOnAMachineThatCannotTellWhoConnectsTouchesNoPhone(t *testing.T) {
	m := newMachine(t)
	m.attach(samsung("R5CT30ABCDE"))
	var out strings.Builder
	code := Setup(context.Background(), SetupOptions{Store: m.store, Adb: m.bench.adb, In: strings.NewReader("\n"), Out: &out,
		Now: m.clock.Now, Serial: "R5CT30ABCDE", CheckWait: time.Second, PortFree: benchPortFree,
		PeerCheck: func() error { return netcap.ErrPeerCheck }})
	if code != 1 || len(m.bench.commands()) != 0 {
		t.Fatalf("exit %d, commands %q:\n%s", code, m.bench.commands(), out.String())
	}
	if !strings.Contains(out.String(), "This machine could not check which of its accounts connects to the capture proxy, so network capture cannot run here. No phone was touched.") {
		t.Fatalf("setup must say why:\n%s", out.String())
	}
}
