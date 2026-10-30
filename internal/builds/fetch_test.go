package builds

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// A BUILD IT HAS COUNTED: THE FETCH. The agent fetches the build itself, over the farm's transport rule, from the farm's own
// host and nowhere else, with no redirect followed, into its own folder, with a byte cap and a disk floor, and counts it before
// anything else may read it. Every seal here answers from memory: nothing dials the farm, Appium, a phone or the network.

const (
	theFarm  = "https://farm.nthcase.com"
	ipaAt    = "https://farm.nthcase.com/app-cache/4f9c0a8b7e6d5c4b3a29181706f5e4d3c2b1a09f8e7d6c5b4a3928170605f4e3.ipa?grant=1761480000.AbCdEf0123456789"
	apkAt    = "https://farm.nthcase.com/app-cache/4f9c0a8b7e6d5c4b3a29181706f5e4d3c2b1a09f8e7d6c5b4a3928170605f4e3.apk?grant=1761480000.AbCdEf0123456789"
	plenty   = uint64(64) << 30
	smallCap = int64(64 << 10)
)

// stubFarm answers from memory, records every request, and dials nothing.
type stubFarm struct {
	mu       sync.Mutex
	asked    []*http.Request
	answer   func(*http.Request) (*http.Response, error)
	bodyRead int64
}

func (s *stubFarm) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.asked = append(s.asked, r)
	s.mu.Unlock()
	return s.answer(r)
}

// counting is a body that counts what was read of it.
type counting struct {
	io.Reader
	farm *stubFarm
}

func (c counting) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.farm.mu.Lock()
	c.farm.bodyRead += int64(n)
	c.farm.mu.Unlock()
	return n, err
}
func (c counting) Close() error { return nil }

func serving(data []byte, header http.Header, length int64) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if header == nil {
			header = http.Header{}
		}
		return &http.Response{StatusCode: 200, Header: header, ContentLength: length, Request: r,
			Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	}
}

