package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// THE npm SHIM, WRITTEN FROM ITS SPEC (before 0.3.0). publish/npm/index.js said it was taken from another project's shim,
// whose licence that project's own repository and its package.json disagreed about. It was rewritten from what it must do,
// and these seals hold the rewrite: it names no other project as its source, and it does what the spec says when it is RUN —
// the binary for this machine, the arguments, the terminal, the exit code or the signal, SIGINT and SIGTERM forwarded, one
// sentence on a machine it has no binary for.

// provenanceClaims are the words a file uses to say its code came from somewhere else.
var provenanceClaims = regexp.MustCompile(`(?i)taken from|copied from|adapted from|derived from|ported from|borrowed from|` +
	`based on|inspired by|courtesy of|originally from|credit to|mobile-mcp|mobilecli|mobile-next|https?://|github\.com|npmjs\.com`)

func TestTheShimNamesNoOtherProjectAsItsSource(t *testing.T) {
	// INSTRUMENT: the sentence the shim used to open with must be caught, or this seal could pass on anything.
	if !provenanceClaims.MatchString("Taken from mobile-mcp's `mobilecli/publish/npm/index.js`, which is the working reference") {
		t.Fatal("instrument guard: the pattern does not catch the attribution the shim used to carry")
	}
	shim := npmFile(t, "index.js")
	if !strings.Contains(shim, "spawn(") {
		t.Fatal("instrument guard: the file read is not the shim")
	}
	for i, line := range strings.Split(shim, "\n") {
		if m := provenanceClaims.FindString(line); m != "" {
			t.Errorf("publish/npm/index.js:%d names a source (%q) — the shim is this repository's own, written from its spec:\n  %s",
				i+1, m, strings.TrimSpace(line))
		}
	}
}

// fakeAgent is a stand-in for each binary the package ships: it says which one it is and what it was given, exits with a
// code, ends itself with a signal, or waits for one and says which arrived.
const fakeAgentScript = `#!/bin/sh
case "$1" in
  echo) shift; echo "ran $(basename "$0")"; for a in "$@"; do echo "arg [$a]"; done ;;
  exit) exit "$2" ;;
  die) kill -"$2" $$; sleep 5; exit 0 ;;
  wait) trap 'echo "got INT"; exit 3' INT; trap 'echo "got TERM"; exit 4' TERM; echo ready
        while :; do sleep 0.05; done ;;
esac
`

// shimPackage lays the package out as npm unpacks it — index.js beside bin/ — with a fake for every binary publish-npm.yml
// puts there, and returns its directory.
func shimPackage(t *testing.T) (dir string, binaries []string) {
	t.Helper()
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "index.js"), []byte(npmFile(t, "index.js")), 0o755)
	binaries = publishedBinaries(t)
	for _, name := range binaries {
		writeFile(t, filepath.Join(dir, "bin", name), []byte(fakeAgentScript), 0o755)
	}
	// What the platform reads is replaced before the shim runs, so every target is exercised on any machine.
	writeFile(t, filepath.Join(dir, "fake-platform.js"), []byte(`Object.defineProperty(process, "platform", { value: process.env.FAKE_PLATFORM });
Object.defineProperty(process, "arch", { value: process.env.FAKE_ARCH });
`), 0o644)
	return dir, binaries
}

// publishedBinaries is every file publish-npm.yml moves into bin/: `nth-agent-<goos>-<goarch>` for each target it unpacks.
func publishedBinaries(t *testing.T) []string {
	t.Helper()
	fetch := workflowText(t, "publish-npm.yml")
	m := regexp.MustCompile(`for goos_goarch in ([a-z0-9_ ]+); do`).FindStringSubmatch(fetch)
	if m == nil || !strings.Contains(fetch, `"publish/npm/bin/nth-agent-${goos_goarch//_/-}"`) {
		t.Fatal("instrument guard: publish-npm.yml's fetch no longer names the binaries it puts in bin/ the way this seal reads")
	}
	var names []string
	for _, target := range strings.Fields(m[1]) {
		names = append(names, "nth-agent-"+strings.ReplaceAll(target, "_", "-"))
	}
	return names
}

// shimCommand is node running the shim as if on platform-arch, in a process group of its own: when ctx ends, the whole
// group goes — the shim AND the binary it started — so a shim that never ends fails the test instead of hanging it.
func shimCommand(ctx context.Context, dir, platform, arch string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "node", append([]string{"-r", filepath.Join(dir, "fake-platform.js"), filepath.Join(dir, "index.js")}, args...)...)
	cmd.Env = append(os.Environ(), "FAKE_PLATFORM="+platform, "FAKE_ARCH="+arch)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// runShim runs the shim and returns what it printed, its exit code, and the signal that ended it (0 when none did).
