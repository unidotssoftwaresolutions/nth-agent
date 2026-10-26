package doctor

import (
	"context"
	"go/scanner"
	"go/token"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// TWO SURFACES, ONE PHONE, TWO DATES.
//
// ⚠️ THE PREVIOUS SLICE FIXED ONE HALF OF THIS PACKAGE AND NOT THE OTHER, AND THAT IS WHAT THESE SEAL. Before it,
// both the findings pipeline and the CLI check read `match.ForTeam` — the first profile matching the bundle id
// and the team, coverage never asked — so both were wrong and they AGREED. Then the findings half started asking
// per device, over the profiles that cover that device, and the CLI half did not: a customer running
// `nth-agent doctor` and an operator reading the Devices grid could see TWO DIFFERENT EXPIRY DATES FOR THE
// SAME PHONE, with neither surface saying what it was answering about. A wrong answer that agrees with itself is
// easier to trust than two answers that differ.
//
// The resolution is not "make the CLI copy the finding". It is that both now go through the SAME population
// (MatchFor.CoveringDevice) and the SAME ranking (soonestExpiry), so they agree by construction rather than by
// anyone remembering to keep them in step — and the seal below proves that agreement on a Mac built to break it.
//
// ⚠️ Every identifier here is COINED: no real udid, Apple team id, certificate code or bundle id.

const (
	surfTeam   = "TEAMPLACEHOLD"
	surfWda    = "com.example.surfaces.WebDriverAgentRunner"
	surfRunner = surfWda + ".xctrunner"
	surfPhoneA = "UDID-SURF-A"
	surfPhoneB = "UDID-SURF-B"
	surfPhoneC = "UDID-SURF-UNCOVERED"
)

func surfProfile(name, expires string, devices []string) ios.Profile {
	return ios.Profile{
		Name: name, AppID: surfTeam + "." + surfRunner, TeamID: surfTeam,
		Expires: expires, ProvisionedDevices: devices,
	}
}

// surfProfileOfTeam is a profile belonging to some OTHER team — only reachable when no team is declared, which
// is exactly the case these seals are about.
func surfProfileOfTeam(team, name, expires string, devices []string) ios.Profile {
	return ios.Profile{
		Name: name, AppID: team + "." + surfRunner, TeamID: team,
		Expires: expires, ProvisionedDevices: devices,
	}
}

// surfEnvNoTeam declares NO team: ios.Match then keeps every profile covering the bundle id, whatever team it
// belongs to, and the profiles' own teams become the only place that fact can be read.
func surfEnvNoTeam(profiles ...ios.Profile) Env {
	return Env{
		IosWdaBundleID: surfWda,
		IosProfiles:    func(context.Context) ([]ios.Profile, error) { return profiles, nil },
	}
}

func surfSeen(udids ...string) []inventory.Outcome {
	offerable := make([]inventory.Offerable, 0, len(udids))
	for _, u := range udids {
		offerable = append(offerable, inventory.Offerable{ID: u, Manufacturer: "Apple"})
	}
	return []inventory.Outcome{inventory.Answered(inventory.IOS, offerable, nil)}
}

func surfEnv(profiles ...ios.Profile) Env {
	return Env{
		IosWdaBundleID: surfWda,
		IosTeamID:      surfTeam,
		IosProfiles:    func(context.Context) ([]ios.Profile, error) { return profiles, nil },
	}
}

// ── INJECTIONS 1 AND 3: one line cannot state one date when the phones differ ────────────────────────────────

func TestTwoPhonesCoveredByTwoProfilesAreGivenTwoDatesAndNotOne(t *testing.T) {
	// The Mac this slice exists for: two attached phones, each covered by a different profile, different dates.
	env := surfEnv(
		surfProfile("early profile", "2026-09-11T00:00:00Z", []string{surfPhoneA}),
		surfProfile("late profile", "2027-09-02T00:00:00Z", []string{surfPhoneB}),
	)

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneB))

	if c.State != Satisfied {
		t.Fatalf("both phones are covered, so the check is %q: got %q (%s)", Satisfied, c.State, c.Detail)
	}
	for _, want := range []string{surfPhoneA, "2026-09-11T00:00:00Z", surfPhoneB, "2027-09-02T00:00:00Z"} {
		if !strings.Contains(c.Detail, want) {
			t.Fatalf("this Mac has two attached iPhones covered by two DIFFERENT profiles with two DIFFERENT "+
				"expiry dates, and the doctor stated a single machine-level date. A person reading "+
				"`nth-agent doctor` is told one date that cannot be true of both phones, while the operator's "+
				"Devices grid — fed by the findings pipeline, which asks per device — shows the other. Two "+
				"surfaces, one phone, two dates. %q is missing from: %s", want, c.Detail)
		}
	}
}

