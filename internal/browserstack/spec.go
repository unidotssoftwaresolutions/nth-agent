package browserstack

import (
	"fmt"
	"sort"
	"strings"
)

// SpecPrefix marks a device identifier as a cloud SPECIFICATION rather than a serial.
//
// THE PREFIX IS LOAD-BEARING, not decoration. This string travels as a device's identifier all the way into the
// platform's session body (`appium:udid`), and every reader that predates cloud rows assumes a serial there. A
// value that cannot be mistaken for one is what makes the agent's parse TOTAL: it either recognises a spec and
// translates it, or it does not and leaves the value alone.
const SpecPrefix = "browserstack:"

// specSeparator divides the device name from the OS version. A device name contains spaces ("Google Pixel 9")
// so the value needs quoting in a shell either way; `|` cannot occur in a device name or an OS version, which a
// space, a slash and a dash all can.
const specSeparator = "|"

// DeviceSpec is what a customer asks their BrowserStack account for: a device name and an OS version.
//
// TWO CUSTOMERS ASKING FOR THE SAME SPEC GET DIFFERENT HANDSETS, and that is why this is not an identity. It is
// what the row STANDS FOR, which is the only thing that is true of a device nobody here can touch.
type DeviceSpec struct {
	DeviceName      string
	PlatformVersion string
}

// ParseSpec reads the customer's own input — "Google Pixel 9|15.0".
func ParseSpec(value string) (DeviceSpec, error) {
	name, version, found := strings.Cut(strings.TrimSpace(value), specSeparator)
	name, version = strings.TrimSpace(name), strings.TrimSpace(version)
	if !found || name == "" || version == "" {
		return DeviceSpec{}, fmt.Errorf(
			"a BrowserStack device is a name and an OS version separated by %q, e.g. \"Google Pixel 9%s15.0\" — got %q",
			specSeparator, specSeparator, value)
	}
	return DeviceSpec{DeviceName: name, PlatformVersion: version}, nil
}

// Wire is the identifier this spec travels under: `browserstack:Google Pixel 9|15.0`.
func (s DeviceSpec) Wire() string {
	return SpecPrefix + s.DeviceName + specSeparator + s.PlatformVersion
}

// String is what a person reads.
func (s DeviceSpec) String() string { return s.DeviceName + " / " + s.PlatformVersion }

// ParseWire reads a spec back off the wire, and reports whether the value was one at all.
//
// FALSE IS THE ORDINARY ANSWER. Every local device's identifier is a serial, and this is asked of every session
// the agent forwards — so "not a spec" must be cheap, total, and must leave the value untouched.
func ParseWire(value string) (DeviceSpec, bool) {
	if !strings.HasPrefix(value, SpecPrefix) {
		return DeviceSpec{}, false
	}
	spec, err := ParseSpec(strings.TrimPrefix(value, SpecPrefix))
	if err != nil {
		return DeviceSpec{}, false
	}
	return spec, true
}

// CatalogueDevice is one row of the account's own device list (`app-automate/devices.json`).
type CatalogueDevice struct {
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Device    string `json:"device"`
	Real      bool   `json:"realMobile"`
}

// Match finds the catalogue entry a spec names, and reports whether the account offers it at all.
//
// THE ENTRY, NOT A BOOLEAN — and that is the point. The catalogue carries each device's OS, and an earlier
// version of this asked only "does it exist" and threw the rest away. The row was then published as Android
// whatever the customer had asked for, so `--browserstack-device "iPhone 15|17.0"` — a spec BrowserStack really
// offers, so it passed validation — produced a row asserting a platform nobody established. The row was not
// absent; it was WRONG, which is worse.
func Match(catalogue []CatalogueDevice, spec DeviceSpec) (CatalogueDevice, bool) {
	for _, d := range catalogue {
		if strings.EqualFold(d.Device, spec.DeviceName) && strings.EqualFold(d.OSVersion, spec.PlatformVersion) {
			return d, true
		}
	}
	return CatalogueDevice{}, false
}

// Offers reports whether the account's catalogue contains this exact spec.
func Offers(catalogue []CatalogueDevice, spec DeviceSpec) bool {
	_, ok := Match(catalogue, spec)
	return ok
}

// IsIos reports whether a catalogue entry is an iOS device. BrowserStack writes "ios"/"android" in `os`.
func (d CatalogueDevice) IsIos() bool { return strings.EqualFold(strings.TrimSpace(d.OS), "ios") }

// NearestTo lists the catalogue entries a person most likely meant — same device, other versions first, then
// devices whose name contains the same words.
//
// THE REFUSAL IS ONLY AS GOOD AS THIS LIST. "That device is not on your account" sends somebody to a web page;
// "…here are the three versions of it that are" ends the question.
func NearestTo(catalogue []CatalogueDevice, spec DeviceSpec) []string {
	var sameDevice, similar []string
	wanted := strings.ToLower(spec.DeviceName)
	for _, d := range catalogue {
		entry := d.Device + specSeparator + d.OSVersion
		switch name := strings.ToLower(d.Device); {
		case name == wanted:
			sameDevice = append(sameDevice, entry)
		case strings.Contains(name, wanted) || strings.Contains(wanted, name):
			similar = append(similar, entry)
		}
	}
	sort.Strings(sameDevice)
	sort.Strings(similar)
	nearest := append(sameDevice, similar...)
	if len(nearest) > 8 {
		nearest = nearest[:8] // a refusal is a sentence, not a catalogue dump
	}
	return nearest
}

// RefusalFor is the sentence a customer reads when they asked for a device their account does not offer.
//
// REFUSED ON THEIR OWN MACHINE, AT STARTUP, and for the same reason the missing-build refusal is: the hub's own
// answer for this mistake is "Platform and Browser not valid.", which names a platform and a browser that are
// both fine and never mentions the device list. This one names the list.
func RefusalFor(spec DeviceSpec, catalogue []CatalogueDevice) string {
	nearest := NearestTo(catalogue, spec)
	if len(nearest) == 0 {
		return fmt.Sprintf("your BrowserStack account does not offer %q, and nothing on it has a similar name — "+
			"check the device list in the App Automate dashboard. (%d devices are on the account.)",
			spec, len(catalogue))
	}
	return fmt.Sprintf("your BrowserStack account does not offer %q. It does offer:\n  %s",
		spec, strings.Join(nearest, "\n  "))
}

// RefuseUnofferedSpecs is the whole of the startup check: the sentence to print, or "" when every configured
// device is one the account actually offers.
//
// A FUNCTION RATHER THAN THREE LINES AT THE CALL SITE, and the reason is that the three lines were untestable.
// They lived inside runConnect between a network call and a process exit, so an injection that accepted a device
// the account does not offer turned NOTHING red — the refusal was real and nothing could reach it. A refusal
// nobody can test is a refusal nobody knows still fires.
func RefuseUnofferedSpecs(specs []DeviceSpec, catalogue []CatalogueDevice) string {
	for _, spec := range specs {
		if !Offers(catalogue, spec) {
			return RefusalFor(spec, catalogue)
		}
	}
	return ""
}
