package netcap

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── WHO MAY USE A CAPTURE PROXY: THIS ACCOUNT, AND NO OTHER ACCOUNT ON THE MACHINE (AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE rule 2)

var (
	theProxy  = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 47001} // the capture proxy's own end
	itsClient = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 41000} // the connecting end: adb's server, or another account
)

// procNetRow is one /proc/net/tcp row as a little-endian Linux kernel prints it.
func procNetRow(slot int, local, remote, state string, uid int) string {
	return procNetRowOwnedBy(slot, local, remote, state, strconv.Itoa(uid))
}

func procNetRowOwnedBy(slot int, local, remote, state, uid string) string {
	return "   " + strconv.Itoa(slot) + ": " + local + " " + remote + " " + state +
		" 00000000:00000000 00:00000000 00000000  " + uid + "        0 " + strconv.Itoa(1000+slot) + " 1 0000000000000000 20 4 30 10 -1"
}

const procNetHeading = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"

// 127.0.0.1 is 0100007F on a little-endian kernel; 41000 is A028 and 47001 is B799.
const (
	clientEnd = "0100007F:A028"
	proxyEnd  = "0100007F:B799"
)

func TestTheLinuxTableNamesTheAccountThatOpenedTheConnection(t *testing.T) {
	for _, c := range []struct {
		name      string
		rows      []string
		wantUID   int
		wantFound bool
	}{
		{"another account's connection", []string{
			procNetRow(0, proxyEnd, "00000000:0000", "0A", 501),  // the proxy's own listener
			procNetRow(1, proxyEnd, clientEnd, "01", 501),        // the proxy's accepted end — not the one asked about
			procNetRow(2, clientEnd, proxyEnd, "01", 1002),       // the connecting end, opened by uid 1002
			procNetRow(3, "0100007F:A029", proxyEnd, "01", 1003), // a different connection to the same proxy
		}, 1002, true},
		{"this account's connection", []string{procNetRow(0, clientEnd, proxyEnd, "01", 501)}, 501, true},
		{"the administrator's", []string{procNetRow(0, clientEnd, proxyEnd, "01", 0)}, 0, true},
		// The kernel keeps a TIME_WAIT row with no owner (uid 0) after its socket is gone; it can share the live
		// connection's four addresses, and reading it would let any account through as root.
		{"a TIME_WAIT row is never the answer", []string{
			procNetRow(0, clientEnd, proxyEnd, "06", 0),
			procNetRow(1, clientEnd, proxyEnd, "01", 1002),
		}, 1002, true},
		{"only a TIME_WAIT row", []string{procNetRow(0, clientEnd, proxyEnd, "06", 0)}, 0, false},
		{"two live rows that disagree", []string{
			procNetRow(0, clientEnd, proxyEnd, "01", 501),
			procNetRow(1, clientEnd, proxyEnd, "01", 1002),
		}, 0, false},
		{"no such connection", []string{procNetRow(0, "0100007F:A029", proxyEnd, "01", 1002)}, 0, false},
		{"the same ports on another address", []string{procNetRow(0, "0200007F:A028", proxyEnd, "01", 1002)}, 0, false},
		{"a uid that is not a number", []string{procNetRowOwnedBy(0, clientEnd, proxyEnd, "01", "x")}, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			table := procNetHeading + "\n" + strings.Join(c.rows, "\n") + "\n"
			uid, found := uidInProcNet([]byte(table), itsClient, theProxy, binary.LittleEndian)
			if uid != c.wantUID || found != c.wantFound {
				t.Fatalf("got uid %d found %v, want uid %d found %v", uid, found, c.wantUID, c.wantFound)
			}
		})
	}
}

func TestTheLinuxTcp6TableNamesADualStackSocket(t *testing.T) {
	// A client may connect to 127.0.0.1 from an IPv6 socket: /proc/net/tcp6 lists it as ::ffff:127.0.0.1, four words.
	mapped := "0000000000000000FFFF00000100007F"
	table := procNetHeading + "\n" + procNetRow(0, mapped+":A028", mapped+":B799", "01", 1002) + "\n"
	if uid, found := uidInProcNet([]byte(table), itsClient, theProxy, binary.LittleEndian); uid != 1002 || !found {
		t.Fatalf("got uid %d found %v", uid, found)
	}
}

