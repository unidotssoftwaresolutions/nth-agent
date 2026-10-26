package ios

import (
	"net"
	"testing"
	"time"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — the forward to a phone's WebDriverAgent. WebDriverAgent asks for no
// credential, so whoever reaches the forward drives the phone. It listens on this machine's loopback only, and hands on
// only connections this account opened: the agent's own probe and stop are the only callers it has. No phone and no
// usbmuxd is involved here — forwardTo stands in for the relay to the phone.

func TestTheWdaForwardListensOnLoopbackOnly(t *testing.T) {
	addr, _, stop, err := startWdaForward(func(c net.Conn) { _ = c.Close() }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if ip := addr.(*net.TCPAddr).IP; !ip.IsLoopback() {
		t.Fatalf("the forward to a phone's WebDriverAgent listens on %s — every host this machine's network reaches could drive the phone", addr)
	}
}

func TestTheWdaForwardHandsOnOnlyThisAccountsConnections(t *testing.T) {
	handed := make(chan struct{}, 4)
	forwardTo := func(c net.Conn) { handed <- struct{}{}; _ = c.Close() }

	_, baseURL, stop, err := startWdaForward(forwardTo, func(net.Conn) (int, bool) { return 1002, true })
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp4", baseURL[len("http://"):], time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The forward closes a connection it refuses: the read ends.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("a refused connection was kept open")
	}
	_ = conn.Close()
	stop()
	select {
	case <-handed:
		t.Fatal("another account's connection reached the phone's WebDriverAgent")
	default:
	}

	// …and the agent's own connection, asked of the real kernel, is handed on.
	_, baseURL, stop, err = startWdaForward(forwardTo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	conn, err = net.DialTimeout("tcp4", baseURL[len("http://"):], time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-handed:
	case <-time.After(2 * time.Second):
		t.Fatal("this account's own connection was not handed to the phone")
	}
}

func TestAStoppedWdaForwardListensNoMore(t *testing.T) {
	_, baseURL, stop, err := startWdaForward(func(c net.Conn) { _ = c.Close() }, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if conn, err := net.DialTimeout("tcp4", baseURL[len("http://"):], time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("the forward was still listening after it was stopped")
	}
}
