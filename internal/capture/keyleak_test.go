package capture

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── THE KEY IN NO OUTPUT: a whole capture, and everything it wrote, said or sent, searched for the CA's key ──────
//
// cmd/nth-agent/key_never_leaves_test.go holds the source to one way out; this drives the code and looks. The key is
// read here — a test may — and turned into every form it could leave in: its PEM, its DER (PKCS #8 and PKCS #1), their
// base64, a slice of the PEM's body, and its secret numbers (the private exponent and both primes) as bytes, hex and
// decimal. Then a capture runs on the bench — HTTPS, a body sent, plain HTTP, a client that refuses the certificate, the
// stop, the farm's question, the pull — and every output it has is searched for every form.

type recorded struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (r *recorded) add(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.b.Write(p)
}

func (r *recorded) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.b.Bytes()...)
}

// recordingConn keeps a copy of everything read from it and written to it.
type recordingConn struct {
	net.Conn
	to *recorded
}

func (c recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.to.add(p[:n])
	return n, err
}

func (c recordingConn) Write(p []byte) (int, error) {
	c.to.add(p)
	return c.Conn.Write(p)
}

type recordingListener struct {
	net.Listener
	to *recorded
}

func (l recordingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return recordingConn{Conn: conn, to: l.to}, nil
}

