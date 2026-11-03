package netcap

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
)

// ── Windows: THE OWNER OF A TCP SOCKET, FROM GetExtendedTcpTable ────────────────────────────────────────────────────
//
// Windows keeps no account on a socket. It keeps the PROCESS that owns it: GetExtendedTcpTable with
// TCP_TABLE_OWNER_PID_ALL answers a MIB_TCPTABLE_OWNER_PID, a count (a DWORD) and then one MIB_TCPROW_OWNER_PID per IPv4
// TCP socket, six DWORDs each: state, local address, local port, remote address, remote port, owning process id. The
// addresses are in network order as they lie in memory, and each port is the low two bytes of its DWORD, in network
// order. The process's account is then the user of its token (OpenProcess, OpenProcessToken, GetTokenUser).
//
// A socket that listens on every address (`::`, both IPv4 and IPv6) is in the IPv6 table only, as are the connections it
// accepts from IPv4, written as IPv4 addresses mapped into IPv6; so the IPv6 table (MIB_TCP6TABLE_OWNER_PID) is read too.
// Its rows are an address of 16 bytes, a scope id, a port, the same for the remote end, then the state and the process id.
//
// Built on every system, so the reading of the tables and the rule below are tested everywhere with tables the test
// writes; only peer_windows.go asks Windows, and a test there asks it for real.
//
// THE ACCOUNT, IN THE NUMBERS THE REST OF THIS PACKAGE SPEAKS. MayUseTheProxy compares a uid with os.Geteuid() and with 0,
// so the answer is mapped onto them:
//   - this process's own account (the same SID) is os.Geteuid()'s own answer on that system — -1 on Windows;
//   - LocalSystem (S-1-5-18), Windows' root, is 0, let through for the reason root is (peer.go);
//   - any other account is its SID's last part, its relative id (1001 for a second local user), which is never -1 and
//     never 0: it is refused, and the sentence that refuses it names that number.
//
// A process whose account cannot be read (a service of another account, a process that has ended) is no answer, and no
// answer is refused.
const (
	afInet           = 2  // AF_INET
	afInet6          = 23 // AF_INET6 on Windows
	tcp6RowLength    = 56 // sizeof(MIB_TCP6ROW_OWNER_PID)
	tcpRowLength     = 24 // sizeof(MIB_TCPROW_OWNER_PID): six DWORDs
	tcpStateListen   = 2  // MIB_TCP_STATE_LISTEN
	tcpStateTimeWait = 11 // MIB_TCP_STATE_TIME_WAIT: kept after its socket is gone, and never an answer
	localSystemSID   = "S-1-5-18"
)

// tcpRow is one socket of the table.
type tcpRow struct {
	state                 uint32
	local, remote         net.IP
	localPort, remotePort int
	pid                   uint32
}

// tcpOwners is what the rule needs from Windows, behind an interface so a test can be the table and the accounts.
type tcpOwners interface {
	// table is GetExtendedTcpTable's answer for family (afInet or afInet6), TCP_TABLE_OWNER_PID_ALL.
	table(family int) ([]byte, error)
	// accountOf is the SID, as a string, of the account the process pid runs as.
	accountOf(pid uint32) (string, bool)
	// thisAccount is this process's own SID.
	thisAccount() (string, bool)
}

// tcp6Rows reads a MIB_TCP6TABLE_OWNER_PID. A table whose count does not fit what it holds is no table.
func tcp6Rows(table []byte) ([]tcpRow, bool) {
	if len(table) < 4 {
		return nil, false
	}
	count := int(binary.LittleEndian.Uint32(table[0:4]))
	if count < 0 || count > (len(table)-4)/tcp6RowLength {
		return nil, false
	}
	rows := make([]tcpRow, 0, count)
	for i := 0; i < count; i++ {
		row := table[4+i*tcp6RowLength : 4+(i+1)*tcp6RowLength]
		rows = append(rows, tcpRow{
			local:      net.IP(append([]byte(nil), row[0:16]...)),
			localPort:  int(binary.BigEndian.Uint16(row[20:22])),
			remote:     net.IP(append([]byte(nil), row[24:40]...)),
			remotePort: int(binary.BigEndian.Uint16(row[44:46])),
			state:      binary.LittleEndian.Uint32(row[48:52]),
			pid:        binary.LittleEndian.Uint32(row[52:56]),
		})
	}
	return rows, true
}

