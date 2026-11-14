package farm_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// A FARM ON THIS MACHINE'S LOOPBACK IS THIS ACCOUNT'S, OR IT IS NOT SENT THE TOKEN (ANOTHER_ACCOUNT_AND_LIMITS).
//
// ValidateBaseURL lets plain http through to a loopback farm because a loopback packet never leaves the machine. But
// every account on the machine can listen on 127.0.0.1: one that holds the farm's port while the local farm is down
// took the workspace token from the first request. The listener here stands in for that account's program: the Owner
// seam says another account runs it, since a test cannot open a listener as another account.

func anotherAccountsFarm(net.Conn) (int, bool) { return 1002, true }

// aListenerThatCounts accepts connections and counts every byte sent to it.
func aListenerThatCounts(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var received atomic.Int64
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
				n, _ := io.Copy(io.Discard, c)
				received.Add(n)
			}()
		}
	}()
	return listener.Addr().String(), &received
}

func TestAFarmOnThisMachinesLoopbackRunByAnotherAccountIsSentNothing(t *testing.T) {
	address, received := aListenerThatCounts(t)
	_, port, _ := net.SplitHostPort(address)
	for _, base := range []string{"http://" + address, "http://localhost:" + port} {
		client := &farm.Client{BaseURL: base, Token: "sk-agent-N3-NEVER-SENT", HTTP: &http.Client{Timeout: 2 * time.Second},
			Owner: anotherAccountsFarm}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		status, err := client.Leave(ctx, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
		cancel()
		if err == nil {
			t.Fatalf("%s: a farm another account runs must not be used, got status %v and no error", base, status)
		}
		if !strings.Contains(err.Error(), "another account") {
			t.Errorf("%s: the error must say why: %v", base, err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := received.Load(); n != 0 {
		t.Fatalf("another account's listener on the farm's port was sent %d bytes; it must be sent none", n)
	}
}
