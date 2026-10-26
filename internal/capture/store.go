// Package capture is network capture on the customer's machine, as far as phones go: whether this machine has it on, which
// phones were set up for it, starting and stopping a capture when the farm asks — only when every gate holds — and
// always giving the phone back. The proxy, the CA, the masking and the capture file are internal/netcap's; this package
// is what touches a phone, over adb, and nothing it does to a phone happens before it is written down.
package capture

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── THE STATE FOLDER ────────────────────────────────────────────────────────────────────────────────────────────
//
// Everything capture keeps lives in <user config dir>/nth-agent/capture — beside the node id — every folder 0700 and every
// file 0600:
//
//   enabled        the mark: capture is on for this machine (`capture enable` writes it, `capture disable` deletes it)
//   ca/            the capture CA's certificate and key (internal/netcap/ca.go)
//   phones.json    the phones set up for capture, each with the CA it was set up for
//   handback/      one record per capture (or trust check) that changed a phone, written BEFORE the phone is touched and
//                  removed only once the phone reads back with no proxy of ours
//   files/         the masked capture files, one per lease, deleted after their pull or their hour
//   locks/         one lock file per phone: whichever nth-agent process is changing a phone holds it
//
// The running `connect` and a `capture` command typed in another terminal meet here, and only here.

// Store is the capture state folder.
type Store struct{ Root string }

// StoreIn is the capture state folder under a user config directory.
func StoreIn(configDir string) Store {
	return Store{Root: filepath.Join(configDir, "nth-agent", "capture")}
}

func (s Store) CADir() string      { return filepath.Join(s.Root, "ca") }
func (s Store) FilesDir() string   { return filepath.Join(s.Root, "files") }
func (s Store) recordsDir() string { return filepath.Join(s.Root, "handback") }
func (s Store) locksDir() string   { return filepath.Join(s.Root, "locks") }
func (s Store) markPath() string   { return filepath.Join(s.Root, "enabled") }
func (s Store) phonesPath() string { return filepath.Join(s.Root, "phones.json") }

// Mark is what the enabled mark says.
type Mark struct {
	EnabledAtUtc time.Time `json:"enabledAtUtc"`
	AgentVersion string    `json:"agentVersion"`
}

// Enabled reads the mark: capture is on for this machine exactly when it is there.
func (s Store) Enabled() (Mark, bool) {
	var mark Mark
	data, err := readOwn(s.markPath())
	if err != nil {
		return mark, false
	}
	if json.Unmarshal(data, &mark) != nil {
		return Mark{}, true // a mark that cannot be read is still a mark: the person turned capture on
	}
	return mark, true
}

func (s Store) writeMark(mark Mark) error { return writeOwn(s.markPath(), mark) }

