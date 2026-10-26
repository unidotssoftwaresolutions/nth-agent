package tunnel_test

import (
	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// offeringPhones makes the agent under test offer exactly these phones, as the connect loop does from the rows it publishes.
func offeringPhones(c *tunnel.Config, udids ...string) {
	c.Phones = allowed.Offering(udids...)
}

// fixturePhones are the phones the older seals here start local sessions on: the agent offers them by default
// (liveTunnel), so those seals go on testing what they were written for. A seal about which phones are offered sets its
// own (offeringPhones).
var fixturePhones = []string{"00008140-0000C0FFEE0000A1", "00008030-HEALTHY", "LOCAL-SERIAL-1", "R5CT64M346B"}
