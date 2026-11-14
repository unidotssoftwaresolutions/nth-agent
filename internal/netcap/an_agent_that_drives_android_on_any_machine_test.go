package netcap

import (
	"encoding/binary"
	"net"
	"testing"
)

// THE WINDOWS OWNER LOOKUP, ON A TABLE THE TEST WRITES (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE). Windows answers the
// owner question in two steps — GetExtendedTcpTable names the process behind each socket, and the process's token names its
// account — and both sit behind tcpOwners, so the rule is held here on every system. The real call is held by
// peer_windows_test.go on a Windows runner.

const (
	meSID    = "S-1-5-21-1111111111-2222222222-3333333333-1001"
	otherSID = "S-1-5-21-1111111111-2222222222-3333333333-1002"
)

type fakeOwners struct {
	rows     [][6]uint32 // state, local address, local port, remote address, remote port, pid — as Windows lays them out
	rows6    []row6      // the IPv6 table's sockets
	accounts map[uint32]string
	me       string
	cut      bool // the table's count claims a row it does not hold
}

// row6 is one socket of the IPv6 table: its addresses are 16 bytes.
type row6 struct {
	state                 uint32
	local, remote         net.IP
	localPort, remotePort uint16
	pid                   uint32
}

func (f fakeOwners) table(family int) ([]byte, error) {
	if family == afInet6 {
		table := binary.LittleEndian.AppendUint32(nil, uint32(len(f.rows6)))
		for _, r := range f.rows6 {
			table = append(table, r.local.To16()...)
			table = binary.LittleEndian.AppendUint32(table, 0) // scope id
			table = append(table, port(uint32(r.localPort))...)
			table = append(table, r.remote.To16()...)
			table = binary.LittleEndian.AppendUint32(table, 0)
			table = append(table, port(uint32(r.remotePort))...)
			table = binary.LittleEndian.AppendUint32(table, r.state)
			table = binary.LittleEndian.AppendUint32(table, r.pid)
		}
		return table, nil
	}
	count := uint32(len(f.rows))
	if f.cut {
		count++
	}
	table := binary.LittleEndian.AppendUint32(nil, count)
	for _, r := range f.rows {
		table = binary.LittleEndian.AppendUint32(table, r[0])
		table = append(table, ip4(r[1])...)
		table = append(table, port(r[2])...)
		table = append(table, ip4(r[3])...)
		table = append(table, port(r[4])...)
		table = binary.LittleEndian.AppendUint32(table, r[5])
	}
	return table, nil
}

func (f fakeOwners) accountOf(pid uint32) (string, bool) {
	sid, ok := f.accounts[pid]
	return sid, ok
}

func (f fakeOwners) thisAccount() (string, bool) { return f.me, f.me != "" }

// ip4 is an address as Windows keeps it in a DWORD: its four bytes in network order.
func ip4(a uint32) []byte { return binary.BigEndian.AppendUint32(nil, a) }

// port is a port as Windows keeps it in a DWORD: its two bytes in network order, then two zero bytes.
func port(p uint32) []byte { return append(binary.BigEndian.AppendUint16(nil, uint16(p)), 0, 0) }

const (
	loopback   = 0x7f000001 // 127.0.0.1
	anyAddress = 0
	listen     = 2  // MIB_TCP_STATE_LISTEN
	estab      = 5  // MIB_TCP_STATE_ESTAB
	timeWait   = 11 // MIB_TCP_STATE_TIME_WAIT
)

func TestTheWindowsTableNamesTheAccountBehindAConnection(t *testing.T) {
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50001}
	us := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 31002}
	// The connecting socket (peer → us) and the accepted one (us → peer), each with its own process.
	connection := func(connectingPID uint32) [][6]uint32 {
		return [][6]uint32{
			{estab, loopback, 50001, loopback, 31002, connectingPID},
			{estab, loopback, 31002, loopback, 50001, 900},
		}
	}
	for _, c := range []struct {
		name  string
		f     fakeOwners
		uid   int
		found bool
		may   bool
	}{
		{"this account's own program", fakeOwners{rows: connection(42), accounts: map[uint32]string{42: meSID, 900: meSID}, me: meSID}, ThisAccount(), true, true},
		{"LocalSystem, Windows' root", fakeOwners{rows: connection(4), accounts: map[uint32]string{4: localSystemSID}, me: meSID}, 0, true, true},
		{"another account on this machine", fakeOwners{rows: connection(77), accounts: map[uint32]string{77: otherSID}, me: meSID}, 1002, true, false},
		{"a process whose account Windows would not say", fakeOwners{rows: connection(78), accounts: map[uint32]string{}, me: meSID}, 0, false, false},
		{"no such connection", fakeOwners{rows: connection(42)[1:], accounts: map[uint32]string{900: meSID}, me: meSID}, 0, false, false},
		{"a socket gone to TIME_WAIT", fakeOwners{rows: [][6]uint32{{timeWait, loopback, 50001, loopback, 31002, 42}}, accounts: map[uint32]string{42: meSID}, me: meSID}, 0, false, false},
		{"two sockets that disagree", fakeOwners{rows: append(connection(42), [6]uint32{estab, loopback, 50001, loopback, 31002, 77}),
			accounts: map[uint32]string{42: meSID, 77: otherSID}, me: meSID}, 0, false, false},
		{"a table cut short", fakeOwners{rows: connection(42), accounts: map[uint32]string{42: meSID}, me: meSID, cut: true}, 0, false, false},
		{"this process's own account unknown", fakeOwners{rows: connection(42), accounts: map[uint32]string{42: meSID}}, 0, false, false},
	} {
		uid, found := uidOfPeerInTable(c.f, peer, us)
		if found != c.found || (found && uid != c.uid) {
			t.Errorf("%s: got uid %d found %v, want uid %d found %v", c.name, uid, found, c.uid, c.found)
		}
		if may := found && MayUseTheProxy(uid); may != c.may {
			t.Errorf("%s: may use the proxy %v, want %v", c.name, may, c.may)
		}
	}
}

