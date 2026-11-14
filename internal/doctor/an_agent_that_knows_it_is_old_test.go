package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
)

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 3: doctor explains capture ONCE ──────────────────────────────────────────
//
// Filmed on 2026-10-20: `doctor` explained network capture two times in a row — SixThings, then ThePromise, and both say
// that the agent never sends the CA's key anywhere, keeps it out of backups, that the CA lasts one year and how to remove
// it. With capture on, the ON line said the key sentence a third time. Each fact is said once now, whatever the state.

// eachOnce are the facts the two paragraphs both said, and the words that find each one.
var eachOnce = []struct{ fact, words string }{
	{"what capture means here", "What network capture means here"},
	{"the key is never sent anywhere", "never sends"},
	{"the key is kept out of backups", "out of backups"},
	{"an earlier backup may still hold a copy", "backup made before that"},
	{"the CA lasts one year", "one year"},
	{"how to turn it off", "capture disable"},
	{"the promise about signing", "never signs or re-signs"},
}

func TestDoctorExplainsCaptureOnce(t *testing.T) {
	off := capture.ReadStatus(capture.StoreIn(t.TempDir()), time.Now())
	offReport := tonight()
	offReport.Capture = &off
	for name, r := range map[string]Report{"capture off": offReport, "capture on": withCapture(t)} {
		for _, why := range []bool{false, true} {
			out := strings.Join(strings.Fields(render(t, r, Options{Why: why})), " ")
			for _, f := range eachOnce {
				if n := strings.Count(out, f.words); n != 1 {
					t.Errorf("%s (--why %v): doctor says %s %d times (%q); once is enough:\n%s", name, why, f.fact, n, f.words, out)
				}
			}
		}
	}
}
