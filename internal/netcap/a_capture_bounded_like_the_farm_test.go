package netcap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// A CAPTURE BOUNDED AS THE FARM'S OWN NODE BOUNDS ONE, and as the runner reads it. The capture keeps the LAST 500 requests
// (the runner shows the last 500 too), cuts a header value at 16,384 characters and an address at 4,000 with the runner's
// own marker, and, when it left requests out, says so in the HAR's log.comment in the one sentence the runner believes:
//
//	This app made N requests; the capture keeps the last K, so the first M are not in it.
//
// The runner takes only the three numbers, and only when the sentence is exactly that: digits, K and M above zero, N = K + M,
// and no more entries in the file than K. A capture under its bound carries no comment at all.

// theSentence is the comment, written out by hand: a change to the writer's sentence is a change to this line too.
func theSentence(made, kept int) string {
	return "This app made " + strconv.Itoa(made) + " requests; the capture keeps the last " + strconv.Itoa(kept) +
		", so the first " + strconv.Itoa(made-kept) + " are not in it."
}

// runnerBelieves is the runner's reading of a capture's log.comment, ported rule for rule: the number of requests the app
// made, or false when the runner would read the capture as if it had no comment.
func runnerBelieves(comment string, held int) (int64, bool) {
	const made, keeps, first, notInIt = "This app made ", " requests; the capture keeps the last ", ", so the first ", " are not in it."
	if !strings.HasPrefix(comment, made) || !strings.HasSuffix(comment, notInIt) || len(comment) < len(made)+len(notInIt) {
		return 0, false
	}
	body := comment[len(made) : len(comment)-len(notInIt)]
	k := strings.Index(body, keeps)
	if k < 0 {
		return 0, false
	}
	f := strings.Index(body[k+len(keeps):], first)
	if f < 0 {
		return 0, false
	}
	f += k + len(keeps)
	count := func(s string) (int64, bool) {
		if len(s) < 1 || len(s) > 9 {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
	n, ok1 := count(body[:k])
	kept, ok2 := count(body[k+len(keeps) : f])
	m, ok3 := count(body[f+len(first):])
	if !ok1 || !ok2 || !ok3 || kept <= 0 || m <= 0 || n != kept+m || int64(held) > kept {
		return 0, false
	}
	return n, true
}

// logOf is the HAR's log object, raw: whether it carries a comment at all is part of what is sealed.
func logOf(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Log map[string]json.RawMessage `json:"log"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the capture file must be a whole HAR at every moment: %v", err)
	}
	return doc.Log
}

func newRec(t *testing.T) *Recording {
	t.Helper()
	rec, err := NewRecording(filepath.Join(t.TempDir(), "files"), lease1, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.Close)
	return rec
}

func addressOf(entry map[string]any) string { return entry["request"].(map[string]any)["url"].(string) }

// THE LAST 500, and the sentence the runner believes.
func TestACaptureKeepsTheLast500AndSaysHowManyItLeftOut(t *testing.T) {
	for _, made := range []int{501, 520, 1234} {
		rec := newRec(t)
		for i := 0; i < made; i++ {
			rec.Record(Flow{Method: "GET", URL: fmt.Sprintf("https://host.example/item/%d", i), Status: 200})
		}
		data, err := rec.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		entries, comment := readHAR(t, data)
		if len(entries) != 500 || rec.Recorded() != 500 {
			t.Fatalf("%d requests: the capture keeps 500, holds %d (Recorded %d)", made, len(entries), rec.Recorded())
		}
		for i, entry := range entries {
			if want := fmt.Sprintf("https://host.example/item/%d", made-500+i); addressOf(entry) != want {
				t.Fatalf("%d requests: entry %d is %s, want %s — the capture keeps the LAST 500, in their order", made, i, addressOf(entry), want)
			}
		}
		if comment != theSentence(made, 500) {
			t.Fatalf("%d requests: the comment is the farm's sentence exactly\n got %q\nwant %q", made, comment, theSentence(made, 500))
		}
		if n, ok := runnerBelieves(comment, len(entries)); !ok || n != int64(made) {
			t.Fatalf("%d requests: the runner must believe the comment and read %d, read %d (believed %v)", made, made, n, ok)
		}
	}
}

// UNDER THE BOUND, NO COMMENT: nothing was left out, so the log says nothing about it.
func TestACaptureUnderItsBoundCarriesNoComment(t *testing.T) {
	for _, made := range []int{0, 1, 7, 499, 500} {
		rec := newRec(t)
		for i := 0; i < made; i++ {
			rec.Record(Flow{Method: "GET", URL: "https://host.example/item", Status: 200})
		}
		data, _ := rec.Snapshot()
		entries, _ := readHAR(t, data)
		if len(entries) != made {
			t.Fatalf("%d requests: all of them are kept, got %d", made, len(entries))
		}
		if raw, has := logOf(t, data)["comment"]; has {
			t.Fatalf("%d requests: a capture under its bound carries no comment, got %s", made, raw)
		}
	}
}

// INSTRUMENT: the port of the runner's reading refuses every shape the runner refuses — so the seal above, which it
// passes, means something.
func TestTheRunnersReadingIsPortedRuleForRule(t *testing.T) {
	if n, ok := runnerBelieves(theSentence(520, 500), 500); !ok || n != 520 {
		t.Fatal("the farm's own sentence is believed")
	}
	for name, c := range map[string]struct {
		comment string
		held    int
	}{
		"numbers that do not add up":   {"This app made 520 requests; the capture keeps the last 500, so the first 21 are not in it.", 500},
		"more entries than it keeps":   {theSentence(520, 500), 501},
		"nothing left out":             {"This app made 500 requests; the capture keeps the last 500, so the first 0 are not in it.", 500},
		"nothing kept":                 {"This app made 20 requests; the capture keeps the last 0, so the first 20 are not in it.", 0},
		"ten digits":                   {"This app made 1000000500 requests; the capture keeps the last 500, so the first 1000000000 are not in it.", 500},
		"a number grouped for people":  {"This app made 1,520 requests; the capture keeps the last 500, so the first 1,020 are not in it.", 500},
		"another wording":              {"this capture reached its bound of 500 requests; requests after that were not recorded", 500},
		"the right words, a sign":      {"This app made +520 requests; the capture keeps the last 500, so the first 20 are not in it.", 500},
		"the right words, then more":   {theSentence(520, 500) + " ", 500},
		"the runner's own Network tab": {"This run's app made 520 requests; the capture keeps the last 500, so the first 20 are not shown.", 500},
	} {
		if _, ok := runnerBelieves(c.comment, c.held); ok {
			t.Errorf("%s: the runner would not believe %q, and the port did", name, c.comment)
		}
	}
}

// THE SENTENCE, THE BOUNDS AND THE MARKER ARE THE FARM'S AND THE RUNNER'S OWN, read from their source when it is beside
// this repository: the farm node's add-on writes the sentence, the runner's reader takes it apart, and the runner's
// reading bounds and cut marker are the numbers and words this capture uses.
func TestTheSentenceBoundsAndMarkerAreTheFarmsAndTheRunnersOwn(t *testing.T) {
	addon := readSibling(t, farmtest.FarmLiveHarAddon)
	m := regexp.MustCompile(`log\["comment"\] = "([^"]+)" % \(seen, kept, seen - kept\)`).FindStringSubmatch(addon)
	if m == nil {
		t.Fatal("the farm node's add-on no longer writes log.comment as this seal reads it")
	}
	farmSays := strings.Replace(strings.Replace(strings.Replace(m[1], "%d", "1234", 1), "%d", "500", 1), "%d", "734", 1)
	if farmSays != theSentence(1234, 500) {
		t.Fatalf("the farm node writes %q, this agent %q", farmSays, theSentence(1234, 500))
	}
	for name, want := range map[string]string{"KEEP": "500", "HEADER_CHARS": "16384", "ADDRESS_CHARS": "4000"} {
		if !regexp.MustCompile(`(?m)^` + name + ` = ` + want + `$`).MatchString(addon) {
			t.Errorf("the farm node's add-on no longer says %s = %s", name, want)
		}
	}

	reader := readSibling(t, farmtest.RunnerHarNodeCut)
	parts := regexp.MustCompile(`const string Made = "([^"]+)", Keeps = "([^"]+)", First = "([^"]+)", NotInIt = "([^"]+)";`).FindStringSubmatch(reader)
	if parts == nil {
		t.Fatal("the runner's reader no longer holds its sentence as this seal reads it")
	}
	if runnerSays := parts[1] + "1234" + parts[2] + "500" + parts[3] + "734" + parts[4]; runnerSays != theSentence(1234, 500) {
		t.Fatalf("the runner reads %q, this agent writes %q", runnerSays, theSentence(1234, 500))
	}

	bounds := readSibling(t, farmtest.RunnerReadingBounds)
	for name, want := range map[string]string{"MaxCapturedRequests": "500", "MaxCapturedHeaderCharacters": "16_384", "MaxCapturedAddressCharacters": "4_000"} {
		if !strings.Contains(bounds, "public const int "+name+" = "+want+";") {
			t.Errorf("the runner's %s is no longer %s", name, want)
		}
	}
	marker := readSibling(t, farmtest.RunnerBoundedText)
	if !strings.Contains(marker, `internal static string Marker(int max) => $" [truncated at {max} chars]";`) {
		t.Error("the runner's cut marker is no longer \" [truncated at N chars]\"")
	}
}

