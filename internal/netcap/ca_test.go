package netcap

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var caMadeAt = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newTestCA(t *testing.T) (*CA, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ca")
	ca, err := CreateCA(dir, "Adas-MacBook-Pro.local", caMadeAt)
	if err != nil {
		t.Fatalf("CreateCA: %v", err)
	}
	return ca, dir
}

// THE CA LIVES ONE YEAR — the operator's decision, not mitmproxy's ten years and not the spike's 825 days.
func TestTheCALivesOneYearAndIsRSA2048SignedWithSHA256(t *testing.T) {
	ca, _ := newTestCA(t)
	cert := ca.Certificate()
	if want := caMadeAt.AddDate(1, 0, 0); !cert.NotAfter.Equal(want) {
		t.Fatalf("the CA must end one year after it is made (%s), it ends %s", want, cert.NotAfter)
	}
	if want := caMadeAt.Add(-time.Hour); !cert.NotBefore.Equal(want) {
		t.Fatalf("the CA starts an hour early, for a phone whose clock runs behind: %s, got %s", want, cert.NotBefore)
	}
	public, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || public.N.BitLen() != 2048 {
		t.Fatalf("the CA key is RSA 2048 — the kind already trusted on the target phone; got %T", cert.PublicKey)
	}
	if cert.SignatureAlgorithm != x509.SHA256WithRSA {
		t.Fatalf("the CA is signed with SHA-256, got %s", cert.SignatureAlgorithm)
	}
	if !cert.IsCA || !cert.BasicConstraintsValid || !cert.MaxPathLenZero || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("the CA must be a CA that signs leaves and nothing below them")
	}
}

func TestTheSubjectNamesTheAgentAndTheMachine(t *testing.T) {
	ca, _ := newTestCA(t)
	subject := ca.Certificate().Subject
	if subject.CommonName != "NthCase agent CA — Adas-MacBook-Pro.local" {
		t.Fatalf("the CA's name is what a person reads in the phone's certificate list, got %q", subject.CommonName)
	}
	if len(subject.Organization) != 1 || subject.Organization[0] != "NthCase" {
		t.Fatalf("O=NthCase, got %v", subject.Organization)
	}
	long := CommonNameFor(strings.Repeat("m", 200) + "\x07")
	if utf8.RuneCountInString(long) != maxCommonName || strings.ContainsRune(long, 7) {
		t.Fatalf("a long machine name is cut to X.509's bound and loses what is not printable, got %q", long)
	}
}

// mitmproxyCASubject is mitmproxy's own CA subject, DER — read from the CA mitmproxy 12.2.3 made on the operator's Mac
// (`openssl asn1parse` of ~/.mitmproxy/mitmproxy-ca-cert.pem, the public certificate): CN=mitmproxy, O=mitmproxy, both
// UTF8String. `openssl x509 -subject_hash_old` of that certificate prints c8750f0d.
const mitmproxyCASubject = "3028311230100603550403" + "0c096d69746d70726f7879" + "3112301006035504" + "0a0c096d69746d70726f7879"

func TestTheSubjectHashIsNotMitmproxys(t *testing.T) {
	der, err := hex.DecodeString(mitmproxyCASubject)
	if err != nil {
		t.Fatal(err)
	}
	// THE INSTRUMENT FIRST: the hash rule reproduces OpenSSL's answer for mitmproxy's own CA.
	if got := SubjectHashOld(der); got != "c8750f0d" {
		t.Fatalf("subject_hash_old of mitmproxy's CA is c8750f0d (openssl), this rule says %s", got)
	}
	ca, _ := newTestCA(t)
	ours := SubjectHashOld(ca.Certificate().RawSubject)
	if ours == "c8750f0d" {
		t.Fatalf("the agent's CA must not share mitmproxy's on-phone file name (%s.0)", ours)
	}
	other, err := CreateCA(filepath.Join(t.TempDir(), "ca"), "another-machine", caMadeAt)
	if err != nil {
		t.Fatal(err)
	}
	if SubjectHashOld(other.Certificate().RawSubject) == ours {
		t.Fatal("two machines' CAs must not share an on-phone file name")
	}
}

// THE KEY IS 0600 IN A 0700 FOLDER, and a CA anyone else on the machine could read is not used.
func TestTheKeyAndItsFolderAreThisUsersOnly(t *testing.T) {
	ca, dir := newTestCA(t)
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, caKeyFile): 0o600, filepath.Join(dir, caCertFile): 0o600} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s is %04o, want %04o", path, info.Mode().Perm(), want)
		}
	}
	loaded, err := LoadCA(dir, caMadeAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("the CA it made must load: %v", err)
	}
	if loaded.Fingerprint() != ca.Fingerprint() {
		t.Fatal("the loaded CA is not the one made")
	}

	if err := os.Chmod(filepath.Join(dir, caKeyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(dir, caMadeAt.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "can be read by other users") {
		t.Fatalf("a key other users can read must not be used, got %v", err)
	}
	_ = os.Chmod(filepath.Join(dir, caKeyFile), 0o600)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(dir, caMadeAt.Add(time.Hour)); err == nil {
		t.Fatal("a CA folder other users can enter must not be used")
	}
	_ = os.Chmod(dir, 0o700)

	// A LINK WHERE THE KEY SHOULD BE IS NOT THE KEY.
	moved := filepath.Join(t.TempDir(), "elsewhere.pem")
	if err := os.Rename(filepath.Join(dir, caKeyFile), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, filepath.Join(dir, caKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(dir, caMadeAt.Add(time.Hour)); err == nil {
		t.Fatal("a link in the key's place must not be followed")
	}
}

func TestACAIsNeverReplacedAndNeverUsedOutsideItsYear(t *testing.T) {
	ca, dir := newTestCA(t)
	if _, err := CreateCA(dir, "again", caMadeAt); err == nil {
		t.Fatal("an existing CA must not be replaced — rotation is disable, then enable")
	}
	if _, err := LoadCA(dir, ca.Certificate().NotAfter.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "renew it") {
		t.Fatalf("an expired CA must not be used, and the refusal says how to renew: %v", err)
	}
	if _, err := LoadCA(dir, caMadeAt.Add(-2*time.Hour)); err == nil {
		t.Fatal("a CA not yet valid must not be used")
	}
}

func TestRemoveCADeletesTheKeyAndTheCertificate(t *testing.T) {
	_, dir := newTestCA(t)
	if err := RemoveCA(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, caKeyFile)); !os.IsNotExist(err) {
		t.Fatal("the key must be gone")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the emptied CA folder goes too")
	}
	if err := RemoveCA(dir); err != nil {
		t.Fatalf("removing a CA that is not there is not a failure: %v", err)
	}
}

func TestALeafVerifiesAgainstTheCAForItsHostAndLivesTwoDays(t *testing.T) {
	ca, _ := newTestCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate())
	for _, host := range []string{"www.example.com", "10.0.2.2"} {
		leaf, err := ca.leafFor(host)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := leaf.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, CurrentTime: time.Now()}); err != nil {
			t.Fatalf("a leaf for %s must verify against the CA: %v", host, err)
		}
		if life := leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore); life != leafLifetime+clockSkew {
			t.Fatalf("a leaf lives two days (plus the hour it starts early), got %s", life)
		}
		if leaf.PrivateKey == nil || len(leaf.Certificate) != 2 {
			t.Fatal("a leaf carries its key, in memory, and the CA's certificate after its own")
		}
	}
}
