package localconn_test

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// ANOTHER ACCOUNT ON THE SAME COMPUTER (ANOTHER_ACCOUNT_AND_LIMITS). The kernel names the account that MADE a socket,
// not the one whose program serves it. On a Mac, launchd (root) makes the listening socket a LaunchAgent asks for and
// hands it to that agent, so a program another account starts can sit behind a socket root made; a program this agent
// dials there is not taken for the customer's own. On Linux and Windows, a socket root or LocalSystem made reaches
// another account's program only by an administrator's own doing (a socket unit, a service), and an adb server started
// with sudo runs as root, so root stays let through there.

func rootMadeIt(net.Conn) (int, bool) { return 0, true }

func TestAProgramBehindASocketRootMadeIsNotTakenForTheCustomersOnAMac(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := localconn.Dialer{What: "your Appium", Owner: rootMadeIt}.DialContext(ctx, "tcp", listener.Addr().String())
	if runtime.GOOS != "darwin" {
		if err != nil {
			t.Fatalf("on %s a program root runs is still used: %v", runtime.GOOS, err)
		}
		_ = conn.Close()
		return
	}
	if err == nil {
		_ = conn.Close()
		t.Fatal("on a Mac, a program behind a socket root made must not be used as your Appium")
	}
	var refused *localconn.NotThisAccount
	if !errors.As(err, &refused) {
		t.Fatalf("want a NotThisAccount, got %T %v", err, err)
	}
	for _, words := range []string{"root", "your Appium", "yourself"} {
		if !strings.Contains(err.Error(), words) {
			t.Errorf("the refusal must say %q: %s", words, err)
		}
	}
}