func readSibling(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(farmtest.ServerSource(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(data)
}

// utf16Len is a text's length as the runner counts characters.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// A HEADER VALUE IS CUT AT 16,384 CHARACTERS AND AN ADDRESS AT 4,000, each with the runner's marker, the marker counted
// inside the bound — as the farm's node and the runner cut — and never through a character.
func TestAHeaderValueAndAnAddressAreCutWithTheRunnersMarker(t *testing.T) {
	long := strings.Repeat("a", 20000)
	atBound := strings.Repeat("b", 16384)
	address := "https://host.example/" + strings.Repeat("p", 5000)
	elsewhere := "https://other.example/" + strings.Repeat("q", 5000)
	faces := strings.Repeat("\U0001F600", 9000) // 18,000 characters as the runner counts them, 9,000 as Go does
	rec := newRec(t)
	rec.Record(Flow{Method: "GET", URL: address, Status: 302,
		ReqHeaders:  []Header{{"X-Long", long}, {"X-At-Bound", atBound}, {"X-Faces", faces}},
		RespHeaders: []Header{{"Location", elsewhere}, {"X-Long", long}}})
	data, _ := rec.Snapshot()
	entries, _ := readHAR(t, data)
	request := entries[0]["request"].(map[string]any)
	response := entries[0]["response"].(map[string]any)
	value := func(side map[string]any, name string) string {
		for _, h := range side["headers"].([]any) {
			if h.(map[string]any)["name"] == name {
				return h.(map[string]any)["value"].(string)
			}
		}
		t.Fatalf("no header %s", name)
		return ""
	}
	cut := func(what, got, original string, bound int) {
		t.Helper()
		marker := fmt.Sprintf(" [truncated at %d chars]", bound)
		if !strings.HasSuffix(got, marker) || !strings.HasPrefix(original, strings.TrimSuffix(got, marker)) {
			t.Errorf("%s: cut with the runner's marker after a prefix of the original, got …%q", what, got[max(0, len(got)-60):])
		}
		if n := utf16Len(got); n > bound || n < bound-1 {
			t.Errorf("%s: cut to %d characters as the runner counts them, marker included; got %d", what, bound, n)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: a cut never goes through a character", what)
		}
	}
	cut("a request header", value(request, "X-Long"), long, 16384)
	cut("a response header", value(response, "X-Long"), long, 16384)
	cut("a header of characters the runner counts twice", value(request, "X-Faces"), faces, 16384)
	cut("the address", request["url"].(string), address, 4000)
	cut("the redirect address", response["redirectURL"].(string), elsewhere, 4000)
	if value(request, "X-At-Bound") != atBound {
		t.Error("a header value at its bound is kept whole")
	}
}

// A CREDENTIAL THE CUT WOULD SPLIT IS MASKED WHOLE FIRST: the value is masked, then cut, so a cut can only shorten what
// is already masked. Cut first, the half of a token left before the cut no longer has a token's shape and would be kept.
func TestACredentialTheCutWouldSplitIsMaskedWholeFirst(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZGEiLCJuYW1lIjoiQWRhIEV4YW1wbGUifQ.c2lnbmF0dXJlLW9mLXRoZS10b2tlbg"
	header := strings.Repeat("a", 16384-60) + " " + token
	address := "https://host.example/" + strings.Repeat("p", 4000-60) + "/" + token
	rec := newRec(t)
	rec.Record(Flow{Method: "GET", URL: address, Status: 200, ReqHeaders: []Header{{"X-Note", header}}})
	data, _ := rec.Snapshot()
	if strings.Contains(string(data), "eyJ") {
		t.Fatalf("a part of the token was kept: %s", quoted(string(data[strings.Index(string(data), "eyJ")-20:])))
	}
	if !strings.Contains(string(data), Mask) {
		t.Fatal("the token was masked")
	}
}

// PAST THE BYTE BOUND THE OLDEST GO FIRST: an entry that does not fit even without its bodies makes room by leaving out
// the oldest, so what is kept is still the last K requests, and the sentence's numbers still add up.
func TestPastTheByteBoundTheOldestGoFirstAndTheNumbersStillAddUp(t *testing.T) {
	rec := newRec(t)
	big := strings.Repeat("h", 15000) // under the header bound, so nothing is cut: only the byte bound acts
	headers := make([]Header, 100)
	for i := range headers {
		headers[i] = Header{fmt.Sprintf("X-H%d", i), big}
	}
	const made = 30 // about 1.5 MB each: ten fit in 16 MB
	for i := 0; i < made; i++ {
		rec.Record(Flow{Method: "GET", URL: fmt.Sprintf("https://host.example/item/%d", i), Status: 200, ReqHeaders: headers})
	}
	data, _ := rec.Snapshot()
	if len(data) > MaxFileBytes {
		t.Fatalf("the file holds at most %d bytes, holds %d", MaxFileBytes, len(data))
	}
	entries, comment := readHAR(t, data)
	if len(entries) == 0 || len(entries) >= made {
		t.Fatalf("some, not all, of %d requests of 1.5 MB fit in 16 MB: %d kept", made, len(entries))
	}
	if addressOf(entries[len(entries)-1]) != fmt.Sprintf("https://host.example/item/%d", made-1) {
		t.Fatalf("the newest request is kept, got %s last", addressOf(entries[len(entries)-1]))
	}
	for i, entry := range entries {
		if want := fmt.Sprintf("https://host.example/item/%d", made-len(entries)+i); addressOf(entry) != want {
			t.Fatalf("entry %d is %s, want %s: what is kept is the last K, in order", i, addressOf(entry), want)
		}
	}
	if comment != theSentence(made, len(entries)) {
		t.Fatalf("the comment says what was left out\n got %q\nwant %q", comment, theSentence(made, len(entries)))
	}
	if n, ok := runnerBelieves(comment, len(entries)); !ok || n != made {
		t.Fatalf("the runner must believe it: %d %v", n, ok)
	}
}

// THE FILE ON DISK CATCHES UP: past the bound the document is rewritten at most once a second while requests keep coming,
// and whole when the capture stops — so the file a pull reads after a stop, or after the agent restarted, is the same
// document a pull during the capture read.
func TestTheFileOnDiskIsTheSameDocumentOnceTheCaptureStops(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	clock := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	rec, err := NewRecording(dir, lease1, "dev", func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		rec.Record(Flow{Method: "GET", URL: fmt.Sprintf("https://host.example/item/%d", i), Status: 200})
	}
	live, _ := rec.Snapshot()
	rec.Close()
	onDisk, err := os.ReadFile(filepath.Join(dir, lease1+".har"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(live) {
		t.Fatalf("the stopped capture's file is the document a pull read before the stop (%d bytes on disk, %d live)", len(onDisk), len(live))
	}
	stopped, err := rec.Snapshot()
	if err != nil || string(stopped) != string(live) {
		t.Fatalf("a pull after the stop reads the same document: %v", err)
	}
	if _, comment := readHAR(t, onDisk); comment != theSentence(600, 500) {
		t.Fatalf("the file on disk says what was left out: %q", comment)
	}
}
