package netcap

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// ── ONE HOME FOR THE MASKING VECTORS, READ ACROSS THE REPOSITORY BOUNDARY ────────────────────────────────────────
//
// The runner masks a capture on arrival; this agent masks it on the customer's machine before it leaves. Both are held to one
// file, which lives in the runner (tests/SharedVectors/network-capture-mask-vectors.json) beside the test that runs the runner's
// real HarRedactor over it (OneHomeForTheMaskingVectorsTests). This test runs THIS agent's real record path — the decode
// and the masker entryFor uses — over the same file. A rule changed on either side turns that side red.
//
// HOW IT READS THE FILE, and the precedent: internal/farm/correspondence_test.go reads the farm's source the same way —
// up two directories from this package, across to the sibling. When the runner is not beside this repository (nth-agent's own
// CI checks out nth-agent alone) the test SKIPS, and says in its skip line, loudly, that the masker was not held to the
// contract in that run. In the dev checkout, where every sibling is present, it always runs. It never passes having read
// nothing: a file that is there and unreadable, or smaller than the contract, fails.

// savedSecretsAreTheRunners is the ONE reason this side skips a case.
const savedSecretsAreTheRunners = "the run's saved secrets are the runner's to mask on arrival; this agent never asks for them"

type sharedCase struct {
	ID              string   `json:"id"`
	Location        string   `json:"location"`
	Name            string   `json:"name"`
	ContentType     string   `json:"contentType"`
	ContentEncoding string   `json:"contentEncoding"`
	Secrets         []string `json:"secrets"`
	Input           string   `json:"input"`
	Absent          []string `json:"absent"`
	Present         []string `json:"present"`
	Note            string   `json:"note"`
}

func readSharedVectors(t *testing.T) []sharedCase {
	t.Helper()
	data, err := os.ReadFile(farmtest.ServerSource(t, farmtest.RunnerMaskVectors))
	if err != nil {
		t.Fatalf("the shared masking vectors are there and cannot be read: %v", err)
	}
	var file struct {
		About string       `json:"about"`
		Cases []sharedCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("the shared masking vectors do not parse: %v", err)
	}
	if !strings.Contains(file.About, "nth-agent") || !strings.Contains(file.About, "shared_vectors_test.go") {
		t.Fatalf("the shared file no longer names this side as one it binds — read its \"about\" before changing either")
	}
	return file.Cases
}

func TestTheSharedVectorsHoldThroughTheAgentsMasker(t *testing.T) {
	cases := readSharedVectors(t)
	if len(cases) < 30 {
		t.Fatalf("the contract must be read whole: %d cases, and it had 30 when this side joined it", len(cases))
	}
	ran, skipped, withSecrets := 0, 0, 0
	for _, c := range cases {
		if len(c.Secrets) > 0 {
			withSecrets++
		}
		t.Run(c.ID, func(t *testing.T) {
			if len(c.Secrets) > 0 {
				skipped++
				t.Skip(savedSecretsAreTheRunners)
			}
			ran++
			body := encodeAs(t, c.ContentEncoding, c.Input)
			flow := placedFlow(t, c.Location, c.Name, c.ContentType, c.ContentEncoding, body, c.Input)
			got := fieldOf(t, entryFor(flow), c.Location, c.Name)
			for _, gone := range c.Absent {
				if strings.Contains(got, gone) {
					t.Errorf("%s: %s must be masked on this machine and survived in %s", c.ID, quoted(gone), quoted(got))
				}
			}
			for _, kept := range c.Present {
				if !strings.Contains(got, kept) {
					t.Errorf("%s: %s must survive masking and was lost from %s", c.ID, quoted(kept), quoted(got))
				}
			}
		})
	}
	// THE SKIPS ARE THE ONE RULE'S AND NOTHING ELSE'S: every case with saved secrets, and only those.
	if skipped != withSecrets || withSecrets == 0 {
		t.Fatalf("this side skips exactly the saved-secret cases (%d in the file) and skipped %d", withSecrets, skipped)
	}
	if ran+skipped != len(cases) {
		t.Fatalf("%d cases ran and %d were skipped, of %d", ran, skipped, len(cases))
	}
}
