package netcap

import (
	"encoding/binary"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ── WHOSE PROGRAM ANSWERED A CONNECTION THIS PROCESS DIALLED (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, item 3) ──────────────

func TestTheLinuxTableNamesTheAccountThatListens(t *testing.T) {
	appium := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 47001}
	for _, c := range []struct {
		name      string
		rows      []string
		wantUID   int
		wantFound bool
	}{
		{"a listener on the address itself", []string{
			procNetRow(0, proxyEnd, "00000000:0000", "0A", 1002),
			procNetRow(1, proxyEnd, clientEnd, "01", 0), // the accepted end, not accept()ed yet: no owner of its own
		}, 1002, true},
		{"a listener on every address", []string{procNetRow(0, "00000000:B799", "00000000:0000", "0A", 501)}, 501, true},
		{"a dual-stack listener on ::", []string{procNetRow(0, "00000000000000000000000000000000:B799", "00000000000000000000000000000000:0000", "0A", 501)}, 501, true},
		{"the administrator's listener", []string{procNetRow(0, proxyEnd, "00000000:0000", "0A", 0)}, 0, true},
		{"a listener on another port", []string{procNetRow(0, "0100007F:B79A", "00000000:0000", "0A", 1002)}, 0, false},
		{"a listener on another address", []string{procNetRow(0, "0200007F:B799", "00000000:0000", "0A", 1002)}, 0, false},
		{"a connection is not a listener", []string{procNetRow(0, proxyEnd, clientEnd, "01", 1002)}, 0, false},
		{"two listeners that disagree", []string{
			procNetRow(0, proxyEnd, "00000000:0000", "0A", 501),
			procNetRow(1, "00000000:B799", "00000000:0000", "0A", 1002),
		}, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			table := procNetHeading + "\n" + strings.Join(c.rows, "\n") + "\n"
			uid, found := listenerUIDInProcNet([]byte(table), appium, binary.LittleEndian)
			if uid != c.wantUID || found != c.wantFound {
				t.Fatalf("got uid %d found %v, want uid %d found %v", uid, found, c.wantUID, c.wantFound)
			}
		})
	}
}

func TestTheMacListNamesTheAccountThatListens(t *testing.T) {
	const established = 4
	at := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 47001}
	anywhere := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0}
	wildcard := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 47001}
	elsewhere := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 47001}
	for _, c := range []struct {
		name      string
		list      []byte
		wantUID   int
		wantFound bool
	}{
		{"a listener on the address itself", pcbList(pcbRecord(at, anywhere, 1002, tcpsListen, true)), 1002, true},
		{"a listener on every address", pcbList(pcbRecord(wildcard, anywhere, 501, tcpsListen, true)), 501, true},
		{"a listener on another address", pcbList(pcbRecord(elsewhere, anywhere, 1002, tcpsListen, true)), 0, false},
		{"a connection is not a listener", pcbList(pcbRecord(at, itsClient, 1002, established, true)), 0, false},
		{"two listeners that disagree", pcbList(
			pcbRecord(at, anywhere, 501, tcpsListen, true),
			pcbRecord(wildcard, anywhere, 1002, tcpsListen, true),
		), 0, false},
		{"nothing at all", nil, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			uid, found := listenerUIDInPCBList(c.list, at)
			if uid != c.wantUID || found != c.wantFound {
				t.Fatalf("got uid %d found %v, want uid %d found %v", uid, found, c.wantUID, c.wantFound)
			}
		})
	}
}

// THE REAL KERNEL: a listener this test opens, on 127.0.0.1 and on every address (Go's ":0" is a dual-stack socket), is
// found to be this account's from the connection this process dialled to it — before it is accept()ed and after.
func TestThisMachineNamesTheAccountBehindAConnectionItDialled(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("the kernel is asked only on macOS and Linux")
	}
	for _, address := range []string{"127.0.0.1:0", ":0"} {
		t.Run(address, func(t *testing.T) {
			network := "tcp4"
			if address == ":0" {
				network = "tcp"
			}
			listener, err := net.Listen(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			dialled, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer dialled.Close()
			if uid, found := OwnerOfListenerBehind(dialled); !found || uid != os.Geteuid() {
				t.Fatalf("before accept: got uid %d found %v, want this account (%d)", uid, found, os.Geteuid())
			}
			accepted, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer accepted.Close()
			if uid, found := OwnerOfListenerBehind(dialled); !found || uid != os.Geteuid() {
				t.Fatalf("after accept: got uid %d found %v, want this account (%d)", uid, found, os.Geteuid())
			}
		})
	}
}