func (s Store) removeMark() error {
	if err := os.Remove(s.markPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ── phones set up for capture ───────────────────────────────────────────────────────────────────────────────────

// Phone is one phone set up for capture: its trust check passed for the CA named by CAFingerprint.
type Phone struct {
	Serial        string    `json:"serial"`
	Model         string    `json:"model,omitempty"`
	Manufacturer  string    `json:"manufacturer,omitempty"`
	CAFingerprint string    `json:"caFingerprint"`
	SetUpAtUtc    time.Time `json:"setUpAtUtc"`
}

// Phones is every phone set up for capture on this machine.
func (s Store) Phones() []Phone {
	data, err := readOwn(s.phonesPath())
	if err != nil {
		return nil
	}
	var file struct {
		Phones []Phone `json:"phones"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	return file.Phones
}

// Phone is the set-up record of one phone, if it has one.
func (s Store) Phone(serial string) (Phone, bool) {
	for _, p := range s.Phones() {
		if p.Serial == serial {
			return p, true
		}
	}
	return Phone{}, false
}

func (s Store) setPhone(phone Phone) error {
	phones := []Phone{phone}
	for _, p := range s.Phones() {
		if p.Serial != phone.Serial {
			phones = append(phones, p)
		}
	}
	sort.Slice(phones, func(i, j int) bool { return phones[i].Serial < phones[j].Serial })
	return writeOwn(s.phonesPath(), struct {
		Phones []Phone `json:"phones"`
	}{phones})
}

func (s Store) forgetPhones() error {
	if err := os.Remove(s.phonesPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ── handback records ────────────────────────────────────────────────────────────────────────────────────────────

// Kinds of record: a capture's, and a trust check's (`capture setup` points the phone's proxy here for a moment too).
const (
	KindCapture    = "capture"
	KindTrustCheck = "trust-check"
)

// Record is what one capture — or one trust check — is about to change on one phone, written before the phone is
// touched: the node's CaptureHandbackRecord, minus what this agent never does (it never drops a CA on a phone and never
// runs adb root, so there is no CA hash and nothing to unroot).
type Record struct {
	Serial  string    `json:"serial"`
	Lease   string    `json:"lease"`
	Kind    string    `json:"kind"`
	Port    int       `json:"port"`
	Proxy   string    `json:"proxy"`
	Reverse bool      `json:"reverse"`
	Written time.Time `json:"writtenUtc"`
	// LastTried and LastObservation are what the last handback that could not finish saw — what `doctor` repeats.
	LastTried       *time.Time `json:"lastTriedUtc,omitempty"`
	LastObservation string     `json:"lastObservation,omitempty"`
}

// ProxyValue is the global http_proxy a capture on port sets on a phone: the phone's own loopback, which `adb reverse`
// carries to this machine's.
func ProxyValue(port int) string { return "127.0.0.1:" + strconv.Itoa(port) }

// IsAdbSerial is the node's rule for a phone's name as adb prints it: something, at most 128 characters, with no white
// space or control character. It reaches adb only as the value of -s, never a shell line.
func IsAdbSerial(serial string) bool {
	if serial == "" || len(serial) > 128 {
		return false
	}
	for _, r := range serial {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// wellFormed is a record this agent could have written. Anything else is said once, kept, and never acted on.
func (r Record) wellFormed() bool {
	return IsAdbSerial(r.Serial) && netcap.IsLease(r.Lease) && (r.Kind == KindCapture || r.Kind == KindTrustCheck) &&
		netcap.IsCapturePort(r.Port) && r.Proxy == ProxyValue(r.Port)
}

func (s Store) recordPath(lease string) string { return filepath.Join(s.recordsDir(), lease+".json") }

// writeRecord writes (or rewrites) one record atomically. An error means the phone must not be touched.
func (s Store) writeRecord(r Record) error {
	if !r.wellFormed() {
		return fmt.Errorf("a handback record must be one this agent can read back")
	}
	return writeOwn(s.recordPath(r.Lease), r)
}

func (s Store) removeRecord(lease string) error {
	if err := os.Remove(s.recordPath(lease)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s Store) holdsRecord(lease string) bool {
	_, err := os.Lstat(s.recordPath(lease))
	return err == nil
}

// Records is every record kept: a regular file named <lease>.json holding a record this agent could have written. A
// file of another name, a link or a folder is not read; a record that cannot be read is returned in unreadable, so it is
// said, and is never removed — it may be the only trace of a phone left proxied.
func (s Store) Records() (records []Record, unreadable []string) {
	entries, err := os.ReadDir(s.recordsDir())
	if err != nil {
		return nil, nil
	}
	for _, entry := range entries {
		lease, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !entry.Type().IsRegular() || !netcap.IsLease(lease) {
			continue
		}
		data, err := readOwn(filepath.Join(s.recordsDir(), entry.Name()))
		var r Record
		if err != nil || json.Unmarshal(data, &r) != nil || r.Lease != lease || !r.wellFormed() {
			unreadable = append(unreadable, filepath.Join(s.recordsDir(), entry.Name()))
			continue
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Written.Before(records[j].Written) })
	return records, unreadable
}

// lockPath is one phone's lock file. The serial is spelled in hex, because a serial may hold characters a file name must not.
func (s Store) lockPath(serial string) string {
	return filepath.Join(s.locksDir(), hex.EncodeToString([]byte(serial))+".lock")
}

// ── owner-only files ────────────────────────────────────────────────────────────────────────────────────────────

// writeOwn writes value as JSON to path, owner-only, atomically: a temporary file in the same folder, then a rename over
// the old one — never through a link at the final name, which the rename replaces rather than follows.
func writeOwn(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := ownFolder(dir); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_, werr := temporary.Write(append(data, '\n'))
	cerr := temporary.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o600)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// readOwn reads a regular file this agent wrote; a link is not followed.
func readOwn(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.ReadFile(path)
}

// ownFolder makes dir and leaves it 0700 — and the capture folder above it too.
func ownFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}
