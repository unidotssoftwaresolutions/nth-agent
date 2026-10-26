package netcap

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// ── THE AGENT'S OWN CERTIFICATE AUTHORITY, AND THE ONE FILE ALLOWED TO MINT ─────────────────────────────────────
//
// A capture reads a phone's HTTPS only when the phone trusts the certificate the proxy presents. The farm's node uses
// mitmproxy's CA; a customer's machine has no mitmproxy, so the agent is its own authority: ONE CA per machine, made by
// `nth-agent capture enable` and nothing else, its private key never leaving this machine, and a short-lived leaf
// minted per host in memory as the phone asks for it — what mitmproxy does, in the standard library.
//
// ⚠️ THIS IS THE ONLY FILE IN THE TREE THAT CREATES A CERTIFICATE OR A KEY, ON PURPOSE. cmd/nth-agent/never_signs_test.go
// lets this one file make an RSA key and an ECDSA key and create certificates — and nothing else, here or anywhere: every
// other way to sign or re-sign an APP or touch a keychain stays forbidden in this file too. This file signs TLS leaves for
// the hosts a test phone talks to, and nothing a customer builds or installs. Its minting calls are counted, call by call
// and function by function, so a new one is a decision someone makes in that seal, not a line that slips in.
//
// NOTHING IN THE AGENT SENDS THE KEY ANYWHERE, and the shape of this file is how: it is generated here, written by writeKeyFile and
// read back by readKeyFile — both here, both unexported — and held in an unexported field. No function returns it, no
// error names its bytes, and nothing here logs. The seal in key_never_leaves_test.go walks the tree for every other way
// out (a marshal, a "PRIVATE KEY" block, a reader of the key file) and drives a whole capture looking for the key in
// every output it can see. A COPY OF THE FILE is another matter, which no code path here can see: an OS backup takes one
// unless it is told not to, and internal/capture/backup.go tells Time Machine (and, on Linux, tools that honour
// CACHEDIR.TAG) to leave the capture folder out — A_MACHINE_THAT_STAYS_THE_CUSTOMERS.
//
// KEY TYPES, and why two:
//   - the CA: RSA 2048, SHA-256. It is the key a phone is asked to trust, so it is the kind already proven on the target:
//     mitmproxy 12.2.3's CA is RSA 2048, and an app's debug build trusted it on an Android phone in a live capture.
//   - a leaf: ECDSA P-256. Minted on the hot path, once per host; an EC key costs a fraction of an RSA one, and a leaf's
//     key type is the server's choice in TLS. The leaf is never trusted directly — only the CA is.
//
// LIFETIMES: the CA lives ONE YEAR (the operator's decision — not mitmproxy's ten, not the spike's 825 days): long enough
// that a by-hand install on a test phone is not monthly chore, short enough that a forgotten one ends. `doctor` says
// when it ends and, thirty days before, how to renew. A leaf lives two days and is never written anywhere.
//
// SUBJECT: "NthCase agent CA — <this machine's name>", O=NthCase — a person scrolling the phone's user certificates sees
// whose it is and which machine made it, and its on-device name (subject_hash_old) is not mitmproxy's, so a phone that
// also carries the farm's CA cannot have one file stand for the other.

const (
	// caKeyFile and caCertFile are the CA's two files in its folder.
	caKeyFile  = "ca-key.pem"
	caCertFile = "ca-cert.pem"

	// CALifetimeYears is how long a CA this agent makes stays valid: one calendar year from the moment it is made.
	CALifetimeYears = 1
	// leafLifetime is how long a per-host leaf is valid; clockSkew backdates both so a phone whose clock runs a little
	// behind this machine's still accepts them.
	leafLifetime = 48 * time.Hour
	clockSkew    = time.Hour

	// caSubjectPrefix and caOrganization name the CA the way a person reads it in the phone's certificate list.
	caSubjectPrefix = "NthCase agent CA — "
	caOrganization  = "NthCase"
	// maxCommonName is X.509's upper bound on a common name (ub-common-name, RFC 5280): a long machine name is cut to fit.
	maxCommonName = 64

	// maxLeaves bounds the per-host leaf cache. A capture can meet hundreds of hosts; past this the cache starts over,
	// which costs a re-mint and nothing else.
	maxLeaves = 1024
)

