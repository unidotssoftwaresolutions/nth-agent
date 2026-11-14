package capture

import (
	"context"
	"errors"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── TURNING CAPTURE ON AND OFF FOR THIS MACHINE ─────────────────────────────────────────────────────────────────

// ErrUnsupported is `capture enable` on a system this agent cannot keep the key to one user on.
var ErrUnsupported = errors.New("network capture is not available on this system")

// ErrAlreadyOn is `capture enable` when capture is on and its CA is usable.
var ErrAlreadyOn = errors.New("network capture is already on")

// Enable turns capture on: the CA first, then the mark — the mark means "on", and "on" needs a CA. Only `capture enable`
// calls it, and only after the person typed ENABLE. A CA left half-made by an enable that did not finish (a key with no
// mark) is this agent's own, and is replaced; a CA under a mark is never replaced.
func Enable(st Store, machineName, agentVersion string, now time.Time, goos string) (*netcap.CA, error) {
	if !Supported(goos) {
		return nil, ErrUnsupported
	}
	if _, on := st.Enabled(); on {
		if _, err := netcap.ReadCertificate(st.CADir()); err == nil {
			return nil, ErrAlreadyOn
		}
	} else if err := netcap.RemoveCA(st.CADir()); err != nil {
		return nil, err
	}
	if err := ownFolder(st.Root); err != nil {
		return nil, err
	}
	// OUT OF BACKUPS BEFORE THE KEY EXISTS (backup.go): a folder that cannot be kept out gets no key.
	if err := keepOutOfBackups(st.Root); err != nil {
		return nil, err
	}
	ca, err := netcap.CreateCA(st.CADir(), machineName, now)
	if err != nil {
		return nil, err
	}
	if err := st.writeMark(Mark{EnabledAtUtc: now.UTC(), AgentVersion: agentVersion}); err != nil {
		_ = netcap.RemoveCA(st.CADir())
		return nil, err
	}
	return ca, nil
}

// Owed is one phone a handback could not finish on, and what it saw.
type Owed struct {
	Record      Record
	Observation string
}

// DisableResult is what `capture disable` did — and what it could not.
type DisableResult struct {
	CAName       string
	MarkPath     string
	MarkFailure  error // the mark is still there: capture is still on
	HandedBack   []string
	StillOwed    []Owed
	FilesDeleted int
	FilesFailed  []string
	CAFailure    error
}

// Failed says whether anything `capture disable` set out to do is left undone.
func (r DisableResult) Failed() bool {
	return r.MarkFailure != nil || r.CAFailure != nil || len(r.FilesFailed) > 0 || len(r.StillOwed) > 0
}

// Disable turns capture off for this machine, in the order that leaves nothing behind:
//
//  1. the mark goes first, so no capture starts — a running `connect` sees it within seconds, stops its captures and hands
//     their phones back;
//  2. every phone a record names is handed back — each one's lock taken first, waiting up to lockWait for a `connect` that
//     is handing it back itself;
//  3. every capture file is deleted, then the CA's key and certificate, then the list of phones set up.
//
// A phone that cannot be reached keeps its record (the next `connect` hands it back) and is reported, with what it is left
// with.
func Disable(ctx context.Context, st Store, adb Adb, now func() time.Time, lockWait time.Duration) DisableResult {
	result := DisableResult{MarkPath: st.markPath()}
	if cert, err := netcap.ReadCertificate(st.CADir()); err == nil {
		result.CAName = cert.Subject.CommonName
	}
	result.MarkFailure = st.removeMark()

	records, _ := st.Records()
	seen := map[string]bool{}
	for _, r := range records {
		wait, cancel := context.WithTimeout(ctx, lockWait)
		lock, err := st.lockPhone(wait, r.Serial)
		cancel()
		if err != nil {
			result.StillOwed = append(result.StillOwed, Owed{Record: r,
				Observation: "another nth-agent process held the phone and did not let it go; it hands the phone back itself"})
			continue
		}
		if !st.holdsRecord(r.Lease) {
			lock.unlock() // the running connect handed it back while this waited
			if !seen[r.Serial] {
				seen[r.Serial] = true
				result.HandedBack = append(result.HandedBack, r.Serial)
			}
			continue
		}
		ok, observation := handBackSteps(ctx, st, adb, now, r, nil)
		lock.unlock()
		if ok {
			if !seen[r.Serial] {
				seen[r.Serial] = true
				result.HandedBack = append(result.HandedBack, r.Serial)
			}
			continue
		}
		result.StillOwed = append(result.StillOwed, Owed{Record: r, Observation: observation})
	}

	result.FilesDeleted, result.FilesFailed = netcap.RemoveAll(st.FilesDir())
	result.CAFailure = netcap.RemoveCA(st.CADir())
	_ = st.forgetPhones()
	return result
}
