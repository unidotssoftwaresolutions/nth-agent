package tunnel_test

import (
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// ANOTHER ACCOUNT ON THE SAME COMPUTER (ANOTHER_ACCOUNT_AND_LIMITS). A farm on this machine's loopback is reached the
// way Appium is: only when the program that took the connection runs as this account. FarmOwner is the seam: a test
// cannot open a listener as another account.

func TestATunnelToAFarmOnLoopbackRunByAnotherAccountSendsNothing(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var received, accepted atomic.Int64
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				n, _ := io.Copy(io.Discard, c)
				received.Add(n)
			}()
		}
	}()
	out := &syncWriter{}
	liveTunnel(t, "http://"+listener.Addr().String(), out, func(c *tunnel.Config) { c.FarmOwner = anotherAccount })
	deadline := time.Now().Add(3 * time.Second)
	for accepted.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if accepted.Load() == 0 {
		t.Fatal("the tunnel never dialled the farm's port")
	}
	time.Sleep(300 * time.Millisecond)
	if n := received.Load(); n != 0 {
		t.Fatalf("another account's listener on the farm's port was sent %d bytes (the upgrade carries the token); it must be sent none", n)
	}
	if !strings.Contains(out.String(), "another account") {
		t.Errorf("the terminal must say why the farm was not used:\n%s", out.String())
	}
}
