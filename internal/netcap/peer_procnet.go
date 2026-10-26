package netcap

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// ── LINUX: THE OWNER OF A TCP SOCKET, FROM /proc/net/tcp ───────────────────────────────────────────────────────────
//
// Each row is one socket:
//
//	sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
//	 0: 0100007F:A5F0 0100007F:B3A2 01 00000000:00000000 00:00000000 00000000  1000        0 123456 …
//
// An address is the kernel's 32-bit words printed in hex as they sit in memory (so 127.0.0.1 reads 0100007F on a
// little-endian machine); /proc/net/tcp6 prints four such words. A port is its number in hex. Built on every system, so
// the parsing is tested everywhere; only peer_linux.go reads the files.

// tcpTimeWait is the state of a row the kernel keeps, with no owner (uid 0), after its socket is gone. Such a row can
// share a live connection's four addresses, so it is never an answer.
const tcpTimeWait = "06"

// uidInProcNet finds, in one /proc/net/tcp or /proc/net/tcp6 table, the socket whose local end is peer and whose remote
// end is us, and returns the account that owns it. Two live rows that disagree about the owner are no answer.
func uidInProcNet(table []byte, peer, us *net.TCPAddr, order binary.ByteOrder) (int, bool) {
	owner, found := 0, false
	scanner := bufio.NewScanner(bytes.NewReader(table))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || !strings.HasSuffix(fields[0], ":") {
			continue // the heading, or a row this reader does not know
		}
		if fields[3] == tcpTimeWait {
			continue
		}
		if !procNetEndIs(fields[1], peer, order) || !procNetEndIs(fields[2], us, order) {
			continue
		}
		uid, err := strconv.Atoi(fields[7])
		if err != nil || uid < 0 {
			return 0, false
		}
		if found && uid != owner {
			return 0, false
		}
		owner, found = uid, true
	}
	if scanner.Err() != nil {
		return 0, false
	}
	return owner, found
}

// procNetEndIs says whether one "ADDRESS:PORT" column names want — an IPv4 address, or an IPv4 address mapped into IPv6.
func procNetEndIs(column string, want *net.TCPAddr, order binary.ByteOrder) bool {
	hexAddress, hexPort, ok := strings.Cut(column, ":")
	if !ok {
		return false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil || int(port) != want.Port {
		return false
	}
	raw, err := hex.DecodeString(hexAddress)
	if err != nil || (len(raw) != net.IPv4len && len(raw) != net.IPv6len) {
		return false
	}
	address := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		// The kernel printed each 32-bit word as a number; put its bytes back in the order they sat in memory.
		order.PutUint32(address[i:i+4], binary.BigEndian.Uint32(raw[i:i+4]))
	}
	return address.Equal(want.IP)
}

// tcpListen is the state of a listening socket's row.
const tcpListen = "0A"

// listenerUIDInProcNet finds, in one /proc/net/tcp or /proc/net/tcp6 table, the LISTENING socket that holds at — at's own
// address, or the wildcard on at's port — and returns the account that owns it. Two listeners that disagree about the
// owner are no answer.
func listenerUIDInProcNet(table []byte, at *net.TCPAddr, order binary.ByteOrder) (int, bool) {
	owner, found := 0, false
	scanner := bufio.NewScanner(bytes.NewReader(table))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || !strings.HasSuffix(fields[0], ":") || fields[3] != tcpListen {
			continue
		}
		address, port, ok := procNetEnd(fields[1], order)
		if !ok || port != at.Port || !(address.Equal(at.IP) || address.IsUnspecified()) {
			continue
		}
		uid, err := strconv.Atoi(fields[7])
		if err != nil || uid < 0 {
			return 0, false
		}
		if found && uid != owner {
			return 0, false
		}
		owner, found = uid, true
	}
	if scanner.Err() != nil {
		return 0, false
	}
	return owner, found
}

// procNetEnd reads one "ADDRESS:PORT" column.
func procNetEnd(column string, order binary.ByteOrder) (net.IP, int, bool) {
	hexAddress, hexPort, ok := strings.Cut(column, ":")
	if !ok {
		return nil, 0, false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return nil, 0, false
	}
	raw, err := hex.DecodeString(hexAddress)
	if err != nil || (len(raw) != net.IPv4len && len(raw) != net.IPv6len) {
		return nil, 0, false
	}
	address := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		order.PutUint32(address[i:i+4], binary.BigEndian.Uint32(raw[i:i+4]))
	}
	return address, int(port), true
}
