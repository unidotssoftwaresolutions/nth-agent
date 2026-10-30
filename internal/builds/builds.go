// Package builds is A BUILD THE AGENT HAS COUNTED. Appium on this machine installs only a build this agent has fetched,
// measured and checked, and it gets that build as a file.
//
// Appium used to fetch a session's build itself, from the address the farm gave it: with no size cap, into its own cache,
// where it stayed after the lease ended, and — for a .zip on a real iPhone, or an .apks — unpacked on this computer with no
// bound at all. Now the agent fetches the build (Fetch: the farm's own host, the farm's transport rule, no redirect, a byte
// cap and a disk floor), counts it as a zip archive without unpacking anything (Count), and the tunnel hands Appium the file
// it wrote in place of the address. Appium installs a local .ipa or .apk as it is: it neither unpacks it here nor keeps a
// copy of its own.
//
// The files live in the agent's own folder (<config>/nth-agent/builds), one per build, named by the agent. A file is kept
// while the session it was fetched for lives and removed when that session ends — by its DELETE, or by the release that ends
// a session nobody ended — and every one is removed when the agent stops, and by a sweep at the agent's start for anything a
// crash left behind. Only one connect uses a config folder at a time (internal/state), so the sweep removes no other run's
// file.
package builds

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	// MaxBuildBytes is the most one build may be: the platform's own cap on a build a customer hands it (400 MiB; builds are
	// 18–200 MB), so every build the platform accepts is one this agent fetches.
	MaxBuildBytes int64 = 400 << 20
	// MaxEntries is the most files a build may list: the platform's own bound, and the farm's for an unpack.
	MaxEntries = 50_000
	// MaxUnpackedBytes is the most a build may declare its files unpack to in all: the platform's own bound, and the farm's.
	MaxUnpackedBytes int64 = 1 << 30
	// KeepFreeBytes is the free disk a fetch must leave behind it: the farm's own floor for an unpack. Appium, the phones'
	// tools and the next build all write to the same disk, and a machine whose disk a build filled stops every phone on it.
	KeepFreeBytes int64 = 2 << 30
	// FetchWithin bounds one fetch: the forward bound a frame's answer is waited for (the tunnel's AppiumForwardTimeout,
	// 200 s). Past it the farm has stopped waiting for the session the build is for.
	FetchWithin = 200 * time.Second
)

// Options are the shelf's seams. Each zero value is what the agent uses.
type Options struct {
	// Client fetches builds; nil = the farm's transport rule (farm.Proxy) with TLS. Whatever it is, its redirect policy is
	// replaced: no redirect is followed.
	Client *http.Client
	// TLS is the TLS a fetch uses when Client is nil: the tunnel's own (nil = the system's).
	TLS *tls.Config
	// MaxBytes is the byte cap; zero = MaxBuildBytes.
	MaxBytes int64
	// FreeBytes says how much disk is free for this account where dir is; nil = the file system's own answer.
	FreeBytes func(dir string) (uint64, error)
}

// Shelf is the agent's folder of builds.
type Shelf struct {
	dir       string
	client    *http.Client
	maxBytes  int64
	freeBytes func(string) (uint64, error)

	mu       sync.Mutex
	reserved int64
	wrote    map[string]bool     // every file this shelf wrote and has not removed
	held     map[string][]string // session id → the files fetched for it
}

// ours is every name the shelf writes: 32 hex digits and the build's extension, and ".part" while it arrives.
var ours = regexp.MustCompile(`^[0-9a-f]{32}\.(?:ipa|apk)(?:\.part)?$`)

// Open makes the shelf in configDir/nth-agent/builds (this account's only), and sweeps it: every file of the agent's own a
// crash left behind is removed, and nothing else is touched. It answers how many it removed.
func Open(configDir string, o Options) (*Shelf, int, error) {
	dir := filepath.Join(configDir, "nth-agent", "builds")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, 0, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, 0, err
	}
	s := &Shelf{dir: dir, maxBytes: o.MaxBytes, freeBytes: o.FreeBytes, wrote: map[string]bool{}, held: map[string][]string{}}
	if s.maxBytes <= 0 {
		s.maxBytes = MaxBuildBytes
	}
	if s.freeBytes == nil {
		s.freeBytes = freeBytesAt
	}
	s.client = fetchClient(o.Client, o.TLS)
	swept := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if ours.MatchString(e.Name()) && !e.IsDir() && os.Remove(filepath.Join(dir, e.Name())) == nil {
			swept++
		}
	}
	return s, swept, nil
}

// Dir is the shelf's folder.
func (s *Shelf) Dir() string { return s.dir }

// Wrote says whether path is a file this shelf wrote and has not removed.
func (s *Shelf) Wrote(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wrote[path]
}

// Hold keeps paths for as long as session lives.
func (s *Shelf) Hold(session string, paths []string) {
	if len(paths) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[session] = append(s.held[session], paths...)
}

// Drop removes every file held for session, which has ended, and answers how many it removed.
func (s *Shelf) Drop(session string) int {
	s.mu.Lock()
	paths := s.held[session]
	delete(s.held, session)
	s.mu.Unlock()
	return s.remove(paths)
}

// Forget removes each of paths that no session holds: a build fetched for a request that did not start a session.
func (s *Shelf) Forget(paths []string) {
	s.mu.Lock()
	var loose []string
	for _, p := range paths {
		held := false
		for _, list := range s.held {
			for _, h := range list {
				held = held || h == p
			}
		}
		if !held {
			loose = append(loose, p)
		}
	}
	s.mu.Unlock()
	s.remove(loose)
}

// RemoveAll removes every file the shelf wrote, held or not: the agent is stopping.
func (s *Shelf) RemoveAll() int {
	s.mu.Lock()
	var all []string
	for p := range s.wrote {
		all = append(all, p)
	}
	s.held = map[string][]string{}
	s.mu.Unlock()
	return s.remove(all)
}

func (s *Shelf) remove(paths []string) int {
	removed := 0
	for _, p := range paths {
		s.mu.Lock()
		mine := s.wrote[p]
		delete(s.wrote, p)
		s.mu.Unlock()
		if mine && os.Remove(p) == nil {
			removed++
		}
	}
	return removed
}

// reserve holds room for a build of size bytes: what the disk has free, less what fetches in flight have held, must leave
// KeepFreeBytes after it. It answers how to give the room back, or the refusal's reason.
func (s *Shelf) reserve(size int64) (func(), string) {
	free, err := s.freeBytes(s.dir)
	if err != nil {
		return nil, "this agent could not tell how much free disk this computer has"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if free > 1<<62 {
		free = 1 << 62
	}
	if int64(free)-s.reserved-size < KeepFreeBytes {
		return nil, "fetching it would leave this computer less than " + sizeInWords(KeepFreeBytes) + " of free disk"
	}
	s.reserved += size
	return func() {
		s.mu.Lock()
		s.reserved -= size
		s.mu.Unlock()
	}, ""
}

// newName is a name of the agent's choosing for a build of kind.
func newName(kind string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + kind, nil
}

// sizeInWords says a bound as a person reads it.
func sizeInWords(bytes int64) string {
	switch {
	case bytes%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", bytes>>30)
	case bytes%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", bytes>>20)
	case bytes%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", bytes>>10)
	}
	return fmt.Sprintf("%d bytes", bytes)
}
