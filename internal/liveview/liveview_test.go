package liveview_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// ── 1 · THE RULE IS THE NODE'S, ON THE SHARED VECTORS ────────────────────────────────────────────────────────────

// sharedTable reads wda-ports.tsv — the rule in words, and a table a THIRD implementation computed — checking the
// digest its own header claims, so a table edited by hand cannot pass as the shared one. The farm reads the same file
// byte for byte (APhoneTheRackOffersAndCannotDriveTests), and this slice does not touch it.
func sharedTable(t *testing.T) map[string]uint16 {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "wda", "testdata", "wda-ports.tsv"))
	if err != nil {
		t.Fatalf("the shared port table: %v", err)
	}
	defer file.Close()
	rows := map[string]uint16{}
	var claimed string
	var block strings.Builder
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			if text := strings.TrimSpace(strings.TrimPrefix(line, "#")); len(text) == 64 && !strings.Contains(text, " ") {
				claimed = text
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		port, err := strconv.Atoi(parts[1])
		if len(parts) != 2 || err != nil {
			t.Fatalf("a row that is not <UDID>\\t<port>: %q", line)
		}
		rows[parts[0]] = uint16(port)
		block.WriteString(line + "\n")
	}
	sum := sha256.Sum256([]byte(block.String()))
	if hex.EncodeToString(sum[:]) != claimed || len(rows) != 6 {
		t.Fatalf("the shared table is not the one its header describes: %d rows, digest %x, claimed %s", len(rows), sum, claimed)
	}
	return rows
}

func TestTheMirrorRuleIsTheNodesOnTheSharedVectors(t *testing.T) {
	// The farm's rack node's MirrorPortChooser.PortFor = 30000 + (WdaPortChooser.PortFor - 20000): the shared table's own
	// answer moved one decade up. Every row, by the table's number, not by this package's own wda.PortFor.
	for udid, wdaPort := range sharedTable(t) {
		if got, want := liveview.PortFor(udid), wdaPort+10000; got != want {
			t.Fatalf("UDID %q: the mirror port is %d, and the node's rule on the shared table gives %d", udid, got, want)
		}
	}
	// …and the node's own seal's UDIDs (AMirrorPortTheCommentGotWrongTests): the same offset in both decades.
	for _, udid := range []string{"C0FFEE51-0000-4000-8000-000000000001", "a", "00008140-0000C0FFEE0000A1"} {
		if liveview.PortFor(udid)-liveview.PortBase != wda.PortFor(udid)-wda.PortBase {
			t.Fatalf("UDID %q has a different offset in the two decades — that is a second hash, not the shared rule", udid)
		}
	}
	// The decades are disjoint and below macOS's ephemeral range, as the node states them.
	if uint32(liveview.PortBase) < uint32(wda.PortBase)+wda.PortSpan || uint32(liveview.PortBase)+liveview.PortSpan > 49152 {
		t.Fatalf("the mirror decade %d+%d overlaps WebDriverAgent's or the ephemeral range", liveview.PortBase, liveview.PortSpan)
	}
}

// ── 2 · CHECKED FREE, AND NOTHING WHEN NOTHING IS ───────────────────────────────────────────────────────────────

func TestAMachineThatCanReserveNothingDeclaresNothing(t *testing.T) {
	var asked []uint16
	chooser := &liveview.Chooser{Free: func(port uint16) bool { asked = append(asked, port); return false }}
	got := chooser.For("C0FFEE51-0000-4000-8000-000000000001")
	if got.Reserved {
		t.Fatalf("a machine with every candidate held reserved %d — a number it knows is taken", got.Port)
	}
	if len(asked) != liveview.DefaultTries || got.Tried != liveview.DefaultTries {
		t.Fatalf("the walk looked %d times and says %d, not the node's %d", len(asked), got.Tried, liveview.DefaultTries)
	}
	for _, port := range asked {
		if port < liveview.PortBase || uint32(port) >= uint32(liveview.PortBase)+liveview.PortSpan {
			t.Fatalf("the walk considered %d, outside its own decade", port)
		}
	}
	for _, blank := range []string{"", "   "} {
		if r := chooser.For(blank); r.Reserved || r.Tried != 0 {
			t.Fatalf("a blank UDID reserved something: %+v", r)
		}
	}
}

func TestTheWalkTakesTheNextFreeCandidateWrapsInsideTheDecadeAndRemembersIt(t *testing.T) {
	// A UDID whose derived port is the last of the decade, so the walk must wrap to 30000.
	var udid string
	for i := 0; i < 200000 && udid == ""; i++ {
		if candidate := "PHONE-" + strconv.Itoa(i); liveview.PortFor(candidate) == 39999 {
			udid = candidate
		}
	}
	if udid == "" {
		t.Fatal("no UDID derives 39999 in the search — widen it")
	}
	calls := 0
	chooser := &liveview.Chooser{Free: func(port uint16) bool { calls++; return port == 30000 }}
	first := chooser.For(udid)
	if !first.Reserved || first.Port != 30000 || first.Derived != 39999 || first.Tried != 2 {
		t.Fatalf("39999 held, 30000 free: the walk must wrap to 30000 on its second candidate, got %+v", first)
	}
	again := chooser.For(udid)
	if again != first || calls != 2 {
		t.Fatalf("the answer is remembered for the life of the process: %+v then %+v after %d checks", first, again, calls)
	}
}

