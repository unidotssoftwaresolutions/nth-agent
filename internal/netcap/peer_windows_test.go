//go:build windows

package netcap

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE OWNER LOOKUP, ASKED OF WINDOWS ITSELF (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): a listener this test opens, a
// connection this test makes to it, and the real GetExtendedTcpTable and token reads. It runs in CI's windows-latest job.
func TestWindowsNamesThisAccountBehindItsOwnLoopbackConnection(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err == nil {
			accepted <- c
		}
		close(accepted)
	}()
	dialed, err := net.DialTimeout("tcp4", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()
	server, ok := <-accepted
	if !ok {
		t.Fatal("the listener accepted nothing")
	}
	defer server.Close()

	if uid, found := OwnerOfPeer(server); !found || uid != ThisAccount() {
		t.Fatalf("the connecting end is this process's: got uid %d found %v, want %d", uid, found, ThisAccount())
	}
	if uid, found := OwnerOfListenerBehind(dialed); !found || uid != ThisAccount() {
		t.Fatalf("the listener behind a dialled connection is this process's: got uid %d found %v", uid, found)
	}
	if err := PeerCheckWorks(); err != nil {
		t.Fatalf("the peer check must work on Windows: %v", err)
	}
	pid, program, found := ListenerProcess(listener.Addr().(*net.TCPAddr).Port)
	self, _ := os.Executable()
	if !found || pid != os.Getpid() || !strings.EqualFold(program, filepath.Base(self)) {
		t.Fatalf("the program on the test's port is this test (%d, %s): got %d %q %v", os.Getpid(), filepath.Base(self), pid, program, found)
	}
}
