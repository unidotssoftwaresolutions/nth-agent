// Package liveview is THE DEVICE MIRROR A PERSON WATCHES ON THE RUN PAGE, as far as this machine is concerned: the
// port each phone's screen server is asked to listen on, whether this machine can show a phone at all and why not,
// and which mirrors are on offer at this moment (the only ones the drive tunnel will stream).
//
// The screen server is Appium's own MJPEG broadcast — UiAutomator2's on Android, WebDriverAgent's on iOS — started
// for a session that asks for it with `mjpegServerPort`. MEASURED IN THE DRIVERS' OWN SOURCE (installed here,
// 2026-10-03): both spend that number ON THIS MACHINE. appium-uiautomator2-driver 7.5.2 runs
// `adb forward tcp:<mjpegServerPort> tcp:7810` (lib/uiautomator2-server/session.ts:96-104) and removes it when the
// session ends; appium-xcuitest-driver 11.9.0 forwards host port N to device port N for a real iPhone
// (lib/driver.ts:1217-1244) and FAILS THE SESSION when the host port cannot be bound. So a mirror port is a host
// port, a held one costs a run and not only a picture, and checking it free here is the check that matters.
package liveview

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// ── THE RULE IS SHARED, THE ANSWER IS NOT ────────────────────────────────────────────────────────────────────
//
// The farm's rack node declares a mirror port per device with MirrorPortChooser: WebDriverAgent's rule (wda-ports.tsv — trim
// and upper-case the UDID, FNV-1a it, take the remainder into a span of 10000) moved into its own decade, then CHECKED
// with a loopback bind, walking at most 32 candidates and declaring NOTHING when every one is held. This is that
// chooser, and it implements no arithmetic of its own: PortFor is wda.PortFor plus ten thousand, so the mirror port is
// readable straight off the WebDriverAgent one — the same last four digits, in the 30000s — and the two ranges are
// disjoint, so one process is never asked to hold one number for two purposes.
//
// ONE DIFFERENCE FROM THE NODE, AND IT IS SCOPE: the node declares a mirror for iOS only, because the farm's rack
// gets its Android mirrors elsewhere. A customer's machine has Android phones too, and two of them asked for one
// fixed number fight over one adb forward (the second `adb forward tcp:7810` takes the port from the first phone and
// its end removes it), so here every phone gets its own.

// PortBase and PortSpan bound the mirror port to 30000-39999 — the farm's rack node's MirrorPortChooser.PortBase/PortSpan,
// immediately above WebDriverAgent's decade and entirely below macOS's ephemeral range (49152-65535).
const (
	PortBase uint16 = 30000
	PortSpan        = wda.PortSpan
)

// DefaultTries is how many candidates the walk looks at before it declares nothing — the node's number
// (MirrorPortChooser.DefaultTries = WdaPortChooser.DefaultTries), and the same as this agent's WebDriverAgent walk.
const DefaultTries = wda.DefaultPortTries

// PortFor is THE RULE: wda.PortFor's hash moved into this decade. Deterministic and total — no clock, no socket.
func PortFor(udid string) uint16 {
	return PortBase + (wda.PortFor(udid) - wda.PortBase)
}

// Reservation is what the walk found for one device.
type Reservation struct {
	// Reserved says a port was found free here; Port is it. When false there is no port, and that is the answer —
	// never "use the consumer's default".
	Reserved bool
	Port     uint16
	// Derived is the rule's own number for this UDID and Tried how many candidates the walk looked at from it —
	// what a sentence about "nothing was free" has to name.
	Derived uint16
	Tried   int
}

// Last is the last candidate the walk looked at (it wraps inside the decade, like the node's).
func (r Reservation) Last() uint16 {
	if r.Tried <= 0 {
		return r.Derived
	}
	return candidate(r.Derived, r.Tried-1)
}

func candidate(derived uint16, offset int) uint16 {
	return PortBase + uint16((uint32(derived-PortBase)+uint32(offset))%PortSpan)
}

// Chooser turns the rule into a declaration: the derived port when it is free on this machine, else the next free
// candidate, else none. The answer is remembered per UDID for the life of the process — the inventory is rebuilt
// every tick, and a number that moved between ticks would publish a different mirror for the same phone.
type Chooser struct {
	// Free answers "could a listener bind this loopback port right now". nil = LoopbackFree, the real check.
	Free func(port uint16) bool
	// Tries bounds the walk; zero = DefaultTries.
	Tries int

	mu     sync.Mutex
	chosen map[string]Reservation
}

// For returns this machine's mirror port for a device. A blank UDID reserves nothing.
func (c *Chooser) For(udid string) Reservation {
	if strings.TrimSpace(udid) == "" {
		return Reservation{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chosen == nil {
		c.chosen = map[string]Reservation{}
	}
	if remembered, ok := c.chosen[udid]; ok {
		return remembered
	}
	picked := c.pick(udid)
	c.chosen[udid] = picked
	return picked
}

func (c *Chooser) pick(udid string) Reservation {
	free := c.Free
	if free == nil {
		free = LoopbackFree
	}
	tries := c.Tries
	if tries <= 0 {
		tries = DefaultTries
	}
	derived := PortFor(udid)
	for offset := 0; offset < tries; offset++ {
		port := candidate(derived, offset)
		// A PORT THIS MACHINE ALREADY GAVE ANOTHER PHONE IS NOT FREE, though nothing listens on it yet — a mirror
		// server starts only when a session asks for it, so the loopback check cannot see the other phone's claim.
		// Two UDIDs whose hashes meet (about one pair in ten thousand, and a pair that meets always meets) would
		// otherwise both be told P: on iOS the second session fails to start, on Android the second adb forward
		// takes the port and the first phone's viewer watches the second phone. The review's finding, 2026-10-03.
		if c.givenElsewhere(port, udid) {
			continue
		}
		if free(port) {
			return Reservation{Reserved: true, Port: port, Derived: derived, Tried: offset + 1}
		}
	}
	// EVERY CANDIDATE HELD. Declare NOTHING rather than a number this machine knows is taken — the node's direction,
	// for the node's reason: a session asked for a held mirror port does not start at all on iOS (see the package doc).
	return Reservation{Derived: derived, Tried: tries}
}

func (c *Chooser) givenElsewhere(port uint16, udid string) bool {
	for other, given := range c.chosen {
		if other != udid && given.Reserved && given.Port == port {
			return true
		}
	}
	return false
}

// LoopbackFree reports whether 127.0.0.1:port is free right now: NOTHING ANSWERS a connection to it, and a listener
// can bind it — the node's probe (a loopback TcpListener started and stopped), with one check in front of it. It holds
// nothing afterwards.
//
// ⚠️ WHY THE CONNECT COMES FIRST, MEASURED 2026-10-03 on this Mac: Go binds with SO_REUSEADDR, and a bind of
// 127.0.0.1:P SUCCEEDED while another process listened on 0.0.0.0:P and on [::]:P — the shape a developer's own
// server takes on the very machine this agent runs on. A connect to 127.0.0.1:P reached the first two. (On Linux a
// listening wildcard already refuses the bind; there the connect is a second lock, not the only one.)
func LoopbackFree(port uint16) bool {
	return loopbackFree(port, dialLoopback, bindLoopback)
}

func loopbackFree(port uint16, answers func(address string) bool, binds func(address string) bool) bool {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	return !answers(address) && binds(address)
}

func dialLoopback(address string) bool {
	conn, err := net.DialTimeout("tcp4", address, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func bindLoopback(address string) bool {
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
