package connect_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ONE PHONE, ONE CONNECT, ON THE WHOLE MACHINE (ANOTHER_ACCOUNT_AND_LIMITS). A phone another connect on this machine
// holds is not offered by this one, and that is said once.
func TestAPhoneAnotherConnectHoldsIsNotOffered(t *testing.T) {
	stub, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = func(context.Context) []inventory.Outcome {
		return []inventory.Outcome{inventory.Answered(inventory.Android,
			[]inventory.Offerable{{ID: "R5CT30ABCDE", Model: "SM-A546B"}, {ID: "R5CT31FGHIJ", Model: "SM-A546B"}}, nil)}
	}
	cfg.HoldPhone = func(serial string) (func(), error) {
		if serial == "R5CT30ABCDE" {
			return nil, errors.New("another connect on this machine already offers this phone")
		}
		return func() {}, nil
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, body := range [][]byte{stub.lastRegisterBody.Load().([]byte), stub.lastHeartbeat.Load().([]byte)} {
		if strings.Contains(string(body), "R5CT30ABCDE") {
			t.Fatalf("a phone another connect holds was sent to the farm:\n%s", body)
		}
		if !strings.Contains(string(body), "R5CT31FGHIJ") {
			t.Fatalf("the phone this connect holds is still sent:\n%s", body)
		}
	}
	if said := strings.Count(out.String(), "another connect on this machine already offers this phone"); said != 1 {
		t.Fatalf("it is said once, got %d times:\n%s", said, out.String())
	}
}
