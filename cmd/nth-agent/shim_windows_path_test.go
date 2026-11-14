//go:build !windows

package main

import (
	"bufio"
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitingAgentScript says its process id and waits, ending with its own code on INT (3) or TERM (4).
const waitingAgentScript = `#!/bin/sh
trap 'echo "got INT"; exit 3' INT; trap 'echo "got TERM"; exit 4' TERM; echo "ready $$"
while :; do sleep 0.05; done
`

// ON WINDOWS THE SHIM PASSES NO SIGNAL ON (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE). A Ctrl-C in a Windows console reaches
// the binary itself, and Node can only end a Windows program outright, which would stop the agent before it handed its
// phones back. So on win32 the shim hears SIGINT and SIGTERM, passes neither on, keeps running while the binary does, and
// ends with the binary's own exit code. Run with the platform the shim reads set to win32; the binary here ends itself on
// the TERM this test sends it directly.
func TestTheShimOnWindowsLeavesCtrlCToTheBinaryAndEndsWithIt(t *testing.T) {
	needTools(t, "node", "sh")
	// Its own package: the shim as it is, the platform it reads replaced, and one binary — the one the shim must choose for
	// win32-x64 — that says its process id and waits.
	dir, _ := shimPackage(t)
	writeFile(t, filepath.Join(dir, "bin", "nth-agent-windows-amd64.exe"), []byte(waitingAgentScript), 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := shimCommand(ctx, dir, "win32", "x64", "waitpid")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(pipe)
	var pid int
	if !lines.Scan() || !strings.HasPrefix(lines.Text(), "ready ") {
		t.Fatalf("the binary never said it was waiting: %q", lines.Text())
	}
	for _, r := range strings.TrimPrefix(lines.Text(), "ready ") {
		pid = pid*10 + int(r-'0')
	}
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		if err := cmd.Process.Signal(signal); err != nil {
			t.Fatalf("the shim ended before the binary did (%v): %v", signal, err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if cmd.ProcessState != nil {
		t.Fatal("the shim ended on a signal on win32; it must wait for the binary")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("the binary (pid %d) was no longer running — the shim passed a signal on: %v", pid, err)
	}
	var rest strings.Builder
	for lines.Scan() {
		rest.WriteString(lines.Text() + "\n")
	}
	err = cmd.Wait()
	code, sig := exitOf(t, cmd, err)
	if strings.Contains(rest.String(), "got INT") || !strings.Contains(rest.String(), "got TERM") || code != 4 || sig != 0 {
		t.Fatalf("on win32 the binary must hear only its own TERM, and the shim end with its code 4: got exit %d signal %v, output %q",
			code, sig, rest.String())
	}
}
