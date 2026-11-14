package capture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── EVERY TEXT, PINNED AS WRITTEN ───────────────────────────────────────────────────────────────────────────────
//
// What a person reads about capture — before they turn it on, while they set up a phone, when a check fails, when a phone
// is left proxied, when they turn it off, and what a run is told — is pinned here word for word, so a change to any of it
// is a change someone reads in a diff of testdata/texts.golden.txt. (cmd/nth-agent's census reads the same texts, with the
// README's section and --help, for the six things each must say.)
//
// To write the golden after a deliberate change: NTH_AGENT_WRITE_TEXTS_GOLDEN=1 go test ./internal/capture -run TestEveryCustomerTextIsPinned

func everyText() []struct{ name, text string } {
	until := time.Date(2027, 10, 4, 9, 30, 0, 0, time.UTC)
	const serial, model, caName, proxy = "R5CT30ABCDE", "SM-A546B", "NthCase agent CA — bench-mac", "127.0.0.1:41234"
	owed := []Owed{
		{Record: Record{Serial: serial, Proxy: proxy}, Observation: unreachableObservation("adb exit 1: adb: device 'R5CT30ABCDE' not found")},
		{Record: Record{Serial: "R5CT30AAAA2", Proxy: "127.0.0.1:47001"}, Observation: "its global proxy still points at this machine's capture (127.0.0.1:47001) after it was cleared"},
	}
	return []struct{ name, text string }{
		{"SixThings", SixThings()},
		{"ThePromise", ThePromise()},
		{"SharedMachine", SharedMachine},
		{"EnableExplanation", EnableExplanation()},
		{"ConfirmPrompt", ConfirmPrompt},
		{"NotEnabled", NotEnabled},
		{"Enabled", Enabled(caName, until)},
		{"AlreadyEnabled", AlreadyEnabled(caName, until)},
		{"UnsupportedSystem", UnsupportedSystem("plan9")},
		{"SetupSteps Samsung", SetupSteps(serial, model, VendorSamsung)},
		{"SetupSteps stock Android", SetupSteps("39051FDJH00ABC", "Pixel 8", VendorStock)},
		{"TrustTrusted", TrustTrusted(serial, true)},
		{"TrustRefused", TrustRefused(serial, true)},
		{"TrustNoAnswer", TrustNoAnswer(serial, true)},
		{"TrustNoAnswer, the phone not put back", TrustNoAnswer(serial, false)},
		{"Unreachable", Unreachable(serial, proxy)},
		{"stillOwed", stillOwed(serial, proxy, "its global proxy still points at this machine's capture (127.0.0.1:41234) after it was cleared")},
		{"DisableSummary clean", DisableSummary(DisableResult{CAName: caName, HandedBack: []string{serial}, FilesDeleted: 2}, "/home/ada/.config/nth-agent/capture/ca")},
		{"DisableSummary with phones owed", DisableSummary(DisableResult{CAName: caName, StillOwed: owed}, "/home/ada/.config/nth-agent/capture/ca")},
		{"DisableSummary that could not delete", DisableSummary(DisableResult{CAName: caName, MarkPath: "/home/ada/.config/nth-agent/capture/enabled",
			MarkFailure: errors.New("permission denied"), CAFailure: errors.New("could not delete ca-key.pem in /home/ada/.config/nth-agent/capture/ca"),
			FilesDeleted: 1, FilesFailed: []string{leaseA + ".har"}}, "/home/ada/.config/nth-agent/capture/ca")},
		{"refused: off", refusedOff()},
		{"refused: CA", refusedCA()},
		{"refused: four", refusedAt4},
		{"refused: no port", refusedNoPort},
		{"refused: not recorded", refusedNotRecorded},
		{"refused: file", refusedFile},
		{"refused: reverse", refusedReverse},
		{"refused: proxy", refusedProxy},
		{"refused: not Android", refusedNotAndroid},
		{"refused: the peer check", refusedPeerCheck},
		{"refused: ended", refusedEnded},
		{"refused: not set up", refusedNotSetUp(serial)},
		{"refused: earlier CA", refusedOldCA(serial)},
		{"refused: busy", refusedBusy(serial)},
		{"refused: live", refusedLive(serial)},
		{"pull: taken", pullAlreadyTaken},
		{"pull: expired", pullExpired},
		{"pull: deleted", pullDeleted},
		{"pull: nothing known", pullNothingKnown},
		{"pull: unreadable", pullUnreadableState},
		// ADDED (the capture bounded like the farm's own node): what a stopped capture says it kept, when it kept every request
		// and when it kept only the last of them.
		{"stopped: every request kept", recordedAndMasked(7, 7)},
		{"stopped: the last kept", recordedAndMasked(500, 520)},
	}
}

func TestEveryCustomerTextIsPinned(t *testing.T) {
	var b strings.Builder
	for _, text := range everyText() {
		b.WriteString("===== " + text.name + " =====\n" + text.text)
		if !strings.HasSuffix(text.text, "\n") {
			b.WriteString("\n")
		}
	}
	got := b.String()
	golden := filepath.Join("testdata", "texts.golden.txt")
	if os.Getenv("NTH_AGENT_WRITE_TEXTS_GOLDEN") == "1" {
		must(t, os.MkdirAll("testdata", 0o755))
		must(t, os.WriteFile(golden, []byte(got), 0o644))
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("the golden is missing: %v", err)
	}
	if got != string(want) {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range min(len(gotLines), len(wantLines)) {
			if gotLines[i] != wantLines[i] {
				t.Fatalf("a customer text moved at line %d:\n got %q\nwant %q\n(rewrite the golden only for a change you mean)", i+1, gotLines[i], wantLines[i])
			}
		}
		t.Fatalf("a customer text moved: %d lines now, %d pinned", len(gotLines), len(wantLines))
	}
}

// THE RUN'S SENTENCES never name a lease or a path, and each says what happened on the machine.
func TestWhatARunIsToldNamesNoLeaseAndNoPath(t *testing.T) {
	for _, text := range everyText() {
		if !strings.HasPrefix(text.name, "refused") && !strings.HasPrefix(text.name, "pull") {
			continue
		}
		if !strings.HasPrefix(text.text, "no capture") || strings.Contains(text.text, leaseA) || strings.Contains(text.text, "/") {
			t.Errorf("%s: %q", text.name, text.text)
		}
	}
}
