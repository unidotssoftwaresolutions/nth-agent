package netcap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── THE CAPTURE FILE: MASKED, OWNER-ONLY, AND SHORT-LIVED ───────────────────────────────────────────────────────
//
// THE ONLY CAPTURE THAT EVER TOUCHES THE CUSTOMER'S DISK, and it holds only masked entries: a raw exchange is masked in
// memory (entryFor) before this file sees a byte of it. It lives in the agent's state folder at 0600, named by its lease
// (<lease>.har), and it is deleted as soon as the farm has pulled it, and in any case an hour after its capture stopped;
// a sweep at start and on a timer removes anything older, restarts included (Sweep).
//
// IT IS A WHOLE HAR AT EVERY MOMENT, not only at the end. While nothing has been left out, each entry is written over the
// document's closing brackets, which are written again after it. Once a request has been left out, what is kept is no
// longer what the file begins with, so the document is written whole again: at most once a second while requests keep
// coming (recording is never in the way of the phone's traffic), and whenever it is read (a pull) or stops (seal, close).
// So a pull in the middle of a capture reads a document that parses, and the file a stopped capture leaves — the one a
// pull reads after a restart too — is the document a pull would have read before the stop.
//
// THREE BOUNDS, and why each — the farm's own node keeps the same three, and the runner reads a capture to them:
//   - THE LAST 500 REQUESTS (MaxEntries) — the runner shows the last 500 of a run, so an older one would cross the network
//     to be dropped on arrival. Past it the oldest is left out, and the log's comment says how many requests the app made
//     and how many of the first are not in it, in the one sentence the runner believes (leftOutSentence).
//   - A HEADER VALUE AT 16,384 CHARACTERS AND AN ADDRESS AT 4,000 (MaxHeaderChars, MaxAddressChars; har.go) — masked
//     first, then cut with the runner's marker, so a cut never splits a credential the masker would have found.
//   - 16 MB (MaxFileBytes) — the runner reads at most 48 MB of a capture and gives the pull 30 seconds
//     (NetworkCapturePull.DownloadCapBytes, PullTimeout), and this file crosses the customer's own uplink to get there. Past
//     the bound an entry keeps its method, address, status, headers and sizes, and its bodies are left out with the reason;
//     one that does not fit even so makes room by leaving out the oldest, so what is kept is still the last requests.

const (
	// MaxEntries is how many exchanges one capture keeps: the last ones — the runner's own per-run bound.
	MaxEntries = 500
	// MaxFileBytes is the most one capture file holds — a third of what the runner reads, so the pull through the customer's
	// uplink fits inside the runner's 30-second pull bound from about 4.3 Mbit/s up (128 Mbit in 30 s).
	MaxFileBytes = 16 << 20

	harSuffix = "]}}"

	// rewriteEvery is how often, at most, a capture that has left a request out writes its whole document while requests
	// keep coming.
	rewriteEvery = time.Second
)

// leftOutSentence is what the log's comment says once the capture has left requests out: the farm's own node writes the
// same sentence, and the runner believes this one only — plain digits, made = kept + the first left out, and no more
// entries in the file than kept. A capture that left nothing out carries no comment.
func leftOutSentence(made, kept int) string {
	return fmt.Sprintf("This app made %d requests; the capture keeps the last %d, so the first %d are not in it.", made, kept, made-kept)
}

// captureFile is one capture's masked HAR: the entries it keeps, and the document on disk.
type captureFile struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	prefix  string    // the document up to its first entry
	kept    [][]byte  // the entries the document holds, oldest first
	keptLen int64     // their bytes, separators not counted
	seen    int       // the requests this capture took, kept or left out
	size    int64     // the bytes the document on disk holds
	suffix  string    // what closes the document on disk
	behind  bool      // the document on disk is not yet the one kept
	written time.Time // when the document was last written whole while requests kept coming
	sealed  bool
}

// fileNameFor is a capture file's name: the lease, as the farm spells it, and .har.
func fileNameFor(lease string) string { return lease + ".har" }

