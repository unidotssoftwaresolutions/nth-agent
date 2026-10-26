package capture

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// ── WHAT THIS AGENT ASKS A PHONE, OVER adb, AND NOTHING ELSE ────────────────────────────────────────────────────
//
// Every adb command capture runs is built here, and the list is short on purpose: set, clear and read the phone's global
// http_proxy; add and remove one `adb reverse` by its port; copy the capture certificate to the phone's Downloads; read
// two of the phone's build properties; open one address in its browser; and list the phones attached. NEVER `adb root`,
// `unroot` or `remount`, and nothing that reads or writes the phone's certificate stores: the certificate is installed by
// the person holding the phone, and removed by them (both places, on a Samsung). The seal in adb_test.go holds the list.

// Adb runs one adb command: what it printed on each stream and how it exited. err is set when adb could not be run at
// all (then exit is -1).
type Adb func(ctx context.Context, args ...string) (stdout, stderr string, exit int, err error)

// adbTimeout bounds one adb command — the node's 30 seconds for each call on its handback path.
const adbTimeout = 30 * time.Second

// RealAdb runs the adb on PATH.
func RealAdb(ctx context.Context, args ...string) (string, string, int, error) {
	return realAdb(ctx, nil, args...)
}

// NewRealAdb is RealAdb with the kernel's answer about who runs adb's server replaced — A TEST SEAM.
func NewRealAdb(owner localconn.Owner) Adb {
	return func(ctx context.Context, args ...string) (string, string, int, error) {
		return realAdb(ctx, owner, args...)
	}
}

// realAdb runs one command through an adb server this account runs, and only then (android.CheckServer, before and after:
// A_MACHINE_THAT_STAYS_THE_CUSTOMERS). A refusal is err, with exit -1, as for an adb that could not be run.
func realAdb(ctx context.Context, owner localconn.Owner, args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, adbTimeout)
	defer cancel()
	if err := android.CheckServer(ctx, os.Getenv, owner); err != nil {
		return "", "", -1, err
	}
	stdout, stderr, exit, err := execAdb(ctx, args...)
	if checkErr := android.CheckServer(ctx, os.Getenv, owner); checkErr != nil {
		return "", "", -1, checkErr
	}
	return stdout, stderr, exit, err
}

func execAdb(ctx context.Context, args ...string) (string, string, int, error) {
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, "adb", args...)
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return stdout.String(), stderr.String(), 0, nil
	case errors.As(err, &exitErr):
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	default:
		return stdout.String(), stderr.String(), -1, err
	}
}

// DeviceCertificatePath is where `capture setup` copies the capture certificate on the phone, under the name the
// person looks for in the phone's installer.
const DeviceCertificatePath = "/sdcard/Download/nthcase-agent-ca.crt"

func proxySetArgs(serial string, port int) []string {
	return []string{"-s", serial, "shell", "settings", "put", "global", "http_proxy", ProxyValue(port)}
}

// proxyClearArgs clears the proxy with ":0", the value that reliably means "no proxy" (the node's).
func proxyClearArgs(serial string) []string {
	return []string{"-s", serial, "shell", "settings", "put", "global", "http_proxy", ":0"}
}

func proxyReadArgs(serial string) []string {
	return []string{"-s", serial, "shell", "settings", "get", "global", "http_proxy"}
}

func reverseAddArgs(serial string, port int) []string {
	p := "tcp:" + strconv.Itoa(port)
	return []string{"-s", serial, "reverse", p, p}
}

func reverseRemoveArgs(serial string, port int) []string {
	return []string{"-s", serial, "reverse", "--remove", "tcp:" + strconv.Itoa(port)}
}

func pushCertificateArgs(serial, localPath string) []string {
	return []string{"-s", serial, "push", localPath, DeviceCertificatePath}
}

func propertyArgs(serial, property string) []string {
	return []string{"-s", serial, "shell", "getprop", property}
}

// openInBrowserArgs opens address in the phone's browser, in the tab this agent's earlier opens used: Android's
// EXTRA_APPLICATION_ID asks the browser to reuse the window it opened for the same caller (AN_AGENT_CAPTURE_READY_TO_SHIP,
// rule 4: the check's last page replaces the check's own page, and the warning a refusing browser shows for it, in place). The
// same command as before with one extra — nothing stops, clears or closes the browser.
func openInBrowserArgs(serial, address string) []string {
	return []string{"-s", serial, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", address,
		"--es", "com.android.browser.application_id", "nthcase-capture-check"}
}

func devicesArgs() []string { return []string{"devices"} }

// adbCouldNotReach is the node's rule (NetworkCaptureController.AdbCouldNotReach): the command never reached the phone —
// it is not attached, offline, not authorised, or adb could not run — as against the phone refusing the command, which
// says nothing about whether the rest of a handback can be done.
func adbCouldNotReach(exit int, stderr string) bool {
	return exit == -1 ||
		(strings.Contains(stderr, "device '") && strings.Contains(stderr, "' not found")) ||
		strings.Contains(stderr, "device offline") ||
		strings.Contains(stderr, "no devices/emulators found") ||
		strings.Contains(stderr, "unauthorized") ||
		strings.Contains(stderr, "still authorizing")
}

// adbFailure names a failed adb command in a sentence: its exit code and what it said, on one line.
func adbFailure(exit int, stderr string) string {
	said := strings.Join(strings.Fields(stderr), " ")
	if len(said) > 160 {
		said = said[:160] + "…"
	}
	if said == "" {
		return "adb exit " + strconv.Itoa(exit)
	}
	return "adb exit " + strconv.Itoa(exit) + ": " + said
}
