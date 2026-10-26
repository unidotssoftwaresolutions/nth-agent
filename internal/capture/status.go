package capture

import (
	"fmt"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── WHAT `doctor` SAYS ABOUT NETWORK CAPTURE ────────────────────────────────────────────────────────────────────
//
// Read from the state folder alone — the mark, the CA's CERTIFICATE (never its key), the phones set up, and the handback
// records still owed — so `doctor` keeps its promise: it installs nothing, changes nothing and starts nothing.

// RenewWithin is how far ahead of the CA's end `doctor` starts saying how to renew it.
const RenewWithin = 30 * 24 * time.Hour

// Status is network capture on this machine, as `doctor` states it. --json carries every field.
type Status struct {
	On        bool          `json:"on"`
	CAName    string        `json:"caName,omitempty"`
	CAMade    *time.Time    `json:"caMadeUtc,omitempty"`
	CAEnds    *time.Time    `json:"caEndsUtc,omitempty"`
	CAProblem string        `json:"caProblem,omitempty"`
	Phones    []PhoneStatus `json:"phonesSetUp,omitempty"`
	Owed      []OwedStatus  `json:"owedHandbacks,omitempty"`
	Lines     []string      `json:"lines"`
}

// PhoneStatus is one phone set up for capture.
type PhoneStatus struct {
	Serial    string    `json:"serial"`
	Model     string    `json:"model,omitempty"`
	SetUpUtc  time.Time `json:"setUpUtc"`
	ForThisCA bool      `json:"forThisCa"`
}

// OwedStatus is one phone a capture's handback could not finish on.
type OwedStatus struct {
	Serial      string     `json:"serial"`
	Proxy       string     `json:"proxy"`
	SinceUtc    time.Time  `json:"sinceUtc"`
	LastTried   *time.Time `json:"lastTriedUtc,omitempty"`
	Observation string     `json:"observation,omitempty"`
}

// ReadStatus reads network capture's state on this machine.
func ReadStatus(st Store, now time.Time) Status {
	var status Status
	_, status.On = st.Enabled()
	records, _ := st.Records()
	for _, r := range records {
		status.Owed = append(status.Owed, OwedStatus{Serial: r.Serial, Proxy: r.Proxy, SinceUtc: r.Written,
			LastTried: r.LastTried, Observation: r.LastObservation})
	}

	if !status.On {
		status.Lines = append(status.Lines, "network capture: off — "+command.Line("capture enable")+" turns it on.")
		status.Lines = append(status.Lines, owedLines(status.Owed)...)
		status.Lines = append(status.Lines, explained()...)
		return status
	}

	cert, err := netcap.ReadCertificate(st.CADir())
	fingerprint := ""
	if err != nil {
		status.CAProblem = "the capture CA's certificate cannot be read"
		status.Lines = append(status.Lines, "network capture: ON, and its CA cannot be read — turn capture off and on: "+renewal()+".")
	} else {
		fingerprint = netcap.Fingerprint(cert)
		made, ends := cert.NotBefore.Add(time.Hour).UTC(), cert.NotAfter.UTC()
		status.CAName, status.CAMade, status.CAEnds = cert.Subject.CommonName, &made, &ends
		left := ends.Sub(now)
		status.Lines = append(status.Lines, fmt.Sprintf("network capture: ON — the capture CA \"%s\" was made %s and ends %s (%s).",
			status.CAName, made.Format(time.DateOnly), ends.Format(time.DateOnly), daysLeft(left)))
		switch {
		case left <= 0:
			status.CAProblem = "the capture CA has ended"
			status.Lines = append(status.Lines, "  the capture CA has ended, so nothing is captured on this machine. To renew it: "+renewal()+" — and remove the old certificate from each phone.")
		case left <= RenewWithin:
			status.Lines = append(status.Lines, "  the capture CA ends within 30 days. To renew it: "+renewal()+" — and remove the old certificate from each phone.")
		}
	}

	phones := st.Phones()
	if len(phones) == 0 {
		status.Lines = append(status.Lines, "  no phone is set up for capture yet: "+command.Line("capture setup [serial]"))
	}
	for _, p := range phones {
		ps := PhoneStatus{Serial: p.Serial, Model: p.Model, SetUpUtc: p.SetUpAtUtc, ForThisCA: fingerprint != "" && p.CAFingerprint == fingerprint}
		status.Phones = append(status.Phones, ps)
		name := p.Serial
		if p.Model != "" {
			name += " (" + p.Model + ")"
		}
		if ps.ForThisCA {
			status.Lines = append(status.Lines, fmt.Sprintf("  phone set up: %s — its browser trusted the capture certificate on %s", name, p.SetUpAtUtc.UTC().Format(time.DateOnly)))
		} else {
			status.Lines = append(status.Lines, fmt.Sprintf("  phone %s was set up for an earlier capture CA, so it is not captured: %s", name, command.Line("capture setup "+p.Serial)))
		}
	}
	status.Lines = append(status.Lines, owedLines(status.Owed)...)
	status.Lines = append(status.Lines, explained()...)
	return status
}

// explained is what `doctor` says capture is, said ONCE (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 3): SixThings, then the part of
// ThePromise that SixThings does not already say. Both used to be printed whole, so the key, the backups, the CA's year and
// how to turn it off came twice — three times with capture on, whose first line said the key sentence too.
func explained() []string {
	own, _, _ := strings.Cut(ThePromise(), promiseRepeatsFrom)
	return []string{"  " + SixThings(), "  " + own}
}

// promiseRepeatsFrom is where ThePromise starts saying again what SixThings has just said: the key, the backups, the year
// and how to remove the CA. README and --help still say ThePromise whole.
const promiseRepeatsFrom = " The agent never sends its private key anywhere"

// owedLines is each phone still owed a handback, with the whole message: what it is left with and how to fix it.
func owedLines(owed []OwedStatus) []string {
	var lines []string
	for _, o := range owed {
		text := unreachableBody(o.Serial, o.Proxy)
		if o.Observation != "" && !strings.HasPrefix(o.Observation, "the phone was not reachable") {
			text = stillOwed(o.Serial, o.Proxy, o.Observation)
		}
		lines = append(lines, "  "+strings.ReplaceAll(text, "\n", "\n  "))
		if o.LastTried != nil {
			lines = append(lines, fmt.Sprintf("    this machine last tried at %s", o.LastTried.UTC().Format(time.RFC3339)))
		}
	}
	return lines
}

func daysLeft(left time.Duration) string {
	days := int(left / (24 * time.Hour))
	switch {
	case left <= 0:
		return "it has ended"
	case days == 0:
		return "less than a day from now"
	case days == 1:
		return "1 day from now"
	default:
		return fmt.Sprintf("%d days from now", days)
	}
}

// renewal is the three commands that make a new capture CA, in order.
func renewal() string {
	return command.Line("capture disable") + ", then " + command.Line("capture enable") + ", then set up each phone with " +
		command.Line("capture setup")
}
