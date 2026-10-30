package builds

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// THE BOUNDS ARE THE PLATFORM'S, READ FROM ITS SOURCE. The byte cap, the file count and the unpacked total are the build
// door's own defaults, so every build the platform accepts is one this agent fetches; the disk floor is the farm's own floor
// for an unpack; and the sentences this agent shares with the farm's unpacker are the farm's, word for word.

// product evaluates a C# constant written as a product of whole numbers ("400L * 1024 * 1024", "50_000").
func product(t *testing.T, expression string) int64 {
	t.Helper()
	total := int64(1)
	for _, factor := range strings.Split(expression, "*") {
		factor = strings.TrimSuffix(strings.ReplaceAll(strings.TrimSpace(factor), "_", ""), "L")
		n, err := strconv.ParseInt(factor, 10, 64)
		if err != nil {
			t.Fatalf("%q is not a product of whole numbers", expression)
		}
		total *= n
	}
	return total
}

func constant(t *testing.T, source, name string) int64 {
	t.Helper()
	m := regexp.MustCompile(`const (?:long|int) ` + name + ` = ([0-9_L* ]+);`).FindStringSubmatch(source)
	if m == nil {
		t.Fatalf("the platform's source no longer declares %s as a number", name)
	}
	return product(t, m[1])
}

func TestTheBoundsAreThePlatformsOwn(t *testing.T) {
	data, err := os.ReadFile(farmtest.ServerSource(t, farmtest.RunnerBuildLimits))
	if err != nil {
		t.Fatal(err)
	}
	limits := string(data)
	if got := constant(t, limits, "DefaultMaxFileBytes"); got != MaxBuildBytes {
		t.Errorf("the cap on a build is the platform's (%d), not %d", got, MaxBuildBytes)
	}
	if got := constant(t, limits, "DefaultMaxZipEntries"); got != MaxEntries {
		t.Errorf("the files a build may list are the platform's (%d), not %d", got, MaxEntries)
	}
	if got := constant(t, limits, "DefaultMaxUncompressedBytes"); got != MaxUnpackedBytes {
		t.Errorf("what a build may unpack to is the platform's (%d), not %d", got, MaxUnpackedBytes)
	}

	data, err = os.ReadFile(farmtest.ServerSource(t, farmtest.FarmBoundedUnzip))
	if err != nil {
		t.Fatal(err)
	}
	unzip := string(data)
	m := regexp.MustCompile(`Default \{ get; \} = new\(([0-9_]+), ([0-9L* ]+), ([0-9L* ]+)\);`).FindStringSubmatch(unzip)
	if m == nil {
		t.Fatal("the farm's unpacker no longer declares its default limits as three numbers")
	}
	if product(t, m[1]) != MaxEntries || product(t, m[2]) != MaxUnpackedBytes || product(t, m[3]) != KeepFreeBytes {
		t.Errorf("the farm's own unpack limits are %s, %s, %s; this agent's are %d, %d, %d", m[1], m[2], m[3],
			MaxEntries, MaxUnpackedBytes, KeepFreeBytes)
	}
	// The sentences shared with the farm's unpacker, word for word (its numbers filled in as it fills them).
	for fault, name := range map[Fault]string{NotAZip: "NotAZip", RunsPastItsRecord: "RunsPastItsRecord", UnlistedEntry: "UnlistedEntry",
		NamedTwice: "NamedTwice", UnknownPacking: "UnknownPacking"} {
		m := regexp.MustCompile(`UnzipFault\.` + name + ` => "([^"]+)",`).FindStringSubmatch(unzip)
		if m == nil || m[1] != fault.Sentence() {
			t.Errorf("%s: the farm says %q, this agent %q", name, m, fault.Sentence())
		}
	}
	if !strings.Contains(unzip, `UnzipFault.TooManyEntries => $"it lists more than {limits!.MaxEntries:N0} files",`) ||
		TooManyEntries.Sentence() != "it lists more than 50,000 files" {
		t.Errorf("the count's sentence is the farm's: %q", TooManyEntries.Sentence())
	}
	if !strings.Contains(unzip, `UnzipFault.TooLarge => $"it unpacks to more than {Size(limits!.MaxUnpackedBytes)}",`) ||
		TooLarge.Sentence() != "it unpacks to more than 1 GiB" {
		t.Errorf("the total's sentence is the farm's: %q", TooLarge.Sentence())
	}
}
