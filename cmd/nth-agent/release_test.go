package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A_BINARY_NOBODY_CAN_DOWNLOAD — the release exists, and it must not ship the defect it was written to remove.
//
// `var version = "dev"` has carried a comment saying it is stamped at build time since the day it was written,
// and nothing in this repository ever passed those ldflags: every binary anybody has ever run reports `dev`.
// `--version` is wired, is covered by the CLI's own question-vs-mistake sweep, and answers with a default nobody
// has ever displaced. A release that ships that is the same defect with a download button on it.

func releaseWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the release workflow: %v — a release nobody can find is the defect this file seals", err)
	}
	return string(data)
}

func TestTheReleaseStampsTheVersionFromTheTag(t *testing.T) {
	workflow := releaseWorkflow(t)

	// INSTRUMENT GUARD: it must really be the release workflow, or every assertion below passes on a typo.
	if !strings.Contains(workflow, "go build") {
		t.Fatal("instrument guard: the file read does not build anything — the seal is reading the wrong thing")
	}

	// ⚠️ THE STAMP ITSELF. The variable is `main.version` and the mechanism is `-X`; both are what cli.go's own
	// comment documents, and neither has ever been driven.
	if !strings.Contains(workflow, "-X main.version=") {
		t.Fatal("the release does not stamp the version: every binary it ships would report `dev`, which is the " +
			"defect this workflow exists to remove, with a download button on it")
	}

	// …AND FROM THE TAG, not from a literal somebody keeps in step by hand. A hard-coded version is a count in a
	// comment wearing a build flag: right on the day it is typed and wrong at the next release.
	//
	// ⚠️ EVERY OCCURRENCE, NOT THE FIRST. The first version of this seal used FindStringSubmatch and matched the
	// WORKFLOW'S OWN COMMENT — which names `-X main.version=` while explaining it — and reported the backtick
	// after it as a literal version. A seal that reads prose and calls it a command is the shape this programme
	// keeps deleting; what it must find is a stamp that is substituted, wherever in the file it lives.
	stamp := regexp.MustCompile(`-X main\.version=([^"'\s]+)`)
	matches := stamp.FindAllStringSubmatch(workflow, -1)
	if len(matches) == 0 {
		t.Fatal("could not read the stamped value out of the workflow")
	}
	substituted := false
	var literals []string
	for _, match := range matches {
		if strings.Contains(match[1], "$") {
			substituted = true
		} else {
			literals = append(literals, match[1])
		}
	}
	if !substituted {
		t.Fatalf("the release stamps only literal version(s) %v — the value must come from the tag, or the next "+
			"release ships the last one's number", literals)
	}

	// The tag is what triggers it, so the tag is what it may name.
	if !strings.Contains(workflow, `- "v*"`) {
		t.Fatal("the release must run on a tag; nothing else can tell it which version it is")
	}
}

func TestTheReleaseShipsChecksumsBesideTheBinaries(t *testing.T) {
	// A person is about to run this against their own phones. This is not a signature and does not claim to be —
	// it proves the file you have is the file the release page lists, which is the honest minimum and one line.
	workflow := releaseWorkflow(t)

	if !strings.Contains(workflow, "SHA256SUMS") {
		t.Fatal("no SHA256SUMS is published beside the archives")
	}
	if !strings.Contains(workflow, "shasum -a 256") {
		t.Fatal("the checksums must be produced in the format `shasum -a 256 -c` reads, so verifying is one command")
	}
}

func TestTheReleaseSaysWhatAnUnsignedBinaryDoesOnMacOS(t *testing.T) {
	// ⚠️ THEY WILL SEE SOMETHING, so the release notes say what. Gatekeeper quarantines an unsigned download and
	// refuses to open it; a person who is not told that concludes the binary is broken. Signing and notarization
	// need an Apple Developer ID certificate and a notarization submission — both credentials, neither in scope.
	workflow := releaseWorkflow(t)

	if !strings.Contains(workflow, "com.apple.quarantine") {
		t.Fatal("the release notes must tell a macOS user what they will actually see, and what to do about it")
	}
	if !strings.Contains(strings.ToLower(workflow), "not signed or notarized") {
		t.Fatal("…and must say plainly that the binaries are unsigned, rather than leaving them to find out")
	}
}

func TestTheReleaseMatrixIsTheSameSetCiBuilds(t *testing.T) {
	// The matrix is a claim this repository makes, and the platform-claims seal in internal/inventory now reads
	// EVERY workflow's `goos:` values — in both directions. This asserts the narrower thing that seal cannot:
	// that the release ships exactly what CI compiles, so a person cannot download a binary for a target the
	// gate never built.
	root := repoRoot(t)
	pairs := func(name string) []string {
		data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		var out []string
		var goos string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			if after, ok := strings.CutPrefix(line, "goos:"); ok {
				goos = strings.TrimSpace(after)
			}
			if after, ok := strings.CutPrefix(line, "goarch:"); ok && goos != "" {
				out = append(out, goos+"/"+strings.TrimSpace(after))
			}
		}
		return out
	}

	ci, release := pairs("ci.yml"), pairs("release.yml")
	if len(ci) < 3 {
		t.Fatalf("instrument guard: only %d targets parsed from ci.yml — the parse is wrong, not the matrix", len(ci))
	}
	if strings.Join(ci, ",") != strings.Join(release, ",") {
		t.Fatalf("the release ships %v and CI builds %v — a binary for a target the gate never compiled is a "+
			"platform this repository has not tested and must not hand somebody", release, ci)
	}
}
