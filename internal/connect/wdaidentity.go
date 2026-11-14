package connect

import (
	"context"
	"fmt"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// ── THE WEBDRIVERAGENT ON THE PHONE IS THE ONE THIS MACHINE DECLARED (WHAT_THE_FIRST_TRY_SHOWED, decision A6) ────────────
//
// The port declared for an iPhone is where, on the phone, the driver reaches WebDriverAgent; what answers there is asked
// (wda.Identify, through Config.WdaIdentity) at three moments:
//
//	before the port is declared: an iPhone whose port something else answers is not offered, and is shown as not usable
//	  with what answered, here and on the farm's Devices page. A phone whose port held this machine's WebDriverAgent, or
//	  nothing, is not asked again while it stays offered; one that is withheld is asked every tick, and offered once it
//	  clears. One the agent could not ask is declared as before: a cable that drops a forward is not a stranger.
//	before each session is created: the port is asked again, since an idle stop may have freed it since; anything but
//	  this machine's WebDriverAgent, or nothing, refuses the create before Appium sees it (allowed.Phones.OfferHeld).
//	once the session has started: the declared WebDriverAgent must answer there, holding a session — the one the driver
//	  just made, since WebDriverAgent ends any other when a session is created and creates on a machine take turns.
//	  Anything else and the agent ends the session at Appium and tells the farm why (allowed.Phones.Started).
//
// The question is asked only of an iPhone this machine declares a port and a WebDriverAgent build for: with no identity
// declared no iPhone is offered (farm.CanOffer), and with no way to ask (Config.WdaIdentity nil) nothing changes.

// identityState is what the declaration remembers between ticks: the iPhones already found to hold this machine's
// WebDriverAgent or nothing, and what was last said about each one withheld.
type identityState struct {
	verified map[string]bool
	said     map[string]string
}

// asksIdentity says whether this loop asks what answers on an iPhone's WebDriverAgent port at all.
func asksIdentity(cfg Config) bool {
	return cfg.WdaIdentity != nil && cfg.WdaPortFor != nil && cfg.IosWdaBundleID != ""
}

// withholdStrangers is outcomes with every offered iPhone whose port something else answers moved to not usable, with
// what answered, said once on the terminal.
func withholdStrangers(ctx context.Context, cfg Config, narrator *narrator, outcomes []inventory.Outcome) []inventory.Outcome {
	if !asksIdentity(cfg) {
		return outcomes
	}
	state := &narrator.identities
	if state.verified == nil {
		state.verified, state.said = map[string]bool{}, map[string]string{}
	}
	seen := map[string]bool{}
	out := make([]inventory.Outcome, 0, len(outcomes))
	for _, o := range outcomes {
		if o.Platform() != inventory.IOS || !o.IsAnswered() {
			out = append(out, o)
			continue
		}
		var kept []inventory.Offerable
		unusable := append([]inventory.Unusable(nil), o.Unusable()...)
		for _, d := range o.Offerable() {
			seen[d.ID] = true
			if state.verified[d.ID] {
				kept = append(kept, d)
				continue
			}
			port := cfg.WdaPortFor(ctx, d.ID)
			id := cfg.WdaIdentity(ctx, d.ID, port)
			switch id.Is {
			case wda.IdentityDeclared, wda.IdentityNothing:
				state.verified[d.ID] = true
				kept = append(kept, d)
			case wda.IdentityNotAsked:
				kept = append(kept, d) // declared as before; asked again next tick and at the create
			default:
				why := strangerOnThePort(d.ID, port, cfg.IosWdaBundleID, id)
				unusable = append(unusable, inventory.NewUnusable(d.ID, "something other than this machine's webdriveragent answers on its webdriveragent port", why))
				if state.said[d.ID] != why {
					state.said[d.ID] = why
					narrator.problem(ui.Problem{Level: ui.LevelLook, What: why,
						Why: "A session on it would send its commands, and what it types, to whatever answers there.",
						Do:  "close the app that holds that port on the phone, or restart the phone; the agent asks again each tick and offers it once its own WebDriverAgent, or nothing, answers there"})
				}
			}
		}
		rebuilt := inventory.AnsweredWithCaveat(inventory.IOS, kept, unusable, o.Reason(), o.Remedy())
		out = append(out, inventory.WithLeftOut(rebuilt, o.LeftOut()))
	}
	for udid := range state.verified {
		if !seen[udid] {
			delete(state.verified, udid) // unplugged or no longer offered: asked again when it comes back
		}
	}
	for udid := range state.said {
		if !seen[udid] {
			delete(state.said, udid)
		}
	}
	return out
}

// strangerOnThePort is the sentence for an iPhone whose port something else answers.
func strangerOnThePort(udid string, port uint16, bundle string, id wda.Identity) string {
	return fmt.Sprintf("iPhone %s is not offered: what answers on its WebDriverAgent port %d on the phone is not the "+
		"WebDriverAgent this machine declared (%s) — it %s", udid, port, farm.OneLineForTerminal(bundle), farm.OneLineForTerminal(id.Found))
}

// identityAtCreate is the create-time question for allowed.Phones: guard first (who holds the port on this machine), then
// what answers on the phone. declared is each offered iPhone's declared port.
func identityAtCreate(cfg Config, declared map[string]int, guard func(udid string, wdaPort int) string) func(udid string, wdaPort int) string {
	if !asksIdentity(cfg) {
		return guard
	}
	return func(udid string, wdaPort int) string {
		if why := guard(udid, wdaPort); why != "" {
			return why
		}
		own, isIPhone := declared[udid]
		if !isIPhone {
			return ""
		}
		port := own
		if wdaPort != 0 {
			port = wdaPort
		}
		ctx, cancel := context.WithTimeout(context.Background(), identityBound)
		defer cancel()
		id := cfg.WdaIdentity(ctx, udid, uint16(port))
		switch id.Is {
		case wda.IdentityDeclared, wda.IdentityNothing:
			return ""
		case wda.IdentityNotAsked:
			return fmt.Sprintf("the farm asked to start a session on iPhone %s, and the agent could not ask what answers on its "+
				"WebDriverAgent port %d on the phone (%s), so it could not tell that it is the WebDriverAgent this machine declared",
				udid, port, farm.OneLineForTerminal(id.Found))
		default:
			return fmt.Sprintf("the farm asked to start a session on iPhone %s, and what answers on its WebDriverAgent port %d on "+
				"the phone is not the WebDriverAgent this machine declared (%s) — it %s", udid, port,
				farm.OneLineForTerminal(cfg.IosWdaBundleID), farm.OneLineForTerminal(id.Found))
		}
	}
}

// identityOnceStarted is the question allowed.Phones asks once a session has started: the declared WebDriverAgent answers
// on the iPhone's declared port, holding a session. "" for a phone this machine declares no port for.
func identityOnceStarted(cfg Config, declared map[string]int) func(ctx context.Context, udid string) string {
	if !asksIdentity(cfg) {
		return nil
	}
	return func(ctx context.Context, udid string) string {
		port, isIPhone := declared[udid]
		if !isIPhone {
			return ""
		}
		ctx, cancel := context.WithTimeout(ctx, identityBound)
		defer cancel()
		id := cfg.WdaIdentity(ctx, udid, uint16(port))
		var found string
		switch {
		case id.Is == wda.IdentityDeclared && id.SessionID != "":
			return ""
		case id.Is == wda.IdentityDeclared:
			found = fmt.Sprintf("the WebDriverAgent on its port %d holds no session, so the session Appium started is not driving it", port)
		case id.Is == wda.IdentityNothing:
			found = fmt.Sprintf("nothing answers on its WebDriverAgent port %d on the phone", port)
		case id.Is == wda.IdentityNotAsked:
			found = fmt.Sprintf("the agent could not ask what answers on its WebDriverAgent port %d on the phone (%s)", port,
				farm.OneLineForTerminal(id.Found))
		default:
			found = fmt.Sprintf("what answers on its WebDriverAgent port %d on the phone is not that WebDriverAgent — it %s", port,
				farm.OneLineForTerminal(id.Found))
		}
		return fmt.Sprintf("the session on iPhone %s started, and %s; a session here is driven only through the WebDriverAgent "+
			"this machine declared (%s), so the agent ended it at the local Appium", udid, found, farm.OneLineForTerminal(cfg.IosWdaBundleID))
	}
}

// identityBound is the longest one question waits, the forward included.
const identityBound = 10 * time.Second