// keyForms is every form the key could leave in.
func keyForms(t *testing.T, keyPEM []byte) map[string][]byte {
	t.Helper()
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("the key file is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key := parsed.(*rsa.PrivateKey)
	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	body := strings.Join(strings.Split(strings.TrimSpace(string(keyPEM)), "\n")[1:4], "") // a slice of the PEM's body
	forms := map[string][]byte{
		"the key file's PEM":          bytes.TrimSpace(keyPEM),
		"a slice of the PEM's body":   []byte(body[40:120]),
		"the PKCS #8 DER":             block.Bytes,
		"the PKCS #8 DER in base64":   []byte(base64.StdEncoding.EncodeToString(block.Bytes)),
		"the PKCS #1 DER":             pkcs1,
		"the PKCS #1 DER in base64":   []byte(base64.StdEncoding.EncodeToString(pkcs1)),
		"the PKCS #8 DER, URL base64": []byte(base64.RawURLEncoding.EncodeToString(block.Bytes)),
	}
	for name, number := range map[string][]byte{"the private exponent": key.D.Bytes(), "the first prime": key.Primes[0].Bytes(),
		"the second prime": key.Primes[1].Bytes()} {
		forms[name] = number[len(number)/2-16 : len(number)/2+16] // 32 bytes from its middle: no accident matches that
		forms[name+" in hex"] = []byte(hex.EncodeToString(number)[:64])
	}
	forms["the private exponent in decimal"] = []byte(key.D.String()[:60])
	forms["the private exponent in base64"] = []byte(base64.StdEncoding.EncodeToString(key.D.Bytes())[:60])
	return forms
}

func TestTheCAKeyIsInNoOutputOfAWholeCapture(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	keyPEM, err := os.ReadFile(netcap.KeyPathIn(m.store.CADir()))
	if err != nil {
		t.Fatal(err)
	}
	forms := keyForms(t, keyPEM)

	// THE SERVERS the phone's apps talk to, recording every byte they are sent.
	servers := &recorded{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	secure := httptest.NewUnstartedServer(handler)
	secure.Listener = recordingListener{Listener: secure.Listener, to: servers}
	secure.StartTLS()
	t.Cleanup(secure.Close)
	plain := httptest.NewUnstartedServer(handler)
	plain.Listener = recordingListener{Listener: plain.Listener, to: servers}
	plain.Start()
	t.Cleanup(plain.Close)
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())

	ctl := m.controller(func(cfg *Config) { cfg.UpstreamRoots = roots })
	ctl.Start(context.Background(), leaseA, phone.serial)
	if ctl.Running() != 1 {
		t.Fatalf("the capture did not start:\n%s", m.out.String())
	}

	// THE PHONE'S APPS, recording every byte the capture proxy sends them.
	apps := &recorded{}
	client := func(trust *x509.CertPool) *http.Client {
		proxyURL, _ := url.Parse("http://" + m.bench.proxyOf(phone.serial))
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: trust},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return recordingConn{Conn: conn, to: apps}, nil
			},
			DisableKeepAlives: true,
		}}
	}
	trusting := client(m.trusts())
	get, _ := http.NewRequest(http.MethodGet, secure.URL+"/me", nil)
	fetch(t, trusting, get)
	post, _ := http.NewRequest(http.MethodPost, secure.URL+"/login", strings.NewReader(`{"user":"ada","password":"hunter2"}`))
	post.Header.Set("Content-Type", "application/json")
	fetch(t, trusting, post)
	plainGet, _ := http.NewRequest(http.MethodGet, plain.URL+"/plain", nil)
	fetch(t, trusting, plainGet)
	if _, err := client(x509.NewCertPool()).Get(secure.URL + "/pinned"); err == nil { // an app that does not trust it
		t.Fatal("a client that does not trust the capture certificate must refuse it")
	}
	eventually(t, "the exchanges recorded", func() bool {
		data, _ := ctl.Pull(leaseA)
		return strings.Contains(string(data), "/plain") && strings.Contains(string(data), "not readable")
	})
	clean, observation := ctl.HandBackForFarm(context.Background(), phone.serial, leaseA)
	if !clean {
		t.Fatalf("handback: %q", observation)
	}
	pulled, _ := ctl.Pull(leaseA)
	ctl.Pulled(leaseA)
	status := ReadStatus(m.store, m.clock.Now())
	statusJSON, _ := json.Marshal(status)

	outputs := map[string][]byte{
		"the console": []byte(m.out.String()),
		"the pulled capture (what the farm gets)": pulled,
		"what the phone's apps were sent":         apps.bytes(),
		"what the servers were sent":              servers.bytes(),
		"doctor --json":                           statusJSON,
		"doctor":                                  []byte(strings.Join(status.Lines, "\n")),
		"every adb command":                       []byte(strings.Join(m.bench.commands(), "\n")),
		"fmt of the CA":                           []byte(fmt.Sprintf("%v %+v %#v %s %q", m.ca, m.ca, m.ca, m.ca, m.ca)),
	}
	// …and every file the capture's folder holds, but the key's own.
	err = filepath.WalkDir(m.store.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == netcap.KeyPathIn(m.store.CADir()) {
			return err
		}
		data, err := os.ReadFile(path)
		outputs["the file "+strings.TrimPrefix(path, m.store.Root)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// INSTRUMENT GUARDS: every output was really filled (a phone's lock file is empty by design), the folder's own files
	// were read, and every form is really found where the key is.
	for name, output := range outputs {
		if len(output) == 0 && !strings.HasPrefix(name, "the file /locks/") {
			t.Fatalf("%s is empty — this would search nothing", name)
		}
	}
	for _, file := range []string{"/enabled", "/phones.json", "/ca/ca-cert.pem"} {
		if _, read := outputs["the file "+file]; !read {
			t.Fatalf("the walk did not read %s — it is reading the wrong folder", file)
		}
	}
	for name, form := range forms {
		if !bytes.Contains(append(append([]byte(nil), keyPEM...), mustDecodePEMish(keyPEM, form)...), form) {
			t.Fatalf("instrument guard: %s is not a form of the key", name)
		}
	}

	for output, data := range outputs {
		for form, needle := range forms {
			if bytes.Contains(data, needle) {
				t.Errorf("%s holds the CA's key (%s)", output, form)
			}
		}
	}
}

// mustDecodePEMish is the key's DER and its numbers' forms, so the guard can find each form in the key itself.
func mustDecodePEMish(keyPEM, _ []byte) []byte {
	block, _ := pem.Decode(keyPEM)
	parsed, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
	key := parsed.(*rsa.PrivateKey)
	var all bytes.Buffer
	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	for _, part := range [][]byte{block.Bytes, pkcs1, key.D.Bytes(), key.Primes[0].Bytes(), key.Primes[1].Bytes()} {
		all.Write(part)
		all.WriteString(hex.EncodeToString(part))
		all.WriteString(base64.StdEncoding.EncodeToString(part))
		all.WriteString(base64.RawURLEncoding.EncodeToString(part))
	}
	all.WriteString(key.D.String())
	return all.Bytes()
}