func shelfFor(t *testing.T, farm *stubFarm, free uint64, maxBytes int64) *Shelf {
	t.Helper()
	shelf, _, err := Open(t.TempDir(), Options{
		Client:    &http.Client{Transport: farm},
		FreeBytes: func(string) (uint64, error) { return free, nil },
		MaxBytes:  maxBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return shelf
}

func leftIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func refusedSaying(t *testing.T, what, path, refusal string, says ...string) {
	t.Helper()
	if path != "" || refusal == "" {
		t.Fatalf("%s: must be refused, got the file %q", what, path)
	}
	for _, s := range says {
		if !strings.Contains(refusal, s) {
			t.Fatalf("%s: the refusal must say %q: %q", what, s, refusal)
		}
	}
	if !strings.Contains(refusal, "nothing was sent to Appium") {
		t.Fatalf("%s: the refusal must end as every allow-list refusal does: %q", what, refusal)
	}
}

var fileName = regexp.MustCompile(`^[0-9a-f]{32}\.(ipa|apk)$`)

// A legitimate .ipa or .apk is written to the agent's own folder, under a name of the agent's choosing with the build's own
// extension, readable by this account only, byte for byte what the farm sent — and fetched without the agent's token, from
// the address the farm gave, once.
func TestALegitimateBuildBecomesTheAgentsOwnFile(t *testing.T) {
	for _, address := range []string{ipaAt, apkAt} {
		build := rawZip{entries: anApp()}.bytes()
		farm := &stubFarm{answer: serving(build, http.Header{"Content-Type": {"application/octet-stream"}}, int64(len(build)))}
		shelf := shelfFor(t, farm, plenty, 0)
		path, refusal := shelf.Fetch(context.Background(), address, theFarm)
		if refusal != "" {
			t.Fatalf("%s: a legitimate build must be fetched, got %q", address, refusal)
		}
		if filepath.Dir(path) != shelf.Dir() || !fileName.MatchString(filepath.Base(path)) || filepath.Ext(path) != filepath.Ext(strings.SplitN(address, "?", 2)[0]) {
			t.Fatalf("%s: the file must be the agent's own, named by it with the build's extension: %s", address, path)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(build) {
			t.Fatalf("%s: the file must hold exactly what the farm sent (%v)", address, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: the file must be readable by this account only, got %v", address, info.Mode().Perm())
		}
		if len(farm.asked) != 1 || farm.asked[0].URL.String() != address || farm.asked[0].Method != http.MethodGet {
			t.Fatalf("%s: one GET of the address the farm gave, got %+v", address, farm.asked)
		}
		for name := range farm.asked[0].Header {
			if strings.EqualFold(name, "X-Agent-Token") || strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Cookie") {
				t.Fatalf("%s: a build's address is its own grant, and the fetch carries no credential: %v", address, farm.asked[0].Header)
			}
		}
		if names := leftIn(t, shelf.Dir()); len(names) != 1 {
			t.Fatalf("%s: the folder must hold the one file, got %v", address, names)
		}
	}
}

func TestABuildPastTheCapIsRefused(t *testing.T) {
	build := rawZip{entries: anApp()}.bytes()
	big := make([]byte, smallCap+1)
	copy(big, build)
	t.Run("the farm says its size, and it is past the cap: refused before a byte is read", func(t *testing.T) {
		farm := &stubFarm{}
		farm.answer = func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: smallCap + 1, Request: r,
				Body: counting{strings.NewReader(string(big)), farm}}, nil
		}
		shelf := shelfFor(t, farm, plenty, smallCap)
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		refusedSaying(t, "a stated size past the cap", path, refusal, "larger than 64 KiB")
		if farm.bodyRead != 0 {
			t.Fatalf("not a byte of a build past the cap may be read, read %d", farm.bodyRead)
		}
		if names := leftIn(t, shelf.Dir()); len(names) != 0 {
			t.Fatalf("nothing may be left behind, got %v", names)
		}
	})
	t.Run("the farm says no size, and sends past the cap: stopped one byte past it", func(t *testing.T) {
		farm := &stubFarm{}
		farm.answer = func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: -1, Request: r,
				Body: counting{strings.NewReader(string(big) + strings.Repeat("x", 1<<20)), farm}}, nil
		}
		shelf := shelfFor(t, farm, plenty, smallCap)
		path, refusal := shelf.Fetch(context.Background(), apkAt, theFarm)
		refusedSaying(t, "a stream past the cap", path, refusal, "larger than 64 KiB")
		if farm.bodyRead > smallCap+64<<10 {
			t.Fatalf("the read must stop just past the cap, read %d", farm.bodyRead)
		}
		if names := leftIn(t, shelf.Dir()); len(names) != 0 {
			t.Fatalf("nothing may be left behind, got %v", names)
		}
	})
	t.Run("the platform's own cap on a build is the default", func(t *testing.T) {
		if MaxBuildBytes != 400<<20 {
			t.Fatalf("the cap is the platform's own cap on a build, 400 MiB: %d", MaxBuildBytes)
		}
		farm := &stubFarm{answer: serving(nil, nil, MaxBuildBytes+1)}
		shelf := shelfFor(t, farm, plenty, 0)
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		refusedSaying(t, "a stated size past 400 MiB", path, refusal, "larger than 400 MiB")
	})
}

func TestADiskThatWouldRunOutIsRefused(t *testing.T) {
	build := rawZip{entries: anApp()}.bytes()
	size := uint64(len(build))
	for name, c := range map[string]struct {
		free   uint64
		length int64
		pass   bool
	}{
		"exactly the floor left after a stated size":       {uint64(KeepFreeBytes) + size, int64(len(build)), true},
		"one byte under the floor after a stated size":     {uint64(KeepFreeBytes) + size - 1, int64(len(build)), false},
		"no size stated: the cap is what must fit":         {uint64(KeepFreeBytes) + uint64(smallCap), -1, true},
		"no size stated, and the cap would not fit":        {uint64(KeepFreeBytes) + uint64(smallCap) - 1, -1, false},
		"a disk with less free than the floor itself":      {uint64(KeepFreeBytes) - 1, int64(len(build)), false},
		"a disk that cannot be measured (free reads zero)": {0, int64(len(build)), false},
	} {
		t.Run(name, func(t *testing.T) {
			farm := &stubFarm{answer: serving(build, nil, c.length)}
			shelf := shelfFor(t, farm, c.free, smallCap)
			path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
			if c.pass {
				if refusal != "" {
					t.Fatalf("must be fetched, got %q", refusal)
				}
				return
			}
			refusedSaying(t, name, path, refusal, "less than 2 GiB of free disk")
			if names := leftIn(t, shelf.Dir()); len(names) != 0 {
				t.Fatalf("nothing may be left behind, got %v", names)
			}
		})
	}
	t.Run("a disk whose free space cannot be read", func(t *testing.T) {
		farm := &stubFarm{answer: serving(build, nil, int64(len(build)))}
		shelf, _, _ := Open(t.TempDir(), Options{Client: &http.Client{Transport: farm},
			FreeBytes: func(string) (uint64, error) { return 0, errors.New("statfs: not here") }})
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		refusedSaying(t, "an unmeasured disk", path, refusal, "could not tell how much free disk")
	})
	t.Run("two fetches at once each hold their room", func(t *testing.T) {
		farm := &stubFarm{answer: serving(build, nil, int64(len(build)))}
		shelf := shelfFor(t, farm, uint64(KeepFreeBytes)+size+size/2, smallCap)
		release, refusal := shelf.reserve(int64(size))
		if refusal != "" {
			t.Fatalf("the first room must be held, got %q", refusal)
		}
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		refusedSaying(t, "a second fetch while the first holds its room", path, refusal, "less than 2 GiB of free disk")
		release()
		if _, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm); refusal != "" {
			t.Fatalf("once the first room is given back, the fetch must pass, got %q", refusal)
		}
	})
}

