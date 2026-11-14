package capture

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── AN_AGENT_CAPTURE_READY_TO_SHIP: what the operator's acceptance run found on this side, fixed ───────────────────
//
//   rule 1  a capture made before the phone was lost is kept through the farm's handback, for its own pull;
//   rule 3  the phone handed back is said once — on start, on a pass, and on the farm's question;
//   rule 4  the trust check leaves the phone's browser on a page that says it is over, and says what may still be there.

// ── rule 1: the capture survives the farm's handback, for its pull ──────────────────────────────────────────────────

// The acceptance run's case (run 0aa12db8): the cable comes out during a capture, the farm ends the lease and asks this machine
// for the phone back. The phone cannot be reached, so it is kept as owed — and the masked capture is kept too: the farm still
// serves that lease's own pull for a short window after it ended, and this machine keeps the file for an hour after the
// capture stops, so nothing here deletes it between the two.
func TestTheAgentKeepsACaptureThroughTheFarmsHandbackAfterThePhoneWasLost(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	server, roots := upstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"ok":true}`) })
	ctl := m.controller(func(cfg *Config) { cfg.UpstreamRoots = roots })
	t.Cleanup(func() { ctl.StopAll(context.Background()) })
	ctl.Start(context.Background(), leaseA, phone.serial)
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/before-the-cable-came-out", nil)
	fetch(t, m.appsOn(phone.serial, m.trusts()), request)
	eventually(t, "the exchange recorded", func() bool {
		data, _ := ctl.Pull(leaseA)
		return strings.Contains(string(data), "/before-the-cable-came-out")
	})

	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = true }) // the cable is pulled
	clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
	if clean || !strings.Contains(observation, "not reachable") {
		t.Fatalf("an unreachable phone is not clean: %v %q", clean, observation)
	}

	data, refusal := ctl.Pull(leaseA)
	if data == nil || !strings.Contains(string(data), "/before-the-cable-came-out") {
		t.Fatalf("the capture made before the phone was lost must be pullable after the farm's handback: %q\n%s", refusal, data)
	}

	// Past the farm's window for that lease (ten minutes), well inside this machine's hour: still here, and still pullable.
	m.clock.Advance(11 * time.Minute)
	ctl.pass(context.Background())
	if data, refusal = ctl.Pull(leaseA); data == nil {
		t.Fatalf("a pass inside the hour must not delete a capture nobody pulled: %q", refusal)
	}
	ctl.Pulled(leaseA)
	if _, refusal = ctl.Pull(leaseA); refusal != pullAlreadyTaken {
		t.Fatalf("once pulled it is gone: %q", refusal)
	}
}

// ── rule 3: the handed-back line, once, on every path that hands a phone back ───────────────────────────────────

const handedBackLine = "was handed back: the proxy setting a capture left on it is cleared"

func handedBackSaid(out, serial string) int {
	return strings.Count(out, "phone "+serial+" "+handedBackLine)
}

// keptAsOwed starts a capture, pulls the cable, and has the capture stopped: the phone is kept as owed, its record on disk.
func keptAsOwed(t *testing.T, m *machine, ctl *Controller, serial string) {
	t.Helper()
	ctl.Start(context.Background(), leaseA, serial)
	if proxy := m.bench.proxyOf(serial); !strings.HasPrefix(proxy, "127.0.0.1:") {
		t.Fatalf("the capture did not start: %q", proxy)
	}
	m.bench.with(serial, func(p *fakePhone) { p.unreachable = true })
	ctl.Stop(context.Background(), leaseA)
	if records, _ := m.store.Records(); len(records) != 1 {
		t.Fatalf("an unreachable phone keeps its record: %+v", records)
	}
	if !strings.Contains(m.out.String(), "could not be handed back yet") {
		t.Fatalf("the stop must say the phone was not handed back:\n%s", m.out.String())
	}
}

// THE PATH THAT MISSED IT. The farm asks for an owed phone every half minute, and this machine's own pass tries every half
// minute; once the phone is plugged back in, whichever comes first clears it. On the acceptance run the farm's question came
// first: the record went, `doctor` stopped listing the phone, and the line never came — only the pass said it.
func TestThePhoneHandedBackOnTheFarmsQuestionIsSaidOnce(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil)
	keptAsOwed(t, m, ctl, phone.serial)
	if clean, _ := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA); clean {
		t.Fatal("still unplugged: not clean")
	}

	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = false }) // plugged back in
	clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
	if !clean {
		t.Fatalf("the phone is back and its proxy cleared: %q", observation)
	}
	if got := handedBackSaid(m.out.String(), phone.serial); got != 1 {
		t.Fatalf("the farm's question handed the phone back and must say so once, said %d times:\n%s", got, m.out.String())
	}

	ctl.pass(context.Background())                                  // the pass finds nothing left to hand back…
	ctl.HandBackForFarm(context.Background(), phone.serial, leaseA) // …and the farm, asking again, nothing either
	if got := handedBackSaid(m.out.String(), phone.serial); got != 1 {
		t.Fatalf("said once, not again: %d\n%s", got, m.out.String())
	}
}

func TestThePhoneHandedBackOnAPassIsSaidOnce(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(nil)
	keptAsOwed(t, m, ctl, phone.serial)
	ctl.pass(context.Background()) // still unplugged: nothing said
	if got := handedBackSaid(m.out.String(), phone.serial); got != 0 {
		t.Fatalf("an unplugged phone is not handed back: %d", got)
	}

	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = false })
	ctl.pass(context.Background())
	ctl.pass(context.Background())
	ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
	if got := handedBackSaid(m.out.String(), phone.serial); got != 1 {
		t.Fatalf("handed back on a pass, said once: %d\n%s", got, m.out.String())
	}
	if proxy := m.bench.proxyOf(phone.serial); proxy != ":0" {
		t.Fatalf("the proxy is cleared: %q", proxy)
	}
}

// On start: `connect` hands back what a run before it left — here a capture's record and a trust check's, both for one phone,
// from a process that died. The phone is said once.
func TestThePhoneHandedBackOnStartIsSaidOnceForAllItsRecords(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	for _, r := range []Record{
		{Serial: phone.serial, Lease: leaseA, Kind: KindCapture, Port: 47001, Proxy: ProxyValue(47001), Reverse: true, Written: m.clock.Now()},
		{Serial: phone.serial, Lease: leaseB, Kind: KindTrustCheck, Port: 47002, Proxy: ProxyValue(47002), Reverse: true, Written: m.clock.Now()},
	} {
		must(t, m.store.writeRecord(r))
	}
	m.bench.with(phone.serial, func(p *fakePhone) {
		p.proxy = ProxyValue(47002)
		p.reverses["tcp:47001"], p.reverses["tcp:47002"] = true, true
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.controller(nil).Run(ctx) // start-up's pass, and nothing after it

	if got := handedBackSaid(m.out.String(), phone.serial); got != 1 {
		t.Fatalf("two records for one phone, handed back at start: said %d times:\n%s", got, m.out.String())
	}
	leftAsFound(t, m, phone.serial)
}

// A phone kept by one of its records is not said to be handed back, though another of its records went.
func TestAPhoneStillKeptByOneOfItsRecordsIsNotSaidToBeHandedBack(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	must(t, m.store.writeRecord(Record{Serial: phone.serial, Lease: leaseA, Kind: KindCapture, Port: 47001, Proxy: ProxyValue(47001), Reverse: true, Written: m.clock.Now()}))
	m.bench.with(phone.serial, func(p *fakePhone) { p.proxy = ProxyValue(47001); p.stuckProxy = true }) // clearing does not take

	m.controller(nil).pass(context.Background())

	if got := handedBackSaid(m.out.String(), phone.serial); got != 0 {
		t.Fatalf("a phone whose proxy is still ours was said to be handed back:\n%s", m.out.String())
	}
}

// ── rule 4: the trust check leaves nothing alarming behind ──────────────────────────────────────────────────────

// browserLoading is a phone's browser that records what it was shown: it loads each address it is asked to open through the
// phone's proxy, as Chrome does, trusting what the person installed (an empty pool: nothing).
type browserLoading struct {
	mu     sync.Mutex
	shown  map[string]string // address → the page it got, or "" when it got none
	opened sync.WaitGroup
}

func (b *browserLoading) open(bench *bench, serial string, trusts *x509.CertPool) func(address string) {
	return func(address string) {
		defer b.opened.Done()
		page := ""
		if response, err := clientThrough(bench.proxyOf(serial), trusts).Get(address); err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			page = string(body)
		}
		b.mu.Lock()
		b.shown[address] = page
		b.mu.Unlock()
	}
}

// clientThrough is a client that goes through the proxy at address (host:port), trusting trusts (nil: the system's roots).
func clientThrough(address string, trusts *x509.CertPool) *http.Client {
	proxyURL, _ := url.Parse("http://" + address)
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: trusts}, DisableKeepAlives: true,
	}}
}

func TestTheCheckLeavesThePhonesBrowserOnItsLastPageOnEveryOutcome(t *testing.T) {
	for _, c := range []struct {
		name      string
		trusts    bool
		noBrowser bool
		code      int
		said      string
	}{
		{name: "trusted", trusts: true, code: 0, said: browserAfterTrusted},
		{name: "refused", trusts: false, code: 1, said: browserAfterRefused},
		{name: "no answer", noBrowser: true, code: 1, said: browserAfterNoAnswer},
	} {
		t.Run(c.name, func(t *testing.T) {
			phone := pixel("R5CT30ABCDE")
			m := newMachine(t)
			m.attach(phone)
			browser := &browserLoading{shown: map[string]string{}}
			if !c.noBrowser {
				pool := x509.NewCertPool()
				if c.trusts {
					pool = m.trusts()
				}
				browser.opened.Add(2)
				m.bench.with(phone.serial, func(p *fakePhone) { p.browser = browser.open(m.bench, phone.serial, pool) })
			}
			wait := 5 * time.Second
			if c.noBrowser {
				wait = 300 * time.Millisecond
			}

			run := runSetup(t, m, phone.serial, wait)
			if !c.noBrowser {
				waitOpened(t, &browser.opened)
			}

			if run.code != c.code {
				t.Fatalf("exit %d:\n%s", run.code, run.out)
			}
			// The check's page, then its last page — in that order, and both before the phone's proxy was put back.
			var opened []string
			m.bench.with(phone.serial, func(p *fakePhone) { opened = append(opened, p.opened...) })
			if len(opened) != 2 || opened[0] != "https://"+netcap.TrustCheckHost+"/" || opened[1] != netcap.TrustCheckOverAddress {
				t.Fatalf("the browser must be asked to open the check, then its last page: %q", opened)
			}
			commands := m.bench.commandsTo(phone.serial)
			lastPage, putBack := indexOf(commands, netcap.TrustCheckOverAddress), indexOf(commands, "http_proxy :0")
			if lastPage < 0 || putBack < 0 || lastPage > putBack {
				t.Fatalf("the last page must be opened while the phone still points at the check, before it is put back: %q", commands)
			}
			if !c.noBrowser {
				browser.mu.Lock()
				page := browser.shown[netcap.TrustCheckOverAddress]
				browser.mu.Unlock()
				if !strings.Contains(page, "The capture check is over.") || strings.Contains(page, "trusts the capture certificate") {
					t.Fatalf("the browser must be shown the check's last page, which says nothing about trust: %q", page)
				}
			}
			if !strings.Contains(run.out, c.said) {
				t.Fatalf("the terminal must say what the phone may still show, and that it is the check:\n%s", run.out)
			}
			leftAsFound(t, m, phone.serial)
		})
	}
}

// The refused case's sentence names the warning a person sees, the address it is for, that it is the check, and that the tab
// can be closed — the words a person needs not to read it as an attack.
func TestTheRefusedChecksSentenceSaysTheWarningIsTheCheck(t *testing.T) {
	for _, want := range []string{"\"Your connection is not private\"", netcap.TrustCheckHost, "this check itself", "not an attack", "close that tab"} {
		if !strings.Contains(browserAfterRefused, want) {
			t.Errorf("the refused check's sentence must say %s: %q", want, browserAfterRefused)
		}
	}
	for _, text := range []string{TrustTrusted("R5CT30ABCDE", true), TrustRefused("R5CT30ABCDE", true), TrustNoAnswer("R5CT30ABCDE", true), TrustNoAnswer("R5CT30ABCDE", false)} {
		if !strings.Contains(text, "On the phone, the browser may") {
			t.Errorf("every outcome says what the phone's browser may still show:\n%s", text)
		}
	}
}

// The last page is the check's own: answered by the check's proxy, in plain HTTP, and never sent anywhere — while every other
// address the phone asks for in plain HTTP during the check still passes through.
func TestTheChecksLastPageIsAnsweredByItsOwnProxyAndNeverSent(t *testing.T) {
	m := newMachine(t)
	ca, err := netcap.LoadCA(m.store.CADir(), m.clock.Now())
	must(t, err)
	check := netcap.NewTrustCheck()
	proxy := netcap.NewProxy(netcap.Options{CA: ca, TrustCheck: check, Now: m.clock.Now})
	address, err := proxy.Listen(0)
	must(t, err)
	go proxy.Serve()
	t.Cleanup(func() { _ = proxy.Close() })

	response, err := clientThrough(address, nil).Get(netcap.TrustCheckOverAddress)
	if err != nil {
		t.Fatalf("the last page through the check's proxy: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "The capture check is over.") {
		t.Fatalf("got %d %q", response.StatusCode, body)
	}
	shown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !check.WaitOver(shown) {
		t.Fatal("the check must know its last page was shown")
	}
}

// ── rule 4's seal: nothing here stops or clears the customer's browser ─────────────────────────────────────────

// NEVER FORCE-STOP OR CLEAR THE CUSTOMER'S BROWSER. The check puts the browser on its last page with the same command that
// opened the check; nothing in this agent stops an app on the phone, clears its data, or kills it. Held from the source, the
// whole tree, so a new file is read the day it is added.
func TestNothingInTheTreeStopsOrClearsAnAppOnThePhone(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)force-stop|forcestop|"pm"\s*,\s*"clear"|\bpm\s+clear\b|"am"\s*,\s*"kill|\bam\s+kill\b|"kill(-all)?"|killall|clear-data`)
	// INSTRUMENT GUARD: the pattern sees each spelling it claims to forbid.
	for _, planted := range []string{`"shell", "am", "force-stop", "com.android.chrome"`, `"shell", "pm", "clear", "com.android.chrome"`,
		`adb shell pm clear com.sec.android.app.sbrowser`, `"am", "kill", "com.android.chrome"`, `am kill-all`} {
		if !forbidden.MatchString(planted) {
			t.Fatalf("the seal does not see %q", planted)
		}
	}
	scanned := 0
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if loc := forbidden.FindIndex(source); loc != nil {
			t.Errorf("%s says %q — this agent never stops, clears or kills an app on the customer's phone", path, source[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("scanned %d files — the walk is reading the wrong tree", scanned)
	}
}

func indexOf(commands []string, part string) int {
	for i, c := range commands {
		if strings.Contains(c, part) {
			return i
		}
	}
	return -1
}
