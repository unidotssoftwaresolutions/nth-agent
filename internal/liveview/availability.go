package liveview

import (
	"fmt"
	"runtime"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// Availability is ONE PHONE'S LIVE VIEW on this machine: whether a person can watch it, and why not when they
// cannot — said from facts, never from a hope. The connect loop declares Port on the phone's descriptor exactly when
// Available; `devices` and `doctor` print Reason (and Unchecked) in one line per phone.
type Availability struct {
	Available bool
	Port      uint16 // the mirror port, when Available
	// Reason is one plain sentence: where the mirror is, or why there is none. Remedy is what a person does about a
	// "none" — empty when nothing on this machine can change it.
	Reason string
	Remedy string
	// Unchecked is what an Available answer did NOT establish — empty when nothing. An unread driver list is said,
	// never read as "installed" and never as "missing".
	Unchecked string
}

// Facts are what one phone's live view is judged from.
type Facts struct {
	Platform    inventory.Platform
	Reservation Reservation
	Drivers     Drivers
	// System is the system this runs on, for the words that say who holds a port; "" = runtime.GOOS. Holder names the
	// program listening on a port (netcap.ListenerProcess); nil = that one. Both are TEST SEAMS.
	System string
	Holder func(port int) (pid int, program string, found bool)
}

// XcuitestForwardsFrom is the first appium-xcuitest-driver that forwards a REAL iPhone's screen server to this
// machine when a session names its port: 8.1.0, 2025-01-21 — "create port forward when mjpegServerPort is provided
// in caps" (appium/appium-xcuitest-driver#2517, "keeping port forward in sync with android"; the CHANGELOG installed
// here says the same). Before it, WebDriverAgent listened on the phone and nothing listened on the mirror port here.
// Every iPhone this agent offers is a real one (usbmuxd), so the rule applies to all of them.
var XcuitestForwardsFrom = Driver{Name: "xcuitest", Major: 8, Minor: 1, Patch: 0}

// driverFor is the Appium driver whose screen server a platform's mirror is. The runner drives Android with
// UiAutomator2 and iOS with XCUITest; nothing else this agent offers has one.
func driverFor(platform inventory.Platform) (string, bool) {
	switch platform {
	case inventory.Android:
		return "uiautomator2", true
	case inventory.IOS:
		return "xcuitest", true
	default:
		return "", false
	}
}

// Judge decides one phone's live view. THE ORDER IS THE DEPENDENCY ORDER: a mirror needs a screen server (the
// platform), something on this machine that starts it (the driver), and a free port to put it on (the reservation).
// The first missing one is the reason; a later one is not guessed at.
func Judge(f Facts) Availability {
	driver, known := driverFor(f.Platform)
	if !known {
		return Availability{Reason: fmt.Sprintf("this agent knows no screen server for a %s device", f.Platform)}
	}
	if f.Drivers.Read {
		installed, ok := f.Drivers.Has(driver)
		if !ok {
			return Availability{Reason: fmt.Sprintf("Appium's %s driver is not installed here, so nothing on this machine "+
				"can start this phone's screen server", driver), Remedy: "appium driver install " + driver}
		}
		if f.Platform == inventory.IOS && !installed.AtLeast(XcuitestForwardsFrom.Major, XcuitestForwardsFrom.Minor, XcuitestForwardsFrom.Patch) {
			return Availability{Reason: fmt.Sprintf("Appium's xcuitest driver %s does not forward a real iPhone's screen "+
				"server to this machine — %s and later do", installed.Version(), XcuitestForwardsFrom.Version()),
				Remedy: "appium driver update xcuitest, then restart the agent"}
		}
	}
	if !f.Reservation.Reserved {
		if f.Reservation.Tried == 0 {
			return Availability{Reason: "this phone reported no id to reserve a mirror port for"}
		}
		wraps := ""
		if f.Reservation.Last() < f.Reservation.Derived {
			wraps = fmt.Sprintf(", wrapping within %d-%d", PortBase, uint32(PortBase)+PortSpan-1)
		}
		return Availability{Reason: fmt.Sprintf("no mirror port was free on this machine — the %d candidates for this "+
			"phone, %d to %d%s, are all in use", f.Reservation.Tried, f.Reservation.Derived, f.Reservation.Last(), wraps),
			Remedy: FreePortRemedy(f.System, int(f.Reservation.Derived), f.Holder)}
	}
	available := Availability{Available: true, Port: f.Reservation.Port,
		Reason: fmt.Sprintf("its mirror is 127.0.0.1:%d, reserved on this machine for this phone", f.Reservation.Port)}
	if !f.Drivers.Read {
		available.Unchecked = fmt.Sprintf("whether Appium's %s driver opens it was not checked — %s", driver, f.Drivers.Why)
	}
	return available
}

// FreePortRemedy is how to free port on system: on macOS and Linux, the `lsof` that names what holds it; on Windows, the
// program itself, named by the owner lookup (netcap.ListenerProcess), or the `netstat` line that names it when Windows
// would not say.
func FreePortRemedy(system string, port int, holder func(int) (int, string, bool)) string {
	if system == "" {
		system = runtime.GOOS
	}
	if system != "windows" {
		return fmt.Sprintf("free %d (lsof -nP -iTCP:%d -sTCP:LISTEN names what holds it), then restart the agent", port, port)
	}
	if holder == nil {
		holder = netcap.ListenerProcess
	}
	if pid, program, found := holder(port); found {
		if program == "" {
			program = "a program"
		}
		return fmt.Sprintf("free %d — %s (process %d) holds it: stop it, then restart the agent", port, program, pid)
	}
	return fmt.Sprintf("free %d (netstat -ano -p tcp | findstr :%d names the process that holds it), then restart the agent", port, port)
}

// Line is ONE PHONE'S LIVE VIEW IN ONE LINE — the words `devices` prints and `doctor` carries, so the two surfaces
// cannot disagree about the same phone: "yes" or "no", the sentence, what to do about a no, and what a yes did not
// check.
func (a Availability) Line() string {
	if !a.Available {
		if a.Remedy != "" {
			return "no — " + a.Reason + " (what to do: " + a.Remedy + ")"
		}
		return "no — " + a.Reason
	}
	if a.Unchecked != "" {
		return "yes — " + a.Reason + " (" + a.Unchecked + ")"
	}
	return "yes — " + a.Reason
}
