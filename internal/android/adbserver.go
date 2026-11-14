package android

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── adb's SERVER IS THIS ACCOUNT'S, OR adb IS NOT ASKED (A_MACHINE_THAT_STAYS_THE_CUSTOMERS) ───────────────────
//
// Every adb command goes to adb's background server, on 127.0.0.1:5037 unless the environment says otherwise, and the
// server is what answers: which phones are attached, what each one is, what a capture's proxy is set to. Another account
// on this machine that holds that port while this account's server is not running would answer all of it — invent phones
// for this machine to offer, or hide the real ones.
//
// SO, AROUND EVERY adb COMMAND, the server's port is asked who holds it, of the kernel's own table of listeners — on
// every address adb's client may reach: the one it is told, and for localhost ::1 as well as 127.0.0.1
// (ANOTHER_ACCOUNT_AND_LIMITS):
//   - nothing listening anywhere adb may reach: adb starts its own server, as this account — the ordinary first run;
//   - this account, or root on Linux and Windows (adb started with sudo on Linux runs as root): used — and the connection
//     the agent makes to it is asked too (internal/localconn); a listener that does not take that connection is not
//     "nothing listening", it is refused;
//   - another account, or an account the kernel would not name: adb is not run, and the reason says so.
// It is asked again AFTER the command, because the server that answered is the one listening then: an answer adb gave,
// with no server of this account's listening once it has, came from a program that has gone, and is not used.
//
// A SERVER THE ENVIRONMENT PUTS ELSEWHERE — another host, or a Unix socket — cannot be asked that question, so it is not
// used. The variable is named; its value is not repeated.

// adbServerDefaultPort is adb's own.
const adbServerDefaultPort = 5037

// serverCheckTimeout bounds the one connection the check makes. It never starts a server.
const serverCheckTimeout = 700 * time.Millisecond

// ServerAddress is where adb's server is for this process, read the way adb reads it: ADB_SERVER_SOCKET first
// ("tcp:PORT" or "tcp:HOST:PORT"), else ANDROID_ADB_SERVER_ADDRESS and ANDROID_ADB_SERVER_PORT. It is always this
// machine's 127.0.0.1 — anything else comes back as refusal, a sentence naming the variable.
func ServerAddress(getenv func(string) string) (address, refusal string) {
	address, _, refusal = serverAddress(getenv)
	return address, refusal
}

