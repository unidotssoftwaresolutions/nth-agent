package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — the wiring half: what connect and doctor reach on this machine is this account's.

func anotherAccountsProgram(net.Conn) (int, bool) { return 1002, true }

// the Appium probe decides whether this machine's phones are offered, and doctor reports its answer as a working
// Appium. Another account's listener on the --appium port must be neither.
func TestTheAppiumProbeRefusesAProgramAnotherAccountRuns(t *testing.T) {
	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":{"ready":true}}`))
	}))
	t.Cleanup(impostor.Close)

	err := appiumProbe(2*time.Second, anotherAccountsProgram)(context.Background(), impostor.URL)
	var notOurs *localconn.NotThisAccount
	if !errors.As(err, &notOurs) {
		t.Fatalf("an Appium another account runs must not pass the probe, got %v", err)
	}
	if !strings.Contains(err.Error(), "runs as another account on this machine (uid 1002), so the agent will not use it as your Appium") {
		t.Fatalf("the refusal says why in plain words: %v", err)
	}
	if err := appiumProbe(2*time.Second, nil)(context.Background(), impostor.URL); err != nil {
		t.Fatalf("this account's own listener, asked of the real kernel, must pass: %v", err)
	}
}

func TestTheAppiumProbeUsesOnlyAnAppiumOnThisMachine(t *testing.T) {
	// 192.0.2.1 is TEST-NET-1: the dialer refuses it before a packet is sent.
	err := appiumProbe(2*time.Second, nil)(context.Background(), "http://192.0.2.1:4723")
	var elsewhere *localconn.NotOnThisMachine
	if !errors.As(err, &elsewhere) {
		t.Fatalf("an Appium that is not on this machine must not pass the probe, got %v", err)
	}
}

// The two flags are written out in the CLI table, where the README seal reads them; the sentences that name them read
// their packages' constants. One spelling.
func TestTheTwoNewFlagsAreSpelledOnce(t *testing.T) {
	if inventory.OfferUncabledFlag != "--offer-uncabled-devices" || netcap.AllowLocalFlag != "--capture-allow-local-network" {
		t.Fatalf("the CLI table and the sentences disagree: %q %q", inventory.OfferUncabledFlag, netcap.AllowLocalFlag)
	}
	for _, c := range []struct{ verb, flag string }{
		{"connect", inventory.OfferUncabledFlag}, {"devices", inventory.OfferUncabledFlag}, {"doctor", inventory.OfferUncabledFlag},
		{"connect", netcap.AllowLocalFlag},
	} {
		found := false
		for _, v := range verbs {
			if v.Name == c.verb && v.flag(c.flag) != nil {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s does not take %s", c.verb, c.flag)
		}
	}
}
