package capture

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── `capture setup`: the certificate copied, the steps said, the check made, and the phone always put back ──────

// browserOf is the phone's browser: asked to open an address, it goes through the phone's proxy setting, as Chrome does,
// trusting what the person installed (nil: nothing).
func browserOf(b *bench, serial string, trusts *x509.CertPool, opened *sync.WaitGroup) func(address string) {
	return func(address string) {
		defer opened.Done()
		proxy := b.proxyOf(serial)
		proxyURL, _ := url.Parse("http://" + proxy)
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: trusts}, DisableKeepAlives: true,
		}}
		if response, err := client.Get(address); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}
}

// waitOpened waits for the browser's opens, and fails the test — never hangs it — when one does not come: a check that stops
// opening its last page must turn red, not hold the package until go test's own timeout.
func waitOpened(t *testing.T, opened *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		opened.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("waited 10 s for the phone's browser to be asked to open the check and then its last page")
	}
}

type setupRun struct {
	code int
	out  string
}

func runSetup(t *testing.T, m *machine, serial string, wait time.Duration) setupRun {
	t.Helper()
	var out bytes.Buffer
	code := Setup(context.Background(), SetupOptions{Store: m.store, Adb: m.bench.adb, In: strings.NewReader("\n"), Out: &out,
		Now: m.clock.Now, Serial: serial, CheckWait: wait, LastPageWait: wait, PortFree: benchPortFree})
	return setupRun{code: code, out: out.String()}
}

// leftAsFound checks the phone was left as it was found: no proxy of ours, no reverse, no record.
func leftAsFound(t *testing.T, m *machine, serial string) {
	t.Helper()
	if proxy := m.bench.proxyOf(serial); proxy != ":0" {
		t.Fatalf("the phone's proxy must be put back, it reads %q", proxy)
	}
	if m.bench.reversesOf(serial) != 0 {
		t.Fatal("the check's reverse must be removed")
	}
	if records, _ := m.store.Records(); len(records) != 0 {
		t.Fatalf("no record may outlive a phone put back: %+v", records)
	}
}

func TestSetupPassesOnlyWhenThePhonesBrowserTrustsTheCertificate(t *testing.T) {
	t.Run("trusted", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		m := newMachine(t)
		m.attach(phone)
		var opened sync.WaitGroup
		opened.Add(2) // the check, then its last page (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 4)
		m.bench.with(phone.serial, func(p *fakePhone) { p.browser = browserOf(m.bench, phone.serial, m.trusts(), &opened) })

		run := runSetup(t, m, "", 5*time.Second)
		waitOpened(t, &opened)
		if run.code != 0 || !strings.Contains(run.out, strings.TrimRight(TrustTrusted(phone.serial, true), "\n")) {
			t.Fatalf("exit %d:\n%s", run.code, run.out)
		}
		set, ok := m.store.Phone(phone.serial)
		if !ok || set.CAFingerprint != m.ca.Fingerprint() || set.Model != "SM-A546B" {
			t.Fatalf("a passed check records the phone, for this CA: %+v", set)
		}
		leftAsFound(t, m, phone.serial)
		if !strings.Contains(run.out, strings.TrimRight(SetupSteps(phone.serial, "SM-A546B", VendorSamsung), "\n")) {
			t.Fatalf("a Samsung gets Samsung's steps:\n%s", run.out)
		}
	})
	t.Run("refused", func(t *testing.T) {
		phone := pixel("R5CT30ABCDE")
		m := newMachine(t)
		m.attach(phone)
		var opened sync.WaitGroup
		opened.Add(2) // the check, then its last page (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 4)
		m.bench.with(phone.serial, func(p *fakePhone) { p.browser = browserOf(m.bench, phone.serial, x509.NewCertPool(), &opened) })

		run := runSetup(t, m, phone.serial, 5*time.Second)
		waitOpened(t, &opened)
		if run.code != 1 || !strings.Contains(run.out, strings.TrimRight(TrustRefused(phone.serial, true), "\n")) {
			t.Fatalf("exit %d:\n%s", run.code, run.out)
		}
		if _, ok := m.store.Phone(phone.serial); ok {
			t.Fatal("a failed check records nothing")
		}
		leftAsFound(t, m, phone.serial)
		if !strings.Contains(run.out, "Encryption & credentials") {
			t.Fatalf("a Pixel gets stock Android's steps:\n%s", run.out)
		}
	})
	t.Run("no answer", func(t *testing.T) {
		phone := pixel("R5CT30ABCDE")
		m := newMachine(t)
		m.attach(phone) // its browser opens nothing

		run := runSetup(t, m, phone.serial, 300*time.Millisecond)
		if run.code != 1 || !strings.Contains(run.out, strings.TrimRight(TrustNoAnswer(phone.serial, true), "\n")) {
			t.Fatalf("exit %d:\n%s", run.code, run.out)
		}
		if _, ok := m.store.Phone(phone.serial); ok {
			t.Fatal("a failed check records nothing")
		}
		leftAsFound(t, m, phone.serial)
	})
}

