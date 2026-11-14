package browserstack

import (
	"sort"
	"strings"
)

// WHAT GOES TO BROWSERSTACK WITH THE CUSTOMER'S KEY (WHAT_A_LEASE_CAN_ASK). A cloud session's create is written by whoever
// asked the farm for it, and the agent sends it to BrowserStack authenticated by the customer's own key. So the vendor
// block is built from a list, not extended: the agent's own values (the credentials, the idle timeout, the run's facts and
// the build) and the few labels a session may carry. Everything else in it — BrowserStack Local and its identifier, other
// apps from the account's storage, uploaded media, network and device logs, and any option nobody listed — is left out,
// and so are the older top-level `browserstack.*` names and `otherApps` beside the block. Each is named on the customer's
// terminal; the session starts without them. The farm's runner sends none of them.

// vendorOptionsKept are the names inside bstack:options a session may carry as it came: labels on the account's dashboard,
// and which of BrowserStack's Appium versions runs it. The agent's own writes (userName, accessKey, idleTimeout and the run's
// facts) replace whatever came under their names.
var vendorOptionsKept = map[string]bool{
	"projectName": true, "buildName": true, "buildTag": true, "sessionName": true, "appiumVersion": true,
	"deviceOrientation": true,
}

// vendorOptionsWritten are the names inside bstack:options the agent writes itself (apply).
var vendorOptionsWritten = map[string]bool{"userName": true, "accessKey": true, "idleTimeout": true}

// leaveOut takes out of one capability set what is not sent with the customer's key, and names it. A target set keeps its
// vendor block with only the listed names; a set that is not a target loses its block entirely (Merge's third clause), and
// what it held beyond the list is named the same.
func leaveOut(set map[string]any, target bool) []string {
	var out []string
	for name := range set {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "browserstack.") || lower == "otherapps" || lower == "appium:otherapps" {
			delete(set, name)
			out = append(out, name)
		}
	}
	options, ok := set["bstack:options"].(map[string]any)
	if !ok {
		return out
	}
	kept := map[string]any{}
	for name, value := range options {
		switch {
		case vendorOptionsKept[name]:
			kept[name] = value
		case vendorOptionsWritten[name]:
		default:
			out = append(out, "bstack:options."+name)
		}
	}
	if target {
		set["bstack:options"] = kept
	}
	return out
}

// named is names once each, in order.
func named(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	out := names[:0]
	for i, name := range names {
		if i == 0 || name != names[i-1] {
			out = append(out, name)
		}
	}
	return out
}