// ⚠️ THE SEAL THIS SLICE EXISTS FOR: the two surfaces must agree, phone by phone.
func TestTheDoctorAndTheFindingAgreeAboutEveryPhoneOnTheSameMac(t *testing.T) {
	env := surfEnv(
		surfProfile("early profile", "2026-09-11T00:00:00Z", []string{surfPhoneA}),
		surfProfile("late profile", "2027-09-02T00:00:00Z", []string{surfPhoneB}),
	)
	now := mustParse(t, "2026-09-04T12:00:00Z")

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneB))
	perDevice := profileFindings(context.Background(), env, now)

	for _, udid := range []string{surfPhoneA, surfPhoneB} {
		f := perDevice(udid)
		if f.ExpiresAtUtc == nil {
			t.Fatalf("the finding for %s carries no date to compare against the doctor's line", udid)
		}
		stated := f.ExpiresAtUtc.Format("2006-01-02")
		if !strings.Contains(c.Detail, udid) || !strings.Contains(c.Detail, stated) {
			t.Fatalf("THE TWO SURFACES DISAGREE ABOUT %s. The findings pipeline — which is what reaches the "+
				"OPERATOR'S DEVICES GRID and, through the runner, a paying tenant — says this phone's covering "+
				"profile expires %s. The doctor's own line, which is what the CUSTOMER READS IN THEIR TERMINAL, "+
				"says: %s. Both are this package; they must go through the same population and the same ranking, "+
				"or a person holding one surface cannot trust the other.", udid, stated, c.Detail)
		}
	}
}

// ── INJECTION 2: the profile NAMED must be one that covers the phone it is named beside ──────────────────────

func TestTheProfileNamedForAPhoneIsOneThatCoversThatPhone(t *testing.T) {
	// The first profile enumerated covers only phone B; phone A is covered by the second.
	env := surfEnv(
		surfProfile("profile for B only", "2027-01-01T00:00:00Z", []string{surfPhoneB}),
		surfProfile("profile for A", "2026-12-01T00:00:00Z", []string{surfPhoneA}),
	)

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))

	if !strings.Contains(c.Detail, "profile for A") || strings.Contains(c.Detail, "profile for B only") {
		t.Fatalf("the doctor named the wrong profile as the one in play for this phone. It printed the FIRST "+
			"profile matching the bundle id and the team — which covers a different phone entirely — beside a "+
			"udid whose coverage was decided over every matching profile. A reader asking 'which profile is in "+
			"play for my phone' is handed one that has nothing to do with it, and would go and renew the wrong "+
			"file. got: %s", c.Detail)
	}
}

// ── the sentence when the phones do NOT differ, which is the operator's own Mac ──────────────────────────────

func TestOnePhoneOneProfileStillStatesItsNameAndItsDate(t *testing.T) {
	env := surfEnv(surfProfile("the only profile", "2027-09-02T00:00:00Z", []string{surfPhoneA}))

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))

	if c.State != Satisfied || !strings.Contains(c.Detail, "the only profile") ||
		!strings.Contains(c.Detail, "2027-09-02T00:00:00Z") || !strings.Contains(c.Detail, surfPhoneA) {
		t.Fatalf("the single-profile case is the shape of the operator's own Mac and the only one that has ever "+
			"reached a person; it must still name the profile, the phone and the date: got %q / %s", c.State, c.Detail)
	}
}

