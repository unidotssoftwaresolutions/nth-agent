package wda_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// THE_PORT_BELONGS_TO_THE_MACHINE_TOO — this side of one rule with two readers.
//
// The table is in testdata/wda-ports.tsv and was computed by a THIRD implementation (Python, in the slice
// report), so neither the Go nor the C# side is checked against its own output: a table one side produces is not
// a check, it is an echo. The same file, byte-identical, is in the runner.

const tablePath = "testdata/wda-ports.tsv"

type row struct {
	udid string
	port uint16
}

// tableIn reads the rule's table out of a file. THE PATH IS A PARAMETER, which is the guard: the seal below
// plants a table whose correct answer differs from the real one, so a reader that ignored its argument and
// answered about the shipped file would fail there and nowhere else.
func tableIn(t *testing.T, path string) ([]row, string, string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer file.Close()

	var rows []row
	var block strings.Builder
	claimed := ""
	digestLine := regexp.MustCompile(`^# ([0-9a-f]{64})$`)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			if m := digestLine.FindStringSubmatch(line); m != nil {
				claimed = m[1]
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			t.Fatalf("%s: a row is <UDID>\\t<port>, got %q", path, line)
		}
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("%s: %q is not a port", path, parts[1])
		}
		rows = append(rows, row{udid: parts[0], port: uint16(port)})
		block.WriteString(line + "\n")
	}
	sum := sha256.Sum256([]byte(block.String()))
	return rows, claimed, hex.EncodeToString(sum[:])
}

func TestThePortRuleAgreesWithTheSharedTable(t *testing.T) {
	rows, claimed, actual := tableIn(t, tablePath)
	if len(rows) < 5 {
		t.Fatalf("the walk must have read the real table, got %d rows", len(rows))
	}
	// THE ONE-SIDED EDIT IS CAUGHT HERE. That the two repositories' COPIES agree is not sealed across repos —
	// the report says so — but a change to this copy alone reds this, because the digest travels with the rows.
	if claimed == "" {
		t.Fatal("the table must carry the digest of its own rows")
	}
	if claimed != actual {
		t.Fatalf("the table's rows do not match the digest it carries:\n  claims %s\n  is     %s", claimed, actual)
	}

	for _, r := range rows {
		if got := wda.PortFor(r.udid); got != r.port {
			t.Fatalf("PortFor(%q) = %d, the shared table says %d", r.udid, got, r.port)
		}
	}
}

// THE RULE NORMALISES BEFORE IT HASHES: one phone, one port, however its UDID was spelled by whatever read it.
func TestTheSamePhoneGetsTheSamePortHoweverItsUdidWasSpelled(t *testing.T) {
	const malta = "00008140-0000C0FFEE0000A1"
	want := wda.PortFor(malta)
	for _, spelling := range []string{
		"00008140-0000c0ffee0000a1",
		"  00008140-0000C0FFEE0000A1  ",
		"\t00008140-0000C0FFEE0000A1\n",
	} {
		if got := wda.PortFor(spelling); got != want {
			t.Fatalf("PortFor(%q) = %d, want %d — a phone must not get two ports because two readers spelled it differently", spelling, got, want)
		}
	}
}

// THE RANGE IS PART OF THE RULE. Below macOS's ephemeral range so the OS never hands the number out behind our
// back, and above the crowded registered ports.
func TestEveryDerivedPortIsInTheStatedRange(t *testing.T) {
	for i := 0; i < 5000; i++ {
		udid := "0000" + strconv.Itoa(i) + "-000164E80E98801C"
		port := wda.PortFor(udid)
		if port < 20000 || port > 29999 {
			t.Fatalf("PortFor(%q) = %d, outside the stated 20000-29999", udid, port)
		}
	}
	// …and it is NOT Appium's default, which is the value this rule replaced.
	if wda.PortFor("00008140-0000C0FFEE0000A1") == wda.AppiumDefaultAgentPort {
		t.Fatal("the derived port must not be the library constant the probe used to assume")
	}
}

// THE INSTRUMENT GUARD: the reader reads the table it is given.
func TestTheTableReaderReadsTheFileItIsGiven(t *testing.T) {
	dir := t.TempDir()
	planted := dir + "/planted.tsv"
	// A row whose port is deliberately NOT what the rule produces, and a digest that matches these rows.
	rowsBlock := "PLANTED-UDID\t12345\n"
	sum := sha256.Sum256([]byte(rowsBlock))
	if err := os.WriteFile(planted, []byte("# planted\n# "+hex.EncodeToString(sum[:])+"\n"+rowsBlock), 0o600); err != nil {
		t.Fatal(err)
	}

	rows, claimed, actual := tableIn(t, planted)
	if len(rows) != 1 || rows[0].udid != "PLANTED-UDID" || rows[0].port != 12345 {
		t.Fatalf("the reader must read the table it is given, got %v", rows)
	}
	if claimed != actual {
		t.Fatalf("the planted digest must verify against the planted rows")
	}
	// …and ONLY that one: a reader answering about the shipped table would find Malta here.
	for _, r := range rows {
		if strings.HasPrefix(r.udid, "00008140") {
			t.Fatalf("the reader returned a shipped row from a planted file: %v", rows)
		}
	}
	// The planted port is not what the rule says, which is what makes the seal above capable of failing.
	if wda.PortFor("PLANTED-UDID") == 12345 {
		t.Fatal("the planted row must disagree with the rule, or the guard proves nothing")
	}
}