// No redirect is followed, wherever it points: off the farm's host above all, and on it too — the farm's build route and its
// front door never redirect, so one is an address nobody checked.
func TestARedirectIsNotFollowed(t *testing.T) {
	for name, location := range map[string]string{
		"off the farm's host":        "https://builds.example.com/app.ipa",
		"to plain http":              "http://farm.nthcase.com/app-cache/x.ipa",
		"to a file on this computer": "file:///Users/ada/app.ipa",
		"on the farm's own host":     theFarm + "/app-cache/other.ipa?grant=1.x",
	} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			farm := &stubFarm{}
			farm.answer = func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != ipaAt {
					t.Errorf("%s: the agent followed a redirect to %s", name, r.URL)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {location}}, Request: r,
					Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			shelf := shelfFor(t, farm, plenty, 0)
			path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
			refusedSaying(t, name+" ("+strconv.Itoa(status)+")", path, refusal, "redirect", "does not follow")
			if len(farm.asked) != 1 {
				t.Fatalf("%s: one request, to the farm's address, got %d", name, len(farm.asked))
			}
		}
	}
}

// What the farm's answer says the file is counts too, and the stricter of the address and the answer wins.
func TestAnAnswerThatSaysZipOrApksIsRefused(t *testing.T) {
	build := rawZip{entries: anApp()}.bytes()
	for name, header := range map[string]http.Header{
		"a zip content type":               {"Content-Type": {"application/zip"}},
		"another zip content type":         {"Content-Type": {"Application/X-Zip-Compressed; charset=binary"}},
		"a file name that is a .zip":       {"Content-Disposition": {`attachment; filename="Lantern.zip"`}},
		"a file name that is an .apks":     {"Content-Disposition": {`attachment; filename="lantern.APKS"`}},
		"an inline file name, still a zip": {"Content-Disposition": {`inline; filename=lantern.zip`}},
	} {
		farm := &stubFarm{answer: serving(build, header, int64(len(build)))}
		shelf := shelfFor(t, farm, plenty, 0)
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		refusedSaying(t, name, path, refusal, "the farm's answer says", "no phone this agent drives installs one")
		if names := leftIn(t, shelf.Dir()); len(names) != 0 {
			t.Fatalf("%s: nothing may be left behind, got %v", name, names)
		}
	}
	// What the platform serves today passes: an .apk's own type, and bytes with no type of their own.
	for _, contentType := range []string{"application/vnd.android.package-archive", "application/octet-stream", ""} {
		farm := &stubFarm{answer: serving(build, http.Header{"Content-Type": {contentType}}, int64(len(build)))}
		if _, refusal := shelfFor(t, farm, plenty, 0).Fetch(context.Background(), apkAt, theFarm); refusal != "" {
			t.Fatalf("%q must pass, got %q", contentType, refusal)
		}
	}
}

func TestWhatIsNotABuildFromTheFarmIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	for name, address := range map[string]string{
		"another host":         "https://builds.example.com/app-cache/x.ipa",
		"plain http":           "http://farm.nthcase.com/app-cache/x.ipa",
		"a path":               "/Users/ada/app.ipa",
		"a .zip on the farm":   "https://farm.nthcase.com/app-cache/x.zip?grant=1.x",
		"an .apks on the farm": "https://farm.nthcase.com/app-cache/x.apks?grant=1.x",
	} {
		farm := &stubFarm{answer: serving(nil, nil, 0)}
		path, refusal := shelfFor(t, farm, plenty, 0).Fetch(context.Background(), address, theFarm)
		refusedSaying(t, name, path, refusal)
		if len(farm.asked) != 0 {
			t.Fatalf("%s: nothing may be asked of anyone, got %d requests", name, len(farm.asked))
		}
	}
}

func TestAnAnswerThatIsNotTheBuildIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		answer func(*http.Request) (*http.Response, error)
		says   string
	}{
		"the address's window has closed (410)": {func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 410, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader("closed"))}, nil
		}, "HTTP 410"},
		"the farm is not there":      {func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }, "could not fetch it"},
		"bytes that are not a build": {serving([]byte(strings.Repeat("not a zip ", 100)), nil, 1000), "it is not a zip archive"},
		"a build with a file its list does not name": {serving(rawZip{entries: anApp(),
			beforeList: []byte("PK\x03\x04 hidden")}.bytes(), nil, -1), "its list of files does not name"},
	} {
		farm := &stubFarm{answer: c.answer}
		shelf := shelfFor(t, farm, plenty, 0)
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		refusedSaying(t, name, path, refusal, c.says)
		if names := leftIn(t, shelf.Dir()); len(names) != 0 {
			t.Fatalf("%s: nothing may be left behind, got %v", name, names)
		}
	}
}

// ── CLEAN-UP ─────────────────────────────────────────────────────────────────────────────────────────────────────────

// The sweep at the agent's start removes every file of its own a crash left behind, and nothing else.
func TestTheSweepAtStartRemovesWhatACrashLeft(t *testing.T) {
	config := t.TempDir()
	dir := filepath.Join(config, "nth-agent", "builds")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ours := []string{"0123456789abcdef0123456789abcdef.ipa", "fedcba9876543210fedcba9876543210.apk", "00112233445566778899aabbccddeeff.apk.part"}
	notOurs := []string{"notes.txt", "0123456789abcdef0123456789abcdef.zip"}
	for _, name := range append(append([]string(nil), ours...), notOurs...) {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	}
	shelf, swept, err := Open(config, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if swept != len(ours) || shelf.Dir() != dir {
		t.Fatalf("the sweep must remove the %d files of its own, removed %d (folder %s)", len(ours), swept, shelf.Dir())
	}
	left := leftIn(t, dir)
	if strings.Join(left, ",") != strings.Join([]string{"0123456789abcdef0123456789abcdef.zip", "notes.txt"}, ",") {
		t.Fatalf("only what the agent writes may be removed, left %v", left)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("the folder is this account's only: %v", info.Mode().Perm())
	}
}

// A file is kept while the session it was fetched for lives, and removed when that session ends; one no session holds is
// removed at once; and everything goes when the agent stops.
func TestAFileLivesExactlyAsLongAsItsSession(t *testing.T) {
	build := rawZip{entries: anApp()}.bytes()
	farm := &stubFarm{answer: serving(build, nil, int64(len(build)))}
	shelf := shelfFor(t, farm, plenty, 0)
	fetch := func() string {
		path, refusal := shelf.Fetch(context.Background(), ipaAt, theFarm)
		if refusal != "" {
			t.Fatal(refusal)
		}
		return path
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	a, b, c, d := fetch(), fetch(), fetch(), fetch()
	shelf.Hold("session-a", []string{a})
	shelf.Hold("session-b", []string{b})
	shelf.Forget([]string{a, c}) // the create for c failed; a is held
	if !exists(a) || exists(c) {
		t.Fatalf("a held file stays, one nothing holds goes: a %v, c %v", exists(a), exists(c))
	}
	if n := shelf.Drop("session-a"); n != 1 || exists(a) || !exists(b) {
		t.Fatalf("ending session-a removes its file and no other: removed %d, a %v, b %v", n, exists(a), exists(b))
	}
	if n := shelf.Drop("session-a"); n != 0 {
		t.Fatalf("a session ended twice removes nothing the second time, removed %d", n)
	}
	if n := shelf.RemoveAll(); n != 2 || exists(b) || exists(d) {
		t.Fatalf("the agent's stop removes every file left: removed %d", n)
	}
	// Wrote is about what the shelf wrote and still holds: after the stop it holds nothing.
	if shelf.Wrote(b) {
		t.Fatalf("a removed file is no longer the agent's to hand over")
	}
}