// ── among profiles that all cover ONE phone, the soonest is the one named ────────────────────────────────────

func TestAmongProfilesCoveringOnePhoneTheSoonestIsTheOneNamed(t *testing.T) {
	env := surfEnv(
		surfProfile("the long one", "2027-09-02T00:00:00Z", []string{surfPhoneA}),
		surfProfile("the short one", "2026-09-08T00:00:00Z", []string{surfPhoneA}),
	)

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))

	if !strings.Contains(c.Detail, "the short one") || !strings.Contains(c.Detail, "2026-09-08T00:00:00Z") {
		t.Fatalf("two profiles cover this phone and the doctor named the one that lapses LATER. The soonest is "+
			"the moment the phone stops working, and it is the same ranking the findings pipeline uses — naming "+
			"the other one tells a customer they have a year while their phone dies next week. got: %s", c.Detail)
	}
}

// ── the verbatim text, and what "not said" looks like on this surface ────────────────────────────────────────

func TestAnUnreadableExpiryIsShownAsWhatThePlistSaidAndNotAsADate(t *testing.T) {
	env := surfEnv(surfProfile("odd profile", "whenever", []string{surfPhoneA}))

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))

	if !strings.Contains(c.Detail, "whenever") || !strings.Contains(c.Detail, "cannot read that as a date") {
		t.Fatalf("ios.Profile.Expires is the plist value VERBATIM — what the doctor prints is what the plist "+
			"said — so an unreadable value must be shown as it was stored AND flagged as unreadable, never "+
			"silently dropped and never rendered as a date nobody stated. got: %s", c.Detail)
	}
}

func TestAProfileThatStatesNoExpiryAtAllSaysNotSaid(t *testing.T) {
	env := surfEnv(surfProfile("dateless profile", "", []string{surfPhoneA}))

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))

	if !strings.Contains(c.Detail, "(not said)") {
		t.Fatalf("a profile that states no expiry is NOT SAID, never 'does not expire' — the rule this package "+
			"keeps everywhere else. got: %s", c.Detail)
	}
}

// ── ITEM 3: the Missing branch names no profile, because for an uncovered phone there is none ────────────────

func TestTheUncoveredBranchNamesNoProfileBecauseNoneIsInPlay(t *testing.T) {
	env := surfEnv(
		surfProfile("profile for A", "2027-01-01T00:00:00Z", []string{surfPhoneA}),
		surfProfile("profile for B", "2027-01-01T00:00:00Z", []string{surfPhoneB}),
	)

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneC))

	if c.State != Missing || !strings.Contains(c.Detail, surfPhoneC) {
		t.Fatalf("an attached phone that no matching profile covers must turn the check %q and be named: got "+
			"%q / %s", Missing, c.State, c.Detail)
	}
	if strings.Contains(c.Detail, "profile for A") || strings.Contains(c.Detail, "profile for B") {
		t.Fatalf("the uncovered branch named a specific profile. For a phone that NOTHING covers there is no "+
			"'profile in play' to name, and naming the first bundle-and-team match tells a reader that one "+
			"particular file is failing them when the truth is that none of them lists their phone. got: %s",
			c.Detail)
	}
	if !strings.Contains(c.Detail, "2 profile(s)") {
		t.Fatalf("the honest machine-level fact is HOW MANY matching profiles there are and that none covers the "+
			"phone: got %s", c.Detail)
	}
}

// ── ITEM 1: after this slice ForTeam is a presence flag and nothing reads it as a description ────────────────

