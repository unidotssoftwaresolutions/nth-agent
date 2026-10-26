package netcap

import "github.com/unidotssoftwaresolutions/nth-agent/internal/wda"

// ── THE PORT A CAPTURE LISTENS ON ───────────────────────────────────────────────────────────────────────────────
//
// The phone's proxy setting names it (127.0.0.1:<port>) and `adb reverse` maps the phone's own port to this machine's, so
// the number is spent on both sides. It is derived per phone, the way this agent already derives a phone's WebDriverAgent
// and mirror ports — wda.PortFor's hash, moved into a range of its own — and then CHECKED free with the agent's loopback
// check (liveview.LoopbackFree: nothing answers it, and it binds), walking at most CapturePortTries candidates. The
// capture's controller (internal/capture) walks them; a port another of its captures holds is not free.
//
// THE RANGE, 40000-48999: above the mirror ports (30000-39999) and WebDriverAgent's (20000-29999), so one number is never
// asked to mean two things here, and below macOS's ephemeral range (49152-65535), where the system hands out ports of its
// own. A phone's proxy setting on a port in this range, at 127.0.0.1, is this agent's (IsCapturePort).

const (
	CapturePortBase  = 40000
	CapturePortSpan  = 9000
	CapturePortTries = 32
)

// PortCandidates is the ports a capture on serial tries, in order.
func PortCandidates(serial string) []uint16 {
	start := (int(wda.PortFor(serial)) - int(wda.PortBase)) % CapturePortSpan
	out := make([]uint16, 0, CapturePortTries)
	for i := 0; i < CapturePortTries; i++ {
		out = append(out, uint16(CapturePortBase+(start+i)%CapturePortSpan))
	}
	return out
}

// IsCapturePort reports whether port is in the range a capture listens on.
func IsCapturePort(port int) bool {
	return port >= CapturePortBase && port < CapturePortBase+CapturePortSpan
}