// createCaptureFile starts lease's capture file in dir: owner-only, refusing one that is already there (a lease captures
// once on a machine; a file left from before a restart is the sweep's, never overwritten).
func createCaptureFile(dir, lease, agentVersion string) (*captureFile, error) {
	if !IsLease(lease) {
		return nil, fmt.Errorf("a capture file is named by a lease, and %q is not one", lease)
	}
	if err := ownerOnlyFolder(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fileNameFor(lease))
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("creating the capture file %s: %w", path, errors.Unwrap(err))
	}
	creator, _ := json.Marshal(map[string]string{
		"name": "nth-agent", "version": agentVersion,
		"comment": "masked on the machine that captured it, before it was written",
	})
	prefix := `{"log":{"version":"1.2","creator":` + string(creator) + `,"pages":[],"entries":[`
	c := &captureFile{path: path, file: file, prefix: prefix, suffix: harSuffix}
	if _, err := file.WriteAt([]byte(prefix+harSuffix), 0); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("writing the capture file %s: %w", path, errors.Unwrap(err))
	}
	c.size = int64(len(prefix) + len(harSuffix))
	return c, nil
}

// closing is what closes the document once it keeps kept of seen requests: the log's comment when some were left out.
func closing(seen, kept int) string {
	if seen <= kept {
		return harSuffix
	}
	note, _ := json.Marshal(leftOutSentence(seen, kept))
	return `],"comment":` + string(note) + `}}`
}

// add takes one masked entry. bodiesLeftOut makes the same entry without its bodies — what is written when the whole one
// does not fit the byte bound. It reports whether the entry is kept: one that does not fit even alone is not, and is not
// counted either.
func (c *captureFile) add(entry []byte, bodiesLeftOut func() []byte, at time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed || c.file == nil {
		return false, nil
	}
	// fits says whether the document still fits its byte bound once the oldest drop entries are left out and candidate is added.
	fits := func(candidate []byte, drop int) bool {
		length, n := c.keptLen+int64(len(candidate)), len(c.kept)-drop+1
		for _, gone := range c.kept[:drop] {
			length -= int64(len(gone))
		}
		return int64(len(c.prefix))+length+int64(n-1)+int64(len(closing(c.seen+1, n))) <= MaxFileBytes
	}
	drop := max(0, len(c.kept)-MaxEntries+1)
	write := entry
	if !fits(write, drop) {
		if write = bodiesLeftOut(); write == nil {
			return false, nil
		}
		for !fits(write, drop) {
			if drop == len(c.kept) {
				return false, nil
			}
			drop++
		}
	}
	appending := drop == 0 && !c.behind && c.seen == len(c.kept)
	for _, gone := range c.kept[:drop] {
		c.keptLen -= int64(len(gone))
	}
	c.kept = append(append(make([][]byte, 0, len(c.kept)-drop+1), c.kept[drop:]...), write)
	c.keptLen += int64(len(write))
	c.seen++
	if appending {
		separator := ""
		if len(c.kept) > 1 {
			separator = ","
		}
		if err := c.writeTail(separator+string(write), harSuffix); err != nil {
			c.behind = true // the document is written whole at the next chance: the stop, or the pull
			return true, err
		}
		return true, nil
	}
	c.behind = true
	if at.Sub(c.written) < rewriteEvery {
		return true, nil
	}
	c.written = at
	return true, c.writeWhole()
}

// writeTail writes text over the closing brackets, then the closing brackets again.
func (c *captureFile) writeTail(text, suffix string) error {
	offset := c.size - int64(len(c.suffix))
	tail := text + suffix
	if _, err := c.file.WriteAt([]byte(tail), offset); err != nil {
		return fmt.Errorf("writing the capture file %s: %w", c.path, errors.Unwrap(err))
	}
	c.size = offset + int64(len(tail))
	c.suffix = suffix
	return nil
}

