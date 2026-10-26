// Package ios probes attached iOS devices through go-ios (usbmuxd), never a shelled-out binary.
package ios

import (
	"context"
	"fmt"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// Device is what a lister reports for one attached iPhone/iPad. Deliberately go-ios-shaped but NOT
// go-ios-typed: the seam below stays stubbable without a usbmuxd, and this package's own types are
// what the rest of the agent sees.
type Device struct {
	UDID string
	// State is the device's own word for its condition, verbatim: "" (or "paired") when it is usable,
	// otherwise whatever the pairing/lockdown layer reported — e.g. "pairingdialogresponsepending",
	// "userdeniedpairing", "passwordprotected". NOT an enum, for the adb reason: a state go-ios grows
	// tomorrow must survive the trip and be shown.
	State string
	// Model is what a lister could ESTABLISH as the device's model — the device's own name from a lockdown
	// value read, falling back to its ProductType. Empty when the read could not be made (an untrusted or
	// locked device), never a stand-in such as the connection type. See UsbmuxdLister for what the read costs.
	Model string
	// OsVersion is the device's own ProductVersion from the same read, empty when it could not be made. The
	// Android probe's rule, unchanged: the platform's own answer, absent when the platform did not answer.
	OsVersion string
	// Connection is usbmuxd's transport ("USB" / "Network") — a fact about the cable, not the device.
	Connection string
}

// Lister is THE go-ios seam. It returns the attached devices, or an error when the question could not
// be asked at all (no usbmuxd socket, daemon not running, timeout).
type Lister func(ctx context.Context) ([]Device, error)

// Probe asks usbmuxd what is attached. Enabled=false ⇒ NotAsked.
type Probe struct {
	List    Lister
	Enabled bool
	// OfferUncabled offers phones usbmuxd reaches over the network too — inventory.OfferUncabledFlag. False, the default,
	// offers only phones on a USB cable and names the others as left out.
	OfferUncabled bool
}

func (p Probe) Outcome(ctx context.Context) inventory.Outcome {
	if !p.Enabled {
		return inventory.NotAsked(inventory.IOS)
	}
	devices, err := p.listBounded(ctx)
	if err != nil {
		// THE DEFECT THIS REPO STARTS WITHOUT: a usbmuxd that is not running produced an empty device
		// list in the C# node's shape, indistinguishable from "no iPhone plugged in". Measured here
		// on 2026-08-21: go-ios v1.3.2 answers a dead socket with
		// `dial unix …: connect: no such file or directory` — an error, which becomes CouldNotAsk.
		return inventory.CouldNotAsk(inventory.IOS,
			fmt.Sprintf("usbmuxd could not be asked: %s", strings.TrimSpace(err.Error())),
			"Check the Apple device daemon is running: on macOS it ships with the OS (re-plug the "+
				"cable, or `sudo launchctl kickstart -k system/com.apple.usbmuxd`); on Linux install "+
				"and start usbmuxd.")
	}
	offerable, unusable, uncabled := classify(devices, p.OfferUncabled)
	return inventory.WithLeftOut(inventory.Answered(inventory.IOS, offerable, unusable), uncabled)
}

// listBounded makes the command's deadline REAL on this platform.
//
// WHAT IT DOES AND WHAT IT CANNOT DO, stated plainly because the difference matters: go-ios's
// ios.ListDevices() takes no context and cannot be cancelled mid-call, so this runs the lister on its
// own goroutine and stops WAITING for it when the context expires. The COMMAND is bounded — a hung
// usbmuxd becomes a CouldNotAsk with a remedy instead of a process that never returns — while the
// abandoned goroutine lives until the underlying socket call gives up. The buffered channel is what
// makes that safe: the late send never blocks and the goroutine always exits, so a hung probe leaks
// one goroutine for the life of a short-lived CLI process, and nothing more. When go-ios grows a
// context-aware ListDevices, this wrapper's body is the one place that changes.
func (p Probe) listBounded(ctx context.Context) ([]Device, error) {
	type answer struct {
		devices []Device
		err     error
	}
	done := make(chan answer, 1) // buffered: the abandoned goroutine must never block on the send
	go func() {
		devices, err := p.List(ctx)
		done <- answer{devices, err}
	}()

	select {
	case a := <-done:
		return a.devices, a.err
	case <-ctx.Done():
		return nil, fmt.Errorf("the device daemon did not answer within the probe's deadline: %w", ctx.Err())
	}
}

// Classify splits what usbmuxd reported into the two lists. A device whose state is empty or "paired"
// is offerable; anything else is attached-but-unusable and is SHOWN with its verbatim state, because
// "iPhone 15 — tap Trust on the device to continue" is worth more than a device that never appears.
//
// ONLY A PHONE ON THE CABLE IS OFFERABLE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1). usbmuxd also lists a phone it
// reaches over Wi-Fi, as "Network"; that one is left out unless the customer asks (Probe.OfferUncabled), and named with
// how it is reached. A phone usbmuxd lists BOTH ways is one phone, and appears once, as the one on its cable. Classify
// drops the left-out ones; classify keeps them.
func Classify(devices []Device) ([]inventory.Offerable, []inventory.Unusable) {
	offerable, unusable, _ := classify(devices, false)
	return offerable, unusable
}

func classify(devices []Device, offerUncabled bool) ([]inventory.Offerable, []inventory.Unusable, []inventory.LeftOut) {
	offerable := []inventory.Offerable{}
	unusable := []inventory.Unusable{}
	var uncabled []inventory.LeftOut
	cabled := map[string]bool{}
	for _, d := range devices {
		if IsCabled(d.Connection) {
			cabled[d.UDID] = true
		}
	}
	seen := map[string]bool{}
	for _, d := range devices {
		if cabled[d.UDID] && !IsCabled(d.Connection) {
			continue // the same phone, on its cable: that entry speaks for it
		}
		if seen[d.UDID] {
			continue
		}
		seen[d.UDID] = true
		if !IsCabled(d.Connection) && !offerUncabled {
			how := "reached over Wi-Fi or the network"
			if !strings.EqualFold(strings.TrimSpace(d.Connection), "Network") {
				how = "not on a USB cable, as far as usbmuxd says"
			}
			uncabled = append(uncabled, inventory.LeftOut{ID: d.UDID, How: how})
			continue
		}
		state := strings.ToLower(strings.TrimSpace(d.State))
		if state == "" || state == "paired" {
			// Manufacturer "Apple" is definitional for a usbmuxd device, not a guess; the rest is whatever the
			// lister established — for usbmuxd's listing, nothing (see UsbmuxdLister), so they stay unset.
			offerable = append(offerable, inventory.Offerable{
				ID: d.UDID, Model: d.Model, Manufacturer: "Apple", OsVersion: d.OsVersion,
			})
			continue
		}
		unusable = append(unusable, inventory.NewUnusable(d.UDID, state, remedyFor(state)))
	}
	return offerable, unusable, uncabled
}

func remedyFor(state string) string {
	switch {
	case strings.Contains(state, "pairingdialogresponsepending"), strings.Contains(state, "trust"):
		return "Unlock the device and tap Trust on the 'Trust This Computer?' dialog, then re-run."
	case strings.Contains(state, "userdeniedpairing"):
		return "Trust was declined: on the device, Settings → General → Transfer or Reset → Reset Location & Privacy, re-plug, then tap Trust."
	case strings.Contains(state, "passwordprotected"):
		return "Unlock the device with its passcode while it is plugged in, then re-run."
	case strings.Contains(state, "invalidhostid"), strings.Contains(state, "unpaired"):
		return "The pairing record on this host is stale — re-plug the device and accept the Trust prompt again."
	default:
		return "The device reports a state this agent does not recognise (shown verbatim above) — " +
			"unlock it, re-plug the cable, and if it persists check the state's meaning for your iOS version."
	}
}

// IsCabled says whether usbmuxd's transport for an entry is the USB cable. Anything else — "Network", or a word go-ios
// grows tomorrow, or nothing at all — is not.
func IsCabled(connection string) bool { return strings.EqualFold(strings.TrimSpace(connection), "USB") }
