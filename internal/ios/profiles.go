package ios

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ProvisioningProfileDirs are the two places Xcode has kept .mobileprovision files. The classic path is where
// every Xcode up to 15 wrote them; Xcode 16 moved them under the user's Xcode data. BOTH ARE READ, because a Mac
// that has run both versions has profiles in both, and a check that knew only one would report "missing" about a
// machine that is ready. Read-only: nothing here writes, installs, or asks Apple for anything.
func ProvisioningProfileDirs(home string) []string {
	return []string{
		filepath.Join(home, "Library", "MobileDevice", "Provisioning Profiles"),
		filepath.Join(home, "Library", "Developer", "Xcode", "UserData", "Provisioning Profiles"),
	}
}

// Profile is what one .mobileprovision says about itself, in the four fields a signing check needs.
//
// MEASURED ON A REAL MACHINE (2026-08-24) AND IT IS WHY TeamID EXISTS AS A SEPARATE FIELD: a profile's
// application-identifier is TEAMID.bundle.id, and the TEAM there is the APPLE TEAM — which is NOT the ten
// characters printed in a certificate's common name. On this Mac the one identity is
// `Apple Development: <email> (CERT8KD3M1)` while the certificate's subject carries `OU=TEAM7QZ4X2`, and both
// profiles say `TeamIdentifier: TEAM7QZ4X2`. Passing the CN's value as xcodeOrgId is what made xcodebuild answer
// "No profiles for '<bundle>.xctrunner' were found" WHILE A PROFILE FOR THAT EXACT BUNDLE ID SAT ON DISK. A check
// that read only the bundle id said "satisfied" about a build that could not run.
type Profile struct {
	Name    string // "iOS Team Provisioning Profile: com.example.WebDriverAgentRunner.xctrunner"
	AppID   string // the application-identifier entitlement: TEAMID.bundle.id (possibly ending in *)
	TeamID  string // TeamIdentifier — the APPLE TEAM, the value DEVELOPMENT_TEAM / xcodeOrgId wants
	Expires string // ExpirationDate, verbatim; a free Personal Team's profile lasts SEVEN DAYS
	// ProvisionedDevices is the udid list a DEVELOPMENT profile carries — its whole job is to say which devices
	// it covers, and the check that never opened this list said "ready" on a Mac where xcodebuild refused the
	// attached phone BY NAME (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE; Apple's words: "doesn't include the
	// currently selected device"). Nil when the plist carries no such array — a distribution/enterprise profile,
	// which does not restrict by device — and nil is therefore "covers any device", not "covers none".
	ProvisionedDevices []string
}

var (
	appIdentifier = regexp.MustCompile(`(?s)<key>application-identifier</key>\s*<string>([^<]+)</string>`)
	profileName   = regexp.MustCompile(`(?s)<key>Name</key>\s*<string>([^<]*)</string>`)
	teamIdentifer = regexp.MustCompile(`(?s)<key>TeamIdentifier</key>\s*<array>\s*<string>([^<]*)</string>`)
	expiration    = regexp.MustCompile(`(?s)<key>ExpirationDate</key>\s*<date>([^<]*)</date>`)
	// One more regex beside the four already here — the same decoded plist, the field nobody read.
	provisionedDevices = regexp.MustCompile(`(?s)<key>ProvisionedDevices</key>\s*<array>(.*?)</array>`)
	deviceEntry        = regexp.MustCompile(`<string>([^<]+)</string>`)
)

// devices extracts the ProvisionedDevices udids, nil when the plist carries no such array.
func devices(text string) []string {
	m := provisionedDevices.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var out []string
	for _, entry := range deviceEntry.FindAllStringSubmatch(m[1], -1) {
		out = append(out, strings.TrimSpace(entry[1]))
	}
	return out
}

func first(re *regexp.Regexp, text string) string {
	if m := re.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// Profiles returns every provisioning profile on this machine, decoded with the OS's own `security cms -D`. A
// directory that does not exist contributes nothing and is not an error — a Mac with no profiles is a real
// answer. An error is returned only when a profile was found and COULD NOT be read, because that is the
// difference between "there are none" and "I could not tell".
func Profiles(ctx context.Context, dirs []string) ([]Profile, error) {
	var found []Profile
	var firstErr error
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // no such directory: nothing here, not a failure
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mobileprovision") {
				continue
			}
			out, err := exec.CommandContext(ctx, "security", "cms", "-D", "-i", filepath.Join(dir, entry.Name())).Output()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			text := string(out)
			found = append(found, Profile{
				Name:               first(profileName, text),
				AppID:              first(appIdentifier, text),
				TeamID:             first(teamIdentifer, text),
				Expires:            first(expiration, text),
				ProvisionedDevices: devices(text),
			})
		}
	}
	if len(found) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return found, nil
}