func TestTheWindowsTableNamesTheAccountBehindAListener(t *testing.T) {
	at := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4723}
	for _, c := range []struct {
		name  string
		rows  [][6]uint32
		uid   int
		found bool
	}{
		{"your Appium, on 127.0.0.1", [][6]uint32{{listen, loopback, 4723, anyAddress, 0, 42}}, ThisAccount(), true},
		{"your Appium, on every address", [][6]uint32{{listen, anyAddress, 4723, anyAddress, 0, 42}}, ThisAccount(), true},
		{"another account's program", [][6]uint32{{listen, loopback, 4723, anyAddress, 0, 77}}, 1002, true},
		{"a connection on that port is not its listener", [][6]uint32{{estab, loopback, 4723, loopback, 50001, 42}}, 0, false},
		{"nothing listens there", [][6]uint32{{listen, loopback, 4724, anyAddress, 0, 42}}, 0, false},
	} {
		uid, found := uidOfListenerInTable(fakeOwners{rows: c.rows, accounts: map[uint32]string{42: meSID, 77: otherSID}, me: meSID}, at)
		if found != c.found || (found && uid != c.uid) {
			t.Errorf("%s: got uid %d found %v, want uid %d found %v", c.name, uid, found, c.uid, c.found)
		}
	}
}

func TestTheWindowsTableNamesTheProcessThatHoldsAPort(t *testing.T) {
	owners := fakeOwners{rows: [][6]uint32{{estab, loopback, 31002, loopback, 50001, 7}, {listen, anyAddress, 31002, anyAddress, 0, 4242}},
		rows6: []row6{{state: listen, local: net.IPv6unspecified, localPort: 31004, remote: net.IPv6unspecified, pid: 5151}}}
	if pid, ok := pidOfListener(owners, 31002); !ok || pid != 4242 {
		t.Fatalf("the listener on 31002 is process 4242, got %d (%v)", pid, ok)
	}
	if pid, ok := pidOfListener(owners, 31004); !ok || pid != 5151 {
		t.Fatalf("the listener on every address of both kinds, in the IPv6 table, is process 5151, got %d (%v)", pid, ok)
	}
	if _, ok := pidOfListener(owners, 31003); ok {
		t.Fatal("nothing listens on 31003")
	}
}

// A LISTENER ON EVERY ADDRESS (`::`) is in Windows' IPv6 table only, and so are the IPv4 connections it accepts, their
// addresses mapped into IPv6 (::ffff:127.0.0.1).
func TestTheWindowsIPv6TableNamesADualStackListenerAndItsConnections(t *testing.T) {
	mapped := net.ParseIP("::ffff:127.0.0.1")
	owners := fakeOwners{
		rows: [][6]uint32{{estab, loopback, 50001, loopback, 4723, 42}}, // this process's dial, in the IPv4 table
		rows6: []row6{
			{state: listen, local: net.IPv6unspecified, localPort: 4723, remote: net.IPv6unspecified, pid: 77},
			{state: estab, local: mapped, localPort: 4723, remote: mapped, remotePort: 50001, pid: 77},
		},
		accounts: map[uint32]string{42: meSID, 77: otherSID}, me: meSID,
	}
	at := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4723}
	if uid, found := uidOfListenerInTable(owners, at); !found || uid != 1002 {
		t.Fatalf("the dual-stack listener is another account's: got %d %v", uid, found)
	}
	accepted, dialled := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4723}, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50001}
	if uid, found := uidOfPeerInTable(owners, accepted, dialled); !found || uid != 1002 {
		t.Fatalf("the socket it accepted, mapped into IPv6, is that account's too: got %d %v", uid, found)
	}
	if uid, found := uidOfPeerInTable(owners, dialled, accepted); !found || uid != ThisAccount() {
		t.Fatalf("the dialling end is this process's: got %d %v", uid, found)
	}
}

func TestAnAccountIsMappedOnlyFromAWholeSID(t *testing.T) {
	for _, c := range []struct {
		sid   string
		uid   int
		found bool
	}{
		{meSID, ThisAccount(), true},
		{"s-1-5-21-1111111111-2222222222-3333333333-1001", ThisAccount(), true}, // SIDs compare without case
		{localSystemSID, 0, true},
		{otherSID, 1002, true},
		{"S-1-5-21-1-2-3-0", 0, false}, // a relative id of 0 would read as root
		{"not a sid", 0, false},
		{"S-1-5", 0, false},
	} {
		uid, found := uidForAccount(c.sid, meSID)
		if found != c.found || (found && uid != c.uid) {
			t.Errorf("%s: got uid %d found %v, want uid %d found %v", c.sid, uid, found, c.uid, c.found)
		}
	}
}
