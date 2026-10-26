package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
)

// ── `nth-agent capture enable | setup [SERIAL] | disable` ───────────────────────────────────────────────────────
//
// Network capture on this machine is OFF until a person turns it on here, sets up each phone, and a scenario asks for it
// — the three opt-ins the operator decided. These three commands are the first two and the way back out; `connect`, which
// runs unattended, only ever does what they decided (internal/capture).

// captureSeams are the seams a test replaces: where the state lives, the adb, the clock, the system.
type captureSeams struct {
	configDir func() (string, error)
	adb       capture.Adb
	now       func() time.Time
	goos      string
	hostname  func() (string, error)
	lockWait  time.Duration
}

func realCaptureSeams() captureSeams {
	return captureSeams{configDir: os.UserConfigDir, adb: capture.RealAdb, now: time.Now, goos: runtime.GOOS,
		hostname: os.Hostname, lockWait: time.Minute}
}

func runCapture(inv *invocation, stdin io.Reader, stdout, stderr io.Writer, seams captureSeams) int {
	configDir, err := seams.configDir()
	if err != nil {
		fmt.Fprintf(stderr, "cannot locate the user config directory: %v\n", err)
		return 1
	}
	store := capture.StoreIn(configDir)
	switch inv.Subcommand {
	case "enable":
		return captureEnable(store, stdin, stdout, stderr, seams)
	case "disable":
		return captureDisable(store, stdout, seams)
	default:
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return capture.Setup(ctx, capture.SetupOptions{Store: store, Adb: seams.adb, In: stdin, Out: stdout, Now: seams.now,
			Serial: inv.Operand})
	}
}

// captureEnable says everything first, then asks, and does nothing at all unless the answer is ENABLE, typed.
func captureEnable(store capture.Store, stdin io.Reader, stdout, stderr io.Writer, seams captureSeams) int {
	if !capture.Supported(seams.goos) {
		fmt.Fprint(stdout, capture.UnsupportedSystem(seams.goos))
		return 1
	}
	if _, on := store.Enabled(); on {
		if status := capture.ReadStatus(store, seams.now()); status.CAEnds != nil {
			fmt.Fprint(stdout, capture.AlreadyEnabled(status.CAName, *status.CAEnds))
			return 0
		}
	}
	fmt.Fprint(stdout, capture.EnableExplanation())
	fmt.Fprint(stdout, "\n"+capture.ConfirmPrompt)
	answer, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && answer == "" && !errors.Is(err, io.EOF) {
		fmt.Fprintf(stderr, "could not read your answer: %v\n", err)
		return 1
	}
	if strings.TrimSpace(answer) != "ENABLE" {
		fmt.Fprintln(stdout, "\n"+capture.NotEnabled)
		return exitMistake
	}
	machine, _ := seams.hostname()
	ca, err := capture.Enable(store, machine, version, seams.now(), seams.goos)
	switch {
	case errors.Is(err, capture.ErrAlreadyOn):
		status := capture.ReadStatus(store, seams.now())
		if status.CAEnds != nil {
			fmt.Fprint(stdout, capture.AlreadyEnabled(status.CAName, *status.CAEnds))
		}
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "network capture was not turned on: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, "\n"+capture.Enabled(ca.Certificate().Subject.CommonName, ca.Certificate().NotAfter))
	return 0
}

// captureDisable turns it all off and says what is left: each phone that could not be handed back, with what it is left
// with, anything that could not be deleted, and where the certificate is to be removed from each phone. Exit 1 when
// anything is left undone.
func captureDisable(store capture.Store, stdout io.Writer, seams captureSeams) int {
	result := capture.Disable(context.Background(), store, seams.adb, seams.now, seams.lockWait)
	fmt.Fprint(stdout, capture.DisableSummary(result, store.CADir()))
	if result.Failed() {
		return 1
	}
	return 0
}
