package netcap

import (
	"encoding/binary"
	"net"
)

// ── macOS: THE OWNER OF A TCP SOCKET, FROM THE net.inet.tcp.pcblist64 SYSCTL ─────────────────────────────────────────
//
// The sysctl answers a struct xinpgen, then one struct xtcpcb64 per TCP socket, then a closing xinpgen. Each starts with
// its own length (a uint32), so a reader steps by it. The layout is the SDK's public <netinet/tcp_var.h> and
// <netinet/in_pcb.h>, under #pragma pack(4) — the offsets below were MEASURED with the SDK's own compiler (offsetof, the
// same on arm64 and x86_64), and TestThisMachineNamesItsOwnConnection proves them against the kernel wherever the tests run
// on a Mac. Built on every system, so the parsing is tested everywhere; only peer_darwin.go asks the kernel.
const (
	pcbLength   = 0   // xt_len
	pcbFport    = 20  // xt_inpcb.inp_fport, network order
	pcbLport    = 22  // xt_inpcb.inp_lport, network order
	pcbVflag    = 96  // xt_inpcb.inp_vflag
	pcbFaddr4   = 112 // xt_inpcb.inp_dependfaddr.inp46_foreign.ia46_addr4
	pcbLaddr4   = 128 // xt_inpcb.inp_dependladdr.inp46_local.ia46_addr4
	pcbSockLen  = 148 // xt_inpcb.xi_socket.xso_len — 0 when the PCB has no socket left
	pcbProtocol = 176 // xt_inpcb.xi_socket.xso_protocol
	pcbUID      = 252 // xt_inpcb.xi_socket.so_uid
	pcbState    = 292 // t_state
	pcbMinimum  = pcbState + 4

	inpIPv4       = 0x1 // INP_IPV4: the PCB holds an IPv4 address, plain or mapped into IPv6
	ipprotoTCP    = 6
	tcpsTimeWait  = 10 // TCPS_TIME_WAIT: kept after its socket is gone, and never an answer
	xinpgenLength = 24 // sizeof(struct xinpgen)
)

// uidInPCBList finds, in a pcblist64 answer, the socket whose local end is peer and whose remote end is us, and returns
// the account that owns it. Two live records that disagree about the owner are no answer.
func uidInPCBList(list []byte, peer, us *net.TCPAddr) (int, bool) {
	peer4, us4 := peer.IP.To4(), us.IP.To4()
	if peer4 == nil || us4 == nil || len(list) < xinpgenLength {
		return 0, false
	}
	owner, found := 0, false
	at := int(binary.LittleEndian.Uint32(list[0:4])) // past the opening xinpgen
	for at+4 <= len(list) {
		length := int(binary.LittleEndian.Uint32(list[at+pcbLength : at+pcbLength+4]))
		if length <= xinpgenLength || at+length > len(list) {
			break // the closing xinpgen, or a list cut short
		}
		record := list[at : at+length]
		at += length
		if length < pcbMinimum {
			return 0, false // not the struct these offsets describe: refuse rather than misread
		}
		if record[pcbVflag]&inpIPv4 == 0 ||
			binary.LittleEndian.Uint32(record[pcbSockLen:]) == 0 ||
			binary.LittleEndian.Uint32(record[pcbProtocol:]) != ipprotoTCP ||
			binary.LittleEndian.Uint32(record[pcbState:]) == tcpsTimeWait {
			continue
		}
		if int(binary.BigEndian.Uint16(record[pcbLport:])) != peer.Port || int(binary.BigEndian.Uint16(record[pcbFport:])) != us.Port ||
			!net.IP(record[pcbLaddr4:pcbLaddr4+4]).Equal(peer4) || !net.IP(record[pcbFaddr4:pcbFaddr4+4]).Equal(us4) {
			continue
		}
		uid := int(binary.LittleEndian.Uint32(record[pcbUID:]))
		if found && uid != owner {
			return 0, false
		}
		owner, found = uid, true
	}
	return owner, found
}

// tcpsListen is TCPS_LISTEN.
const tcpsListen = 1

// listenerUIDInPCBList finds, in a pcblist64 answer, the LISTENING socket that holds at — at's own address, or the wildcard
// on at's port (a dual-stack listener on :: carries INP_IPV4 and reads 0.0.0.0 here) — and returns the account that owns it.
// Two listeners that disagree about the owner are no answer.
func listenerUIDInPCBList(list []byte, at *net.TCPAddr) (int, bool) {
	at4 := at.IP.To4()
	if at4 == nil || len(list) < xinpgenLength {
		return 0, false
	}
	owner, found := 0, false
	position := int(binary.LittleEndian.Uint32(list[0:4]))
	for position+4 <= len(list) {
		length := int(binary.LittleEndian.Uint32(list[position+pcbLength : position+pcbLength+4]))
		if length <= xinpgenLength || position+length > len(list) {
			break
		}
		record := list[position : position+length]
		position += length
		if length < pcbMinimum {
			return 0, false
		}
		if record[pcbVflag]&inpIPv4 == 0 ||
			binary.LittleEndian.Uint32(record[pcbSockLen:]) == 0 ||
			binary.LittleEndian.Uint32(record[pcbProtocol:]) != ipprotoTCP ||
			binary.LittleEndian.Uint32(record[pcbState:]) != tcpsListen {
			continue
		}
		local := net.IP(record[pcbLaddr4 : pcbLaddr4+4])
		if int(binary.BigEndian.Uint16(record[pcbLport:])) != at.Port || !(local.Equal(at4) || local.IsUnspecified()) {
			continue
		}
		uid := int(binary.LittleEndian.Uint32(record[pcbUID:]))
		if found && uid != owner {
			return 0, false
		}
		owner, found = uid, true
	}
	return owner, found
}
