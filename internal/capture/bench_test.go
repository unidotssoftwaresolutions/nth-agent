package capture

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── THE BENCH: PHONES THAT ANSWER adb, AND NOTHING REAL ─────────────────────────────────────────────────────────
//
// Every test in this package that touches a phone touches one of these. No adb runs, no phone is reached, and no port is
// dialled that a test did not open: the proxy listens on loopback ports the bench checks by binding them (never by
// connecting), the phone's apps are an HTTP client in this process, and the servers they talk to are httptest servers.
//
// THE ALLOWLIST, AT RUN TIME: a command this agent is not allowed to send a phone fails the test that sent it — the list
// is the one adb.go builds, and adb_test.go holds the same list from the source.

type fakePhone struct {
	serial       string
	manufacturer string
	model        string

	proxy     string // the global http_proxy; "" reads back as "null"
	reverses  map[string]bool
	downloads map[string][]byte
	userCAs   []string // what the person installed on the phone by hand; the agent never touches it

	unreachable bool            // adb cannot reach it: unplugged, or locked out
	refuses     map[string]bool // commands the phone refuses: "reverse", "proxy-set"
	stuckProxy  bool            // clearing the proxy does not take — it reads back as it was
	browser     func(address string)
	opened      []string // every address the agent asked its browser to open, in order
}

type bench struct {
	t      *testing.T
	mu     sync.Mutex
	phones map[string]*fakePhone
	log    []string
	// beforeChange runs before a command that CHANGES a phone (proxy set, reverse add) — where a test checks that what
	// is about to change was written down first.
	beforeChange func(serial string, args []string)
}

func newBench(t *testing.T, phones ...*fakePhone) *bench {
	b := &bench{t: t, phones: map[string]*fakePhone{}}
	for _, p := range phones {
		if p.reverses == nil {
			p.reverses = map[string]bool{}
		}
		if p.downloads == nil {
			p.downloads = map[string][]byte{}
		}
		if p.refuses == nil {
			p.refuses = map[string]bool{}
		}
		b.phones[p.serial] = p
	}
	return b
}

func (b *bench) phone(serial string) *fakePhone {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.phones[serial]
}

// with runs f on a phone under the bench's lock — every read or change a test makes of a phone's state.
func (b *bench) with(serial string, f func(p *fakePhone)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(b.phones[serial])
}

func (b *bench) commands() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.log...)
}

// commandsTo is every command sent to one phone.
func (b *bench) commandsTo(serial string) []string {
	var to []string
	for _, c := range b.commands() {
		if strings.HasPrefix(c, "-s "+serial+" ") {
			to = append(to, c)
		}
	}
	return to
}

func (b *bench) proxyOf(serial string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.phones[serial].proxy
}

func (b *bench) reversesOf(serial string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.phones[serial].reverses)
}

func is(args []string, want ...string) bool {
	if len(args) != len(want) {
		return false
	}
	for i := range want {
		if want[i] != "*" && args[i] != want[i] {
			return false
		}
	}
	return true
}