func runShim(t *testing.T, dir, platform, arch string, args ...string) (stdout, stderr string, code int, sig syscall.Signal) {
	t.Helper()
	needTools(t, "node", "sh")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := shimCommand(ctx, dir, platform, arch, args...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the shim %v did not end within 20 s: stdout %q, stderr %q", args, out.String(), errOut.String())
	}
	code, sig = exitOf(t, cmd, err)
	return out.String(), errOut.String(), code, sig
}

func exitOf(t *testing.T, cmd *exec.Cmd, err error) (int, syscall.Signal) {
	t.Helper()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("the shim did not run: %v", err)
	}
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if status.Signaled() {
		return -1, status.Signal()
	}
	return status.ExitStatus(), 0
}

// goarchToNode is Node's spelling of each GOARCH the release builds.
var goarchToNode = map[string]string{"amd64": "x64", "arm64": "arm64"}

func TestTheShimRunsTheBinaryBuiltForThisMachineWithItsArguments(t *testing.T) {
	dir, binaries := shimPackage(t)
	if len(binaries) < 3 {
		t.Fatalf("instrument guard: %d binaries read from publish-npm.yml", len(binaries))
	}
	for _, binary := range binaries {
		parts := strings.Split(strings.TrimPrefix(binary, "nth-agent-"), "-")
		platform, arch := parts[0], goarchToNode[parts[1]]
		out, errOut, code, sig := runShim(t, dir, platform, arch, "echo", "one", "two words", "--flag")
		want := "ran " + binary + "\narg [one]\narg [two words]\narg [--flag]\n"
		if out != want || code != 0 || sig != 0 || errOut != "" {
			t.Errorf("on %s-%s the shim must run bin/%s with the arguments as given: got exit %d signal %v, stdout %q, stderr %q",
				platform, arch, binary, code, sig, out, errOut)
		}
	}
}

func TestTheShimEndsTheWayTheBinaryEnded(t *testing.T) {
	dir, _ := shimPackage(t)
	if _, _, code, sig := runShim(t, dir, "linux", "x64", "exit", "7"); code != 7 || sig != 0 {
		t.Fatalf("the binary exited 7 and the shim exited %d (signal %v) — a script reading the code reads the shim's", code, sig)
	}
	for _, c := range []struct {
		signal syscall.Signal
		name   string
	}{{syscall.SIGTERM, "TERM"}, {syscall.SIGKILL, "KILL"}, {syscall.SIGINT, "INT"}} {
		if _, _, code, sig := runShim(t, dir, "linux", "x64", "die", c.name); sig != c.signal {
			t.Errorf("the binary was ended by %v and the shim exited %d with signal %v — it must re-raise the signal", c.signal, code, sig)
		}
	}
}

func TestTheShimForwardsSIGINTAndSIGTERMToTheBinary(t *testing.T) {
	needTools(t, "node", "sh")
	dir, _ := shimPackage(t)
	for _, c := range []struct {
		signal syscall.Signal
		says   string
		code   int
	}{{syscall.SIGINT, "got INT", 3}, {syscall.SIGTERM, "got TERM", 4}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := shimCommand(ctx, dir, "linux", "x64", "wait")
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(pipe)
		if !lines.Scan() || lines.Text() != "ready" {
			cancel()
			t.Fatalf("the binary never said it was waiting: %q", lines.Text())
		}
		// THE SIGNAL GOES TO THE SHIM ALONE, as a supervisor or `kill <pid>` sends it — the binary hears it only if forwarded.
		if err := cmd.Process.Signal(c.signal); err != nil {
			t.Fatal(err)
		}
		var rest strings.Builder
		for lines.Scan() {
			rest.WriteString(lines.Text() + "\n")
		}
		err = cmd.Wait()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Errorf("%v sent to the shim never reached the binary — 10 s later both were still running (output %q)", c.signal, rest.String())
			continue
		}
		code, sig := exitOf(t, cmd, err)
		if !strings.Contains(rest.String(), c.says) || code != c.code || sig != 0 {
			t.Errorf("%v sent to the shim: the binary must hear it and the shim end with its code %d — got exit %d signal %v, "+
				"output %q", c.signal, c.code, code, sig, rest.String())
		}
	}
}

func TestTheShimSaysInOneSentenceWhichMachinesItRunsOn(t *testing.T) {
	dir, _ := shimPackage(t)
	out, errOut, code, sig := runShim(t, dir, "aix", "ppc64", "devices")
	lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
	if code != 1 || sig != 0 || out != "" || len(lines) != 1 || !strings.HasSuffix(lines[0], ".") {
		t.Fatalf("on a machine with no binary the shim must say one sentence and exit 1: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if !strings.Contains(lines[0], "aix-ppc64") {
		t.Errorf("the sentence does not name the machine it ran on: %q", lines[0])
	}
	for _, key := range regexp.MustCompile(`"([a-z0-9]+-[a-z0-9]+)":`).FindAllStringSubmatch(npmFile(t, "index.js"), -1) {
		if !strings.Contains(lines[0], key[1]) {
			t.Errorf("the sentence does not name %s, which the shim supports: %q", key[1], lines[0])
		}
	}
}
