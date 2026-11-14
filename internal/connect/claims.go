package connect

import (
	"context"
	"fmt"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// ── ONE PHONE, ONE connect, ON THE WHOLE MACHINE (ANOTHER_ACCOUNT_AND_LIMITS) ────────────────────────────────────────
//
// Before a phone is offered, it is claimed on this machine (Config.HoldPhone, state.HoldPhone in production): a phone
// another connect holds — this account's, started with another state folder, or another account's that shares a root-run
// adb server or the system's usbmuxd — is not offered by this one, and that is said once. A claim is kept while the phone
// is offered and given back when it is not, so the other connect can offer it then.

type phoneClaims struct {
	hold func(serial string) (func(), error)
	held map[string]func()
	said map[string]bool
}

// claimed wraps probes so that what they offer is what this connect holds.
func claimed(probes func(context.Context) []inventory.Outcome, hold func(string) (func(), error), n *narrator) (func(context.Context) []inventory.Outcome, func()) {
	if hold == nil {
		return probes, func() {}
	}
	c := &phoneClaims{hold: hold, held: map[string]func(){}, said: map[string]bool{}}
	return func(ctx context.Context) []inventory.Outcome { return c.keep(probes(ctx), n) }, c.releaseAll
}

func (c *phoneClaims) keep(outcomes []inventory.Outcome, n *narrator) []inventory.Outcome {
	offered := map[string]bool{}
	out := make([]inventory.Outcome, 0, len(outcomes))
	for _, o := range outcomes {
		if !o.IsAnswered() {
			out = append(out, o)
			continue
		}
		kept := make([]inventory.Offerable, 0, len(o.Offerable()))
		dropped := false
		for _, d := range o.Offerable() {
			key := string(o.Platform()) + " " + d.ID
			if _, ok := c.held[key]; ok {
				offered[key] = true
				kept = append(kept, d)
				continue
			}
			release, err := c.hold(d.ID)
			if err != nil {
				dropped = true
				if !c.said[key] {
					c.said[key] = true
					n.problem(ui.Problem{Level: ui.LevelLook,
						What: fmt.Sprintf("%s phone %s is not offered: %v", platformName(o.Platform()), d.ID, err),
						Why:  "One phone is offered by one connect on a machine, so a run from one is never driven or recorded from another.",
						Do:   "stop the other connect that offers it (Ctrl-C in its terminal); this one offers the phone once that one lets it go"})
				}
				continue
			}
			c.held[key] = release
			delete(c.said, key)
			offered[key] = true
			kept = append(kept, d)
		}
		if dropped {
			o = inventory.WithLeftOut(inventory.AnsweredWithCaveat(o.Platform(), kept, o.Unusable(), o.Reason(), o.Remedy()), o.LeftOut())
		}
		out = append(out, o)
	}
	for key, release := range c.held {
		if !offered[key] {
			release()
			delete(c.held, key)
		}
	}
	return out
}

func (c *phoneClaims) releaseAll() {
	for key, release := range c.held {
		release()
		delete(c.held, key)
	}
}
