package ios

import (
	"context"

	goios "github.com/danielpaulus/go-ios/ios"
)

// UsbmuxdLister is the ONE place this repo touches go-ios. Everything above it speaks the Device /
// Lister types in probe.go, which is what keeps every test free of a usbmuxd.
//
// MEASURED on this machine (2026-08-21, go-ios v1.3.2, macOS): a healthy daemon with nothing attached
// returns (list, nil) with len(DeviceList)==0 — a real Answered-with-zero. A missing socket returns an
// error (`dial unix …: connect: no such file or directory`) — which becomes CouldNotAsk, never an
// empty list. That difference is the whole slice.
func UsbmuxdLister(ctx context.Context) ([]Device, error) { return listUsbmuxd(false) }

// UsbmuxdListerFor is UsbmuxdLister for a customer who answered inventory.OfferUncabledFlag: with it, a phone usbmuxd
// reaches over the network is offered, so it is asked its name as well.
func UsbmuxdListerFor(offerUncabled bool) Lister {
	return func(context.Context) ([]Device, error) { return listUsbmuxd(offerUncabled) }
}

func listUsbmuxd(nameUncabled bool) ([]Device, error) {
	list, err := goios.ListDevices()
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(list.DeviceList))
	for _, entry := range list.DeviceList {
		device := Device{
			UDID: entry.Properties.SerialNumber,
			// State is left EMPTY here on purpose: usbmuxd's ListDevices reports attachment, not
			// pairing. Establishing "attached but not trusted" needs a lockdown handshake per device,
			// which is a network-shaped act this slice does not take (no pairing, no session). The
			// classifier therefore reports what usbmuxd could establish, and the unusable path is
			// exercised by the lister seam until the pairing probe lands in its own slice.
			State:      "",
			Connection: entry.Properties.ConnectionType,
		}
		// THE PHONE'S OWN NAME. usbmuxd's listing establishes a udid and a cable type, and a cable type is not a
		// model — so this used to leave Model empty and the product rendered the operator's iPhone as "Ios", the
		// platform's name where the phone's name belongs.
		//
		// WHAT THIS COSTS, MEASURED FROM THE CALL: goios.GetValues opens a lockdown session per device
		// (ConnectLockdownWithSession → GetValues → Close), so a machine with N attached devices makes N short
		// connections on every probe rather than one listing call. They are sequential and each is a local USB
		// round trip; the probe's own 20s budget bounds the lot (main.probeTimeout).
		//
		// IT IS PAIRING-DEPENDENT, AND THAT IS WHY THE FAILURE IS SILENT. An untrusted or locked device refuses
		// the session — which is not a fault to report here: the classifier already has a state and a remedy for
		// that phone, and this read adds a name when it can. THE ANDROID RULE, UNCHANGED: the platform's own
		// answer, EMPTY when the platform did not answer, never a guess.
		//
		// ONLY OVER THE CABLE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1): a phone usbmuxd reaches over the network is
		// left out unless the customer asks, so it is not opened a session to just to learn its name.
		if !IsCabled(device.Connection) && !nameUncabled {
			devices = append(devices, device)
			continue
		}
		if values, err := goios.GetValues(entry); err == nil {
			device.Model = firstNonEmpty(values.Value.DeviceName, values.Value.ProductType)
			device.OsVersion = values.Value.ProductVersion
		}
		devices = append(devices, device)
	}
	return devices, nil
}

// firstNonEmpty returns the first value the device actually answered with. DeviceName is what a person named the
// phone ("Ada's iPhone"); ProductType is the model identifier ("iPhone14,5") — either beats the platform's name,
// and neither is invented when both are absent.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
