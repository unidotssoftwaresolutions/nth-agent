package main

import (
	"errors"
	"fmt"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// goodbyeNotTaken is the goodbye's line when the farm took it neither in words of its own nor at all: why is the agent's
// account of it. A 502, 503 or 504 with no envelope is the farm's address answering in its place (NTH_AGENT_0_4_4 item 1,
// farm.GatewayError): the farm was not reached, so it did not take or refuse anything, and the line says so. Here, not in
// main.go, so that no line /privacy cites in main.go moves.
func goodbyeNotTaken(why string, err error) string {
	var gateway *farm.GatewayError
	if errors.As(err, &gateway) {
		return fmt.Sprintf("could not reach the farm to say goodbye (%s), so it takes the phones off offer within a minute instead", gateway)
	}
	return fmt.Sprintf("the farm did not take this machine's goodbye (%s), so it takes the phones off offer within a minute instead", why)
}