func TestForTeamIsReadOnlyAsAPresenceFlagInThisPackage(t *testing.T) {
	// ⚠️ CODE ONLY. The corrected comments in checks.go QUOTE the retired expressions in order to refute them, so
	// a seal that grepped the file would be reddened by the very paragraphs that fix it — the
	// seal-reads-its-own-explanation shape this programme has now met six times. This one caught it on its first
	// run, which is the only reason it is not in the shipped file.
	source := codeOnly(t, readFile(t, "checks.go"))
	for _, forbidden := range []string{"ForTeam.Name", "ForTeam.TeamID", "ForTeam.Expires"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("checks.go reads %s again. MatchFor.ForTeam is the FIRST profile matching the bundle id and "+
				"the team — its own comment calls it a claim about the MACHINE ('the build will work'), which is "+
				"a presence flag. Using it to NAME a profile or state its DATE is using a flag as a description, "+
				"and it is exactly how this surface came to disagree with the findings pipeline about the same "+
				"phone. The nil test at the top of the switch is what the field is for.", forbidden)
		}
	}
	if !strings.Contains(source, "match.ForTeam != nil") {
		t.Fatal("the nil test is ForTeam's one honest use and it has gone missing")
	}
}

// TestCodeOnlyKeepsCodeAndDropsCommentsWhateverEitherContains feeds codeOnly a PLANTED source rather than
// checks.go, because a helper tested only through the file it guards is tested only on the cases that file
// happens to contain today — which is how the scheme-separator hazard sat unnoticed while the docblock named two
// others. Every line below is a case the substring search this replaced got wrong or could get wrong.
func TestCodeOnlyKeepsCodeAndDropsCommentsWhateverEitherContains(t *testing.T) {
	// The forbidden token is spelled from parts so THIS test cannot be reddened by its own fixture: the pin above
	// searches checks.go, not this file, but a future census over the package would find a literal here and the
	// slice whose whole subject is pins reading their own explanation may not plant one.
	const forbidden = "ForTeam" + ".Name"
	planted := "" +
		"a := \"" + "http://127.0.0.1:4723" + "\" + " + forbidden + "\n" + // code AFTER a scheme separator in a literal
		"b := 1 // " + forbidden + "\n" + // a line comment
		"/*\n" + forbidden + "\n*/\n" + // a block comment
		"c := \"// not a comment: " + forbidden + "\"\n" // a literal that merely looks like a comment

	got := codeOnly(t, planted)

	// ⚠️ THE CASE THAT WAS SILENT. `a :=` carries a URL and then the expression; the old helper cut the line at
	// the scheme separator and handed every caller a file with the expression missing.
	if !strings.Contains(got, "a := ") {
		t.Fatalf("codeOnly dropped a line that carries a URL in a string literal. The scheme separator is not a "+
			"comment marker, and cutting there hides every byte after it — which on checks.go is the rest of a "+
			"remedy sentence, and on any future line would be the very expression a pin exists to forbid. The "+
			"failure direction is SILENCE: nothing reddens, the pin passes, and the file is never read. Got:\n%s", got)
	}
	if strings.Count(got, forbidden) != 2 {
		t.Fatalf("codeOnly must keep the expression where it is CODE and remove it where it is PROSE, and it kept "+
			"%d of the 4 planted occurrences instead of 2. Two are code — one after a URL literal, one inside a "+
			"string that merely looks like a comment — and two are comments, one of them a block comment the old "+
			"docblock assumed away rather than handled. A helper that keeps a comment lets a correction redden the "+
			"pin that the correction fixes; one that drops code lets the forbidden expression back in unseen. "+
			"Got:\n%s", strings.Count(got, forbidden), got)
	}
	// …and the code that surrounded the comments is still there, so this is not passing by deleting everything.
	for _, kept := range []string{"b := 1", "c := "} {
		if !strings.Contains(got, kept) {
			t.Fatalf("codeOnly removed %q along with the comment beside it. Blanking a comment must leave the "+
				"statement it sits on intact, or a pin keyed on this helper is reading a file with holes in it "+
				"nobody put there. Got:\n%s", kept, got)
		}
	}
}