// pcbRecord is one struct xtcpcb64 as the pcblist64 sysctl answers it, filled at the offsets peer_pcblist.go reads.
func pcbRecord(local, remote *net.TCPAddr, uid uint32, state uint32, socket bool) []byte {
	record := make([]byte, 472)
	binary.LittleEndian.PutUint32(record[pcbLength:], 472)
	binary.BigEndian.PutUint16(record[pcbFport:], uint16(remote.Port))
	binary.BigEndian.PutUint16(record[pcbLport:], uint16(local.Port))
	record[pcbVflag] = inpIPv4
	copy(record[pcbFaddr4:], remote.IP.To4())
	copy(record[pcbLaddr4:], local.IP.To4())
	if socket {
		binary.LittleEndian.PutUint32(record[pcbSockLen:], 108)
	}
	binary.LittleEndian.PutUint32(record[pcbProtocol:], ipprotoTCP)
	binary.LittleEndian.PutUint32(record[pcbUID:], uid)
	binary.LittleEndian.PutUint32(record[pcbState:], state)
	return record
}

func pcbList(records ...[]byte) []byte {
	xinpgen := func() []byte {
		b := make([]byte, xinpgenLength)
		binary.LittleEndian.PutUint32(b, xinpgenLength)
		binary.LittleEndian.PutUint32(b[4:], uint32(len(records)))
		return b
	}
	list := xinpgen()
	for _, r := range records {
		list = append(list, r...)
	}
	return append(list, xinpgen()...)
}

func TestTheMacListNamesTheAccountThatOpenedTheConnection(t *testing.T) {
	const established = 4
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 47001}
	anywhere := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0}
	other := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 41001}
	for _, c := range []struct {
		name      string
		list      []byte
		wantUID   int
		wantFound bool
	}{
		{"another account's connection", pcbList(
			pcbRecord(listener, anywhere, 501, 1, true),             // the proxy's listener
			pcbRecord(theProxy, itsClient, 501, established, true),  // the proxy's accepted end
			pcbRecord(itsClient, theProxy, 1002, established, true), // the connecting end
			pcbRecord(other, theProxy, 1003, established, true),     // another connection to the proxy
		), 1002, true},
		{"this account's connection", pcbList(pcbRecord(itsClient, theProxy, 501, established, true)), 501, true},
		{"a TIME_WAIT record is never the answer", pcbList(
			pcbRecord(itsClient, theProxy, 0, tcpsTimeWait, true),
			pcbRecord(itsClient, theProxy, 1002, established, true),
		), 1002, true},
		{"a record whose socket is gone is never the answer", pcbList(pcbRecord(itsClient, theProxy, 0, established, false)), 0, false},
		{"two live records that disagree", pcbList(
			pcbRecord(itsClient, theProxy, 501, established, true),
			pcbRecord(itsClient, theProxy, 1002, established, true),
		), 0, false},
		{"no such connection", pcbList(pcbRecord(other, theProxy, 1002, established, true)), 0, false},
		{"a record shorter than the struct this reads", func() []byte {
			short := pcbRecord(itsClient, theProxy, 1002, established, true)[:200]
			binary.LittleEndian.PutUint32(short, 200)
			return pcbList(short)
		}(), 0, false},
		{"a list cut short", pcbList(pcbRecord(itsClient, theProxy, 1002, established, true))[:300], 0, false},
		{"nothing at all", nil, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			uid, found := uidInPCBList(c.list, itsClient, theProxy)
			if uid != c.wantUID || found != c.wantFound {
				t.Fatalf("got uid %d found %v, want uid %d found %v", uid, found, c.wantUID, c.wantFound)
			}
		})
	}
}

func TestOnlyThisAccountAndTheAdministratorMayUseTheProxy(t *testing.T) {
	if !MayUseTheProxy(os.Geteuid()) || !MayUseTheProxy(0) {
		t.Fatal("this account and the administrator may use the proxy")
	}
	if MayUseTheProxy(os.Geteuid()+1) || MayUseTheProxy(-1) {
		t.Fatal("no other account may")
	}
}

// supportedHere says whether this system's kernel is asked (capture runs on macOS and Linux only).
func supportedHere() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" }

