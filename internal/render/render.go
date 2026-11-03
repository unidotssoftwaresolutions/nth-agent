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
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
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
	Emulator     bool          `json:"emulator,omitempty"`
	AVD          string        `json:"avd,omitempty"`
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
				device := jsonDevice{ID: d.ID, Model: d.Model, Manufacturer: d.Manufacturer, OsVersion: d.OsVersion, FormFactor: d.FormFactor, Platform: string(o.Platform()),
					Emulator: d.Emulator, AVD: d.AVD}
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
		case o.NotHereBecause() != "":
			// A platform this system cannot serve: not asked, and why, with nothing to do about it here.
			entry.Reason = o.NotHereBecause()
		}
		payload = append(payload, entry)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

// Human writes the reader's rendering, plain: no colour, folded at ui.ReportWidth. Styled is the same with the dressing
// the caller decided.
func Human(w io.Writer, outcomes []inventory.Outcome, liveView LiveViewFor) error {
	return Styled(w, ui.Mode{}, outcomes, liveView)
}

// Styled writes the reader's rendering (AN_AGENT_THAT_SPEAKS_PLAINLY): what each platform answered, first, a line each;
// then every phone in a small table — name, platform, OS and state, the state in its colour; then each phone's live view;
// then every problem in the three parts every problem is said in.
//
// A probe that could not ask NEVER prints "no devices found": it prints what happened and what to do. An attached-but-
// unusable device is a row with its state and a problem with its remedy; a usable one carries its live view when the
// caller established it.
func Styled(w io.Writer, m ui.Mode, outcomes []inventory.Outcome, liveView LiveViewFor) error {
	width := m.Report()
	var b strings.Builder
	say := func(lines ...string) {
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
	}
	var rows [][]ui.Cell
	var views, problems []string
	for _, o := range outcomes {
		platform := PlatformName(o.Platform())
		switch {
		case o.NotHereBecause() != "":
			say(m.Marked(ui.MarkNote, platform+": "+o.NotHereBecause(), m.Dim, width)...)
		case o.IsNotAsked():
			say(m.Marked(ui.MarkNote, platform+": not checked on this host (the probe is switched off — this is not a report about your devices).", m.Dim, width)...)
		case o.IsCouldNotAsk():
			problems = append(problems, m.Problem(ui.Problem{Level: ui.LevelStop, What: platform + ": could not check — " + o.Reason(),
				Why: "No " + platform + " phone on this machine can be listed or offered until this is fixed.", Do: o.Remedy()}, width, false)...)
			say(m.Marked(ui.MarkStop, platform+": could not check", m.Stop, width)...)
		case len(o.Offerable()) == 0 && len(o.Unusable()) == 0 && len(o.LeftOut()) == 0:
			say(m.Marked(ui.MarkNote, platform+": no devices attached (checked — this is an answer, not a failure).", m.Dim, width)...)
		default:
			mark := ui.MarkOK
			if len(o.Offerable()) == 0 {
				mark = ui.MarkLook
			}
			say(m.Marked(mark, fmt.Sprintf("%s: %d usable, %d attached but unusable", platform, len(o.Offerable()), len(o.Unusable())), nil, width)...)
			for _, d := range o.Offerable() {
				name, kind := deviceName(d.Model, d.ID), platform
				if d.Emulator {
					// AN EMULATOR ON THIS MACHINE is offered like a phone on the cable, and said to be one: its virtual
					// device's name, and "emulator" beside the platform.
					kind = platform + " emulator"
					if d.AVD != "" {
						name = d.AVD
					}
				}
				rows = append(rows, []ui.Cell{{Text: name}, {Text: kind}, {Text: orDash(d.OsVersion)}, {Text: "ready to offer", Paint: m.Good}})
				if liveView == nil {
					continue
				}
				judged := liveView(o.Platform(), d.ID)
				if judged.Available || judged.Remedy == "" {
					views = append(views, name)
					views = append(views, m.Indented("  ", "live view: "+judged.Line(), m.Dim, width)...)
					continue
				}
				// A LIVE VIEW THAT CANNOT OPEN is a problem like any other: what, why, and what to do on this system.
				problems = append(problems, m.Problem(ui.Problem{Level: ui.LevelLook, What: name + ": no live view — " + judged.Reason,
					Why: "Runs on it are still driven; nobody can watch them live.", Do: judged.Remedy}, width, false)...)
			}
			for _, u := range o.Unusable() {
				rows = append(rows, []ui.Cell{{Text: deviceName("", u.ID)}, {Text: platform}, {Text: "—"}, {Text: "left out: " + u.State, Paint: m.Stop}})
				problems = append(problems, m.Problem(ui.Problem{Level: ui.LevelStop, What: fmt.Sprintf("%s phone %s is %s", platform, u.ID, u.State),
					Why: "It is not offered for runs until you do this.", Do: u.Remedy}, width, false)...)
			}
			// NOT ON THE CABLE (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1): seen, ready, and not offered.
			for _, d := range o.LeftOut() {
				rows = append(rows, []ui.Cell{{Text: deviceName("", d.ID)}, {Text: platform}, {Text: "—"}, {Text: "left out: not on a USB cable", Paint: m.Look}})
				problems = append(problems, m.Problem(ui.Problem{Level: ui.LevelLook, What: fmt.Sprintf("%s phone %s is %s", platform, d.ID, d.How),
					Why: "The agent offers phones on a USB cable and the emulators on this machine, so this one is left out.",
					Do:  "connect it by USB cable, or offer it too: start connect with " + inventory.OfferUncabledFlag}, width, false)...)
			}
			// The platform ANSWERED and still has something it could not establish. Worded as a note, never as a failure: a
			// reader must be able to tell "I could not check" from "I checked, and here is what my answer does not cover".
			if o.HasCaveat() {
				problems = append(problems, m.Problem(ui.Problem{Level: ui.LevelLook, What: platform + ": this answer does not cover: " + o.Reason(),
					Why: "The phones above are listed; this is what the listing could not tell.", Do: "check by hand: " + o.Remedy()}, width, false)...)
			}
		}
	}
	if len(rows) > 0 {
		say("")
		say(m.Table("", []string{"NAME", "PLATFORM", "OS", "STATE"}, rows)...)
	}
	if len(views) > 0 {
		say("")
		say(views...)
	}
	if len(problems) > 0 {
		say("")
		say(problems...)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// PlatformName is a platform as a person writes it: Android, iOS.
func PlatformName(p inventory.Platform) string {
	switch p {
	case inventory.Android:
		return "Android"
	case inventory.IOS:
		return "iOS"
	}
	return string(p)
}

// deviceName is what a person calls a phone: its model, or, with none, its id — the ends of a long one, which is enough to
// tell two phones apart on one desk (the whole id is in every problem line about it, and in --json).
func deviceName(model, id string) string {
	if model != "" {
		return model
	}
	if len(id) > 16 {
		return id[:8] + "…" + id[len(id)-4:]
	}
	return id
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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
