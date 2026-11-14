package ios

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os/exec"
	"regexp"
	"strings"
)

// THE TEN CHARACTERS THAT LOOK EXACTLY LIKE A TEAM ID AND ARE NOT ONE.
//
// `security find-identity -v -p codesigning` prints one line per code-signing identity this Mac holds:
//
//	1) 0A1B…9F "Apple Development: dev@example.com (CERT8KD3M1)"
//	   1 valid identities found
//
// The parenthesised code belongs to the CERTIFICATE. The Apple TEAM is what a provisioning profile's
// TeamIdentifier carries — on the machine this was measured on (2026-08-25) the certificate said CERT8KD3M1 while
// both profiles said TEAM7QZ4X2, and passing the certificate's code as the team is what made xcodebuild answer
// `No Account for Team "CERT8KD3M1"`. Our own documentation warns about this trap in a boxed note; until this
// file nothing in the agent had ever run the command that proves it, so the doctor knew the right answer and
// could not say WHY the wrong one had been reached.

// Identity is one code-signing identity this Mac can sign with — the common name as printed, and the
// parenthesised code inside it when there is one.
//
// CertCode is EMPTY when the name carries no parenthesised code (an enterprise or distribution certificate whose
// CN is just the organisation). Empty means "this identity has no such code", never "the code is unknown" — a
// distinction the matcher below depends on, because an empty declared team must never look like a match.
type Identity struct {
	Name     string // "Apple Development: dev@example.com (CERT8KD3M1)"
	CertCode string // "CERT8KD3M1" — the CERTIFICATE's, not the team's
	// TeamID is the APPLE TEAM this certificate really belongs to — its subject's Organizational Unit, which is
	// where Apple puts it. MEASURED on the machine this was written for: the CN said (CERT8KD3M1) and the same
	// certificate's subject said OU=TEAM7QZ4X2, which is what both provisioning profiles carry and what
	// xcodebuild wanted. `security find-identity` does not print the subject, so this costs one more read per
	// identity — and it is what lets the check answer "can this Mac sign as the team you declared" instead of
	// printing a list and leaving the person to compare ten characters by eye.
	//
	// EMPTY = COULD NOT BE READ, never "no team". A caller may not treat an unread OU as a mismatch: absence of
	// an answer is not a negative answer, which is the discipline every check in this agent keeps.
	TeamID string
}

// identityLine matches one line of `security find-identity -v -p codesigning` output: an index, a SHA-1, then the
// quoted common name. The trailing summary line ("1 valid identities found") carries no quotes and never matches.
var identityLine = regexp.MustCompile(`^\s*\d+\)\s+[0-9A-Fa-f]+\s+"([^"]*)"`)

// certCode pulls the parenthesised code out of a common name. Anchored at the END, because an Apple CN puts it
// last and a person's own name may contain parentheses.
var certCode = regexp.MustCompile(`\(([0-9A-Za-z]{6,})\)\s*$`)

// Identities reads what this Mac can sign as. A READ, reported — it establishes what is on THIS machine and
// nothing whatever about the person's Apple account, which is the boundary item 3 of
// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS draws: absence here means Xcode cannot sign as that team HERE AND NOW,
// never that the team is not theirs.
//
// An error is returned as an error — `security` missing, or refusing to read the keychain, is NOT "no identities"
// and the caller must be able to tell those apart (the doctor renders the first as Unknown and says which).
func Identities(ctx context.Context) ([]Identity, error) {
	out, err := exec.CommandContext(ctx, "security", "find-identity", "-v", "-p", "codesigning").CombinedOutput()
	if err != nil {
		return nil, err
	}
	var found []Identity
	for _, line := range strings.Split(string(out), "\n") {
		m := identityLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" {
			continue
		}
		id := Identity{Name: name, TeamID: certificateTeam(ctx, name)}
		if code := certCode.FindStringSubmatch(name); code != nil {
			id.CertCode = code[1]
		}
		found = append(found, id)
	}
	return found, nil
}

// CertCodeMatches reports whether any identity's CERTIFICATE code equals the declared value — the sentence that
// ends the evening this file was written for. An empty declared value matches nothing: "" is not a code.
func CertCodeMatches(identities []Identity, declared string) bool {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return false
	}
	for _, id := range identities {
		if id.CertCode != "" && strings.EqualFold(id.CertCode, declared) {
			return true
		}
	}
	return false
}

// certificateTeam reads ONE identity's real Apple team out of its certificate subject (OU). Empty on any failure,
// deliberately: this is a best-effort second read, and a check that cannot get it must say so rather than treat
// silence as a mismatch.
//
// Nothing is installed and nothing is parsed by hand — `security find-certificate -p` prints the PEM the keychain
// already holds and Go's own x509 reads the subject. The CN is passed as an argument, never through a shell.
func certificateTeam(ctx context.Context, commonName string) string {
	out, err := exec.CommandContext(ctx, "security", "find-certificate", "-c", commonName, "-p").Output()
	if err != nil {
		return ""
	}
	rest := out
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return ""
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if cert.Subject.CommonName != commonName || len(cert.Subject.OrganizationalUnit) == 0 {
			continue
		}
		return strings.TrimSpace(cert.Subject.OrganizationalUnit[0])
	}
}

// TeamsHere is every distinct APPLE TEAM this Mac holds a certificate for, in the order first seen. Empty when no
// identity's team could be read — which a caller must render as "could not establish", never as "none".
func TeamsHere(identities []Identity) []string {
	var teams []string
	seen := map[string]bool{}
	for _, id := range identities {
		team := strings.TrimSpace(id.TeamID)
		if team == "" || seen[strings.ToUpper(team)] {
			continue
		}
		seen[strings.ToUpper(team)] = true
		teams = append(teams, team)
	}
	return teams
}

// CanSignAsTeam reports whether some identity here belongs to the declared team. The second return says whether
// the question could be ANSWERED AT ALL: with no readable OU anywhere, "false" would be a verdict this Mac never
// established, and the caller must render Unknown instead of a refusal.
func CanSignAsTeam(identities []Identity, declared string) (canSign, established bool) {
	declared = strings.TrimSpace(declared)
	teams := TeamsHere(identities)
	if declared == "" || len(teams) == 0 {
		return false, false
	}
	for _, team := range teams {
		if strings.EqualFold(team, declared) {
			return true, true
		}
	}
	return false, true
}
