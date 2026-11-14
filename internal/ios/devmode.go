package ios

import (
	"context"
	"fmt"
	"strings"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
)

// DEVELOPER MODE IS THE PHONE'S OWN GATE, AND ONLY THE PHONE CAN ANSWER IT.
//
// Since iOS 16 a device refuses to install a developer-signed app — WebDriverAgent included — until Developer Mode
// is switched on IN Settings, by a person, on the handset. No amount of toolchain on the Mac substitutes for it,
// which is exactly why it belongs in a report about what a machine can do: an operator with Xcode, the xcuitest
// driver and a trusted cable can still be one toggle away from every session failing.
//
// IT IS A READ. `imagemounter.IsDevModeEnabled` opens the phone's mobile_image_mounter service and asks
// `QueryDeveloperModeStatus`; it mounts nothing, installs nothing and changes nothing — the same class of call as
// the lockdown `GetValues` the device probe already makes on every listing.

// DeveloperMode is ONE phone's answer. Enabled is meaningful only when Err is empty: a phone that could not be
// asked is not a phone that said no, and this type refuses to let those two collapse into one boolean.
type DeveloperMode struct {
	UDID    string
	Enabled bool
	Err     string
}

// ReadDeveloperMode asks every attached device. A device that refuses the question keeps its place in the list
// with its error — an unanswerable phone is a fact about this machine, not a phone to drop.
//
// The error return is for the question that could not be asked AT ALL (no usbmuxd, no daemon): that is the same
// distinction Probe draws between CouldNotAsk and Answered, in the vocabulary this agent already speaks.
func ReadDeveloperMode(ctx context.Context) ([]DeveloperMode, error) {
	entries, err := goios.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("usbmuxd could not be asked: %w", err)
	}
	readings := make([]DeveloperMode, 0, len(entries.DeviceList))
	for _, entry := range entries.DeviceList {
		if ctx.Err() != nil {
			return readings, ctx.Err()
		}
		// ONLY OVER THE CABLE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS): a phone usbmuxd reaches over the network is not
		// opened a session to, and a phone it lists both ways is asked once, on its cable. Such a phone, offered with
		// --offer-uncabled-devices, reads as not asked — Unknown in the doctor, never a guess.
		if !IsCabled(entry.Properties.ConnectionType) {
			continue
		}
		reading := DeveloperMode{UDID: entry.Properties.SerialNumber}
		enabled, err := imagemounter.IsDevModeEnabled(entry)
		if err != nil {
			reading.Err = strings.TrimSpace(err.Error())
		} else {
			reading.Enabled = enabled
		}
		readings = append(readings, reading)
	}
	return readings, nil
}