// ── THE MACHINE PICKS, AND PICKS ONE IT HAS CHECKED ──────────────────────────────────────────────────────────

func TestTheDerivedPortIsDeclaredWhenNothingHoldsIt(t *testing.T) {
	const malta = "00008140-0000C0FFEE0000A1"
	chooser := &wda.PortChooser{
		Probe: func(context.Context, uint16) wda.RunnerStatus { return wda.RunnerStatus{State: wda.RunnerNotListening} },
	}
	if got := chooser.For(context.Background(), malta); got != wda.PortFor(malta) {
		t.Fatalf("a free derived port is the one to declare — stability across restarts is free, got %d want %d", got, wda.PortFor(malta))
	}
}

// THE GIVE-WAY. Which ports are free on this Mac is a fact only this Mac holds, and the rule asks it nothing —
// so the derived number is a PREFERENCE. When a stranger holds it, the machine picks another and declares that.
func TestTheDerivedPortGivesWayWhenSomethingElseHoldsIt(t *testing.T) {
	const malta = "00008140-0000C0FFEE0000A1"
	derived := wda.PortFor(malta)
	chooser := &wda.PortChooser{
		Probe: func(_ context.Context, port uint16) wda.RunnerStatus {
			if port == derived {
				return wda.RunnerStatus{State: wda.RunnerUnknown, Reason: "something else answered"}
			}
			return wda.RunnerStatus{State: wda.RunnerNotListening}
		},
	}

	got := chooser.For(context.Background(), malta)
	if got == derived {
		t.Fatalf("the derived port is held — declaring it anyway is a run that cannot start, got %d", got)
	}
	if got < 20000 || got > 29999 {
		t.Fatalf("the replacement must stay in the stated range, got %d", got)
	}
	// …and the choice is REMEMBERED, so the registration and the probe cannot disagree about the number.
	if again := chooser.For(context.Background(), malta); again != got {
		t.Fatalf("one process, one answer per phone: %d then %d", got, again)
	}
}

// WEBDRIVERAGENT ITSELF ON THE PORT IS NOT A COLLISION. It is ours, from the last run — the very thing the idle
// stop exists to clean up — and giving way to it would move the phone's port every time we looked at it.
func TestWebDriverAgentOnTheDerivedPortIsNotAReasonToMove(t *testing.T) {
	const malta = "00008140-0000C0FFEE0000A1"
	for _, state := range []wda.RunnerState{wda.RunnerIdle, wda.RunnerInUse} {
		chooser := &wda.PortChooser{
			Probe: func(context.Context, uint16) wda.RunnerStatus { return wda.RunnerStatus{State: state} },
		}
		if got := chooser.For(context.Background(), malta); got != wda.PortFor(malta) {
			t.Fatalf("%v is ours, not a stranger — got %d, want the derived %d", state, got, wda.PortFor(malta))
		}
	}
}

// TWO PHONES ON ONE MACHINE GET TWO PORTS, and neither reads the other's. THIS IS THE SEAL THAT WOULD HAVE CAUGHT
// A NODE-LEVEL SEAT: a machine with four iPhones signs them all the same way and cannot give them one port.
func TestTwoPhonesOnOneMachineGetTwoPorts(t *testing.T) {
	chooser := &wda.PortChooser{
		Probe: func(context.Context, uint16) wda.RunnerStatus { return wda.RunnerStatus{State: wda.RunnerNotListening} },
	}
	malta := chooser.For(context.Background(), "00008140-0000C0FFEE0000A1")
	other := chooser.For(context.Background(), "00008110-0000C0FFEE0000B2")

	if malta == other {
		t.Fatalf("two phones on one machine must not be handed one port, both got %d", malta)
	}
}

// A BUILD THAT CANNOT ASK DECLARES THE DERIVED PORT UNCHECKED — honest, because a check that cannot run must not
// pretend to have run.
func TestWithNoWayToAskTheDerivedPortIsDeclaredUnchecked(t *testing.T) {
	const malta = "00008140-0000C0FFEE0000A1"
	chooser := &wda.PortChooser{}
	if got := chooser.For(context.Background(), malta); got != wda.PortFor(malta) {
		t.Fatalf("with no probe the rule is all there is, got %d", got)
	}
}
