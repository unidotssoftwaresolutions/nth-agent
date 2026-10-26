package doctor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
)

// ── NETWORK CAPTURE IN THE DOCTOR'S REPORT: stated after the checks, never one of them ──────────────────────────

func withCapture(t *testing.T) Report {
	t.Helper()
	store := capture.StoreIn(t.TempDir())
	if _, err := capture.Enable(store, "bench-mac", "v-test", time.Now(), "darwin"); err != nil {
		t.Fatal(err)
	}
	status := capture.ReadStatus(store, time.Now())
	r := tonight()
	r.Capture = &status
	return r
}

func TestCaptureIsStatedAfterTheChecksAndWrappedToTheDoctorsWidth(t *testing.T) {
	r := withCapture(t)
	out := render(t, r, Options{})
	at := strings.Index(out, "network capture: ON")
	if at < 0 {
		t.Fatalf("doctor must state capture:\n%s", out)
	}
	for _, check := range r.Checks {
		if i := strings.Index(out, check.Name); i < 0 || i > at {
			t.Fatalf("capture is stated after every check, and %q comes after it:\n%s", check.Name, out)
		}
	}
	for _, line := range strings.Split(out[at:], "\n") {
		if n := len([]rune(line)); n > wrapWidth && strings.Contains(strings.TrimSpace(line), " ") {
			t.Fatalf("a capture line runs past %d columns (%d): %q", wrapWidth, n, line)
		}
	}
	folded := strings.Join(strings.Fields(out), " ")
	promise, _, _ := strings.Cut(capture.ThePromise(), " The agent never sends its private key anywhere")
	for _, said := range []string{capture.SixThings(), promise, "The agent never sends the capture CA's key anywhere and keeps it out of backups"} {
		if !strings.Contains(folded, strings.Join(strings.Fields(said), " ")) {
			t.Fatalf("doctor must say %q:\n%s", said, out)
		}
	}
}

func TestCaptureNeverCountsAmongWhatStopsARun(t *testing.T) {
	without, with := tonight(), withCapture(t)
	if len(with.Blocking()) != len(without.Blocking()) {
		t.Fatalf("capture is not a requirement for a run: %d blocking with it, %d without", len(with.Blocking()), len(without.Blocking()))
	}
}

func TestDoctorJsonCarriesCaptureAndOmitsItWhenNotRead(t *testing.T) {
	data, err := json.Marshal(withCapture(t))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Capture *struct {
			On     bool   `json:"on"`
			CAName string `json:"caName"`
			CAEnds string `json:"caEndsUtc"`
		} `json:"capture"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Capture == nil || !decoded.Capture.On ||
		decoded.Capture.CAName != "NthCase agent CA — bench-mac" || decoded.Capture.CAEnds == "" {
		t.Fatalf("--json must carry capture: %v %s", err, data)
	}
	plain, _ := json.Marshal(tonight())
	if strings.Contains(string(plain), `"capture"`) {
		t.Fatalf("a report that did not read capture says nothing about it: %s", plain)
	}
}