func TestTheRealCheckSeesAPortSomethingHolds(t *testing.T) {
	held, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(held.Addr().(*net.TCPAddr).Port)
	if liveview.LoopbackFree(port) {
		t.Fatalf("127.0.0.1:%d is held and the check called it free", port)
	}
	held.Close()
	if !liveview.LoopbackFree(port) {
		t.Fatalf("127.0.0.1:%d was released and the check still calls it held", port)
	}
}

// ── 3 · AVAILABLE FROM FACTS, AND WHY NOT IN PLAIN WORDS ────────────────────────────────────────────────────────

func drivers(list string) liveview.Drivers {
	return liveview.Drivers{Read: true, Installed: liveview.ParseDriverList(list)}
}

func TestEachPhonesLiveViewIsJudgedFromFacts(t *testing.T) {
	reserved := liveview.Reservation{Reserved: true, Port: 35118, Derived: 35118, Tried: 1}
	nothingFree := liveview.Reservation{Derived: 39990, Tried: 32}
	both := drivers("- uiautomator2@7.5.2 [installed (npm)]\n- xcuitest@11.9.0 [installed (npm)]")
	unread := liveview.Drivers{Why: "Appium is not on PATH here, so its driver list could not be read"}

	cases := []struct {
		name      string
		facts     liveview.Facts
		available bool
		says      []string
	}{
		{"android, driver and port", liveview.Facts{Platform: inventory.Android, Reservation: reserved, Drivers: both}, true, []string{"127.0.0.1:35118"}},
		{"ios, 11.9.0 and port", liveview.Facts{Platform: inventory.IOS, Reservation: reserved, Drivers: both}, true, []string{"127.0.0.1:35118"}},
		{"no port free", liveview.Facts{Platform: inventory.Android, Reservation: nothingFree, Drivers: both}, false,
			[]string{"no mirror port was free", "32 candidates", "39990 to 30021", "wrapping within 30000-39999"}},
		{"uiautomator2 missing", liveview.Facts{Platform: inventory.Android, Reservation: reserved, Drivers: drivers("- xcuitest@11.9.0 [installed (npm)]")}, false,
			[]string{"uiautomator2 driver is not installed"}},
		{"xcuitest before the forward", liveview.Facts{Platform: inventory.IOS, Reservation: reserved, Drivers: drivers("- xcuitest@8.0.9 [installed (npm)]")}, false,
			[]string{"xcuitest driver 8.0.9 does not forward", "8.1.0 and later do"}},
		{"xcuitest at the forward", liveview.Facts{Platform: inventory.IOS, Reservation: reserved, Drivers: drivers("- xcuitest@8.1.0 [installed (npm)]")}, true, nil},
		{"unknown platform", liveview.Facts{Platform: inventory.Platform("tizen"), Reservation: reserved, Drivers: both}, false, []string{"no screen server for a tizen device"}},
	}
	for _, c := range cases {
		judged := liveview.Judge(c.facts)
		if judged.Available != c.available {
			t.Fatalf("%s: available=%v, want %v (%+v)", c.name, judged.Available, c.available, judged)
		}
		for _, want := range c.says {
			if !strings.Contains(judged.Reason, want) {
				t.Fatalf("%s: the reason must say %q, got %q", c.name, want, judged.Reason)
			}
		}
		if judged.Available && judged.Port != c.facts.Reservation.Port {
			t.Fatalf("%s: available on %d, but %d was reserved", c.name, judged.Port, c.facts.Reservation.Port)
		}
		if !judged.Available && judged.Port != 0 {
			t.Fatalf("%s: a phone with no live view carries port %d", c.name, judged.Port)
		}
	}

	// AN UNREAD DRIVER LIST IS NEITHER ANSWER: available on the port, and the line says what was not checked.
	judged := liveview.Judge(liveview.Facts{Platform: inventory.IOS, Reservation: reserved, Drivers: unread})
	if !judged.Available || !strings.Contains(judged.Unchecked, "not on PATH") || !strings.Contains(judged.Line(), "was not checked") {
		t.Fatalf("an unread driver list must leave the port's answer standing and say what it did not check: %+v / %q", judged, judged.Line())
	}
	// …and every "no" a person can act on carries the action, in the one line.
	no := liveview.Judge(liveview.Facts{Platform: inventory.Android, Reservation: nothingFree, Drivers: both})
	if no.Remedy == "" || !strings.Contains(no.Line(), "what to do: free 39990") {
		t.Fatalf("a held port names what to free: %q", no.Line())
	}
}

