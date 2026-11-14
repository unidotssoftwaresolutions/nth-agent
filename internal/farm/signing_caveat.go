package farm

import "github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"

// withSigningCaveat is outcomes with the signing sentence on platform's answer — AND ONLY ABOUT A PHONE THAT IS THERE
// (WHAT_THE_FIRST_TRY_SHOWED). The sentence says iPhones are attached, so it is said only when o holds one this machine
// would offer and holds back for want of an identity. With none, iOS answers as `devices` does, from the same probe; a
// phone left out for not being on the cable is not held back for this.
func withSigningCaveat(outcomes []WireOutcome, platform WirePlatform, o inventory.Outcome) []WireOutcome {
	if len(o.Offerable()) == 0 {
		return outcomes
	}
	return withCaveat(outcomes, platform, IosSigningReason, IosSigningRemedy())
}