// writeWhole writes the document kept over the one on disk, and cuts the file to its length.
func (c *captureFile) writeWhole() error {
	suffix := closing(c.seen, len(c.kept))
	doc := make([]byte, 0, int64(len(c.prefix))+c.keptLen+int64(len(c.kept))+int64(len(suffix)))
	doc = append(doc, c.prefix...)
	for i, entry := range c.kept {
		if i > 0 {
			doc = append(doc, ',')
		}
		doc = append(doc, entry...)
	}
	doc = append(doc, suffix...)
	if _, err := c.file.WriteAt(doc, 0); err != nil {
		return fmt.Errorf("writing the capture file %s: %w", c.path, errors.Unwrap(err))
	}
	if int64(len(doc)) < c.size {
		if err := c.file.Truncate(int64(len(doc))); err != nil {
			return fmt.Errorf("writing the capture file %s: %w", c.path, errors.Unwrap(err))
		}
	}
	c.size, c.suffix, c.behind = int64(len(doc)), suffix, false
	return nil
}

// catchUp writes the document whole when the one on disk is behind it.
func (c *captureFile) catchUp() error {
	if !c.behind || c.file == nil {
		return nil
	}
	return c.writeWhole()
}

// held is how many entries the capture keeps.
func (c *captureFile) held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.kept)
}

// snapshot is the document as it stands — what a pull sends — written whole first when the file is behind it. A capture
// that stopped has let its handle go and kept its file for the pull, so the file is read back by its name: a regular file,
// never through a link.
func (c *captureFile) snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.catchUp(); err != nil {
		return nil, err
	}
	file := c.file
	if file == nil {
		opened, err := openRegular(c.path)
		if err != nil {
			return nil, err
		}
		defer opened.Close()
		file = opened
	}
	data := make([]byte, c.size)
	if _, err := file.ReadAt(data, 0); err != nil {
		return nil, fmt.Errorf("reading the capture file %s: %w", c.path, errors.Unwrap(err))
	}
	return data, nil
}

// openRegular opens path for reading only when it is a regular file — not a link, not a folder — and is still the file
// that was looked at when it is opened.
func openRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, fmt.Errorf("%s changed while it was being opened", path)
	}
	return file, nil
}

// seal stops all further writes, the document written whole first: the capture was pulled, or stopped.
func (c *captureFile) seal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.sealed {
		_ = c.catchUp()
	}
	c.sealed = true
}

// remove deletes the file and stops all further writes.
func (c *captureFile) remove() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealed = true
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
	if err := os.Remove(c.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("deleting the capture file %s: %w", c.path, errors.Unwrap(err))
	}
	return nil
}

// closeFile releases the file handle and keeps the file, the document written whole first.
func (c *captureFile) closeFile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.sealed {
		_ = c.catchUp()
	}
	c.sealed = true
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
}

// IsLease reports whether s is a lease id as the farm spells it: a UUID's 36 characters, lower-case hex.
func IsLease(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// LeaseOfFile reports the lease a capture file's name is for — false for any name this agent does not write.
func LeaseOfFile(name string) (string, bool) {
	stem, ok := strings.CutSuffix(name, ".har")
	if !ok || !IsLease(stem) {
		return "", false
	}
	return stem, true
}

// Sweep deletes every capture file in dir whose capture ended at least Retention ago — when its capture is known to
// this process, from the moment it stopped (stoppedAt); otherwise, after a restart, from the file's own last write — and
// never one whose capture is still running (live). It touches nothing else: not a file of another name, not a folder,
// and not a link of any name, which is never followed and never removed. It returns how many it deleted, and never stops
// on a file it cannot delete — the next pass tries again.
func Sweep(dir string, now time.Time, retention time.Duration, known func(lease string) (live bool, stoppedAt time.Time, ok bool)) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	deleted := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue // a folder, a link, a device: never ours to remove
		}
		lease, ok := LeaseOfFile(entry.Name())
		if !ok {
			continue
		}
		ended := time.Time{}
		if known != nil {
			if live, stoppedAt, isKnown := known(lease); isKnown {
				if live {
					continue
				}
				ended = stoppedAt
			}
		}
		if ended.IsZero() {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			ended = info.ModTime()
		}
		if now.Sub(ended) < retention {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err == nil {
			deleted++
		}
	}
	return deleted
}

// RemoveAll deletes every capture file in dir — `capture disable`. Files of its shape only; nothing else.
func RemoveAll(dir string) (deleted int, failed []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if _, ok := LeaseOfFile(entry.Name()); !ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed = append(failed, entry.Name())
			continue
		}
		deleted++
	}
	return deleted, failed
}
