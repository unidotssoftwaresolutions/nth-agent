package connect_test

import (
	"net"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// AN IPHONE'S WEBDRIVERAGENT PORT (ANOTHER_ACCOUNT_AND_LIMITS). The driver opens its forward
// to the phone's WebDriverAgent on 127.0.0.1:<wdaLocalPort>, the port this machine declared; another account that holds
// that port first would be the session's WebDriverAgent, sent its commands and typed values. And a session that names
// another phone's declared port would be driven through that phone's.
func TestAWebDriverAgentPortAnotherAccountHoldsIsRefusedAtCreate(t *testing.T) {
	me := netcap.ThisAccount()
	declared := map[string]int{"PHONE-A": 21000, "PHONE-B": 21001}
	held := map[int][]netcap.Listener{
		21000: {{IP: net.IPv4(127, 0, 0, 1), UID: 1002, Known: true}},
		21001: {{IP: net.IPv4(127, 0, 0, 1), UID: me, Known: true}},
		21002: {{IP: net.IPv6loopback, Known: false}},
	}
	listeners := func(port int) ([]netcap.Listener, bool) { return held[port], true }
	guard := connect.WdaPortGuard(declared, listeners)
	for _, c := range []struct {
		name, udid string
		port       int
		says       string
	}{
		{"another account holds its declared port", "PHONE-A", 0, "another account"},
		{"another account holds the port the create names", "PHONE-A", 21000, "another account"},
		{"the create names another phone's declared port", "PHONE-B", 21000, "another phone"},
		{"nobody can say who holds the port", "PHONE-B", 21002, "could not tell"},
		{"this account's own forward holds it", "PHONE-B", 0, ""},
		{"nothing listens there yet", "PHONE-B", 21003, ""},
	} {
		got := guard(c.udid, c.port)
		if (c.says == "") != (got == "") || !strings.Contains(got, c.says) {
			t.Errorf("%s: got %q, want words with %q", c.name, got, c.says)
		}
	}
}
