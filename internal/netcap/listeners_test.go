package netcap

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// WHERE A LISTENER IS BOUND, read from the kernel's own table: the census that tells an Appium on 127.0.0.1 from one on
// every address of the computer. Each system's table is read here from one the test writes, and this machine's for real,
// with listeners of this test's own on loopback only.

func addresses(ips []net.IP) string {
	var out []string
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return strings.Join(out, " ")
}

func TestTheLinuxTablesNameWhereEachListenerOnAPortIsBound(t *testing.T) {
	four := procNetHeading + "\n" + strings.Join([]string{
		procNetRow(0, "0100007F:127B", "00000000:0000", "0A", 501), // 127.0.0.1:4731
		procNetRow(1, "00000000:127B", "00000000:0000", "0A", 501), // 0.0.0.0:4731
		procNetRow(2, "0101A8C0:127B", "00000000:0000", "0A", 501), // 192.168.1.1:4731
		procNetRow(3, "0100007F:127B", "0100007F:C350", "01", 501), // a connection, not a listener
		procNetRow(4, "0100007F:127C", "00000000:0000", "0A", 501), // another port
	}, "\n") + "\n"
	got, found := listenAddressesInProcNet([]byte(four), 4731, binary.LittleEndian)
	if !found || addresses(got) != "127.0.0.1 0.0.0.0 192.168.1.1" {
		t.Fatalf("tcp: got %q %v", addresses(got), found)
	}
	six := procNetHeading + "\n" + procNetRow(0, "00000000000000000000000000000000:127B", "00000000000000000000000000000000:0000", "0A", 501) +
		"\n" + procNetRow(1, "00000000000000000000000001000000:127B", "00000000000000000000000000000000:0000", "0A", 501) + "\n"
	got, found = listenAddressesInProcNet([]byte(six), 4731, binary.LittleEndian)
	if !found || addresses(got) != ":: ::1" {
		t.Fatalf("tcp6: got %q %v", addresses(got), found)
	}
}

func TestTheWindowsTablesNameWhereEachListenerOnAPortIsBound(t *testing.T) {
	owners := fakeOwners{
		rows: [][6]uint32{{listen, loopback, 4731, anyAddress, 0, 7}, {listen, 0xC0A80101, 4731, anyAddress, 0, 7},
			{estab, loopback, 4731, loopback, 50001, 7}, {listen, loopback, 4732, anyAddress, 0, 7}},
		rows6: []row6{{state: listen, local: net.IPv6unspecified, localPort: 4731, remote: net.IPv6unspecified, pid: 7}},
	}
	got, found := listenAddressesInTable(owners, 4731)
	if !found || addresses(got) != "127.0.0.1 192.168.1.1 ::" {
		t.Fatalf("got %q %v", addresses(got), found)
	}
	if _, found := listenAddressesInTable(fakeOwners{rows: owners.rows, cut: true}, 4731); found {
		t.Fatal("a table cut short was read as an answer")
	}
}

// This machine's own kernel, on listeners this test opens on loopback — IPv4 and IPv6 — and never on any other address.
func TestThisMachineNamesWhereItsOwnListenersAreBound(t *testing.T) {
	if _, found := ListenersOn(1); !found {
		t.Skip("this system's kernel table is not read here (only macOS, Linux and Windows are)")
	}
	for _, c := range []struct{ network, address, want string }{
		{"tcp4", "127.0.0.1:0", "127.0.0.1"},
		{"tcp6", "[::1]:0", "::1"},
	} {
		listener, err := net.Listen(c.network, c.address)
		if err != nil {
			if c.network == "tcp6" {
				t.Logf("no IPv6 loopback here (%v); the IPv4 case stands", err)
				continue
			}
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		got, found := ListenersOn(port)
		_ = listener.Close()
		if !found || addresses(got) != c.want {
			t.Errorf("%s: a listener on %s read as %q (found %v)", c.network, c.want, addresses(got), found)
		}
	}
	if _, found := ListenersOn(0); found {
		t.Error("port 0 has no listener to name")
	}
}