// adb answers one command the way a phone would, or fails the test for a command not on the list.
func (b *bench) adb(_ context.Context, args ...string) (string, string, int, error) {
	b.mu.Lock()
	b.log = append(b.log, strings.Join(args, " "))
	b.mu.Unlock()

	if is(args, "devices") {
		b.mu.Lock()
		defer b.mu.Unlock()
		out := "List of devices attached\n"
		for serial, p := range b.phones {
			if !p.unreachable {
				out += serial + "\tdevice\n"
			}
		}
		return out + "\n", "", 0, nil
	}
	if len(args) < 3 || args[0] != "-s" {
		b.t.Errorf("adb %q is not a command this agent may send a phone", args)
		return "", "not on the list", 1, nil
	}
	serial, rest := args[1], args[2:]

	b.mu.Lock()
	p := b.phones[serial]
	if p == nil || p.unreachable {
		b.mu.Unlock()
		return "", "adb: device '" + serial + "' not found\n", 1, nil
	}
	changes := is(rest, "shell", "settings", "put", "global", "http_proxy", "*") && rest[5] != ":0" ||
		is(rest, "reverse", "*", "*") && rest[1] != "--remove"
	hook := b.beforeChange
	b.mu.Unlock()
	if changes && hook != nil {
		hook(serial, args)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case is(rest, "shell", "settings", "put", "global", "http_proxy", "*"):
		value := rest[5]
		if value == ":0" {
			if !p.stuckProxy {
				p.proxy = ":0"
			}
			return "", "", 0, nil
		}
		if p.refuses["proxy-set"] {
			return "", "Security exception: permission denied\n", 255, nil
		}
		p.proxy = value
		return "", "", 0, nil
	case is(rest, "shell", "settings", "get", "global", "http_proxy"):
		if p.proxy == "" {
			return "null\n", "", 0, nil
		}
		return p.proxy + "\n", "", 0, nil
	case is(rest, "reverse", "--remove", "*") && strings.HasPrefix(rest[2], "tcp:"):
		delete(p.reverses, rest[2])
		return "", "", 0, nil
	case is(rest, "reverse", "*", "*") && strings.HasPrefix(rest[1], "tcp:") && rest[1] == rest[2]:
		if p.refuses["reverse"] {
			return "", "adb: error: cannot bind listener: Address already in use\n", 1, nil
		}
		p.reverses[rest[1]] = true
		return "", "", 0, nil
	case is(rest, "push", "*", DeviceCertificatePath):
		data, err := os.ReadFile(rest[1])
		if err != nil {
			return "", "adb: error: cannot stat '" + rest[1] + "'\n", 1, nil
		}
		p.downloads[rest[2]] = data
		return rest[1] + ": 1 file pushed\n", "", 0, nil
	case is(rest, "shell", "getprop", "ro.product.manufacturer"):
		return p.manufacturer + "\n", "", 0, nil
	case is(rest, "shell", "getprop", "ro.product.model"):
		return p.model + "\n", "", 0, nil
	case is(rest, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", "*", "--es", "com.android.browser.application_id", "nthcase-capture-check"):
		p.opened = append(p.opened, rest[6])
		if p.browser != nil {
			go p.browser(rest[6])
		}
		return "Starting: Intent { act=android.intent.action.VIEW }\n", "", 0, nil
	}
	b.t.Errorf("adb %q is not a command this agent may send a phone", args)
	return "", "not on the list", 1, nil
}

// ── the clock ───────────────────────────────────────────────────────────────────────────────────────────────────

type benchClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *benchClock { return &benchClock{now: time.Now()} }

func (c *benchClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *benchClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ── ports: judged by binding them, never by connecting ──────────────────────────────────────────────────────────

// benchPortFree binds 127.0.0.1:port WITHOUT SO_REUSEADDR and lets go: on this system that bind is refused when anything
// at all holds the port on an IPv4 address, the wildcard included. Nothing is ever dialled.
func benchPortFree(port uint16) bool {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var setErr error
		err := raw.Control(func(fd uintptr) {
			setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
		})
		return errors.Join(err, setErr)
	}}
	listener, err := config.Listen(context.Background(), "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// ── the machine: capture turned on, and phones set up ───────────────────────────────────────────────────────────

type machine struct {
	t     *testing.T
	store Store
	clock *benchClock
	ca    *netcap.CA
	bench *bench
	out   *syncBuffer
}

// newMachine is a machine with capture turned on (the CA made, the mark written) and the given phones set up for it.
func newMachine(t *testing.T, phones ...*fakePhone) *machine {
	t.Helper()
	store := StoreIn(t.TempDir())
	clock := newClock()
	ca, err := Enable(store, "bench-mac", "v-test", clock.Now(), "darwin")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	for _, p := range phones {
		if err := store.setPhone(Phone{Serial: p.serial, Model: p.model, Manufacturer: p.manufacturer,
			CAFingerprint: ca.Fingerprint(), SetUpAtUtc: clock.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	return &machine{t: t, store: store, clock: clock, ca: ca, bench: newBench(t, phones...), out: &syncBuffer{}}
}

// attach plugs a phone into the bench without setting it up for capture.
func (m *machine) attach(p *fakePhone) {
	other := newBench(m.t, p)
	m.bench.mu.Lock()
	defer m.bench.mu.Unlock()
	m.bench.phones[p.serial] = other.phones[p.serial]
}

func (m *machine) controller(change func(*Config)) *Controller {
	cfg := Config{
		Store: m.store, Adb: m.bench.adb, Out: m.out, AgentVersion: "v-test", Now: m.clock.Now,
		LookPath: func(string) (string, error) { return "/bench/adb", nil },
		GOOS:     "darwin",
		PortFree: benchPortFree,
		// The bench's servers are this machine's own, on loopback: what --capture-allow-local-network opens is open here,
		// and a_machine_that_stays_the_customers_test.go holds the default.
		ReachLocal: true,
	}
	if change != nil {
		change(&cfg)
	}
	return NewController(cfg)
}

// trusts is a pool holding the capture CA — a phone whose person installed the certificate.
func (m *machine) trusts() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(m.ca.Certificate())
	return pool
}

// appsOn is the phone's apps: a client that goes through the phone's proxy setting, which on the bench is this machine's
// loopback (adb reverse carries it there on a real phone). It trusts what it is given.
func (m *machine) appsOn(serial string, trust *x509.CertPool) *http.Client {
	m.t.Helper()
	proxy := m.bench.proxyOf(serial)
	if !strings.HasPrefix(proxy, "127.0.0.1:") {
		m.t.Fatalf("phone %s is not pointed at a capture (its proxy is %q)", serial, proxy)
	}
	proxyURL, _ := url.Parse("http://" + proxy)
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: trust}, DisableKeepAlives: true,
	}}
}

// upstream is a server the phone's apps talk to, on loopback, with a certificate the capture proxy is told to trust.
func upstream(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return server, roots
}

func fetch(t *testing.T, client *http.Client, request *http.Request) (*http.Response, []byte) {
	t.Helper()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s through the capture: %v", request.Method, request.URL, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

// syncBuffer is the console: written by the controller's goroutines, read by the test.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waited 10 s for %s", what)
}

const (
	leaseA = "0f3c2a91-5b7e-4d21-9a3c-6e8f10b2c4d5"
	leaseB = "7d1e9b44-2c6a-4f80-b1d3-95a7e3c0f218"
	leaseC = "c2a0e5f7-9d31-4b6c-8e24-1f7b3d9a5c60"
)

func samsung(serial string) *fakePhone {
	return &fakePhone{serial: serial, manufacturer: "samsung", model: "SM-A546B"}
}

func pixel(serial string) *fakePhone {
	return &fakePhone{serial: serial, manufacturer: "Google", model: "Pixel 8"}
}