// WdaRunnerBundleID is the bundle id an XCUITest session actually needs a profile for. NOT the bundle id a person
// configures: xcodebuild appends `.xctrunner` to the test runner's bundle id, and the error a missing profile
// produces names the SUFFIXED one — measured on this machine:
//
//	error: No profiles for 'com.example.WebDriverAgentRunner.xctrunner' were found
//
// A check that asked about the bare id would say "satisfied" for a machine whose build is about to fail.
func WdaRunnerBundleID(configured string) string {
	if configured == "" {
		return ""
	}
	if strings.HasSuffix(configured, ".xctrunner") {
		return configured
	}
	return configured + ".xctrunner"
}

// Covers reports whether this profile may sign the given bundle id. An application-identifier is TEAMID.bundle.id
// and may end in a wildcard, which is what a "Team Provisioning Profile" often carries; both forms are accepted,
// and neither the team prefix nor a wildcard is invented here — they are read off the profile.
func (p Profile) Covers(bundleID string) bool {
	dot := strings.Index(p.AppID, ".")
	if dot < 0 {
		return false
	}
	pattern := p.AppID[dot+1:]
	if pattern == bundleID {
		return true
	}
	return strings.HasSuffix(pattern, "*") && strings.HasPrefix(bundleID, strings.TrimSuffix(pattern, "*"))
}

// CoversDevice reports whether this profile covers ONE device. A profile with no ProvisionedDevices array does
// not restrict by device and covers any (a distribution/enterprise profile cannot refuse a udid it never lists);
// a development profile covers exactly the udids it names, compared case-insensitively because Apple prints them
// in either case.
func (p Profile) CoversDevice(udid string) bool {
	if p.ProvisionedDevices == nil {
		return true
	}
	for _, d := range p.ProvisionedDevices {
		if strings.EqualFold(strings.TrimSpace(d), strings.TrimSpace(udid)) {
			return true
		}
	}
	return false
}

// MatchFor is what the profiles on this machine say about ONE bundle id and ONE team — the two-part question a
// build actually asks, kept together because answering half of it is what produced a "satisfied" check over a
// build that could not run.
type MatchFor struct {
	// ForTeam is a profile that covers the bundle id AND belongs to the declared team: the build will work.
	ForTeam *Profile
	// ForTeamAll is EVERY profile that covers the bundle id for the team, in file order — the population a
	// per-device question walks: the first bundle+team match may exclude a udid another covers
	// (THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE), so a device answer over ForTeam alone would be answering about
	// one profile while claiming to answer about the machine.
	ForTeamAll []Profile
	// OtherTeams are profiles that cover the bundle id but belong to a DIFFERENT team. Their presence is the
	// single most useful thing this machine can say when a build fails: the profile is there, the team is wrong,
	// and the right team id is right here.
	OtherTeams []Profile
}

// CoveringDevice is EVERY bundle-and-team-matching profile that covers this device — the population that any
// per-device question about this phone must be asked over.
//
// ⚠️ IT EXISTS BECAUSE THE POPULATION WAS ALREADY IN THIS STRUCT AND ONE CONSUMER LOOKED PAST IT. ForTeamAll's
// own comment says it is "the population a device answer must be taken over", and CoversDevice below has always
// walked it — but the doctor's provisioning finding took its EXPIRY DATE from ForTeam, the first bundle-and-team
// match, coverage never asked. The state word was right and the date beside it was a fact about a different
// profile. Returning the profiles rather than a boolean is what lets the date be asked of the same set the
// verdict came from.
func (m MatchFor) CoveringDevice(udid string) []Profile {
	var covering []Profile
	for _, p := range m.ForTeamAll {
		if p.CoversDevice(udid) {
			covering = append(covering, p)
		}
	}
	return covering
}

// CoversDevice reports whether ANY bundle-and-team-matching profile covers this device.
//
// ONE RULE, ONE PLACE: it is CoveringDevice reduced to a boolean rather than a second walk of the same slice,
// because two implementations of "does this profile cover this phone" are two chances to disagree — which is the
// shape that produced the defect above, one field over.
func (m MatchFor) CoversDevice(udid string) bool {
	return len(m.CoveringDevice(udid)) > 0
}

// Match answers "may this machine sign <bundleID> as <teamID>", and if not, what it has instead. A blank teamID
// asks only the bundle-id half and every covering profile counts as a match — the honest degradation for a caller
// that did not say which team it will build as.
func Match(profiles []Profile, bundleID, teamID string) MatchFor {
	var out MatchFor
	for _, p := range profiles {
		if !p.Covers(bundleID) {
			continue
		}
		if teamID == "" || strings.EqualFold(strings.TrimSpace(p.TeamID), strings.TrimSpace(teamID)) {
			if out.ForTeam == nil {
				match := p
				out.ForTeam = &match
			}
			out.ForTeamAll = append(out.ForTeamAll, p)
			continue
		}
		out.OtherTeams = append(out.OtherTeams, p)
	}
	return out
}
