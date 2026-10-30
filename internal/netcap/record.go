package netcap

import (
	"fmt"
	"sync"
	"time"
)

// ── ONE CAPTURE'S RECORD ────────────────────────────────────────────────────────────────────────────────────────
//
// What the proxy hands a flow to. Record masks it IN MEMORY (entryFor) and only then writes it: the capture file is the
// one place an exchange lands, and it lands masked. A write that fails — a full disk — ends the recording (the file keeps
// what it had); the phone's traffic is unaffected, because recording is never in the way of forwarding.

// Recording is one capture's record.
type Recording struct {
	file *captureFile
	now  func() time.Time

	mu       sync.Mutex
	refused  map[string]bool // hosts already recorded as refusing the certificate: each is recorded once
	failure  error
	recorded int // the requests taken, kept or left out (Seen)
}

// NewRecording starts lease's record in dir. agentVersion names the build that wrote it.
func NewRecording(dir, lease, agentVersion string, now func() time.Time) (*Recording, error) {
	if now == nil {
		now = time.Now
	}
	file, err := createCaptureFile(dir, lease, agentVersion)
	if err != nil {
		return nil, err
	}
	return &Recording{file: file, now: now, refused: map[string]bool{}}, nil
}

// Record masks one exchange and writes it.
func (r *Recording) Record(f Flow) {
	if r == nil {
		return
	}
	entry := entryFor(f)
	r.write(entry, func() harEntry { return withoutBodies(entry) })
}

// RecordRefusal records, ONCE per host for the capture, that a client refused the capture certificate there.
func (r *Recording) RecordRefusal(hostPort string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.refused[hostPort] {
		r.mu.Unlock()
		return
	}
	r.refused[hostPort] = true
	r.mu.Unlock()
	entry := refusalEntry(r.now(), hostPort)
	r.write(entry, func() harEntry { return entry })
}

// RecordLocalRefusal records, ONCE per host for the capture, that a tunnel the phone asked for was refused because it led to
// this machine or its local network (destination.go).
func (r *Recording) RecordLocalRefusal(hostPort string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	key := "local " + hostPort
	if r.refused[key] {
		r.mu.Unlock()
		return
	}
	r.refused[key] = true
	r.mu.Unlock()
	entry := refusalEntry(r.now(), hostPort)
	entry.Response.Error = RefusedLocal()
	r.write(entry, func() harEntry { return entry })
}

func (r *Recording) write(entry harEntry, bodiesLeftOut func() harEntry) {
	encoded, err := encodeEntry(entry)
	if err != nil {
		return
	}
	written, err := r.file.add(encoded, func() []byte {
		fallback, err := encodeEntry(bodiesLeftOut())
		if err != nil {
			return nil
		}
		return fallback
	}, r.now())
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil && r.failure == nil {
		r.failure = err
		r.file.seal()
	}
	if written {
		r.recorded++
	}
}

// Recorded is how many entries the capture keeps: the last MaxEntries at most.
func (r *Recording) Recorded() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.held()
}

// Failure is why the recording stopped writing, or nil.
func (r *Recording) Failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failure
}

// Snapshot is the masked HAR as it stands.
func (r *Recording) Snapshot() ([]byte, error) { return r.file.snapshot() }

// Seal stops all further writes and keeps the file.
func (r *Recording) Seal() { r.file.seal() }

// Close stops all further writes, releases the file and keeps it — the capture stopped, and the file waits for its pull
// or its hour.
func (r *Recording) Close() { r.file.closeFile() }

// Remove deletes the file: it was pulled, or the capture is being thrown away.
func (r *Recording) Remove() error { return r.file.remove() }

// Path is where the file is.
func (r *Recording) Path() string { return r.file.path }

// withoutBodies is an entry with its bodies left out — what is written once the file is at its byte bound.
func withoutBodies(entry harEntry) harEntry {
	reason := fmt.Sprintf("the body was left out: this capture reached the %d MB this agent records for one run",
		MaxFileBytes>>20)
	if entry.Request.PostData != nil {
		post := *entry.Request.PostData
		if post.Text != "" {
			post.Text, post.Comment = "", reason
		}
		entry.Request.PostData = &post
	}
	if entry.Response.Content.Text != "" {
		entry.Response.Content.Text, entry.Response.Content.Comment = "", reason
	}
	return entry
}

// Seen is how many requests the capture took, kept or left out.
func (r *Recording) Seen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recorded
}
