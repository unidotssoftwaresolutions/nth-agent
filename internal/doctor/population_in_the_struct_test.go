package doctor

import (
	"context"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// THE POPULATION WAS ALREADY IN THE STRUCT.
//
// ios.MatchFor carries both populations: ForTeam (the FIRST bundle-and-team match) and ForTeamAll ("EVERY profile
// that covers the bundle id for the team — the population a device answer must be taken over"). The provisioning
// finding asked the STATE over ForTeamAll and took the DATE from ForTeam, computed once outside the per-device
// closure. So every phone on a machine reported the same expiry, and that expiry belonged to whichever profile
// the directory enumeration returned first.
//
// ⚠️ AND IT LANDS ON A PAYING CUSTOMER. These findings are not filtered by any offer rule on the customer path —
// The runner's DeviceSessionStart reads the DATE BEFORE THE STATE WORD, on purpose and correctly, on the premise that
// the date belongs to a profile that covers the phone. Break that premise and the platform tells a tenant their
// WebDriverAgent profile expired on a date read off a profile that is not about their phone.
//
// ⚠️ Every identifier below is COINED. No real udid, team id or bundle id appears in this file.

const (
	teamHere = "TEAMPLACEHOLD"
	wdaHere  = "com.example.rack.WebDriverAgentRunner"
	runnerId = teamHere + "." + wdaHere + ".xctrunner"
	phoneA   = "UDID-PHONE-A"
	phoneB   = "UDID-PHONE-B"
	phoneOff = "UDID-NOT-COVERED"
)

var checkedAt = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func inDays(d float64) string {
	return checkedAt.Add(time.Duration(d * float64(24*time.Hour))).Format(time.RFC3339)
}

// envWith builds the doctor environment around a fixed set of profiles, in the order given — because directory
// ORDER is exactly what must stop deciding the answer.
func envWith(profiles ...ios.Profile) Env {
	return Env{
		IosWdaBundleID: wdaHere,
		IosTeamID:      teamHere,
		IosProfiles:    func(context.Context) ([]ios.Profile, error) { return profiles, nil },
	}
}

func profile(expires string, devices []string) ios.Profile {
	return ios.Profile{
		Name: "profile " + expires, AppID: runnerId, TeamID: teamHere,
		Expires: expires, ProvisionedDevices: devices,
	}
}

func findingFor(t *testing.T, env Env, udid string) DeviceFinding {
	t.Helper()
	return profileFindings(context.Background(), env, checkedAt)(udid)
}

// ── INJECTION 1: the date must come from a profile that covers THIS phone ────────────────────────────────────

func TestTheExpiryComesFromAProfileThatCoversThisPhone(t *testing.T) {
	// The ordinary Xcode Mac: a wildcard-ish profile enumerated FIRST that lists a different phone and runs for
	// 200 days, and the profile that actually covers this one, lapsing in 9.
	env := envWith(
		profile(inDays(200), []string{phoneB}),
		profile(inDays(9), []string{phoneA}),
	)

	f := findingFor(t, env, phoneA)

	if f.ExpiresAtUtc == nil || !f.ExpiresAtUtc.Equal(mustParse(t, inDays(9))) {
		t.Fatalf("the expiry on this phone's finding came from a profile that does NOT cover it. Two profiles "+
			"match the runner bundle id and the team; the first one enumerated lists a different phone and runs "+
			"for 200 days, while the profile that covers THIS phone lapses in 9. The state word was already asked "+
			"over every matching profile — ForTeamAll, the population sitting one field away in MatchFor — and the "+
			"date was taken from ForTeam. A paying customer would be told their runner profile is good for 200 "+
			"days on a phone whose profile dies in 9, and the session would fail at xcodebuild. got %v, want %v",
			f.ExpiresAtUtc, inDays(9))
	}
	if f.State != Satisfied {
		t.Fatalf("the phone IS covered, so the state must be %q: got %q", Satisfied, f.State)
	}
}

// ── INJECTION 2: computed once outside the closure means every phone gets one date ───────────────────────────

func TestTwoPhonesOnOneMacDoNotShareTheSameExpiry(t *testing.T) {
	// Two phones, each covered by its own profile with its own date. If the expiry is computed once, outside the
	// per-device closure, both findings carry whichever profile came first.
	env := envWith(
		profile(inDays(30), []string{phoneA}),
		profile(inDays(400), []string{phoneB}),
	)

	a, b := findingFor(t, env, phoneA), findingFor(t, env, phoneB)

	if a.ExpiresAtUtc == nil || b.ExpiresAtUtc == nil || a.ExpiresAtUtc.Equal(*b.ExpiresAtUtc) {
		t.Fatalf("two phones on one Mac, each covered by a DIFFERENT profile with a different expiry, reported "+
			"the same date. The expiry was computed once outside the per-device closure, so it could not vary by "+
			"phone even in principle — one phone's runway was reported for the other. Phone A is covered by a "+
			"profile lapsing in 30 days and phone B by one lapsing in 400. got A=%v B=%v", a.ExpiresAtUtc, b.ExpiresAtUtc)
	}
	if !a.ExpiresAtUtc.Equal(mustParse(t, inDays(30))) || !b.ExpiresAtUtc.Equal(mustParse(t, inDays(400))) {
		t.Fatalf("each phone must carry ITS OWN covering profile's date: got A=%v (want %v), B=%v (want %v)",
			a.ExpiresAtUtc, inDays(30), b.ExpiresAtUtc, inDays(400))
	}
}

// ── INJECTION 3: a `missing` finding may not carry a confident expiry — THE TENANT'S SENTENCE ────────────────

func TestAPhoneNoProfileCoversIsAnsweredWithNoDateAtAll(t *testing.T) {
	env := envWith(profile(inDays(365), []string{phoneA}))

	f := findingFor(t, env, phoneOff)

	if f.State != Missing {
		t.Fatalf("no profile lists this phone, so the state must be %q: got %q", Missing, f.State)
	}
	if f.ExpiresAtUtc != nil {
		t.Fatalf("a MISSING finding carried a confident expiry, and this is the one that reaches a paying "+
			"customer. Nothing on this machine covers this phone, so there is no date to give — but the finding "+
			"opened with the expiry already attached and decided the state afterwards, taking the date off a "+
			"profile that lists a DIFFERENT phone. The runner's DeviceSessionStart reads this date BEFORE the state "+
			"word (deliberately, and correctly, on the premise that it belongs to a covering profile), so the "+
			"tenant is told: their WebDriverAgent provisioning profile expired on %v — a date belonging to a "+
			"profile that is not about their phone, on a phone whose real problem is that no profile covers it "+
			"at all. Two wrong sentences from one wrong field.", f.ExpiresAtUtc)
	}
}

func TestAPhoneOnAMachineWithNoProfilesAtAllCarriesNoDate(t *testing.T) {
	env := envWith()

	f := findingFor(t, env, phoneA)

	if f.State != Missing || f.ExpiresAtUtc != nil {
		t.Fatalf("a machine with no matching profile has nothing to say about when one expires: got state=%q, "+
			"expires=%v", f.State, f.ExpiresAtUtc)
	}
}

// ── INJECTION 4: among covering profiles, the SOONEST is the answer ──────────────────────────────────────────

func TestAmongProfilesThatAllCoverThisPhoneTheSoonestExpiryWins(t *testing.T) {
	env := envWith(
		profile(inDays(365), []string{phoneA}),
		profile(inDays(4), []string{phoneA}),
	)

	f := findingFor(t, env, phoneA)

	if f.ExpiresAtUtc == nil || !f.ExpiresAtUtc.Equal(mustParse(t, inDays(4))) {
		t.Fatalf("two profiles cover this phone, one lapsing in 4 days and one in 365, and the finding reported "+
			"the LATER one. Nothing here knows which profile Xcode will pick — this package's own MatchFor.ForTeam "+
			"is file order — so the honest answer is the soonest, which never overstates the runway. Reporting the "+
			"latest tells an operator he has a year while the phone stops working on Tuesday. got %v, want %v",
			f.ExpiresAtUtc, inDays(4))
	}
}

func TestTheOrderTheDirectoryReturnedDoesNotDecideTheDate(t *testing.T) {
	soonestFirst := findingFor(t, envWith(profile(inDays(4), []string{phoneA}), profile(inDays(365), []string{phoneA})), phoneA)
	latestFirst := findingFor(t, envWith(profile(inDays(365), []string{phoneA}), profile(inDays(4), []string{phoneA})), phoneA)

	if soonestFirst.ExpiresAtUtc == nil || latestFirst.ExpiresAtUtc == nil ||
		!soonestFirst.ExpiresAtUtc.Equal(*latestFirst.ExpiresAtUtc) {
		t.Fatalf("the same two profiles in a different directory order produced two different dates. Directory "+
			"enumeration order is not a ranking, and a number that changes between runs and between machines is "+
			"not a fact about the phone. got %v and %v", soonestFirst.ExpiresAtUtc, latestFirst.ExpiresAtUtc)
	}
}

// ── INJECTION 5: an unparseable Expires is NOT SAID, never "does not expire" ─────────────────────────────────

func TestAProfileWhoseExpiresDoesNotParseDoesNotBecomeOneThatNeverExpires(t *testing.T) {
	env := envWith(profile("not a date", []string{phoneA}))

	f := findingFor(t, env, phoneA)

	if f.ExpiresAtUtc != nil {
		t.Fatalf("the only profile covering this phone states an expiry this build cannot read, and the finding "+
			"invented one. Nil is NOT SAID, never 'does not expire' — treating it as a far-future date puts a "+
			"confident runway on a screen that no profile ever stated, and guarantees no consumer will ever act "+
			"on it. got %v", f.ExpiresAtUtc)
	}
	if f.State != Satisfied {
		t.Fatalf("the profile still COVERS the phone; only its date is unreadable, so the state is unchanged: got %q", f.State)
	}
}

func TestAnUnreadableDateBesideAReadableOneNeitherHidesItNorOutranksIt(t *testing.T) {
	env := envWith(profile("not a date", []string{phoneA}), profile(inDays(11), []string{phoneA}))

	f := findingFor(t, env, phoneA)

	if f.ExpiresAtUtc == nil || !f.ExpiresAtUtc.Equal(mustParse(t, inDays(11))) {
		t.Fatalf("a profile whose date cannot be read must contribute NOTHING to the ordering: it can make the "+
			"soonest date neither sooner nor later. Dropping it must not hide the real date beside it, and "+
			"sorting it as a far-future value must not hide it either. got %v, want %v", f.ExpiresAtUtc, inDays(11))
	}
}

// ── the verbatim string is not replaced by the parsing ───────────────────────────────────────────────────────

func TestParsingForOrderingDoesNotRewriteTheStoredText(t *testing.T) {
	// ios.Profile.Expires is the plist value VERBATIM, because what the doctor prints is what the plist said.
	// Ordering reads it; nothing writes it back.
	raw := inDays(7)
	profiles := []ios.Profile{profile(raw, []string{phoneA})}
	env := Env{IosWdaBundleID: wdaHere, IosTeamID: teamHere,
		IosProfiles: func(context.Context) ([]ios.Profile, error) { return profiles, nil }}

	_ = findingFor(t, env, phoneA)

	if profiles[0].Expires != raw {
		t.Fatalf("the stored Expires text was rewritten by the ordering: got %q, want the plist's own %q — the "+
			"doctor prints what the plist said, and parsing for a comparison must not replace it",
			profiles[0].Expires, raw)
	}
}

// ── the population itself, sealed where it lives ─────────────────────────────────────────────────────────────

func TestCoversDeviceAndCoveringDeviceAnswerOverTheSamePopulation(t *testing.T) {
	match := ios.Match([]ios.Profile{
		profile(inDays(50), []string{phoneB}),
		profile(inDays(60), []string{phoneA}),
	}, ios.WdaRunnerBundleID(wdaHere), teamHere)

	if len(match.ForTeamAll) != 2 {
		t.Fatalf("both profiles match the bundle id and the team: got %d", len(match.ForTeamAll))
	}
	if got := len(match.CoveringDevice(phoneA)); got != 1 {
		t.Fatalf("exactly one of the two profiles lists phone A: got %d", got)
	}
	if !match.CoversDevice(phoneA) || !match.CoversDevice(phoneB) {
		t.Fatal("each phone is covered by one of the two profiles, so the boolean must agree with the slice")
	}
	if match.CoversDevice(phoneOff) || len(match.CoveringDevice(phoneOff)) != 0 {
		t.Fatal("neither profile lists the third phone, so both answers must say so — a boolean that disagrees " +
			"with the population it is derived from is the defect this pair exists to prevent")
	}
}

func mustParse(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("the fixture is not a date: %q", value)
	}
	return at.UTC()
}
