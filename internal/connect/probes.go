package connect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/android"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// ParseSkip turns `--skip` values (comma-separated, repeatable) into the platforms NOT to ask about.
// THE PLATFORM IS A FIELD, NOT A VERB — the switch names platforms as values, so a third platform is
// a new value, never a new flag.
func ParseSkip(values []string) (map[inventory.Platform]bool, error) {
	skip := map[inventory.Platform]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(part)) {
			case "":
			case string(inventory.Android):
				skip[inventory.Android] = true
			case string(inventory.IOS):
				skip[inventory.IOS] = true
			default:
				return nil, fmt.Errorf("unknown platform %q for --skip (valid: android, ios)", part)
			}
		}
	}
	return skip, nil
}

// ProbeSet builds the per-tick probe function — THE ONE HOME for the probe list (the `devices` verb
// and the connect loop both wire through here; a third platform is a new line HERE, nowhere else).
// A SKIPPED PLATFORM PRODUCES NotAsked — the deliberate "this machine will never drive these" state —
// via the probe's own Enabled gate, which never touches the seam at all: not silence, and never a
// disabled probe still reporting a failure.
//
// Each probe runs under ITS OWN budget derived from the passed context — one budget each, never one
// shared: a slow adb must not spend the iOS probe's allowance (the rule A_GUARD_THAT_CANNOT_FAIL
// paid for, now living where the probes are built).
//
// offerUncabled is the customer's answer to inventory.OfferUncabledFlag: false offers only phones on this machine's USB
// cable and names the others as left out; true offers emulators and phones reached over the network too.
func ProbeSet(skip map[inventory.Platform]bool, run android.Runner, list ios.Lister, perProbeBudget time.Duration, offerUncabled bool) func(ctx context.Context) []inventory.Outcome {
	withBudget := func(ctx context.Context, probe func(context.Context) inventory.Outcome) inventory.Outcome {
		budgeted, cancel := context.WithTimeout(ctx, perProbeBudget)
		defer cancel()
		return probe(budgeted)
	}
	// ONE memory for the whole agent, built here because this is where the probe is built and this closure
	// outlives the ticks. A standing device fact is read once per attached device, not seven adb round trips
	// per device every ten seconds — see android.InstallConfirmations.
	confirmations := android.NewInstallConfirmations()
	return func(ctx context.Context) []inventory.Outcome {
		return []inventory.Outcome{
			withBudget(ctx, func(c context.Context) inventory.Outcome {
				return android.Probe{Run: run, Enabled: !skip[inventory.Android], Confirmations: confirmations, OfferUncabled: offerUncabled}.Outcome(c)
			}),
			withBudget(ctx, func(c context.Context) inventory.Outcome {
				return ios.Probe{List: list, Enabled: !skip[inventory.IOS], OfferUncabled: offerUncabled}.Outcome(c)
			}),
		}
	}
}
