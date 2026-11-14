package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// THE PORT DECLARED FOR AN IPHONE'S WEBDRIVERAGENT IS ONE THIS ACCOUNT CAN HOLD (ANOTHER_ACCOUNT_AND_LIMITS). The
// chooser read any WebDriverAgent-shaped answer on a candidate as this machine's own; another account's program
// answering that way would be declared to the farm as the phone's WebDriverAgent port. A port the kernel says another
// account holds, or whose holder it cannot name, is taken, and the program on it is not even asked.
func TestAWebDriverAgentPortAnotherAccountHoldsIsNotDeclared(t *testing.T) {
	var asked atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_, _ = w.Write([]byte(`{"value":{"ready":true,"build":{"productBundleIdentifier":"com.facebook.WebDriverAgentRunner.xctrunner"}},"sessionId":null}`))
	}))
	defer server.Close()
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	for _, c := range []struct {
		name  string
		owner netcap.Listener
		taken bool
	}{
		{"another account's program", netcap.Listener{IP: net.IPv4(127, 0, 0, 1), UID: 1002, Known: true}, true},
		{"a program nobody can name", netcap.Listener{IP: net.IPv4(127, 0, 0, 1)}, true},
		{"this account's own WebDriverAgent forward", netcap.Listener{IP: net.IPv4(127, 0, 0, 1), UID: netcap.ThisAccount(), Known: true}, false},
	} {
		asked.Store(0)
		listeners := func(int) ([]netcap.Listener, bool) { return []netcap.Listener{c.owner}, true }
		status := wdaPortStatus(context.Background(), &http.Client{}, uint16(port), listeners)
		taken := status.State == wda.RunnerUnknown
		if taken != c.taken {
			t.Errorf("%s: read as %v (taken %v), want taken %v", c.name, status.State, taken, c.taken)
		}
		if c.taken && asked.Load() != 0 {
			t.Errorf("%s: the program on the port was asked; it must not be", c.name)
		}
	}
}
