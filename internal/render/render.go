// Package render turns outcomes into the two renderings that are part of this command's contract.
// Both are sealed: neither may collapse what the type separates.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
)

// LiveViewFor answers ONE usable phone's live view on this machine (liveview.Judge, in the command). nil = the caller
// established nothing about live views, and then nothing about them is printed — never a guess in either direction.
//
// ONLY USABLE PHONES GET THE LINE: a phone the probe could not offer cannot be leased, so whether a run on it could
// be watched is not a question anybody can ask yet; its own line already says what to do first.
type LiveViewFor func(platform inventory.Platform, id string) liveview.Availability

// jsonOutcome is the machine rendering. THE SHAPE IS THE CONTRACT:
//   - answered      → "devices" and "unusable" present (possibly empty — a real answer), no reason.
//   - could_not_ask → "devices" ABSENT (never []), "reason" and "remedy" present. Unknown is not
//     empty: HeartbeatRequest.UnusableDevices states the same rule on the farm's wire (null ≠ []).
//   - not_asked     → neither devices nor reason; the probe holds no opinion.
//
// A consumer can tell the three apart from "outcome" alone, without reading prose.
type jsonOutcome struct {
	Platform string        `json:"platform"`
	Outcome  string        `json:"outcome"`
	Devices  *[]jsonDevice `json:"devices,omitempty"`
	Unusable *[]jsonUnusab `json:"unusable,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Remedy   string        `json:"remedy,omitempty"`
}

type jsonDevice struct {
	ID           string        `json:"id"`
	Model        string        `json:"model,omitempty"`
	Manufacturer string        `json:"manufacturer,omitempty"`
	OsVersion    string        `json:"osVersion,omitempty"`
	FormFactor   string        `json:"formFactor,omitempty"`
	Platform     string        `json:"platform"`
	LiveView     *jsonLiveView `json:"liveView,omitempty"`
}

// jsonLiveView is the same one line, machine-readable: available or not, the mirror port when it is, the sentence,
// and what an available answer did not check.
type jsonLiveView struct {
	Available bool   `json:"available"`
	Port      int    `json:"port,omitempty"`
	Reason    string `json:"reason"`
	Remedy    string `json:"remedy,omitempty"`
	Unchecked string `json:"unchecked,omitempty"`
}

type jsonUnusab struct {
	ID       string `json:"id"`
	State    string `json:"state"` // verbatim, lower-cased — never an enum, never "other"
	Remedy   string `json:"remedy"`
	Platform string `json:"platform"`
}

// JSON writes the machine rendering of all outcomes.
func JSON(w io.Writer, outcomes []inventory.Outcome, liveView LiveViewFor) error {
	payload := make([]jsonOutcome, 0, len(outcomes))
	for _, o := range outcomes {
		entry := jsonOutcome{Platform: string(o.Platform()), Outcome: o.KindString()}
		switch {
		case o.IsAnswered():
			devices := make([]jsonDevice, 0, len(o.Offerable()))
			for _, d := range o.Offerable() {
				device := jsonDevice{ID: d.ID, Model: d.Model, Manufacturer: d.Manufacturer, OsVersion: d.OsVersion, FormFactor: d.FormFactor, Platform: string(o.Platform())}
				if liveView != nil {
					judged := liveView(o.Platform(), d.ID)
					device.LiveView = &jsonLiveView{Available: judged.Available, Reason: judged.Reason, Remedy: judged.Remedy, Unchecked: judged.Unchecked}
					if judged.Available {
						device.LiveView.Port = int(judged.Port)
					}
				}
				devices = append(devices, device)
			}
			unusable := make([]jsonUnusab, 0, len(o.Unusable()))
			for _, u := range o.Unusable() {
				unusable = append(unusable, jsonUnusab{ID: u.ID, State: u.State, Remedy: u.Remedy, Platform: string(o.Platform())})
			}
			entry.Devices = &devices
			entry.Unusable = &unusable
			// A STATED BLIND SPOT rides the SAME keys as a failure's reason/remedy, because that is how the
			// farm's vocabulary carries it. The kind is what tells them apart — "answered" with a reason is a
			// caveat, "could_not_ask" with a reason is a failure — never the presence of the words.
			if o.HasCaveat() {
				entry.Reason = o.Reason()
				entry.Remedy = o.Remedy()
			}
		case o.IsCouldNotAsk():
			// No devices key AT ALL — the injection this seals is `"devices": []` with no reason.
			entry.Reason = o.Reason()
			entry.Remedy = o.Remedy()
		}
		payload = append(payload, entry)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

// Human writes the reader's rendering. A probe that could not ask NEVER prints "no devices found":
// it prints what happened and what to do. An attached-but-unusable device is printed with its reason, and a usable
// one with its live view — one line each — when the caller established it.
func Human(w io.Writer, outcomes []inventory.Outcome, liveView LiveViewFor) error {
	var b strings.Builder
	for _, o := range outcomes {
		platform := strings.ToUpper(string(o.Platform()))
		switch {
		case o.IsNotAsked():
			fmt.Fprintf(&b, "%s: not checked on this host (the probe is switched off — this is not a report about your devices).\n", platform)
		case o.IsCouldNotAsk():
			fmt.Fprintf(&b, "%s: COULD NOT CHECK — %s\n", platform, o.Reason())
			fmt.Fprintf(&b, "    what to do: %s\n", o.Remedy())
		default:
			if len(o.Offerable()) == 0 && len(o.Unusable()) == 0 && len(o.LeftOut()) == 0 {
				fmt.Fprintf(&b, "%s: no devices attached (checked — this is an answer, not a failure).\n", platform)
				continue
			}
			fmt.Fprintf(&b, "%s: %d usable, %d attached but unusable.\n", platform, len(o.Offerable()), len(o.Unusable()))
			for _, d := range o.Offerable() {
				label := d.Model
				if label == "" {
					label = "(model unknown)"
				}
				fmt.Fprintf(&b, "    usable   %s  %s\n", d.ID, label)
				if liveView != nil {
					fmt.Fprintf(&b, "             live view: %s\n", liveView(o.Platform(), d.ID).Line())
				}
			}
			for _, u := range o.Unusable() {
				fmt.Fprintf(&b, "    UNUSABLE %s  [%s] — %s\n", u.ID, u.State, u.Remedy)
			}
			// NOT ON THE CABLE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1): seen, ready, and not offered.
			for _, d := range o.LeftOut() {
				fmt.Fprintf(&b, "    LEFT OUT %s  — %s; the agent offers only phones on a USB cable (%s offers it too)\n",
					d.ID, d.How, inventory.OfferUncabledFlag)
			}
			// The platform ANSWERED and still has something it could not establish. Worded as a note, never as
			// a failure: a reader must be able to tell "I could not check" from "I checked, and here is what my
			// answer does not cover".
			if o.HasCaveat() {
				fmt.Fprintf(&b, "    NOTE (this answer does not cover): %s\n", o.Reason())
				fmt.Fprintf(&b, "    what to check by hand: %s\n", o.Remedy())
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// ExitCode: 0 means "I established the truth", not "I finished running". Answered-with-zero-devices is
// a success; every-probe-failed is not. NotAsked alone is not an answer either — nobody asked anyone.
func ExitCode(outcomes []inventory.Outcome) int {
	for _, o := range outcomes {
		if o.IsAnswered() {
			return 0
		}
	}
	return 1
}
