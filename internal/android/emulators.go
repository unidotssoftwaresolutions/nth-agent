package android

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── AN EMULATOR IS OFFERED ONLY WHEN THIS ACCOUNT RUNS IT (ANOTHER_ACCOUNT_AND_LIMITS) ───────────────────────────────
//
// adb names a transport emulator-<console port> for anything that answers on 127.0.0.1 at the port beside the console —
// which any account on this machine can listen on, and which it can ask this account's adb server to connect to.
// Offered on its name alone, such a transport would be sent the leased build, the typed values and the screenshots of a
// session, all to a program another account runs. So an emulator is offered only when the kernel's own table of
// listeners says this account made both of its ports on 127.0.0.1: adb's (console + 1), which the transport is
// connected to, and the console, which Appium's `adb emu` commands reach. Nothing is connected to either to find that
// out. Any other answer — another account, an account the system would not name, or nothing listening at all — makes it
// not usable, said in words, and no flag offers it.

// emulatorsThisAccountRuns moves every emulator in offerable that this account does not run into unusable.
func (p Probe) emulatorsThisAccountRuns(offerable []inventory.Offerable, unusable []inventory.Unusable) ([]inventory.Offerable, []inventory.Unusable) {
	kept := offerable[:0:0]
	for _, d := range offerable {
		if !d.Emulator {
			kept = append(kept, d)
			continue
		}
		if why := p.whyNotThisAccounts(d.ID); why != "" {
			unusable = append(unusable, inventory.NewUnusable(d.ID, why, EmulatorRemedy))
			continue
		}
		kept = append(kept, d)
	}
	return kept, unusable
}

// EmulatorRemedy is what a person does about an emulator this account does not run.
const EmulatorRemedy = "Start your emulators as yourself. An emulator another account on this machine runs is not yours to " +
	"offer, so the agent never sends it a session; if it is yours, close what holds its ports and start it again."

// whyNotThisAccounts is "" when this account made both of the emulator's ports, and otherwise why not, in the words the
// devices table and the farm's row carry.
func (p Probe) whyNotThisAccounts(serial string) string {
	console, err := strconv.Atoi(strings.TrimPrefix(serial, "emulator-"))
	if err != nil {
		return "not an emulator this agent can name"
	}
	listeners := p.EmulatorPorts
	if listeners == nil {
		listeners = netcap.ListenerOwnersOn
	}
	loopback := net.IPv4(127, 0, 0, 1)
	for _, port := range []struct {
		number int
		what   string
	}{{console + 1, "adb port"}, {console, "console"}} {
		all, read := listeners(port.number)
		if !read {
			return fmt.Sprintf("an emulator the agent could not tell is yours: the system would not say who listens on its %s, 127.0.0.1:%d", port.what, port.number)
		}
		held := 0
		for _, l := range all {
			if !l.Takes(loopback) {
				continue
			}
			held++
			switch {
			case !l.Known:
				return fmt.Sprintf("an emulator the agent could not tell is yours: the system would not say which account runs the program on its %s, 127.0.0.1:%d", port.what, port.number)
			case !netcap.MayServeThisAccount(l.UID):
				return fmt.Sprintf("an emulator another account on this machine runs: the program on its %s, 127.0.0.1:%d, runs as uid %d", port.what, port.number, l.UID)
			}
		}
		if held == 0 {
			return fmt.Sprintf("an emulator with nothing listening on its %s, 127.0.0.1:%d, so it is not one running here as you", port.what, port.number)
		}
	}
	return ""
}