func TestAppiumsDriverListIsReadAsAppiumPrintsIt(t *testing.T) {
	// The shape the operator's Mac printed (the doctor's own fixture), with the colour codes a terminal gets and the
	// spinner line Appium prints first.
	escape := string(rune(0x1b))
	out := "✔ Listing installed drivers\n- " + escape + "[33muiautomator2" + escape + "[39m@7.5.2 [installed (npm)]\n- xcuitest@11.9.0 [installed (npm)]\n"
	installed := liveview.ParseDriverList(out)
	if installed["uiautomator2"].Version() != "7.5.2" || installed["xcuitest"].Version() != "11.9.0" || len(installed) != 2 {
		t.Fatalf("the list read as %+v", installed)
	}

	look := func(string) (string, error) { return "/usr/local/bin/appium", nil }
	ran := ""
	read := liveview.ReadDrivers(context.Background(), look, func(_ context.Context, name string, args ...string) (string, error) {
		ran = name + " " + strings.Join(args, " ")
		return out, nil
	})
	if !read.Read || ran != "appium driver list --installed" {
		t.Fatalf("the read runs Appium's own list and nothing else: %q %+v", ran, read)
	}
	missing := liveview.ReadDrivers(context.Background(), func(string) (string, error) { return "", errors.New("not found") }, nil)
	if missing.Read || !strings.Contains(missing.Why, "not on PATH") {
		t.Fatalf("no Appium on PATH is an unread list, said so: %+v", missing)
	}
	failed := liveview.ReadDrivers(context.Background(), look, func(context.Context, string, ...string) (string, error) {
		return "Error: something broke\n", errors.New("exit status 1")
	})
	if failed.Read || !strings.Contains(failed.Why, "Error: something broke") {
		t.Fatalf("a list that could not be read says Appium's own first line: %+v", failed)
	}
}

// ── 4 · WHAT IS ON OFFER, AND WHAT GOING AWAY MEANS ─────────────────────────────────────────────────────────────

func TestAPhoneNoLongerOfferedIsGoneAndItsPortIsNoMirror(t *testing.T) {
	offered := &liveview.Offered{}
	offered.Offer(map[string]uint16{"R5CT30ABCDE": 35118, "00008030-HEALTHY": 31002})
	phone, gone, ok := offered.Mirror(35118)
	if !ok || phone != "R5CT30ABCDE" {
		t.Fatalf("an offered mirror is found by its port: %q %v", phone, ok)
	}
	_, stays, _ := offered.Mirror(31002)

	offered.Offer(map[string]uint16{"00008030-HEALTHY": 31002})
	select {
	case <-gone:
	default:
		t.Fatal("a phone no longer offered must be GONE — its streams end on this channel")
	}
	select {
	case <-stays:
		t.Fatal("a phone still offered on the same port is not gone")
	default:
	}
	if _, _, ok := offered.Mirror(35118); ok {
		t.Fatal("the port of a phone that went away is no mirror any more")
	}

	// A phone that comes back on a DIFFERENT port is a different mirror: the old one is gone.
	offered.Offer(map[string]uint16{"00008030-HEALTHY": 31003})
	select {
	case <-stays:
	default:
		t.Fatal("a phone whose mirror moved must end the streams of the old port")
	}
	if offered.Count() != 1 {
		t.Fatalf("one phone, one mirror: %d", offered.Count())
	}
}

func TestTwoPhonesWhoseHashesMeetAreNeverGivenOnePort(t *testing.T) {
	// No mirror server listens until a session asks for one, so the loopback check cannot see a port this machine
	// already gave another phone. Two UDIDs whose rule answers meet would both be told P — on iOS the second session
	// fails to start, on Android the second adb forward takes the first phone's screen. (The review's finding.)
	seen := map[uint16]string{}
	var first, second string
	for i := 0; i < 100000 && second == ""; i++ {
		udid := "PHONE-" + strconv.Itoa(i)
		port := liveview.PortFor(udid)
		if other, met := seen[port]; met {
			first, second = other, udid
		}
		seen[port] = udid
	}
	if second == "" {
		t.Fatal("no two UDIDs met in the search — widen it")
	}
	chooser := &liveview.Chooser{Free: func(uint16) bool { return true }} // everything free, as on a quiet machine
	a, b := chooser.For(first), chooser.For(second)
	if !a.Reserved || !b.Reserved || a.Port == b.Port {
		t.Fatalf("%s and %s both derive %d and were given %d and %d", first, second, liveview.PortFor(first), a.Port, b.Port)
	}
	if a.Port != liveview.PortFor(first) || b.Port != a.Port+1 {
		t.Fatalf("the first keeps the rule's port and the second walks to the next: %d, %d", a.Port, b.Port)
	}
}
