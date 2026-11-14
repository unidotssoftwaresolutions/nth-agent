package tunnel

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// ── A SESSION FROM A LEASE DOES NOT CHANGE THE CUSTOMER'S APPLE ACCOUNT (A_MACHINE_THAT_STAYS_THE_CUSTOMERS) ────
//
// A local session-create reaches the customer's Appium as the consumer built it. Among XCUITest's capabilities exactly one
// changes the customer's Apple developer account: appium:allowProvisioningDeviceRegistration. With it WebDriverAgent's
// build runs xcodebuild with -allowProvisioningUpdates -allowProvisioningDeviceRegistration, which registers the phone with
// the customer's team and may create and update provisioning profiles there (appium-webdriveragent's xcodebuild, read
// 2026-10-20). That is the yes `npx nthcase-agent setup-ios` asks the person at this machine for, typed; a lease holder
// cannot give it.
//
// The capabilities an iOS run sends every time — appium:xcodeOrgId, appium:xcodeSigningId, appium:updatedWDABundleId —
// choose WHICH team signs and with what; without -allowProvisioningUpdates xcodebuild cannot change anything at Apple with
// them, so they go through as before.
//
// REFUSED, NOT STRIPPED: the body is relayed byte for byte or not at all — a create quietly changed on the way would run
// differently from the one its author wrote, and say nothing. Found wherever Appium would read it: alwaysMatch, every
// firstMatch, appium:options, the old desiredCapabilities, with or without the appium: prefix, in any letter case, and
// through JSON escapes (the body is decoded before it is read). Any value but false (or null) is refused.

const appleRegistrationCapability = "allowprovisioningdeviceregistration"

// refusedAppleAccountChange is the sentence a refused create is answered with, and the console says. The answer is read
// away from this machine, by whoever holds the lease, so its command carries no path of this machine (command.OnThatMachine).
func refusedAppleAccountChange() string {
	return "this session asked the Appium on this machine to register the phone with your Apple " +
		"developer team (appium:allowProvisioningDeviceRegistration), and only the person at this machine may do that — with " +
		command.OnThatMachine("setup-ios") + " — so it was refused and nothing was sent to Appium"
}

// changesTheAppleAccount says whether a local session-create's body asks for that capability.
func changesTheAppleAccount(body []byte) bool {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		// NOT JSON Appium could read either; the raw bytes are still looked at, so a body this reader cannot parse is not
		// a way past it.
		return strings.Contains(strings.ToLower(string(body)), appleRegistrationCapability)
	}
	return asksForRegistration(decoded)
}

// asksForRegistration walks every object in the body: a capability can be nested where this reader does not expect it, and
// a key that is not a capability costs nothing to look at.
func asksForRegistration(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, inner := range v {
			name := strings.ToLower(strings.TrimSpace(key))
			name = strings.TrimPrefix(name, "appium:")
			if name == appleRegistrationCapability && inner != nil && inner != false {
				return true
			}
			if asksForRegistration(inner) {
				return true
			}
		}
	case []any:
		for _, inner := range v {
			if asksForRegistration(inner) {
				return true
			}
		}
	}
	return false
}
