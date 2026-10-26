// Package localconn is how this agent talks to a program on THIS machine — the customer's Appium, adb's server, a phone's
// screen server: over loopback only, and only when the program that answers runs as this account (or as root).
//
// WHY (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, item 3). Every account on a machine can listen on 127.0.0.1. On a machine other
// people log into, another account that holds Appium's port while the customer's Appium is down would be sent the
// workspace's runs, and would write every answer; one that holds a phone's mirror port would be shown to the workspace as
// that phone's screen; one that holds adb's port would decide which phones this machine has. The capture proxy already
// refuses connections from other accounts (internal/netcap/peer.go); this is the same rule for the connections the agent
// opens itself, asked of the same kernel.
//
// WHERE THE OWNER CANNOT BE FOUND — a system other than macOS or Linux, or a kernel that would not say — the connection is
// refused, with a sentence that says so. Not knowing whose program it is, is not knowing it is the customer's.
package localconn

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// Owner names the account whose program answered a connection this process dialled. nil = the kernel's answer
// (netcap.OwnerOfListenerBehind). A TEST SEAM: no flag or variable reaches it.
type Owner func(c net.Conn) (uid int, found bool)

// Dialer opens connections to one program on this machine.
type Dialer struct {
	// What the program is, in the words a sentence about it uses: "your Appium", "adb's server", "a phone's live view".
	What    string
	Timeout time.Duration
	Owner   Owner
}

// DialContext dials address over IPv4 loopback — whatever the network asked for, and whatever a name resolves to — and keeps
// the connection only when the program that answered runs as this account or as root.
func (d Dialer) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout, Control: func(_, resolved string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(resolved)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return &NotOnThisMachine{What: d.What, Host: host}
		}
		return nil
	}}
	conn, err := dialer.DialContext(ctx, "tcp4", address)
	if err != nil {
		return nil, err
	}
	owner := d.Owner
	if owner == nil {
		owner = netcap.OwnerOfListenerBehind
	}
	if uid, found := owner(conn); !found || !netcap.MayUseTheProxy(uid) {
		_ = conn.Close()
		return nil, &NotThisAccount{What: d.What, Address: conn.RemoteAddr().String(), UID: uid, Found: found}
	}
	return conn, nil
}

// Client is an HTTP client for one program on this machine: through Dialer, never through a proxy of this machine's
// environment, and never following a redirect (a redirect would be a second address nobody checked).
func Client(what string, timeout time.Duration, owner Owner) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: Dialer{What: what, Owner: owner}.DialContext,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NotThisAccount is a program on this machine that runs as another account, or whose account could not be found.
type NotThisAccount struct {
	What    string
	Address string
	UID     int
	Found   bool
}

func (e *NotThisAccount) Error() string {
	if !e.Found {
		return fmt.Sprintf("the agent could not tell which account runs the program on %s, so it will not use it as %s", e.Address, e.What)
	}
	return fmt.Sprintf("the program on %s runs as another account on this machine (uid %d), so the agent will not use it as %s",
		e.Address, e.UID, e.What)
}

// NotOnThisMachine is an address that is not this machine's loopback.
type NotOnThisMachine struct {
	What string
	Host string
}

func (e *NotOnThisMachine) Error() string {
	return fmt.Sprintf("the agent uses %s only on this machine (127.0.0.1 or localhost), and %s is not", e.What, e.Host)
}
