package wda

import (
	"context"
	"fmt"
	"testing"
)

// TWO PHONES ARE NEVER DECLARED ONE PORT (ANOTHER_ACCOUNT_AND_LIMITS). The derived port is a hash of the udid into ten
// thousand ports, so two phones can meet; and the walk past a held port can step onto another phone's. The chooser
// remembered each phone's pick but never compared it with the others', and the farm pins each session's wdaLocalPort to
// the declared one, so two leases' sessions would be given one host port.
func TestTwoPhonesAreNeverDeclaredOnePort(t *testing.T) {
	// Two udids whose derived ports meet, found by trying.
	byPort := map[uint16]string{}
	var first, second string
	for i := 0; first == ""; i++ {
		udid := fmt.Sprintf("00008110-%016X", i)
		port := PortFor(udid)
		if other, met := byPort[port]; met {
			first, second = other, udid
			break
		}
		byPort[port] = udid
	}
	nothingListens := func(context.Context, uint16) RunnerStatus { return RunnerStatus{State: RunnerNotListening} }
	chooser := &PortChooser{Probe: nothingListens}
	a, b := chooser.For(context.Background(), first), chooser.For(context.Background(), second)
	if a == b {
		t.Fatalf("%s and %s were both declared port %d", first, second, a)
	}

	// The walk: the first phone's derived port is held by a stranger, so it walks onto the next one — the second
	// phone's derived port. The second must then walk on, not take it too.
	derived := PortFor("00008140-0000c0ffee0000a1")
	var neighbour string
	for i := 0; neighbour == ""; i++ {
		udid := fmt.Sprintf("00008120-%016X", i)
		if PortFor(udid) == derived+1 {
			neighbour = udid
		}
	}
	stranger := func(_ context.Context, port uint16) RunnerStatus {
		if port == derived {
			return RunnerStatus{State: RunnerUnknown}
		}
		return RunnerStatus{State: RunnerNotListening}
	}
	walker := &PortChooser{Probe: stranger}
	a = walker.For(context.Background(), "00008140-0000c0ffee0000a1")
	b = walker.For(context.Background(), neighbour)
	if a != derived+1 || b == a {
		t.Fatalf("the walk gave the first phone %d and the second %d; two phones never share one", a, b)
	}
}
