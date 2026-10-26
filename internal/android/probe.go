// Package android probes attached Android devices through `adb devices -l`, shelled out.
package android

import (
	"context"
	"fmt"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// Runner is THE process seam: tests stub it, so no test needs a running adb server or a phone
// (the farm's AdbDeviceProvider.RunAdbAsync is internal-virtual for exactly this reason).
type Runner func(ctx context.Context, args ...string) (stdout string, err error)

// Probe asks adb what is attached. Enabled=false ⇒ NotAsked: a disabled probe holds no opinion, and
// nothing may read it as "nothing attached".
type Probe struct {
	Run     Runner
	Enabled bool
	// Confirmations is the per-agent memory for the unattended-install read — a STANDING DEVICE FACT that must
	// not be re-measured every ten seconds. Nil is valid and means "read every time" (the single-shot `devices`
	// verb). See InstallConfirmations for why caching this one is correct and what it refuses to remember.
	Confirmations *InstallConfirmations
	// OfferUncabled offers emulators and phones adb reaches over Wi-Fi or the network too — inventory.OfferUncabledFlag.
	// False, the default, offers only phones on a USB cable and names the others as left out.
	OfferUncabled bool
}

func (p Probe) Outcome(ctx context.Context) inventory.Outcome {
	if !p.Enabled {
		return inventory.NotAsked(inventory.Android)
	}
	out, err := p.Run(ctx, "devices", "-l")
	if err != nil && isServerRefused(err) {
		// WHO RUNS adb's SERVER, not adb itself: adb was not run, or its answer was not used (adbserver.go).
		return inventory.CouldNotAsk(inventory.Android, fmt.Sprintf("adb could not be asked: %s", err), ServerRemedy)
	}
	if err != nil {
		// EVERY failure lands here as CouldNotAsk, never as an empty list: adb missing from PATH, the
		// server refusing to start ("ADB server didn't ACK", exit 1 — captured verbatim on this
		// machine), a timeout. The reason carries adb's own words; the remedy is what the reader does.
		return inventory.CouldNotAsk(inventory.Android,
			fmt.Sprintf("adb could not be asked: %s", informativeReason(err.Error(), out)),
			"Check `adb devices` runs in your shell: install Android platform-tools, put adb on PATH, "+
				"and if it says the server didn't ACK, run `adb kill-server && adb start-server`.")
	}
	offerable, unusable, uncabled := parse(out)
	if p.OfferUncabled {
		for _, d := range uncabled {
			offerable = append(offerable, inventory.Offerable{ID: d.ID})
		}
		uncabled = nil
	}
	reads := make([]InstallConfirmation, 0, len(offerable))
	attached := make([]string, 0, len(offerable))
	for i := range offerable {
		offerable[i] = p.describe(ctx, offerable[i])
		// A STEP ONLY A PERSON CAN TAKE: what this host can establish about installing on this device without
		// somebody at it. Offerable devices only — an unusable one has a nearer problem, and a platform with
		// none has no install to be confirmed. READ ONCE PER ATTACHED DEVICE: it changes when a person changes
		// a setting, which takes the device out of the attached set and back (see InstallConfirmations).
		serial := offerable[i].ID
		reads = append(reads, p.Confirmations.get(serial, func() InstallConfirmation {
			return p.unattendedInstall(ctx, serial)
		}))
		attached = append(attached, serial)
	}
	// Evict what is no longer here, so a device that returns is asked again — and so the caveat's count, which
	// is computed from THIS TICK's reads, can never be a stale one.
	p.Confirmations.retain(attached)
	// The platform ANSWERED — the offerable truth is established and every consumer keeps acting on it —
	// and it carries what the probe could not establish beside it. Empty reason ⇒ plain Answered, so a
	// platform with no devices says nothing extra (see AnsweredWithCaveat).
	reason, remedy := InstallCaveat(reads)
	return inventory.WithLeftOut(inventory.AnsweredWithCaveat(inventory.Android, offerable, unusable, reason, remedy), uncabled)
}

// describe reads the four properties the rack's AdbDeviceProvider reads, with the same meaning, so two
// nodes describing one phone describe it identically — and leaves a field EMPTY when adb did not answer
// it (blank, or the read failed). The listing's sanitised `model:` token (SM_M346B1 for a phone whose
// ro.product.model is SM-M346B1) is deliberately NOT a fallback: it is a different string for the same
// fact, and a lease written from the rack's row would not match it.
func (p Probe) describe(ctx context.Context, d inventory.Offerable) inventory.Offerable {
	d.Model = p.getprop(ctx, d.ID, "ro.product.model")
	d.Manufacturer = p.getprop(ctx, d.ID, "ro.product.manufacturer")
	d.OsVersion = p.getprop(ctx, d.ID, "ro.build.version.release")
	d.FormFactor = FormFactorFrom(p.getprop(ctx, d.ID, "ro.build.characteristics"))
	return d
}

func (p Probe) getprop(ctx context.Context, serial, prop string) string {
	out, err := p.Run(ctx, "-s", serial, "shell", "getprop", prop)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// FormFactorFrom decides the form factor from Android's own ro.build.characteristics. The rack's provider maps
// "tablet" to tablet and EVERYTHING ELSE to phone — a guess for a TV, a watch, a car head unit or a value it
// has never seen. This agent does not guess: "tablet" when Android says tablet; "phone" when Android says
// phone, or "default" (its word for the plain handset profile, possibly alongside nosdcard/emulator); unset
// otherwise. A filter treats unset as no match, which is the truthful outcome for a device nobody described.
func FormFactorFrom(characteristics string) string {
	if characteristics == "" {
		return ""
	}
	tokens := strings.Split(strings.ToLower(characteristics), ",")
	handset := true
	for _, raw := range tokens {
		t := strings.TrimSpace(raw)
		switch t {
		case "tablet":
			return "tablet"
		case "phone":
			return "phone"
		case "default", "nosdcard", "emulator", "":
		default:
			handset = false
		}
	}
	if handset {
		return "phone"
	}
	return ""
}

// Parse reads `adb devices -l` output. The header line and blank lines are skipped; every remaining
// line is "<serial> <state> [key:value ...]".
//
// The STATE IS KEPT VERBATIM (lower-cased) — "unauthorized", "offline", "no permissions" and anything
// a future adb prints. Only "device" is offerable; everything else is Unusable, so a new adb state
// this code has never seen is shown to the reader rather than silently offered or silently dropped.
//
// ONLY A PHONE ON THE CABLE IS OFFERABLE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1). adb prints a `usb:` key for a
// cabled transport — `usb:2-1.1` on Linux, `usb:338690048X` on macOS — and none for a phone joined with `adb connect`, one
// reached by Wireless debugging, or an emulator. A "device" line without one is left out (see Probe.OfferUncabled), and
// named with how it is reached; Parse drops it, parse keeps it.
func Parse(out string) ([]inventory.Offerable, []inventory.Unusable) {
	offerable, unusable, _ := parse(out)
	return offerable, unusable
}

func parse(out string) ([]inventory.Offerable, []inventory.Unusable, []inventory.LeftOut) {
	offerable := []inventory.Offerable{}
	unusable := []inventory.Unusable{}
	var uncabled []inventory.LeftOut
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices attached") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		serial := fields[0]
		// The state can be MULTI-WORD and adb can append its own explanation after it. Measured
		// against the real shape: `no permissions (user in plugdev group; are your udev rules
		// wrong?); see [http://…] transport_id:6` — so the state is the words before EITHER the
		// first key:value token OR the first parenthetical. A first draft that stopped only at
		// key:value swallowed adb's whole sentence into the state; the fixture caught it.
		stateWords := []string{}
		for _, f := range fields[1:] {
			if strings.Contains(f, ":") || strings.HasPrefix(f, "(") {
				break
			}
			stateWords = append(stateWords, f)
		}
		state := strings.ToLower(strings.Join(stateWords, " "))
		if state == "device" {
			// The listing's `model:` token is adb's SANITISED spelling (SM_M346B1 for SM-M346B1) — a different
			// string for the model than ro.product.model, which is what the rack publishes and a lease is written
			// in. It is not the model; describe() reads the real one, and an unanswered read stays empty.
			if valueOf(fields, "usb") == "" {
				uncabled = append(uncabled, inventory.LeftOut{ID: serial, How: howReached(serial)})
				continue
			}
			offerable = append(offerable, inventory.Offerable{ID: serial})
			continue
		}
		unusable = append(unusable, inventory.NewUnusable(serial, state, remedyFor(state)))
	}
	return offerable, unusable, uncabled
}

// howReached says, from the serial adb gives it, how a transport with no usb: key is reached: adb names an emulator
// `emulator-<port>`, a Wireless debugging phone by its mDNS service, and an `adb connect` one by host:port.
func howReached(serial string) string {
	switch {
	case strings.HasPrefix(serial, "emulator-"):
		return "an emulator"
	case strings.Contains(serial, "._adb-tls-connect._tcp"), strings.Contains(serial, "._adb._tcp"):
		return "reached over Wi-Fi (wireless debugging)"
	case strings.Contains(serial, ":"):
		return "reached over the network (adb connect)"
	default:
		return "not on a USB cable, as far as adb says"
	}
}

// remedyFor answers with the action for the states adb is known to print, and a HONEST fallback for
// one it has never printed before — an unknown state still gets a next step, and the state itself is
// shown verbatim beside it, so the reader is never told "other".
func remedyFor(state string) string {
	switch {
	case strings.HasPrefix(state, "unauthorized"):
		return "Unlock the phone and tap Allow on the 'Allow USB debugging?' dialog, then re-run."
	case strings.HasPrefix(state, "offline"):
		return "Unplug and re-plug the cable (or reboot the device); adb sees it but cannot talk to it."
	case strings.HasPrefix(state, "no permissions"):
		return "This host cannot open the USB device: on Linux add the udev rule for the vendor id, then `adb kill-server`."
	case strings.HasPrefix(state, "authorizing"):
		return "The device is still deciding — leave it plugged in and unlocked for a moment, then re-run."
	case strings.HasPrefix(state, "recovery"), strings.HasPrefix(state, "sideload"), strings.HasPrefix(state, "bootloader"):
		return "Boot the device back into Android (it is in a service mode adb cannot run tests on)."
	default:
		return "adb reports this device in a state this agent does not recognise (shown verbatim above) — " +
			"run `adb devices -l` yourself and check that state's meaning for your device."
	}
}

func valueOf(fields []string, key string) string {
	for _, f := range fields {
		if strings.HasPrefix(f, key+":") {
			return strings.TrimPrefix(f, key+":")
		}
	}
	return ""
}

// informativeReason picks what a READER is told when adb could not be asked.
//
// THE DEFECT THIS REPLACES, measured against this repo's own captured fixture: the old firstLine took
// the literal first line, which for a server that will not start is `* daemon not running; starting
// now at tcp:1` — adb's PROGRESS narration presented as the cause, while `ADB server didn't ACK` sat
// on the next line and the actual diagnosis (`could not install *smartsocket* listener: Permission
// denied`) further down. `Parse` one function away already knows `*` lines are noise; this did not.
//
// HOW MUCH TO SHOW, decided deliberately: two lines, never the whole dump.
//  1. THE HEADLINE — the first line that is not `*` progress and not blank: what adb could not do.
//  2. THE DIAGNOSIS — adb's own FATAL/ERROR line from the startup log it dumps, identified
//     STRUCTURALLY by the severity field in its log format (`… F adb : main.cpp:165 <message>`),
//     never by keyword matching, and with that log prefix stripped so the sentence reads. This is
//     the line a reader acts on ("Permission denied" ⇒ something else owns the port / a sandbox).
//
// Anything more is a log dump on a row that other systems store and display; anything less is the
// support conversation this agent exists to delete. The whole is capped, because a reason is a
// sentence, not a file.
func informativeReason(errText, out string) string {
	text := strings.TrimSpace(errText)
	if text == "" {
		text = strings.TrimSpace(out)
	}

	headline, diagnosis := "", ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "*") {
			continue // adb's progress narration — the same lines Parse skips
		}
		if headline == "" {
			headline = line
		}
		if message, fatal := adbLogMessage(line); fatal {
			diagnosis = message // keep the LAST fatal/error: adb logs the cause it died on last
		}
	}
	if headline == "" {
		// Everything adb printed was progress. Better the raw first line than an empty reason.
		headline = strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	}

	reason := headline
	if diagnosis != "" && diagnosis != headline {
		reason = headline + " — " + diagnosis
	}
	return truncate(reason, maxReasonChars)
}

