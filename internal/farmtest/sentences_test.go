package farmtest_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// INSTRUMENT: the reader on C# written here — a literal holding a ';', an escaped brace, a hole with a format, a constant —
// and its refusal to return a sentence with a hole it has no value for.
func TestTheReaderReadsCSharpAsTheFarmWritesIt(t *testing.T) {
	source := `
    /// <summary>Mentions ShortMessage before it is declared.</summary>
    public const string ShortMessage =
        "The registry is busy; nothing was applied.";

    public static string LongMessage(int max, TimeSpan since)
        => FormattableString.Invariant(
            $"At most {max} {{per}} machine, " + $"and this came {since.TotalSeconds:0.0} s after the last. "
         + "Write to \"support\".");
`
	if got, err := farmtest.Member(source, "ShortMessage", nil); err != nil || got != "The registry is busy; nothing was applied." {
		t.Fatalf("a constant holding a ';': %q %v", got, err)
	}
	got, err := farmtest.Member(source, "LongMessage", map[string]string{"max": "50", "since.TotalSeconds:0.0": "1.2"})
	if want := `At most 50 {per} machine, and this came 1.2 s after the last. Write to "support".`; err != nil || got != want {
		t.Fatalf("an expression-bodied method:\n got %q %v\nwant %q", got, err, want)
	}
	if _, err := farmtest.Member(source, "LongMessage", map[string]string{"max": "50"}); err == nil {
		t.Fatal("a hole with no value was filled with nothing — a sentence half-read passed as one")
	}
	if _, err := farmtest.Member(source, "MissingMessage", nil); err == nil {
		t.Fatal("a member that is not there was read as one")
	}
}

// What the seals drive the agent with is the farm's own words, read whole.
func TestTheFarmsSentencesAreReadWhole(t *testing.T) {
	said := farmtest.Read(t)
	for name, sentence := range map[string]string{
		"TooManyMachines": said.TooManyMachines, "TooManyDevices": said.TooManyDevices, "TooManyTunnels": said.TooManyTunnels,
		"HeartbeatInFlight": said.HeartbeatInFlight, "RegistryBusy": said.RegistryBusy, "LeftHeartbeat": said.LeftHeartbeat,
		"HeartbeatTooSoon": said.HeartbeatTooSoon(1.2), "UnknownNode": said.UnknownNode("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
	} {
		if len(sentence) < 30 || !strings.HasSuffix(sentence, ".") || strings.ContainsAny(sentence, "{}\x00") {
			t.Errorf("%s was not read whole: %q", name, sentence)
		}
	}
	if !strings.Contains(said.TooManyMachines, "10 machines") || !strings.Contains(said.HeartbeatTooSoon(1.2), "3 s apart") ||
		!strings.Contains(said.HeartbeatTooSoon(1.2), "came 1.2 s after") {
		t.Fatalf("the farm's default limits were not read into its sentences: %q / %q", said.TooManyMachines, said.HeartbeatTooSoon(1.2))
	}
}

// TEST-ONLY: no file that is built into a program imports this package.
func TestNoProgramImportsTheFarmsSentences(t *testing.T) {
	root := filepath.Join("..", "..")
	const path = "github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	importers := 0
	err := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "testdata") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(file, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			if p, _ := strconv.Unquote(spec.Path.Value); p == path {
				importers++
				if !strings.HasSuffix(file, "_test.go") {
					t.Errorf("%s imports the farm's sentences into a program — they are for the seals only", file)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if importers == 0 {
		t.Fatal("instrument: no test imports the farm's sentences — this walk is reading the wrong tree")
	}
}