func TestSetupCopiesTheCertificateAndNeverTheKey(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t)
	m.attach(phone)
	runSetup(t, m, phone.serial, 100*time.Millisecond)

	var pushed []byte
	m.bench.with(phone.serial, func(p *fakePhone) { pushed = p.downloads[DeviceCertificatePath] })
	want, err := os.ReadFile(netcap.CertificatePath(m.store.CADir()))
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(pushed)
	if !bytes.Equal(pushed, want) || block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("the phone must get the certificate, whole and alone:\n%s", pushed)
	}
	if DeviceCertificatePath != "/sdcard/Download/nthcase-agent-ca.crt" {
		t.Fatalf("the person looks for nthcase-agent-ca.crt in Downloads, not %s", DeviceCertificatePath)
	}
}

func TestAPhoneThatDropsDuringTheCheckIsSaidAndKeptForConnect(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t)
	m.attach(phone)
	m.bench.with(phone.serial, func(p *fakePhone) {
		p.browser = func(string) { m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = true }) } // unplugged
	})
	run := runSetup(t, m, phone.serial, 300*time.Millisecond)
	if run.code != 1 || !strings.Contains(run.out, "still has its network proxy pointed at this machine's capture") ||
		!strings.Contains(run.out, strings.TrimRight(TrustNoAnswer(phone.serial, false), "\n")) {
		t.Fatalf("the person must be told the phone was left proxied, and how to clear it:\n%s", run.out)
	}
	if strings.Contains(run.out, "put back as it was") {
		t.Fatalf("it must never say the phone was put back when it was not:\n%s", run.out)
	}
	records, _ := m.store.Records()
	if len(records) != 1 || records[0].Kind != KindTrustCheck {
		t.Fatalf("the check's record is kept for connect to finish: %+v", records)
	}
	// connect's next pass, once the phone is back, puts it back.
	m.bench.with(phone.serial, func(p *fakePhone) { p.unreachable = false })
	m.controller(nil).pass(context.Background())
	leftAsFound(t, m, phone.serial)
}

func TestSetupRefusesWithoutTouchingAPhoneWhenItCannotRun(t *testing.T) {
	t.Run("capture off", func(t *testing.T) {
		m := newMachine(t)
		m.attach(samsung("R5CT30ABCDE"))
		must(t, m.store.removeMark())
		run := runSetup(t, m, "", time.Second)
		if run.code != 1 || !strings.Contains(run.out, "npx nthcase-agent@latest capture enable") || len(m.bench.commands()) != 0 {
			t.Fatalf("exit %d, commands %q:\n%s", run.code, m.bench.commands(), run.out)
		}
	})
	t.Run("two phones and no serial", func(t *testing.T) {
		m := newMachine(t)
		m.attach(samsung("R5CT30AAAA1"))
		m.attach(pixel("R5CT30AAAA2"))
		run := runSetup(t, m, "", time.Second)
		if run.code != 2 || !strings.Contains(run.out, "Say which one: npx nthcase-agent@latest capture setup SERIAL") {
			t.Fatalf("exit %d:\n%s", run.code, run.out)
		}
		if commands := m.bench.commands(); len(commands) != 1 || commands[0] != "devices" {
			t.Fatalf("only the list may be read: %q", commands)
		}
	})
	t.Run("a phone not attached", func(t *testing.T) {
		m := newMachine(t)
		m.attach(samsung("R5CT30AAAA1"))
		run := runSetup(t, m, "R5CT30ZZZZZ", time.Second)
		if run.code != 1 || !strings.Contains(run.out, "No phone R5CT30ZZZZZ is attached and ready. Attached: R5CT30AAAA1") {
			t.Fatalf("exit %d:\n%s", run.code, run.out)
		}
	})
	t.Run("stopped at the prompt", func(t *testing.T) {
		phone := samsung("R5CT30ABCDE")
		m := newMachine(t)
		m.attach(phone)
		ctx, cancel := context.WithCancel(context.Background())
		reader, writer := io.Pipe() // the person never presses Enter
		t.Cleanup(func() { _ = writer.Close() })
		var out bytes.Buffer
		done := make(chan int, 1)
		go func() {
			done <- Setup(ctx, SetupOptions{Store: m.store, Adb: m.bench.adb, In: reader, Out: &out, Now: m.clock.Now,
				Serial: phone.serial, PortFree: benchPortFree})
		}()
		eventually(t, "the steps printed", func() bool { return len(m.bench.commandsTo(phone.serial)) == 3 })
		cancel() // Ctrl-C
		if code := <-done; code != 2 {
			t.Fatalf("exit %d", code)
		}
		for _, command := range m.bench.commandsTo(phone.serial) {
			if !strings.Contains(command, " getprop ") && !strings.Contains(command, " push ") {
				t.Fatalf("stopped at the prompt, the phone got %q", command)
			}
		}
		if !strings.Contains(out.String(), "Only the certificate file was copied to the phone's Downloads.") {
			t.Fatalf("the person must be told what was left:\n%s", out.String())
		}
	})
}