// CA is the agent's certificate authority as the proxy uses it: the certificate, and the key it signs leaves with.
type CA struct {
	cert    *x509.Certificate
	certDER []byte
	key     *rsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// CreateCA makes this machine's CA in dir and writes it there: the key at 0600 in a folder at 0700. It refuses a folder
// that already holds a key, so an existing CA is never replaced behind a person's back — rotation is `capture disable`
// (which deletes it) and `capture enable` again.
func CreateCA(dir, machineName string, now time.Time) (*CA, error) {
	if err := ownerOnlyFolder(dir); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, caKeyFile)
	if _, err := os.Lstat(keyPath); err == nil {
		return nil, fmt.Errorf("a capture CA already exists in %s — this agent does not replace one; turn capture off first (%s)", dir, command.Line("capture disable"))
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("making the capture CA's key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   CommonNameFor(machineName),
			Organization: []string{caOrganization},
		},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.AddDate(CALifetimeYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SignatureAlgorithm:    x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("making the capture CA's certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	// THE KEY FIRST, THEN THE CERTIFICATE: a folder holding a certificate and no key is a CA nobody can use, and is
	// read as "no CA" (LoadCA fails, doctor says so); a key with no certificate is removed below if the second write fails.
	if err := writeKeyFile(keyPath, key); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, caCertFile)
	if err := writeOwnerOnly(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		_ = os.Remove(keyPath)
		return nil, err
	}
	return &CA{cert: cert, certDER: der, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// LoadCA reads the CA in dir FOR THE PROXY — the one reader of the key. It refuses a CA it should not use: one that is
// missing or unreadable, a key or folder that someone other than this user can read, a key that is not the
// certificate's, and a certificate that has expired or is not yet valid.
func LoadCA(dir string, now time.Time) (*CA, error) {
	if err := ownerOnly(dir, true); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, caKeyFile)
	if err := ownerOnly(keyPath, false); err != nil {
		return nil, err
	}
	cert, der, err := readCertificate(dir)
	if err != nil {
		return nil, err
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, fmt.Errorf("the capture CA in %s is valid from %s to %s, and it is %s now — renew it: %s, then %s, then set up each phone again",
			dir, cert.NotBefore.UTC().Format(time.DateOnly), cert.NotAfter.UTC().Format(time.DateOnly), now.UTC().Format(time.DateOnly),
			command.Line("capture disable"), command.Line("capture enable"))
	}
	key, err := readKeyFile(keyPath)
	if err != nil {
		return nil, err
	}
	public, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || !public.Equal(&key.PublicKey) {
		return nil, fmt.Errorf("the capture CA's key in %s is not the key of its certificate — turn capture off and on again (%s, then enable)", dir, command.Line("capture disable"))
	}
	return &CA{cert: cert, certDER: der, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// ReadCertificate reads only the CA's CERTIFICATE from dir — never the key. What `doctor` states and what `capture setup`
// copies to a phone come from here.
func ReadCertificate(dir string) (*x509.Certificate, error) {
	cert, _, err := readCertificate(dir)
	return cert, err
}

// CertificatePath is where the certificate a phone installs lives in dir.
func CertificatePath(dir string) string { return filepath.Join(dir, caCertFile) }

// KeyPathIn is where the key lives in dir — for checking that it is there and owner-only. Reading it is LoadCA's alone.
func KeyPathIn(dir string) string { return filepath.Join(dir, caKeyFile) }

// RemoveCA deletes the CA's key and certificate from dir, and dir itself when nothing else is in it. A CA that is not
// there is removed. It is a file removal: on a disk without encryption a deleted file's blocks can outlive it, which is
// why the README's "Turning it all off" recommends the machine's own disk encryption (it did not, until
// A_MACHINE_THAT_STAYS_THE_CUSTOMERS; this sentence claimed it before it was true).
func RemoveCA(dir string) error {
	var failed []string
	for _, name := range []string{caKeyFile, caCertFile} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not delete %s in %s", strings.Join(failed, " and "), dir)
	}
	_ = os.Remove(dir) // only when empty; anything else of the person's in it stays
	return nil
}

// Certificate is the CA's certificate.
func (c *CA) Certificate() *x509.Certificate { return c.cert }

// String and GoString are all fmt prints of a CA — its name, never its fields: %v of the struct would print the key.
func (c *CA) String() string { return "the capture CA \"" + c.cert.Subject.CommonName + "\"" }

func (c *CA) GoString() string { return c.String() }

// Fingerprint is the SHA-256 of the CA certificate, in hex — what a phone's setup record names, so a phone set up for an
// older CA is never taken for one set up for this one.
func (c *CA) Fingerprint() string { return Fingerprint(c.cert) }

// Fingerprint is a certificate's SHA-256, in hex.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// SubjectHashOld is OpenSSL's `x509 -subject_hash_old` of a DER subject: the first four bytes of its MD5, little-endian,
// as eight lower-case hex digits. Android names a user CA's file `<hash>.0`; the farm's MitmproxyCa.SubjectHashOld applies
// the same rule. MD5 is OpenSSL's choice for this name, not a security property.
func SubjectHashOld(derSubject []byte) string {
	sum := md5.Sum(derSubject)
	return fmt.Sprintf("%08x", binary.LittleEndian.Uint32(sum[:4]))
}

// CommonNameFor is the CA's common name for a machine: the prefix and the machine's name, with anything that is not a
// printable character dropped and the whole cut to X.509's bound.
func CommonNameFor(machineName string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(machineName) {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		name = "this machine"
	}
	full := []rune(caSubjectPrefix + name)
	if len(full) > maxCommonName {
		full = full[:maxCommonName]
	}
	return string(full)
}

// leafFor returns (minting when needed) a TLS certificate for host, signed by the CA and held in memory only.
func (c *CA) leafFor(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return nil, errors.New("no host to make a certificate for")
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if leaf, ok := c.leaves[host]; ok && now.Before(leaf.Leaf.NotAfter.Add(-clockSkew)) {
		return leaf, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    now.Add(-clockSkew),
		NotAfter:     now.Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if len(host) <= maxCommonName {
		template.Subject = pkix.Name{CommonName: host}
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der, c.certDER}, PrivateKey: crypto.Signer(key), Leaf: parsed}
	if len(c.leaves) >= maxLeaves {
		c.leaves = map[string]*tls.Certificate{}
	}
	c.leaves[host] = leaf
	return leaf, nil
}

func randomSerial() (*big.Int, error) {
	// 128 random bits — never a counter, so two of this agent's CAs on one phone cannot collide on a serial.
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

// ── the key's only way to and from the disk ─────────────────────────────────────────────────────────────────────

// writeKeyFile is THE ONE PLACE THE KEY IS SERIALISED, and its bytes go to one place: a file created here, owner-only,
// that did not exist a moment before (O_EXCL — never through a link someone left at that name).
func writeKeyFile(path string, key *rsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("encoding the capture CA's key: %w", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	clear(der)
	defer clear(block)
	return writeOwnerOnly(path, block)
}

// readKeyFile is THE ONE PLACE THE KEY IS READ BACK. It names the file in an error, never its contents.
func readKeyFile(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the capture CA's key at %s: %w", path, errors.Unwrap(err))
	}
	defer clear(data)
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("the capture CA's key at %s is not a key this agent wrote", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	clear(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the capture CA's key at %s is not a key this agent wrote", path)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the capture CA's key at %s is not a key this agent wrote", path)
	}
	return key, nil
}

func readCertificate(dir string) (*x509.Certificate, []byte, error) {
	path := filepath.Join(dir, caCertFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the capture CA's certificate at %s: %w", path, errors.Unwrap(err))
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("the capture CA's certificate at %s is not one this agent wrote", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA {
		return nil, nil, fmt.Errorf("the capture CA's certificate at %s is not one this agent wrote", path)
	}
	return cert, block.Bytes, nil
}

// writeOwnerOnly creates path at 0600, refusing anything already there, and writes data to it.
func writeOwnerOnly(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, errors.Unwrap(err))
	}
	_, werr := file.Write(data)
	serr := file.Sync()
	cerr := file.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ownerOnlyFolder makes dir (and its parents) and leaves dir itself at 0700, whatever the umask or an earlier mode said.
func ownerOnlyFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("making %s readable by this user only: %w", dir, err)
	}
	return ownerOnly(dir, true)
}

// ownerOnly refuses a path that is not what it should be — a folder or a regular file, not a link — or that anyone but
// its owner can read, write or enter.
func ownerOnly(path string, folder bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, errors.Unwrap(err))
	}
	switch {
	case folder && !info.IsDir():
		return fmt.Errorf("%s is not a folder", path)
	case !folder && !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	case info.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("%s can be read by other users of this machine (mode %04o) — this agent keeps its capture CA to this user only; turn capture off and on again (%s, then enable)", path, info.Mode().Perm(), command.Line("capture disable"))
	}
	return nil
}
