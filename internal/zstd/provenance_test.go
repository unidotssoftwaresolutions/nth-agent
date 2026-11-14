package zstd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// ── THIS IS THE GO TOOLCHAIN'S OWN zstd DECODER, BYTE FOR BYTE ──────────────────────────────────────────────────
//
// WHY IT IS HERE. A capture decodes zstd bodies from whatever hosts a test phone talks to, on the customer's machine,
// in the process that holds the capture CA's key. The Go standard library has a zstd decoder — src/internal/zstd, written
// by the Go team for debug/elf, pure Go, no unsafe, no assembly, fuzzed against the reference zstd — but an internal
// package cannot be imported. The obvious module (github.com/klauspost/compress) decodes with some seven thousand lines of
// amd64 and arm64 assembly, and had an out-of-bounds write in that assembly fixed in v1.20.0 and another bounds fix in
// v1.20.1 (2026-09); assembly is outside what Go's memory safety covers. So the agent carries the toolchain's decoder,
// verbatim, with Go's LICENSE and PATENTS beside it, and no module is added for zstd.
//
// THIS SEAL KEEPS IT VERBATIM: every file is byte-equal to the toolchain's own, and the two sets of files are the same. A
// Go upgrade that changes the decoder turns this red, and the fix is to copy the toolchain's files again — never to edit
// them here, and never to stop comparing.

func toolchainZstd(t *testing.T) string {
	t.Helper()
	goroot := runtime.GOROOT()
	if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		goroot = strings.TrimSpace(string(out))
	}
	dir := filepath.Join(goroot, "src", "internal", "zstd")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the toolchain's own decoder is not at %s, so this copy cannot be compared with it: %v", dir, err)
	}
	return dir
}

func goSources(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestThisIsTheToolchainsOwnDecoderByteForByte(t *testing.T) {
	theirs := toolchainZstd(t)
	ours := goSources(t, ".")
	if strings.Join(ours, " ") != strings.Join(goSources(t, theirs), " ") {
		t.Fatalf("the files here (%v) are not the toolchain's (%v) — copy the toolchain's decoder again", ours, goSources(t, theirs))
	}
	if len(ours) < 8 {
		t.Fatalf("the walk must have read the decoder, found %v", ours)
	}
	for _, name := range ours {
		mine, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(filepath.Join(theirs, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(mine, original) {
			t.Errorf("%s differs from %s's src/internal/zstd/%s — copy it again from the toolchain; it is never edited here",
				name, runtime.Version(), name)
		}
	}
	for _, notice := range []string{"LICENSE", "PATENTS"} {
		if _, err := os.Stat(notice); err != nil {
			t.Errorf("Go's %s travels with its code: %v", notice, err)
		}
	}
}

// AND THE NOTICES BESIDE IT ARE THE TOOLCHAIN'S OWN, byte for byte (AN_AGENT_THAT_SHIPS_ITS_NOTICES): THIRD_PARTY_NOTICES.txt
// reads Go's licence from these two files, for this decoder and for the standard library every binary carries. Go's own
// distribution keeps them in GOROOT; Homebrew's go keeps PATENTS there and moves LICENSE up to the keg, beside GOROOT
// (measured: /opt/homebrew/Cellar/go/1.27.0/LICENSE beside libexec). Both places are read, and neither is guessed at: a
// toolchain with LICENSE in neither fails here, by name.
func TestTheLicenceAndPatentsBesideItAreTheToolchainsOwn(t *testing.T) {
	goroot := filepath.Dir(filepath.Dir(filepath.Dir(toolchainZstd(t)))) // GOROOT/src/internal/zstd → GOROOT
	for _, notice := range []string{"LICENSE", "PATENTS"} {
		mine, err := os.ReadFile(notice)
		if err != nil {
			t.Fatal(err)
		}
		var theirs []byte
		var where []string
		for _, dir := range []string{goroot, filepath.Dir(goroot)} {
			path := filepath.Join(dir, notice)
			where = append(where, path)
			if data, err := os.ReadFile(path); err == nil {
				theirs = data
				break
			}
		}
		if theirs == nil {
			t.Fatalf("the toolchain carries no %s where Go's distribution or Homebrew puts it (%v), so this copy cannot be "+
				"compared with it", notice, where)
		}
		if !bytes.Equal(mine, theirs) {
			t.Errorf("internal/zstd/%s differs from the toolchain's — copy it again; the notices quote it", notice)
		}
	}
}