// serverAddress is ServerAddress, and whether adb's client is told "localhost", which it reaches on ::1 as well.
func serverAddress(getenv func(string) string) (address string, localhost bool, refusal string) {
	host, port := "localhost", strconv.Itoa(adbServerDefaultPort)
	if socket := strings.TrimSpace(getenv("ADB_SERVER_SOCKET")); socket != "" {
		rest, isTCP := strings.CutPrefix(socket, "tcp:")
		if !isTCP {
			return "", false, elsewhere("ADB_SERVER_SOCKET")
		}
		if h, p, err := net.SplitHostPort(rest); err == nil {
			host, port = h, p
		} else {
			port = rest
		}
		if !loopbackHost(host) {
			return "", false, elsewhere("ADB_SERVER_SOCKET")
		}
	} else {
		if h := strings.TrimSpace(getenv("ANDROID_ADB_SERVER_ADDRESS")); h != "" {
			if !loopbackHost(h) {
				return "", false, elsewhere("ANDROID_ADB_SERVER_ADDRESS")
			}
			host = h
		}
		if p := strings.TrimSpace(getenv("ANDROID_ADB_SERVER_PORT")); p != "" {
			port = p
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return "", false, "adb's server port in the environment is not a port number, so adb was not asked"
	}
	// THE ADDRESS adb DIALS: the IPv4 loopback address it is told, or 127.0.0.1 for localhost (and ::1 after it).
	if strings.EqualFold(host, "localhost") {
		return net.JoinHostPort("127.0.0.1", strconv.FormatUint(number, 10)), true, ""
	}
	return net.JoinHostPort(net.ParseIP(host).String(), strconv.FormatUint(number, 10)), false, ""
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && ip.IsLoopback()
}

func elsewhere(variable string) string {
	return variable + " points adb at a server that is not on this machine's 127.0.0.1, and the agent uses adb only " +
		"through a server it can tell you run, so adb was not asked"
}

// CheckServer says whether adb's server may be used before a command: nil when nothing listens yet or this account (or
// root, on Linux and Windows) runs it; an error with the reason otherwise. owner nil = the kernel's answer
// (ServerGuard.Before).
func CheckServer(ctx context.Context, getenv func(string) string, owner localconn.Owner) error {
	return ServerGuard{Owner: owner}.Before(ctx, getenv)
}

// ServerRemedy is what a person does about a server CheckServer refused.
const ServerRemedy = "Stop the adb server that is not yours (`adb kill-server` as the account that runs it, or ask its owner), " +
	"then run `adb start-server` as yourself — or unset the variable that points adb elsewhere."

func runAdbAsThisAccount(ctx context.Context, owner localconn.Owner, args ...string) (string, error) {
	return runAdbGuarded(ctx, ServerGuard{Owner: owner}, execAdb, args...)
}

// ServerGuard is the question asked around every adb command. Owner names the account behind a connection, Listeners
// every listener on a port and who made it; nil = the kernel's answer for each. A TEST SEAM: no flag or variable
// reaches it.
type ServerGuard struct {
	Owner     localconn.Owner
	Listeners func(port int) ([]netcap.Listener, bool)
}

// Before is asked before adb runs.
func (g ServerGuard) Before(ctx context.Context, getenv func(string) string) error {
	return g.check(ctx, getenv, false)
}

// After is asked once adb has run; answered says adb ran and exited 0, so a server answered it, and one of this
// account's must be listening now.
func (g ServerGuard) After(ctx context.Context, getenv func(string) string, answered bool) error {
	return g.check(ctx, getenv, answered)
}

func (g ServerGuard) check(ctx context.Context, getenv func(string) string, answered bool) error {
	address, localhost, refusal := serverAddress(getenv)
	if refusal != "" {
		return errors.New(refusal)
	}
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	dialled := net.ParseIP(host)
	listeners := g.Listeners
	if listeners == nil {
		listeners = netcap.ListenerOwnersOn
	}
	all, read := listeners(port)
	if !read {
		return &localconn.NotThisAccount{What: "adb's server", Address: address}
	}
	takesDialled := false
	held := 0
	for _, l := range all {
		if !l.Takes(dialled) && !(localhost && l.Takes(net.IPv6loopback)) {
			continue
		}
		held++
		at := net.JoinHostPort(l.IP.String(), portText)
		if !l.Known {
			return &localconn.NotThisAccount{What: "adb's server", Address: at}
		}
		if !netcap.MayServeThisAccount(l.UID) {
			return &localconn.NotThisAccount{What: "adb's server", Address: at, UID: l.UID, Found: true}
		}
		takesDialled = takesDialled || l.Takes(dialled)
	}
	switch {
	case held == 0 && answered:
		return &serverGone{address: address}
	case held == 0:
		return nil // nothing is listening: adb will start its own server, as this account
	case !takesDialled || answered:
		return nil
	}
	// Something this account runs listens where adb dials. The connection adb makes is asked too, as before.
	dialer := localconn.Dialer{What: "adb's server", Timeout: serverCheckTimeout, Owner: g.Owner}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	var notOurs *localconn.NotThisAccount
	switch {
	case errors.As(err, &notOurs):
		return notOurs
	case err != nil:
		return fmt.Errorf("something is listening on %s and did not take the agent's connection (%v), so the agent could not ask who runs it and did not use it as adb's server", address, err)
	}
	_ = conn.Close()
	return nil
}

// serverGone is an answer adb gave when, once it had, no server of this account's was listening where adb dials.
type serverGone struct{ address string }

func (e *serverGone) Error() string {
	return "adb answered, and then no adb server this account runs was listening on " + e.address +
		" — the program that answered has gone, so its answer was not used"
}

func runAdbGuarded(ctx context.Context, guard ServerGuard, run func(context.Context, ...string) (string, error), args ...string) (string, error) {
	if err := guard.Before(ctx, os.Getenv); err != nil {
		return "", &serverRefused{err}
	}
	out, err := run(ctx, args...)
	if checkErr := guard.After(ctx, os.Getenv, err == nil); checkErr != nil {
		return "", &serverRefused{checkErr}
	}
	return out, err
}

// serverRefused marks an adb command that was not run, or whose answer was not used, because of who runs adb's server.
type serverRefused struct{ err error }

func (e *serverRefused) Error() string { return e.err.Error() }
func (e *serverRefused) Unwrap() error { return e.err }

// isServerRefused reports whether err is such a refusal.
func isServerRefused(err error) bool {
	var refused *serverRefused
	return errors.As(err, &refused)
}
