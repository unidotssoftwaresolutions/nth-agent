package farm

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// ── A FARM ON THIS MACHINE'S LOOPBACK IS THIS ACCOUNT'S, OR IT IS SENT NOTHING (ANOTHER_ACCOUNT_AND_LIMITS) ──────────
//
// ValidateBaseURL lets plain http through to a loopback farm because a loopback packet never leaves this machine. That
// is true, and it is not the whole question: every account on the machine can listen on 127.0.0.1, and one that held
// the farm's port while a local farm was down would have been sent the workspace token with the first request. So a
// connection to this machine's loopback, for the farm's calls or the tunnel's upgrade, is made the way the agent
// reaches Appium (internal/localconn): over 127.0.0.1, kept only when the program that took it runs as this account,
// and closed before a byte of the request is written otherwise. A farm anywhere else is dialled as before.

// DialFunc is a transport's dial.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// DialTimeout is how long a dial to the farm may take: Go's default transport's own.
const DialTimeout = 30 * time.Second

// OnLoopbackOnlyThisAccount wraps dial: an address on this machine's loopback is dialled through localconn, whose owner
// question owner answers (nil = the kernel's); any other address goes to dial unchanged.
func OnLoopbackOnlyThisAccount(dial DialFunc, owner localconn.Owner) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !loopbackFarmHost(host) {
			return dial(ctx, network, address)
		}
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return nil, fmt.Errorf("the farm at %s is on this machine's IPv6 loopback, where the agent cannot tell which account runs it, "+
				"so it sent it nothing — write the farm's address as 127.0.0.1 or localhost", address)
		}
		conn, err := localconn.Dialer{What: "the farm", Timeout: DialTimeout, Owner: owner}.DialContext(ctx, network, address)
		if err != nil {
			return nil, fmt.Errorf("%w — the workspace token was not sent", err)
		}
		return conn, nil
	}
}

// loopbackFarmHost is the host ValidateBaseURL lets plain http reach: exactly localhost, or a loopback address.
func loopbackFarmHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
