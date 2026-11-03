//go:build windows

package netcap

import (
	"net"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows side of the owner question (see peer_tcptable.go for the table and the rule).

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL

// windowsOwners asks this Windows.
type windowsOwners struct{}

// table asks for the IPv4 TCP table. The table can grow between the size question and the answer, which Windows says
// with ERROR_INSUFFICIENT_BUFFER and the size it needs now; it is asked again, a bounded number of times.
func (windowsOwners) table(family int) ([]byte, error) {
	size := uint32(64 << 10)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(family), tcpTableOwnerPIDAll, 0)
		switch windows.Errno(r) {
		case 0:
			return buffer, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 4 << 10
			continue
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}

// accountOf reads the user of the process's token. PROCESS_QUERY_LIMITED_INFORMATION is what any account may ask of
// its own processes; another account's service usually refuses it, and a refusal is no answer.
func (windowsOwners) accountOf(pid uint32) (string, bool) {
	if pid == 0 {
		return "", false
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(process)
	var access windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &access); err != nil {
		return "", false
	}
	defer access.Close()
	user, err := access.GetTokenUser()
	if err != nil {
		return "", false
	}
	return user.User.Sid.String(), true
}

func (windowsOwners) thisAccount() (string, bool) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", false
	}
	return user.User.Sid.String(), true
}

func peerUID(peer, us *net.TCPAddr) (int, bool) { return uidOfPeerInTable(windowsOwners{}, peer, us) }

func listenerUID(at *net.TCPAddr) (int, bool) { return uidOfListenerInTable(windowsOwners{}, at) }

// ListenerProcess names the program listening on port on this machine — its process id and its program's file name —
// so a sentence can say who to ask to let go of a port. found is false when Windows would not say.
func ListenerProcess(port int) (pid int, program string, found bool) {
	id, ok := pidOfListener(windowsOwners{}, port)
	if !ok || id == 0 {
		return 0, "", false
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, id)
	if err != nil {
		return int(id), "", true
	}
	defer windows.CloseHandle(process)
	name := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(process, 0, &name[0], &size); err != nil {
		return int(id), "", true
	}
	return int(id), filepath.Base(windows.UTF16ToString(name[:size])), true
}
