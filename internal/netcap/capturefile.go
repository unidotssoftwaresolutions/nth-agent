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
// IT IS A WHOLE HAR AFTER EVERY ENTRY, not only at the end: each entry is written over the document's closing brackets,
// which are written again after it, so a pull in the middle of a capture reads a document that parses.
//
// TWO BOUNDS, and why each:
//   - 500 ENTRIES (MaxEntries) — the runner keeps at most 500 per run (NetworkCaptureOptions.MaxEntriesPerRun), so an entry
//     past it would cross the network to be dropped on arrival. Past it nothing more is written, and the document says so.
//   - 16 MB (MaxFileBytes) — the runner reads at most 48 MB of a capture and gives the pull 30 seconds
//     (NetworkCapturePull.DownloadCapBytes, PullTimeout), and this file crosses the customer's own uplink to get there. Past
//     the bound an entry keeps its method, address, status, headers and sizes, and its bodies are left out with the reason.

const (
	// MaxEntries is how many exchanges one capture records — the runner's own per-run bound.
	MaxEntries = 500
	// MaxFileBytes is the most one capture file holds — a third of what the runner reads, so the pull through the customer's
	// uplink fits inside the runner's 30-second pull bound from about 4.3 Mbit/s up (128 Mbit in 30 s).
	MaxFileBytes = 16 << 20

	harSuffix = "]}}"
)

// captureFile is one capture's masked HAR on disk.
type captureFile struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	entries int
	suffix  string
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
	c := &captureFile{path: path, file: file, suffix: harSuffix}
	if _, err := file.WriteAt([]byte(prefix+harSuffix), 0); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("writing the capture file %s: %w", path, errors.Unwrap(err))
	}
	c.size = int64(len(prefix) + len(harSuffix))
	return c, nil
}

// add writes one masked entry. bodiesLeftOut makes the same entry without its bodies — what is written once the file
// is at its byte bound. It reports whether anything was written.
func (c *captureFile) add(entry []byte, bodiesLeftOut func() []byte, at time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed || c.file == nil {
		return false, nil
	}
	if c.entries >= MaxEntries {
		return false, nil
	}
	write := entry
	if c.size+int64(len(entry))+1 > MaxFileBytes {
		write = bodiesLeftOut()
		if write == nil || c.size+int64(len(write))+1 > MaxFileBytes {
			return false, nil
		}
	}
	separator := ""
	if c.entries > 0 {
		separator = ","
	}
	c.entries++
	suffix := c.suffix
	if c.entries == MaxEntries {
		// THE DOCUMENT SAYS WHERE IT STOPPED: HAR 1.2's own comment on the log.
		note, _ := json.Marshal(fmt.Sprintf("this capture reached its bound of %d requests at %s; requests after that were "+
			"not recorded", MaxEntries, at.UTC().Format(time.RFC3339)))
		suffix = `],"comment":` + string(note) + `}}`
	}
	if err := c.writeTail(separator+string(write), suffix); err != nil {
		return false, err
	}
	return true, nil
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

// snapshot is the document as it stands — what a pull sends. A capture that stopped has let its handle go and kept its
// file for the pull, so the file is read back by its name: a regular file, never through a link.
func (c *captureFile) snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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

// seal stops all further writes: the capture was pulled, or stopped.
func (c *captureFile) seal() {
	c.mu.Lock()
	defer c.mu.Unlock()
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

// closeFile releases the file handle and keeps the file.
func (c *captureFile) closeFile() {
	c.mu.Lock()
	defer c.mu.Unlock()
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
