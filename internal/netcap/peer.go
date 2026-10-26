package netcap

import (
	"errors"
	"net"
	"os"
	"time"
)

// ── WHO MAY USE A CAPTURE PROXY: THIS ACCOUNT, AND NO OTHER ACCOUNT ON THE MACHINE ─────────────────────────────────
//
// THE RESIDUAL THIS NARROWS (AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE rule 2). A capture's proxy listens on 127.0.0.1, and every
// account on this machine can reach 127.0.0.1. The phone reaches the proxy through adb's server (`adb reverse`), which runs
// as the person who uses adb here. So on a machine other people log into, another account could otherwise send its own
// traffic through the proxy while a capture runs — out through this machine's network, and into that run's record.
//
// WHAT IS CHECKED: every connection, as it arrives, before a byte of it is read. The kernel keeps, for each TCP socket,
// the account that opened it; the connecting end of this connection is looked up there, and a connection opened by any
// account but this one is closed unread.
//
// THE ADMINISTRATOR (root, uid 0) IS LET THROUGH, deliberately. It can already read this account's capture key and files,
// so refusing it protects nothing — and on Linux an adb server started with sudo runs as root, and refusing it would cut
// the phone off for the whole capture.
//
// HOW THE KERNEL IS ASKED:
//   - Linux: /proc/net/tcp (and /proc/net/tcp6, for a dual-stack socket that connected to 127.0.0.1), the row's uid.
//     SO_PEERCRED answers only for Unix-domain sockets, and this one is TCP, so it does not apply here.
//   - macOS: the net.inet.tcp.pcblist64 sysctl — struct xtcpcb64 from the SDK's public <netinet/tcp_var.h>, the list
//     netstat reads — whose socket part carries so_uid. No root, no cgo.
//   - Anywhere else: capture does not run (capture.Supported), and the answer is "not found".
//
// NOT FOUND IS REFUSED: a connection whose owner cannot be found is not known to be ours. And so that a system where the
// kernel cannot be asked never leaves a phone pointed at a proxy that refuses its every connection, the check proves
// itself before a capture or a trust check touches a phone (PeerCheckWorks).

// PeerOwner names the account that opened the connecting end of c, as the kernel holds it. found is false when the kernel
// could not be asked, or holds no such connection.
type PeerOwner func(c net.Conn) (uid int, found bool)

// OwnerOfPeer is this system's PeerOwner.
func OwnerOfPeer(c net.Conn) (int, bool) {
	us, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		return 0, false
	}
	peer, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return 0, false
	}
	// The connecting end is the socket whose OWN local address is our remote one, and whose remote address is ours.
	return peerUID(peer, us)
}

// OwnerOfListenerBehind names the account whose program answered c — a connection THIS process dialled to a listener on
// this machine's loopback: Appium, adb's server, a phone's screen server. The far end of such a connection is the socket
// the listener accepted, and the kernel holds that socket's owner like any other.
//
// ROOT IS NOT TAKEN AT ITS WORD HERE. On Linux a connection the listener has not accept()ed yet has no file of its own,
// and the kernel can print uid 0 for it whoever listens. So an answer of 0, or none, is checked against the LISTENING
// socket on that address and port, whose owner the kernel always knows; nothing found there is no answer.
func OwnerOfListenerBehind(c net.Conn) (int, bool) {
	uid, found := OwnerOfPeer(c)
	if found && uid != 0 {
		return uid, true
	}
	listener, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return 0, false
	}
	return listenerUID(listener)
}

// MayUseTheProxy says whether a connection opened by uid may use a capture proxy: one opened by this account, or by the
// machine's administrator (root). THE ONE HOME of that rule.
func MayUseTheProxy(uid int) bool { return uid == os.Geteuid() || uid == 0 }

// ErrPeerCheck is a peer check that does not work on this machine.
var ErrPeerCheck = errors.New("this machine cannot tell which account opened a connection to the capture proxy")

// PeerCheckWorks proves the check on this machine: a connection this process makes to a loopback listener of its own must
// be found, opened by this account. It touches nothing but that listener, and takes a few milliseconds.
func PeerCheckWorks() error { return peerCheckWorks(OwnerOfPeer) }

func peerCheckWorks(owner PeerOwner) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return ErrPeerCheck
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	dialed, err := net.DialTimeout("tcp4", listener.Addr().String(), 2*time.Second)
	if err != nil {
		return ErrPeerCheck
	}
	defer dialed.Close()
	select {
	case c, ok := <-accepted:
		if !ok {
			return ErrPeerCheck
		}
		defer c.Close()
		uid, found := owner(c)
		if !found || uid != os.Geteuid() {
			return ErrPeerCheck
		}
		return nil
	case <-time.After(2 * time.Second):
		go func() { // a connection accepted after the wait is still this process's to close
			if c, ok := <-accepted; ok {
				_ = c.Close()
			}
		}()
		return ErrPeerCheck
	}
}
