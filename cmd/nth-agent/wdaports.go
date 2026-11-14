package main

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// wdaPortStatus is what is on one local port, in WebDriverAgent's terms, for the chooser that declares an iPhone's port
// and for doctor (ANOTHER_ACCOUNT_AND_LIMITS). The kernel's table is asked first (listeners; nil =
// netcap.ListenerOwnersOn): a port another account holds, or whose holder the system will not name, is a stranger's —
// RunnerUnknown, which the chooser counts as taken — and the program on it is not asked anything. WebDriverAgent's
// answer counts as this machine's own only from a program this account runs.
func wdaPortStatus(ctx context.Context, client *http.Client, port uint16, listeners func(int) ([]netcap.Listener, bool)) wda.RunnerStatus {
	if listeners == nil {
		listeners = netcap.ListenerOwnersOn
	}
	if all, read := listeners(int(port)); read {
		for _, l := range all {
			if !l.Takes(net.IPv4(127, 0, 0, 1)) && !l.Takes(net.IPv6loopback) {
				continue
			}
			switch {
			case !l.Known:
				return wda.RunnerStatus{State: wda.RunnerUnknown, Reason: fmt.Sprintf("the system would not say which account runs the program on port %d", port)}
			case !netcap.MayServeThisAccount(l.UID):
				return wda.RunnerStatus{State: wda.RunnerUnknown, Reason: fmt.Sprintf("a program another account runs (uid %d) holds port %d", l.UID, port)}
			}
		}
	}
	return wda.ReadStatus(ctx, client, fmt.Sprintf("http://127.0.0.1:%d", port))
}
