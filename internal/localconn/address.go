package localconn

import (
	"net"
	"net/url"
	"sort"
	"strconv"
)

// AN APPIUM ONLY THIS COMPUTER CAN REACH. The agent reaches the customer's Appium over loopback, and checks which account
// runs it (Dialer above). Neither check says where Appium itself listens, and Appium asks a client for nothing: an Appium
// that listens on every address of the computer can be driven by anything on the network that reaches it, past every check
// this agent makes on what the farm asks. Started as `appium --address 127.0.0.1` it listens on loopback alone. So doctor and
// connect read where the program on Appium's port is bound — from the kernel's own table (netcap.ListenersOn), sending
// nothing anywhere — and say so when it is anywhere but loopback.

// AppiumOnLoopbackOnly is the command every sentence of this agent gives for starting Appium.
const AppiumOnLoopbackOnly = "appium --address 127.0.0.1"

// AppiumBeyondLoopbackWhy is why an Appium on more than loopback matters, in one sentence.
const AppiumBeyondLoopbackWhy = "Appium asks a client for nothing, so a program on another computer that reaches it drives your phones " +
	"directly, past every check this agent makes."

// AppiumBeyondLoopbackDo is what to do about it.
const AppiumBeyondLoopbackDo = "stop Appium, and start Appium again on this computer's own address only: appium --address 127.0.0.1"

// Listeners names where each listener on a port is bound (netcap.ListenersOn in production).
type Listeners func(port int) (addresses []net.IP, found bool)

// AlsoListensOn names each address other than loopback that the program on appiumURL's port listens on — "0.0.0.0 (every
// address of this computer)", or one of its network addresses — sorted, and whether the kernel's table could be read at all.
func AlsoListensOn(appiumURL string, listeners Listeners) (beyond []string, known bool) {
	if listeners == nil {
		return nil, false
	}
	u, err := url.Parse(appiumURL)
	if err != nil {
		return nil, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		switch u.Scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		default:
			return nil, false
		}
	}
	addresses, found := listeners(port)
	if !found {
		return nil, false
	}
	seen := map[string]bool{}
	for _, ip := range addresses {
		if ip.IsLoopback() {
			continue
		}
		name := ip.String()
		if ip.IsUnspecified() {
			name += " (every address of this computer)"
		}
		if !seen[name] {
			seen[name] = true
			beyond = append(beyond, name)
		}
	}
	sort.Strings(beyond)
	return beyond, true
}
