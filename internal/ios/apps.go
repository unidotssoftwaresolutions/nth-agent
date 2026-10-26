package ios

import (
	"fmt"
	"strings"

	"github.com/danielpaulus/go-ios/ios/installationproxy"
)

// AppInstalled asks ONE phone whether an app with this bundle id is installed — installation_proxy over usbmuxd,
// the same transport the device listing already uses: no tunnel, nothing privileged, nothing changed on the
// phone (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE).
//
// WHY IT EXISTS: "WebDriverAgent does not answer" alone is the RESTING state of a healthy phone (this agent's
// own idle tracker stops WDA between sessions), and a first-ever run has nothing installed yet. Only INSTALLED
// AND SILENT is the signature the trust inference reads, and installed-ness is the half this call establishes.
// User apps only: a developer-signed runner installs as a user app, and walking the system list would be a
// slower answer to a question it cannot change.
func AppInstalled(udid, bundleID string) (bool, error) {
	device, err := deviceOnTheCable(udid) // the USB entry when usbmuxd lists the phone twice
	if err != nil {
		return false, err
	}
	conn, err := installationproxy.New(device)
	if err != nil {
		return false, fmt.Errorf("could not ask the phone about its apps: %w", err)
	}
	defer conn.Close()
	apps, err := conn.BrowseUserApps()
	if err != nil {
		return false, fmt.Errorf("the phone did not answer the app question: %w", err)
	}
	for _, app := range apps {
		if strings.EqualFold(app.CFBundleIdentifier(), bundleID) {
			return true, nil
		}
	}
	return false, nil
}
