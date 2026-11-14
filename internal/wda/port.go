package wda

import (
	"context"
	"strings"
	"sync"
)

// ── THE PORT BELONGS TO THE MACHINE THE PHONE IS PLUGGED INTO ────────────────────────────────────────────────
//
// Two halves of this system have to arrive at the same number for the same phone without either telling the
// other: the WORKER, which puts it in Appium's capabilities from a Linux container, and this AGENT, which asks
// WebDriverAgent about itself on the customer's Mac. The one fact both already hold is the device's UDID, so the
// port is derived from it — the same rule, written down once, implemented once per repo.
//
// WHAT THIS REPLACED, AND WHY IT WAS WRONG TWICE OVER:
//
//	The worker BOUND PORT 0 on its own container's loopback, kept the number the OS handed back, released it, and
//	sent it to Appium — which spends it on the customer's Mac and on their phone. Three machines, one number, and
//	the only one it was ever measured against is the one it is not used on.
//	This agent probed a LIBRARY CONSTANT, 8100, on the strength of `WDA_AGENT_PORT` in Appium's source. The
//	driver actually resolves the DEVICE port as `(realDevice ? wdaRemotePort : null) ?? wdaLocalPort || 8100`
//	(appium-webdriveragent lib/webdriveragent.ts:90-91, and lib/xcodebuild.ts:445 / commands/wda/startup.ts:119
//	spend it), so with a wdaLocalPort in the caps and no wdaRemotePort the phone listens on wdaLocalPort. Measured
//	on the operator's iPhone on 2026-08-24: device port 8100 answered "connection reset by peer" while device port
//	43123 — the wdaLocalPort of run bdc42b11 — answered HTTP 200 with "WebDriverAgent is ready to accept
//	commands". The probe had never once seen a running WebDriverAgent, and its "not-listening" was read as the
//	probe working.

// FnvOffsetBasis and FnvPrime are the standard 32-bit FNV-1a parameters. Named rather than inlined because the
// rule is written out in wda-ports.tsv in words a person can apply by hand, and these are the two numbers that
// sentence contains.
const (
	FnvOffsetBasis uint32 = 2166136261
	FnvPrime       uint32 = 16777619
)

// PortBase and PortSpan bound the derived port to 20000-29999: above the crowded registered range and entirely
// below macOS's ephemeral range (49152-65535), so the operating system never hands this number to an unrelated
// process behind our back.
const (
	PortBase uint16 = 20000
	PortSpan uint32 = 10000
)

// PortFor is THE RULE: trim and upper-case the UDID, FNV-1a it, and take the remainder into the range.
//
// Deterministic and total — the same UDID always gives the same port, on any machine, in any process, with no
// clock and no socket involved. That is the whole property: a number two programs can agree on without a
// conversation.
func PortFor(udid string) uint16 {
	h := FnvOffsetBasis
	for _, b := range []byte(strings.ToUpper(strings.TrimSpace(udid))) {
		h ^= uint32(b)
		h *= FnvPrime
	}
	return PortBase + uint16(h%PortSpan)
}

// ── THE MACHINE PICKS, AND PICKS ONE IT HAS ACTUALLY CHECKED ─────────────────────────────────────────────────

// PortChooser turns the rule into a DECLARATION: the derived port is where it starts, and what it declares is a
// port it has looked at on this machine.
//
// WHY THE RULE STAYS AS THE DEFAULT. A device that keeps its port across agent restarts is a device whose
// in-flight run is not surprised by a new number, and a stable default is free. But the derived number is now a
// PREFERENCE, not a law: which ports are free on this Mac is a fact only this Mac holds, and the rule asks it
// nothing. When something else holds it, the machine picks another and says so.
//
// WEBDRIVERAGENT ITSELF ON THE PORT IS NOT A COLLISION — it is ours, from the last run, which is the state this
// whole family exists to clean up. Only a NON-WebDriverAgent answer counts as taken.
type PortChooser struct {
	// Probe asks what is on one local port, in WebDriverAgent's own terms. nil = this build cannot ask, and then
	// the derived port is declared unchecked — honest, because a check that cannot run must not pretend to.
	Probe func(ctx context.Context, port uint16) RunnerStatus
	// Tries bounds the search; zero means DefaultPortTries.
	Tries int

	mu     sync.Mutex
	chosen map[string]uint16
}

// DefaultPortTries — how many candidates the walk considers before it gives up and declares the derived port
// anyway. A machine with 32 consecutive candidates held has a problem this agent cannot solve by looking harder,
// and the run's own session-start sentence names the number so a person can see it.
const DefaultPortTries = 32

// For returns the port this machine declares for a device — the same number every time within one process, so
// the declaration and the probe cannot disagree about what they are talking about.
func (c *PortChooser) For(ctx context.Context, udid string) uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chosen == nil {
		c.chosen = map[string]uint16{}
	}
	if port, ok := c.chosen[udid]; ok {
		return port
	}

	port := c.pick(ctx, udid)
	c.chosen[udid] = port
	return port
}

func (c *PortChooser) pick(ctx context.Context, udid string) uint16 {
	derived := PortFor(udid)
	if c.Probe == nil {
		return derived
	}

	tries := c.Tries
	if tries <= 0 {
		tries = DefaultPortTries
	}
	base := uint32(derived - PortBase)
	for offset := 0; offset < tries; offset++ {
		candidate := PortBase + uint16((base+uint32(offset))%PortSpan)
		if !c.taken(ctx, candidate) {
			return candidate
		}
	}
	// Every candidate held. Declare the derived one: a number a person can predict beats a number nobody can,
	// and the session-start failure names it.
	return derived
}

// taken reports whether a stranger holds this port. Nothing listening is free; WebDriverAgent answering is ours.
func (c *PortChooser) taken(ctx context.Context, port uint16) bool {
	switch c.Probe(ctx, port).State {
	case RunnerNotListening, RunnerIdle, RunnerInUse:
		return false
	default: // RunnerUnknown — something answered and it was not WebDriverAgent
		return true
	}
}
