package android

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// ── adb's SERVER IS THIS ACCOUNT'S, OR adb IS NOT ASKED (A_MACHINE_THAT_STAYS_THE_CUSTOMERS) ───────────────────
//
// Every adb command goes to adb's background server, on 127.0.0.1:5037 unless the environment says otherwise, and the
// server is what answers: which phones are attached, what each one is, what a capture's proxy is set to. Another account
// on this machine that holds that port while this account's server is not running would answer all of it — invent phones
// for this machine to offer, or hide the real ones.
//
// SO, AROUND EVERY adb COMMAND, the server's port is asked who holds it (internal/localconn, the kernel's answer):
//   - nothing listening: adb starts its own server, as this account — the ordinary first run;
//   - this account, or root (adb started with sudo on Linux runs as root): used;
//   - another account, or an account the kernel would not name: adb is not run, and the reason says so.
// It is asked again AFTER the command, because the server that answered is the one listening then.
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
	host, port := "localhost", strconv.Itoa(adbServerDefaultPort)
	if socket := strings.TrimSpace(getenv("ADB_SERVER_SOCKET")); socket != "" {
		rest, isTCP := strings.CutPrefix(socket, "tcp:")
		if !isTCP {
			return "", elsewhere("ADB_SERVER_SOCKET")
		}
		if h, p, err := net.SplitHostPort(rest); err == nil {
			host, port = h, p
		} else {
			port = rest
		}
		if !loopbackHost(host) {
			return "", elsewhere("ADB_SERVER_SOCKET")
		}
	} else {
		if h := strings.TrimSpace(getenv("ANDROID_ADB_SERVER_ADDRESS")); h != "" {
			if !loopbackHost(h) {
				return "", elsewhere("ANDROID_ADB_SERVER_ADDRESS")
			}
		}
		if p := strings.TrimSpace(getenv("ANDROID_ADB_SERVER_PORT")); p != "" {
			port = p
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return "", "adb's server port in the environment is not a port number, so adb was not asked"
	}
	return net.JoinHostPort("127.0.0.1", strconv.FormatUint(number, 10)), ""
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

// CheckServer says whether adb's server may be used: nil when nothing listens yet or this account (or root) runs it; an
// error with the reason otherwise. owner nil = the kernel's answer.
func CheckServer(ctx context.Context, getenv func(string) string, owner localconn.Owner) error {
	address, refusal := ServerAddress(getenv)
	if refusal != "" {
		return errors.New(refusal)
	}
	dialer := localconn.Dialer{What: "adb's server", Timeout: serverCheckTimeout, Owner: owner}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	var notOurs *localconn.NotThisAccount
	switch {
	case errors.As(err, &notOurs):
		return notOurs
	case err != nil:
		return nil // nothing is listening: adb will start its own server, as this account
	}
	_ = conn.Close()
	return nil
}

// ServerRemedy is what a person does about a server CheckServer refused.
const ServerRemedy = "Stop the adb server that is not yours (`adb kill-server` as the account that runs it, or ask its owner), " +
	"then run `adb start-server` as yourself — or unset the variable that points adb elsewhere."

func runAdbAsThisAccount(ctx context.Context, owner localconn.Owner, args ...string) (string, error) {
	if err := CheckServer(ctx, os.Getenv, owner); err != nil {
		return "", &serverRefused{err}
	}
	out, err := execAdb(ctx, args...)
	if checkErr := CheckServer(ctx, os.Getenv, owner); checkErr != nil {
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
