package netcap

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// ── ONE RULE, TWO MASKERS: THE WORDS OF THE NAME RULE, READ ACROSS THE REPOSITORY BOUNDARY ──────────────────────────
//
// AN_AGENT_CAPTURE_READY_TO_SHIP, rule 6. The shared vectors (shared_vectors_test.go) turn the side that does not honour a
// case red. This turns THIS side red too when the runner's SensitiveNames holds other words than this port — the runner's
// AnAgentCaptureReadyToShipTests reads mask.go the same way — so a word added to one side only turns both suites red.
// Skipped, loudly, only when the runner is not beside this repository, as the shared vectors are.

func TestTheNameRuleHoldsTheRunnersWords(t *testing.T) {
	path := farmtest.ServerSource(t, farmtest.RunnerNameRule)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the runner's name rule is named beside this repository and cannot be read: %v", err)
	}
	for _, pair := range []struct {
		csharp string
		ours   []string
	}{
		{"Within", namesWithin},
		{"Ending", namesEnding},
		{"Exactly", namesExactly},
		{"LastWords", namesLastWords},
	} {
		theirs := csharpList(t, string(source), pair.csharp)
		if !slices.Equal(theirs, pair.ours) {
			t.Errorf("SensitiveNames.%s is %q and this port holds %q — the two maskers must hold one rule", pair.csharp, theirs, pair.ours)
		}
	}
}

// csharpList reads `internal static readonly string[] Name = [ "a", "b", … ];` from the runner's name rule, across lines.
func csharpList(t *testing.T, source, name string) []string {
	t.Helper()
	declared := regexp.MustCompile(`internal static readonly string\[\] ` + name + `\s*=\s*\[([^\]]*)\]`).FindStringSubmatch(source)
	if declared == nil {
		t.Fatalf("the runner's name rule no longer declares %s the way this test reads it — read it before changing either side", name)
	}
	var words []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(declared[1], -1) {
		words = append(words, m[1])
	}
	if len(words) == 0 {
		t.Fatalf("SensitiveNames.%s read as empty — the test is reading the wrong text", name)
	}
	return words
}

// The words a name is read in, and what the last word must not catch — the same table as the runner's
// A_names_last_word_is_read_from_its_words, so the two splitters are held to one reading.
func TestANamesLastWordIsReadFromItsWords(t *testing.T) {
	for name, last := range map[string]string{
		"af_sig": "sig", "X-Hub-Sig": "sig", "afSig": "sig", "HMACSig": "sig", "hub.sig": "sig", "user[af_sig]": "sig",
		"v1Sig": "sig", "sig2": "sig", "x-amz-meta-af-auth-v1": "auth", "X-Hub-Signature-256": "signature",
		"signature_v2": "signature", "signatureMethod": "method", "signature_version": "version", "sig_type": "type",
		"zulaessig": "zulaessig", "AFSIG": "afsig", "authorId": "id", "v1": "", "": "",
	} {
		if got := nameLastWord(name); got != last {
			t.Errorf("the last word of %q is %q, and this port read %q", name, last, got)
		}
	}
	for _, masked := range []string{"af_sig", "X-Hub-Sig", "afSig", "sig", "hub.sig", "signature_v2", "x-amz-meta-af-auth-v1", "X-Auth"} {
		if !nameSensitive(masked) {
			t.Errorf("%q's last word is %q, a credential's: its value must be masked", masked, nameLastWord(masked))
		}
	}
	for _, kept := range []string{"zulaessig", "signatureMethod", "signature_version", "sig_type", "authorId", "author", "oauth", "AFSIG", "X-Signature-Version", "payload_v2"} {
		if nameSensitive(kept) {
			t.Errorf("%q's last word is %q, not a credential's: its value must be kept", kept, nameLastWord(kept))
		}
	}
}