// codeOnly returns the file with every COMMENT blanked out, so a pin keyed on a forbidden expression is never
// reddened by the paragraph that explains why the expression was retired.
//
// ⚠️ THE INSTRUMENT IS go/scanner, AND THE SUBSTRING SEARCH IT REPLACES FAILED SILENTLY. The old body was
// `if i := strings.Index(line, "//"); i >= 0 { line = line[:i] }` — a scanner's job done with a substring search.
// Measured 2026-09-04: checks.go carries a URL inside a string literal (the Appium remedy at checks.go:203,
// `http` followed by the scheme separator), and that search cut the line at the scheme and hid everything after
// it from every check keyed on this helper. THE FAILURE DIRECTION WAS SILENCE: nothing reddened, and a forbidden
// expression written on a line that also carried a URL would simply have passed. go/scanner tokenises, so it
// knows a string literal from a comment and answers the question the search was only guessing at.
//
// ⚠️ AND THE DOCBLOCK THAT STOOD HERE NAMED ASSUMPTIONS THIS FILE'S SUBJECT DOES NOT HAVE. It recorded that
// checks.go uses no block comments (true — measured, zero) and something about comment-only lines inside string
// literals, and NEITHER was the hazard actually present. The scheme separator in a literal was, and no sentence
// mentioned it. With go/scanner both assumptions stop mattering: a block comment is a COMMENT token and is
// blanked, a literal is a STRING token and is kept, whatever either contains.
//
// ⚠️ THE RULE FOR THE NEXT PIN, AND IT IS WHY THIS HELPER EXISTS AT ALL. Eight slices have shipped a pin that
// read its own explanation: the correction quotes the sentence it retires, and the pin forbidding that sentence
// is reddened by the paragraph that fixes it. The remedy is not "write the correction differently" — a person
// writing a source comment never opens this file. It is a property of the PIN, chosen when the pin is written,
// which is before the correction exists: A PIN THAT FORBIDS AN EXPRESSION MUST EITHER READ CODE ONLY (this
// helper) OR REQUIRE THE CORRECTION'S PRESENCE as well as the old text's absence. A pin keyed on absence alone,
// against prose, is the shape that has now failed eight times.
//
// Comments are blanked in place rather than deleted so byte offsets stay valid for the tokens after them.
func codeOnly(t *testing.T, source string) string {
	t.Helper()
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(source))
	var errs int
	var sc scanner.Scanner
	sc.Init(file, []byte(source), func(token.Position, string) { errs++ }, scanner.ScanComments)

	out := []byte(source)
	comments := 0
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		comments++
		off := fset.Position(pos).Offset
		for i := off; i < off+len(lit) && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}

	// ⚠️ VACUITY. A scanner that could not read the file, or one that found no comments in a Go source that has
	// hundreds, has answered nothing — and a helper that answers nothing hands every caller a green.
	if errs > 0 {
		t.Fatalf("codeOnly could not tokenise its input (%d scan error(s)); every pin keyed on this helper is "+
			"reading a file the scanner could not parse, which is a green that means nothing", errs)
	}
	if comments == 0 {
		t.Fatal("codeOnly blanked no comments at all. Every Go file this pins carries them, so either the input " +
			"is not the file the caller meant to read or the scan stopped early — both make the pins above pass " +
			"on evidence nobody produced")
	}
	return string(out)
}

// ── THE TEAM ON THESE TWO LINES IS THE PROFILE'S OWN, NOT THE DECLARED ONE ───────────────────────────────────
//
// ⚠️ BOTH DEFECTS THESE SEAL WERE INTRODUCED BY THE SLICE THAT REWROTE THESE LINES, AND THE OPERATOR FOUND THEM.
// The rewrite put `declared(env)` into a "(team %s)" pattern. `declared` answers "what did you declare" and
// returns the phrase "no team" when nothing was — which reads correctly in "…and you declared no team" and as
// "(team no team)" here. And it dropped a fact: with no team declared, ios.Match accepts every profile covering
// the bundle id whatever team it belongs to, so the PROFILE's team is the thing a person needs. The lines it
// replaced printed exactly that (match.ForTeam.TeamID), and this check's own OtherTeams branch calls naming a
// profile's real team "the most useful sentence this machine can say".

