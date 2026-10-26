package android

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// A STEP ONLY A PERSON CAN TAKE.
//
// A physical Android phone can require somebody standing next to it to approve an installation — the
// promise here is a phone on a desk and a run in a cloud, so an unattended run whose install is waiting on
// a tap fails as a timeout naming the wrong cause. This file answers, per device, what this host can
// ESTABLISH about that, and the answer decides what the platform's outcome says.
//
// WHAT IS READABLE, measured on the operator's Samsung (Android 16, 2026-08-22) rather than assumed:
//
//	settings get global verifier_verify_adb_installs   => null   (unset in system, secure AND global)
//	settings get global package_verifier_enable        => null
//	settings get global package_verifier_user_consent  => 1      (set in global only — see below)
//
// WHAT IS NOT, and why this is a caveat rather than an answer:
//
//  1. `settings get` prints the literal string "null" for a key that is UNSET. Unset is not a value: the
//     platform's own default applies, and that default is compiled into the framework, not readable over
//     adb. So the one documented switch reads as "I cannot tell you".
//  2. package_verifier_user_consent reads null in `secure` and 1 in `global` — THE SAME KEY, a different
//     namespace. A read of one namespace can manufacture a false "unknown", which is why unattendedInstall
//     asks all three and treats "null"/"" as unreadable everywhere rather than as a value.
//  3. Even a definite 0 would not settle it. Vendors add their own confirmation that no adb read exposes —
//     Samsung's Auto Blocker, Xiaomi's "Install via USB" — so the blind spot survives the best case this
//     probe can reach. Establishing it for certain would mean attempting an install, which this probe does
//     not do: it installs nothing to find out.
const (
	// verifierKey is the ONE documented switch: whether installs arriving over adb are verified (and can
	// therefore be gated by a prompt). Asked in every namespace — see (2) above.
	verifierKey = "verifier_verify_adb_installs"

	// unreadable is what `settings get` prints for a key nobody has set. It is NOT a value, and reading it
	// as one is the defect this constant exists to name.
	unreadable = "null"
)

// InstallConfirmation is what this host could establish about unattended installation on ONE device.
type InstallConfirmation struct {
	Serial string
	// VerifierValue is the device's own answer for verifierKey, verbatim, from whichever namespace has it
	// set — and EMPTY when no namespace has it set or the read failed. Empty means UNREADABLE, never off.
	VerifierValue string
	// Asked is whether the DEVICE answered every namespace query — true even when the answer is "unset",
	// which is a real answer. False means adb itself failed, and A FAILED READ IS NOT A FACT: it is the one
	// thing the cache must never keep, or a transient adb hiccup would freeze this device's answer for the
	// whole time it stays attached.
	Asked bool
}

// Established reports whether this read settles anything on its own. It never does today, and saying so in
// one place keeps the caveat honest: even a definite "0" leaves the vendor gates of (3) unread.
func (c InstallConfirmation) Established() bool { return false }

// unattendedInstall reads what it can for one device. A failed read is EMPTY, never a guessed value.
func (p Probe) unattendedInstall(ctx context.Context, serial string) InstallConfirmation {
	c := InstallConfirmation{Serial: serial, Asked: true}
	for _, ns := range []string{"global", "secure", "system"} {
		out, err := p.Run(ctx, "-s", serial, "shell", "settings", "get", ns, verifierKey)
		if err != nil {
			c.Asked = false // adb failed — not an answer, and not cacheable
			continue
		}
		value := strings.TrimSpace(out)
		// "null" is the platform saying "unset". Treating it as a value would put a fact on a screen that
		// the device never stated — and an unset key means the framework default applies, which is exactly
		// what this agent cannot read.
		if value == "" || strings.EqualFold(value, unreadable) {
			continue
		}
		c.VerifierValue = value
		return c
	}
	return c
}

// InstallCaveat is the stated blind spot for a platform that has devices somebody may have to stand next
// to. It is EMPTY when there is nothing to caveat — no offerable device means no install to be confirmed,
// and a caveat about nothing is noise on every screen that renders it.
//
// The conclusion does not depend on what was read: it is the same blind spot either way (see (3) above).
// What the read buys is precision about WHY, which is the difference between a caveat a reader trusts and
// one they learn to skip.
func InstallCaveat(reads []InstallConfirmation) (reason, remedy string) {
	if len(reads) == 0 {
		return "", ""
	}
	unread := 0
	for _, c := range reads {
		if c.VerifierValue == "" {
			unread++
		}
	}

	detail := fmt.Sprintf("%s is unset or unreadable on %d of %d attached device(s), so the platform's own "+
		"default applies and this agent cannot read it", verifierKey, unread, len(reads))
	if unread == 0 {
		detail = fmt.Sprintf("%s reads a value on every attached device, but vendors add their own "+
			"confirmation that no adb read exposes", verifierKey)
	}

	return "this agent cannot establish whether an app can be installed on these devices without somebody at " +
			"them: " + detail + ". A run that installs an app may wait for a tap that never comes.",
		"Approve one installation on the device by hand to see whether it asks — and if it does, allow " +
			"installs over USB once (stock Android: Developer options → \"Verify apps over USB\"; Samsung: " +
			"Auto Blocker; Xiaomi: \"Install via USB\") so unattended runs stop waiting."
}

// InstallConfirmations remembers what each attached device answered — A STANDING DEVICE FACT, READ ONCE.
//
// WHY A CACHE IS CORRECT HERE, not merely cheaper. The verifier setting changes when a PERSON changes it, and
// this repo's own reasoning already puts standing host facts on registration rather than on every tick
// (NodeCaptureAvailability). This is the device-scoped twin of that argument. The eviction rule is what makes it
// true rather than convenient: an entry lives only while its serial is in the attached set, and the ways this
// fact can change — a reboot, a re-plug, a person unlocking the phone to flip a switch — take the device out of
// `adb devices -l`'s `device` state and back, which evicts it and forces a fresh read.
//
// WHAT IS DELIBERATELY NOT CACHED: a read adb could not answer (Asked=false). Keeping a failure would turn one
// transient hiccup into this device's permanent answer.
//
// A nil *InstallConfirmations reads every time — the single-shot `devices` verb needs no memory.
type InstallConfirmations struct {
	mu       sync.Mutex
	bySerial map[string]InstallConfirmation
}

// NewInstallConfirmations builds the per-agent cache. One per process, held by the probe set.
func NewInstallConfirmations() *InstallConfirmations {
	return &InstallConfirmations{bySerial: map[string]InstallConfirmation{}}
}

// get answers from memory, or calls read and remembers a real answer.
func (c *InstallConfirmations) get(serial string, read func() InstallConfirmation) InstallConfirmation {
	if c == nil {
		return read()
	}
	c.mu.Lock()
	remembered, ok := c.bySerial[serial]
	c.mu.Unlock()
	if ok {
		return remembered
	}

	fresh := read()
	if !fresh.Asked {
		return fresh // a failed read is retried next tick, never remembered
	}
	c.mu.Lock()
	c.bySerial[serial] = fresh
	c.mu.Unlock()
	return fresh
}

// retain drops every serial that is no longer attached, so a device that comes back is asked again.
func (c *InstallConfirmations) retain(attached []string) {
	if c == nil {
		return
	}
	keep := make(map[string]bool, len(attached))
	for _, s := range attached {
		keep[s] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for serial := range c.bySerial {
		if !keep[serial] {
			delete(c.bySerial, serial)
		}
	}
}

// Size is what the cache is holding — for the seals, which must be able to see eviction happen.
func (c *InstallConfirmations) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bySerial)
}