// THE REAL KERNEL, ON THE MACHINE THE TESTS RUN ON: a connection this process opens is found, as this account — from both
// of its ends. This is what proves the measured offsets (macOS) and the table's format (Linux) against the kernel itself.
func TestThisMachineNamesItsOwnConnection(t *testing.T) {
	if !supportedHere() {
		t.Skipf("capture does not run on %s", runtime.GOOS)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := listener.Accept(); err == nil {
			accepted <- c
		}
	}()
	dialed, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()
	server := <-accepted
	defer server.Close()
	for name, c := range map[string]net.Conn{"the accepted end": server, "the dialled end": dialed} {
		if uid, found := OwnerOfPeer(c); !found || uid != os.Geteuid() {
			t.Fatalf("%s: got uid %d found %v, want this account (%d)", name, uid, found, os.Geteuid())
		}
	}
	if err := PeerCheckWorks(); err != nil {
		t.Fatalf("the check must prove itself on this machine: %v", err)
	}
}

func TestACheckThatCannotFindItsOwnConnectionDoesNotWork(t *testing.T) {
	for name, owner := range map[string]PeerOwner{
		"the kernel could not be asked": func(net.Conn) (int, bool) { return 0, false },
		"it names another account":      func(net.Conn) (int, bool) { return os.Geteuid() + 1, true },
		"it names the administrator":    func(net.Conn) (int, bool) { return 0, os.Geteuid() != 0 },
	} {
		if err := peerCheckWorks(owner); !errors.Is(err, ErrPeerCheck) {
			t.Errorf("%s: the check must not pass, got %v", name, err)
		}
	}
	if err := peerCheckWorks(func(net.Conn) (int, bool) { return os.Geteuid(), true }); err != nil {
		t.Fatalf("a check that finds this account works: %v", err)
	}
}

// THE PROXY ITSELF: a connection another account opened is closed before a byte of it is read, said once to whoever
// listens, and never reaches an upstream or the record; this account's and the administrator's are served.
func TestTheProxyRefusesAnotherAccountsConnectionUnread(t *testing.T) {
	var served sync.WaitGroup
	upstreamHits := 0
	var mu sync.Mutex
	upstream := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		upstreamHits++
		mu.Unlock()
		_, _ = io.WriteString(w, "hello")
	})}
	upstreamListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served.Add(1)
	go func() { defer served.Done(); _ = upstream.Serve(upstreamListener) }()
	defer func() { _ = upstream.Close(); served.Wait() }()

	for _, c := range []struct {
		name    string
		uid     int
		found   bool
		allowed bool
	}{
		{"another account", os.Geteuid() + 1, true, false},
		{"an account that could not be found", 0, false, false},
		{"this account", os.Geteuid(), true, true},
		{"the administrator", 0, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var refused []string
			var refusedMu sync.Mutex
			proxy := NewProxy(Options{
				// The upstream is this machine's own test server, so what --capture-allow-local-network opens is open here.
				ReachLocal: true,
				PeerOwner:  func(net.Conn) (int, bool) { return c.uid, c.found },
				Refused: func(uid int, found bool) {
					refusedMu.Lock()
					defer refusedMu.Unlock()
					refused = append(refused, strconv.Itoa(uid)+map[bool]string{true: " found", false: " not found"}[found])
				},
			})
			address, err := proxy.Listen(0)
			if err != nil {
				t.Fatal(err)
			}
			go proxy.Serve()
			defer proxy.Close()

			mu.Lock()
			before := upstreamHits
			mu.Unlock()
			proxyURL, _ := url.Parse("http://" + address)
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
			request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+upstreamListener.Addr().String()+"/", nil)
			response, err := client.Do(request)
			mu.Lock()
			after := upstreamHits
			mu.Unlock()
			refusedMu.Lock()
			defer refusedMu.Unlock()
			if c.allowed {
				if err != nil {
					t.Fatalf("this connection may use the proxy: %v", err)
				}
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if string(body) != "hello" || after != before+1 || len(refused) != 0 {
					t.Fatalf("served %q, upstream hits %d→%d, refused %v", body, before, after, refused)
				}
				return
			}
			if err == nil {
				response.Body.Close()
				t.Fatalf("another account's connection was served (status %d)", response.StatusCode)
			}
			if after != before {
				t.Fatal("a refused connection reached the upstream")
			}
			if len(refused) != 1 || !strings.HasSuffix(refused[0], map[bool]string{true: " found", false: " not found"}[c.found]) {
				t.Fatalf("the refusal must be heard once, as it was found: %v", refused)
			}
		})
	}
}
