package capture

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 2 — a capture keeps to the internet unless the customer opens it, and
// while it is open the console says so at every capture's start.

func TestACaptureByDefaultDoesNotReachThisMachineAndRecordsWhy(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	reached := false
	server, roots := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached = true })
	ctl := m.controller(func(cfg *Config) { cfg.UpstreamRoots = roots; cfg.ReachLocal = false })
	ctl.Start(context.Background(), leaseA, phone.serial)
	if ctl.Running() != 1 {
		t.Fatalf("the capture did not start:\n%s", m.out.String())
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/internal", nil)
	response, body := fetch(t, m.appsOn(phone.serial, m.trusts()), request)
	if response.StatusCode != http.StatusBadGateway || reached || !strings.Contains(string(body), netcap.RefusedLocal()) {
		t.Fatalf("a request for this machine is refused, and the phone is told why: %d %q (reached %v)", response.StatusCode, body, reached)
	}
	eventually(t, "the refusal recorded", func() bool {
		data, _ := ctl.Pull(leaseA)
		return strings.Contains(string(data), "/internal") && strings.Contains(string(data), netcap.RefusedLocal())
	})
	if strings.Contains(m.out.String(), LocalNetworkOpen) {
		t.Fatalf("nothing is open, so nothing is said to be: %s", m.out.String())
	}
	ctl.Stop(context.Background(), leaseA)
}

func TestWhileTheFlagIsOnEveryCaptureSaysSo(t *testing.T) {
	phone := samsung("R5CT30ABCDE")
	m := newMachine(t, phone)
	ctl := m.controller(func(cfg *Config) { cfg.ReachLocal = true })
	ctl.Start(context.Background(), leaseA, phone.serial)
	if !strings.Contains(m.out.String(), LocalNetworkOpen) {
		t.Fatalf("a capture that can reach this machine says so when it starts:\n%s", m.out.String())
	}
	ctl.Stop(context.Background(), leaseA)
	if !strings.Contains(LocalNetworkOpen, netcap.AllowLocalFlag) {
		t.Fatalf("the sentence names the flag: %q", LocalNetworkOpen)
	}
}