// allRows is both tables' sockets. A table that cannot be read or read through is no answer at all.
func allRows(owners tcpOwners) ([]tcpRow, bool) {
	four, err := owners.table(afInet)
	if err != nil {
		return nil, false
	}
	rows, ok := tcpRows(four)
	if !ok {
		return nil, false
	}
	six, err := owners.table(afInet6)
	if err != nil {
		return nil, false
	}
	more, ok := tcp6Rows(six)
	if !ok {
		return nil, false
	}
	return append(rows, more...), true
}

// tcpRows reads a MIB_TCPTABLE_OWNER_PID. A table whose count does not fit what it holds is no table.
func tcpRows(table []byte) ([]tcpRow, bool) {
	if len(table) < 4 {
		return nil, false
	}
	count := int(binary.LittleEndian.Uint32(table[0:4]))
	if count < 0 || count > (len(table)-4)/tcpRowLength {
		return nil, false
	}
	rows := make([]tcpRow, 0, count)
	for i := 0; i < count; i++ {
		row := table[4+i*tcpRowLength : 4+(i+1)*tcpRowLength]
		rows = append(rows, tcpRow{
			state:      binary.LittleEndian.Uint32(row[0:4]),
			local:      net.IPv4(row[4], row[5], row[6], row[7]).To4(),
			localPort:  int(binary.BigEndian.Uint16(row[8:10])),
			remote:     net.IPv4(row[12], row[13], row[14], row[15]).To4(),
			remotePort: int(binary.BigEndian.Uint16(row[16:18])),
			pid:        binary.LittleEndian.Uint32(row[20:24]),
		})
	}
	return rows, true
}

// uidOfPeerInTable finds the socket whose local end is peer and whose remote end is us, and maps its process's account.
func uidOfPeerInTable(owners tcpOwners, peer, us *net.TCPAddr) (int, bool) {
	peer4, us4 := peer.IP.To4(), us.IP.To4()
	if peer4 == nil || us4 == nil {
		return 0, false
	}
	return uidInTable(owners, func(r tcpRow) bool {
		return r.state != tcpStateTimeWait && r.state != tcpStateListen &&
			r.localPort == peer.Port && r.remotePort == us.Port && r.local.Equal(peer4) && r.remote.Equal(us4)
	})
}

// uidOfListenerInTable finds the LISTENING socket that holds at — at's own address, or the wildcard on at's port.
func uidOfListenerInTable(owners tcpOwners, at *net.TCPAddr) (int, bool) {
	at4 := at.IP.To4()
	if at4 == nil {
		return 0, false
	}
	return uidInTable(owners, func(r tcpRow) bool {
		return r.state == tcpStateListen && r.localPort == at.Port && (r.local.Equal(at4) || r.local.IsUnspecified())
	})
}

// uidInTable maps the account behind every matching row. Two rows that disagree about the account are no answer.
func uidInTable(owners tcpOwners, matches func(tcpRow) bool) (int, bool) {
	rows, ok := allRows(owners)
	if !ok {
		return 0, false
	}
	me, known := owners.thisAccount()
	if !known {
		return 0, false
	}
	owner, found := 0, false
	for _, r := range rows {
		if !matches(r) {
			continue
		}
		sid, ok := owners.accountOf(r.pid)
		if !ok {
			return 0, false
		}
		uid, ok := uidForAccount(sid, me)
		if !ok || (found && uid != owner) {
			return 0, false
		}
		owner, found = uid, true
	}
	return owner, found
}

// uidForAccount maps a SID onto this package's numbers (see the top of this file).
func uidForAccount(sid, me string) (int, bool) {
	switch {
	case strings.EqualFold(sid, me):
		return ThisAccount(), true
	case strings.EqualFold(sid, localSystemSID):
		return 0, true
	}
	parts := strings.Split(sid, "-")
	if len(parts) < 4 || !strings.EqualFold(parts[0], "S") {
		return 0, false
	}
	rid, err := strconv.ParseUint(parts[len(parts)-1], 10, 32)
	if err != nil || int(rid) == ThisAccount() || rid == 0 {
		return 0, false
	}
	return int(rid), true
}

// pidOfListener is the process that holds a listening socket on port, on any address: who to ask to let go of it.
func pidOfListener(owners tcpOwners, port int) (uint32, bool) {
	rows, ok := allRows(owners)
	if !ok {
		return 0, false
	}
	for _, r := range rows {
		if r.state == tcpStateListen && r.localPort == port {
			return r.pid, true
		}
	}
	return 0, false
}