func TestNoTeamDeclaredStillReadsTheProfilesOwnTeamOnTheSatisfiedLine(t *testing.T) {
	const otherTeam = "OTHERTEAMPLCE"
	env := surfEnvNoTeam(surfProfileOfTeam(otherTeam, "someone else's profile", "2027-09-02T00:00:00Z", []string{surfPhoneA}))

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))

	if !strings.Contains(c.Detail, otherTeam) {
		t.Fatalf("no team was declared, and the line no longer says which team the profile covering this phone "+
			"belongs to. That is the fact a person needs — this very check calls naming a profile's real team "+
			"'the most useful sentence this machine can say' in its OtherTeams branch — and with nothing declared "+
			"it is the ONLY place the team can be read, because ios.Match kept every profile covering the bundle "+
			"id whatever team it belongs to. got: %s", c.Detail)
	}
}

func TestNoTeamDeclaredStillReadsTheProfilesOwnTeamOnTheUncoveredLine(t *testing.T) {
	const otherTeam = "OTHERTEAMPLCE"
	env := surfEnvNoTeam(surfProfileOfTeam(otherTeam, "someone else's profile", "2027-09-02T00:00:00Z", []string{surfPhoneA}))

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneC))

	if c.State != Missing {
		t.Fatalf("one attached phone is covered by nothing, so the check is %q: got %q", Missing, c.State)
	}
	if !strings.Contains(c.Detail, otherTeam) {
		t.Fatalf("the uncovered line no longer says which team the matching profiles belong to. With no team "+
			"declared this is the only place that fact exists, and a person looking at a phone xcodebuild will "+
			"refuse needs to know whose profiles are on the disk. got: %s", c.Detail)
	}
}

func TestNeitherLineEverPrintsTeamNoTeam(t *testing.T) {
	// ⚠️ THE EXACT STRING THE DEFECT PRODUCED. `declared(env)` returns the PHRASE "no team", which slots into
	// "you declared %s" and into nothing else.
	env := surfEnvNoTeam(surfProfile("a profile", "2027-09-02T00:00:00Z", []string{surfPhoneA}))

	satisfied := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA))
	uncovered := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneC))

	for _, c := range []Check{satisfied, uncovered} {
		if strings.Contains(c.Detail, "team no team") {
			t.Fatalf("the line printed \"(team no team)\" — `declared(env)`'s phrase for 'nothing was declared' "+
				"dropped into a slot that wanted a team id. A sentence that reads as nonsense is a sentence a "+
				"person stops reading, and this one is on the surface a customer runs in their own terminal. "+
				"got: %s", c.Detail)
		}
	}
}

func TestTwoPhonesCoveredByProfilesOfTwoTeamsEachCarryItsOwn(t *testing.T) {
	// Only reachable with no team declared — and the case a single machine-level team could not state at all.
	const teamOne, teamTwo = "TEAMONEPLCE", "TEAMTWOPLCE"
	env := surfEnvNoTeam(
		surfProfileOfTeam(teamOne, "first team's profile", "2027-01-01T00:00:00Z", []string{surfPhoneA}),
		surfProfileOfTeam(teamTwo, "second team's profile", "2027-02-01T00:00:00Z", []string{surfPhoneB}),
	)

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneB))

	if !strings.Contains(c.Detail, teamOne) || !strings.Contains(c.Detail, teamTwo) {
		t.Fatalf("two phones are covered by profiles belonging to two DIFFERENT teams and the line named fewer "+
			"than both. The team is a property of the profile, not of the machine, and the profile is already "+
			"named per phone — so each clause carries its own. got: %s", c.Detail)
	}
}

func TestAProfileStatingNoTeamAtAllSaysSoRatherThanLeavingAGap(t *testing.T) {
	env := surfEnvNoTeam(surfProfileOfTeam("", "teamless profile", "2027-09-02T00:00:00Z", []string{surfPhoneA}))

	c := iosProvisioningProfile(context.Background(), env, surfSeen(surfPhoneA, surfPhoneC))

	if !strings.Contains(c.Detail, "(none stated)") {
		t.Fatalf("a profile that carries no team must say so rather than print an empty gap — the same not-said "+
			"rule this file keeps for a date. got: %s", c.Detail)
	}
}
