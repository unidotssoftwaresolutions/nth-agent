package android

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// AdbRunner is the production side of the Runner seam — one home, beside the probe that consumes it, exactly
// as ios.UsbmuxdLister sits beside the iOS probe. It lived in package main until 2026-08-22, where the test
// that claims to span the runner/probe seam could only ever REPLICATE it: a change to the runner moved nothing.
// Here, that test calls this function.
//
// A non-zero exit or a missing binary becomes an error, which the probe turns into a CouldNotAsk carrying the
// INFORMATIVE line of adb's output — not its first line, which for a server that will not start is the
// progress narration `* daemon not running; starting now at …` (see informativeReason).
//
// AND ONLY THROUGH A SERVER THIS ACCOUNT RUNS (adbserver.go): another account's server on adb's port is not asked.
func AdbRunner(ctx context.Context, args ...string) (string, error) {
	return runAdbAsThisAccount(ctx, nil, args...)
}

// NewAdbRunner is AdbRunner with the kernel's answer about who runs adb's server replaced — A TEST SEAM.
func NewAdbRunner(owner localconn.Owner) Runner {
	return func(ctx context.Context, args ...string) (string, error) {
		return runAdbAsThisAccount(ctx, owner, args...)
	}
}

func execAdb(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "adb", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return "", err
		}
		return string(out), fmt.Errorf("%s", text)
	}
	return string(out), nil
}
