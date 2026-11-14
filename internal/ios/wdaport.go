package ios

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/forward"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// WithWdaPort forwards one device's WebDriverAgent port to a local one, hands the caller the base URL to talk to
// it on, and takes the forward down again.
//
// USBMUXD, NOT THE CoreDevice TUNNEL — and that is the whole reason this is possible at all. `appservice`, which
// can list and kill processes on the phone, needs `ios tunnel start`: a privileged daemon on somebody else's Mac,
// which this programme may not put there. A port forward over usbmuxd needs nothing that is not already running
// for the device listing to work, and WebDriverAgent's own HTTP API on the other end answers both questions this
// agent has: is anything using you, and would you please stop.
//
// The forward lives exactly as long as the call. A forward this agent left open would be one more thing running on
// a machine we do not own, which is the defect this file exists to close.
// THE DEVICE PORT IS A PARAMETER, not a derivation hidden inside a transport helper. Two reasons, and the second
// is the one that cost this slice: the fact "which port is this phone's WebDriverAgent on" belongs beside the
// caller that also tells Appium about it, and a probe whose port cannot be substituted cannot be pointed at a
// RUNNING server to prove it reads one. The shipped version hid a library constant in here and its "nothing is
// listening" was read as the probe working.
//
// THE FORWARD IS THIS AGENT'S OWN, NOT go-ios's (A_MACHINE_THAT_STAYS_THE_CUSTOMERS). go-ios v1.3.2's forward.Forward
// listens on 0.0.0.0 and relays every connection to the phone; WebDriverAgent asks for no credential, so any host this
// machine's network reaches, and any other account on it, could have driven the phone while the forward was open. This
// one listens on 127.0.0.1, on a port the system picks as it binds, and hands on only connections this account opened —
// the agent's own probe and stop are its only callers — asking the kernel, as the capture proxy does (netcap/peer.go).
// Root is not let through here: nothing but this process has a reason to connect.
//
// THE PHONE ON THE CABLE: when usbmuxd lists a phone twice, over USB and over the network, the USB entry is the one
// forwarded to.
func WithWdaPort(udid string, devicePort uint16, do func(baseURL string) error) error {
	device, err := deviceOnTheCable(udid)
	if err != nil {
		return err
	}
	forwardTo := func(c net.Conn) { _ = forward.StartNewProxyConnection(context.TODO(), c, device.DeviceID, devicePort) }
	_, baseURL, stop, err := startWdaForward(forwardTo, nil)
	if err != nil {
		return fmt.Errorf("could not forward WebDriverAgent's port: %w", err)
	}
	defer stop()
	return do(baseURL)
}

// deviceOnTheCable is usbmuxd's entry for udid, the USB one when there are two.
func deviceOnTheCable(udid string) (goios.DeviceEntry, error) {
	list, err := goios.ListDevices()
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf("no such device %s: %w", udid, err)
	}
	found, ok := goios.DeviceEntry{}, false
	for _, entry := range list.DeviceList {
		if entry.Properties.SerialNumber != udid {
			continue
		}
		if IsCabled(entry.Properties.ConnectionType) {
			return entry, nil
		}
		if !ok {
			found, ok = entry, true
		}
	}
	if !ok {
		return goios.DeviceEntry{}, fmt.Errorf("no such device %s", udid)
	}
	return found, nil
}

// startWdaForward listens for the forward on 127.0.0.1 and hands every connection this account opened to forwardTo; owner
// nil = the kernel's answer. It returns the listener's own address, the base URL to talk to it on, and stop, which closes
// the listener and every connection it handed on.
func startWdaForward(forwardTo func(net.Conn), owner netcap.PeerOwner) (net.Addr, string, func(), error) {
	if owner == nil {
		owner = netcap.OwnerOfPeer
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, fmt.Errorf("could not open a local port: %w", err)
	}
	var mu sync.Mutex
	open := map[net.Conn]struct{}{}
	stopped := false
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			if uid, found := owner(c); !found || uid != os.Geteuid() {
				_ = c.Close()
				continue
			}
			mu.Lock()
			if stopped {
				mu.Unlock()
				_ = c.Close()
				return
			}
			open[c] = struct{}{}
			mu.Unlock()
			go forwardTo(c)
		}
	}()
	stop := func() {
		_ = listener.Close()
		mu.Lock()
		stopped = true
		for c := range open {
			_ = c.Close()
		}
		mu.Unlock()
	}
	return listener.Addr(), "http://" + listener.Addr().String(), stop, nil
}
