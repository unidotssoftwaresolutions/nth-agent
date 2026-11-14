package connect

import (
	"fmt"
	"net"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── AN IPHONE'S WEBDRIVERAGENT PORT, ASKED AS ITS SESSION STARTS (ANOTHER_ACCOUNT_AND_LIMITS) ────────────────────────
//
// For each iPhone this machine declares a port on 127.0.0.1, and the farm pins the session's wdaLocalPort to it. The
// driver opens its forward to the phone's WebDriverAgent there; a program another account started on that port first
// would be taken for the phone's WebDriverAgent and sent the session's commands and typed values. The agent holds
// nothing on the port between sessions, so it is asked again as each session starts, of the kernel's own table of
// listeners: a program on it that another account runs, or whose account the system will not name, refuses the session.
// A session that names another phone's declared port is refused too: it would be driven through that phone's
// WebDriverAgent.

// WdaPortGuard is the question for allowed.Phones.OfferHeld: declared is each offered iPhone's declared port, listeners
// the kernel's table (nil = netcap.ListenerOwnersOn).
func WdaPortGuard(declared map[string]int, listeners func(port int) ([]netcap.Listener, bool)) func(udid string, wdaPort int) string {
	if listeners == nil {
		listeners = netcap.ListenerOwnersOn
	}
	return func(udid string, wdaPort int) string {
		own, isIPhone := declared[udid]
		if !isIPhone {
			return ""
		}
		port := own
		if wdaPort != 0 && wdaPort != own {
			for other, theirs := range declared {
				if other != udid && theirs == wdaPort {
					return fmt.Sprintf("the farm asked to start a session on iPhone %s with wdaLocalPort %d, the port this machine "+
						"declared for another phone's WebDriverAgent (%s), so the session would be driven through that phone", udid, wdaPort, other)
				}
			}
			port = wdaPort
		}
		all, read := listeners(port)
		if !read {
			return fmt.Sprintf("the farm asked to start a session on iPhone %s, and the agent could not tell who holds its "+
				"WebDriverAgent port %d on this machine (the system would not say)", udid, port)
		}
		for _, l := range all {
			if !l.Takes(net.IPv4(127, 0, 0, 1)) && !l.Takes(net.IPv6loopback) {
				continue
			}
			switch {
			case !l.Known:
				return fmt.Sprintf("the farm asked to start a session on iPhone %s, whose WebDriverAgent port %d on this machine "+
					"is held by a program the agent could not tell is yours (the system would not say which account runs it)", udid, port)
			case !netcap.MayServeThisAccount(l.UID):
				return fmt.Sprintf("the farm asked to start a session on iPhone %s, whose WebDriverAgent port %d on this machine "+
					"is held by a program another account runs (uid %d), which the driver would take for the phone's WebDriverAgent", udid, port, l.UID)
			}
		}
		return ""
	}
}

// declaredWdaPorts is the port each of this machine's iPhones is declared with on the rows report publishes.
func declaredWdaPorts(report farm.Report) map[string]int {
	declared := map[string]int{}
	for _, device := range report.Devices {
		if device.Descriptor.CloudSpec == nil && device.Descriptor.WdaPort != nil {
			declared[device.Descriptor.Udid] = *device.Descriptor.WdaPort
		}
	}
	return declared
}
