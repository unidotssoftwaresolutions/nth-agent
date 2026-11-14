package netcap

import (
	"encoding/binary"
	"net"
	"testing"
)

// ANOTHER ACCOUNT ON THE SAME COMPUTER (ANOTHER_ACCOUNT_AND_LIMITS): what the kernel's tables say about a socket is the
// owner only when the socket still has an owner to say.

// On Linux, a client that closed before the lookup has no file left: its row (a FIN_WAIT2 timewait printed as 05,
// or an orphaned FIN_WAIT1, CLOSING or LAST_ACK) reads uid 0 and inode 0 whoever opened it. Such a row is no answer.
func TestALinuxRowWithNoSocketFileLeftIsNoAnswer(t *testing.T) {
	orphan := func(state string) string {
		return "   0: " + clientEnd + " " + proxyEnd + " " + state +
			" 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 20 4 30 10 -1"
	}
	for _, state := range []string{"04", "05", "07", "08", "09", "0B"} {
		table := []byte(procNetHeading + "\n" + orphan(state) + "\n")
		if uid, found := uidInProcNet(table, itsClient, theProxy, binary.LittleEndian); found {
			t.Errorf("state %s with inode 0: got uid %d found; a row whose socket has no file is no answer", state, uid)
		}
	}
	// A live row beside it still answers, and the orphan does not make it disagree.
	table := []byte(procNetHeading + "\n" + orphan("05") + "\n" + procNetRow(1, clientEnd, proxyEnd, "01", 1002) + "\n")
	if uid, found := uidInProcNet(table, itsClient, theProxy, binary.LittleEndian); !found || uid != 1002 {
		t.Errorf("the live row's owner is the answer: got %d %v", uid, found)
	}
}

// On Windows a socket's owner is read from the process that holds its recorded id now. A socket another account
// kept alive after its own process ended, whose process id Windows then gave to one of this account's programs, would
// read as this account's. A process that started after the socket was made did not make it, and is no answer.
func TestAWindowsProcessYoungerThanTheSocketIsNotItsOwner(t *testing.T) {
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50001}
	us := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 31002}
	rows := [][6]uint32{{estab, loopback, 50001, loopback, 31002, 42}, {estab, loopback, 31002, loopback, 50001, 900}}
	for _, c := range []struct {
		name    string
		started uint64
		found   bool
	}{
		{"the process started before its socket was made", 1000, true},
		{"the process id was given again after the socket was made", 3000, false},
	} {
		f := fakeOwners{rows: rows, accounts: map[uint32]string{42: meSID, 900: meSID}, me: meSID,
			created: 2000, started: map[uint32]uint64{42: c.started, 900: 1000}}
		uid, found := uidOfPeerInTable(f, peer, us)
		if found != c.found || (found && uid != ThisAccount()) {
			t.Errorf("%s: got uid %d found %v, want found %v", c.name, uid, found, c.found)
		}
	}
	listener := [][6]uint32{{listen, loopback, 4723, anyAddress, 0, 42}}
	f := fakeOwners{rows: listener, accounts: map[uint32]string{42: meSID}, me: meSID, created: 2000, started: map[uint32]uint64{42: 3000}}
	if uid, found := uidOfListenerInTable(f, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4723}); found {
		t.Errorf("a listener whose process started after it was made: got uid %d found; it is no answer", uid)
	}
}