// maxReasonChars bounds the sentence. adb's startup dump is tens of lines; a reason that carries all
// of it stops being readable and starts being a log file with nowhere to live.
const maxReasonChars = 400

// adbLogMessage recognises adb's own startup-log line format and returns its message when the line's
// SEVERITY field is F (fatal) or E (error):
//
//	08-21 23:05:51.665 98413 31147127 F adb     : main.cpp:165 could not install *smartsocket* …
//	<date>  <time>     <pid> <tid>    ^severity  ^source        ^message
//
// Structural, not a keyword list: whatever adb dies of tomorrow still carries an F, and a message
// this code has never seen survives verbatim — the same rule the device states follow.
func adbLogMessage(line string) (string, bool) {
	fields := strings.Fields(line)
	for i := 0; i+2 < len(fields); i++ {
		if (fields[i] != "F" && fields[i] != "E") || fields[i+1] != "adb" {
			continue
		}
		rest := fields[i+2:]
		if len(rest) > 0 && rest[0] == ":" {
			rest = rest[1:]
		}
		// Drop the `main.cpp:165` source locator — a file and line inside adb is not the reader's.
		if len(rest) > 0 && strings.Contains(rest[0], ".cpp:") {
			rest = rest[1:]
		}
		if message := strings.TrimSpace(strings.Join(rest, " ")); message != "" {
			return message, true
		}
	}
	return "", false
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "…"
}
