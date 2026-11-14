//go:build !darwin && !linux && !windows

package state

const phoneClaimsDir = ""

// holdPhoneIn has no machine-wide place to claim in here, where connect offers no phone it can claim; it claims nothing.
func holdPhoneIn(_, _ string) (func(), error) { return func() {}, nil }
