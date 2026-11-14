package doctor

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
)

// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS — an evening went by on a machine that had already read every fact the
// operator needed. Every value here is that evening's, verbatim: the declared team CERT8KD3M1 (which is the code
// inside the certificate's own name), the real team TEAM7QZ4X2 that both profiles carry, and the identity
// "Apple Development: dev@example.com (CERT8KD3M1)".

const (
	declaredCertCode = "CERT8KD3M1"                                      // what was in NTH_AGENT_IOS_TEAM_ID
	realTeam         = "TEAM7QZ4X2"                                      // what the profiles carry
	theIdentity      = "Apple Development: dev@example.com (CERT8KD3M1)" // the one identity on that Mac
)

func iphoneSeen() []inventory.Outcome {
	return []inventory.Outcome{inventory.Answered(inventory.IOS, []inventory.Offerable{{ID: "00008130-0000C0FFEE0000C3", Model: "iPhone"}}, nil)}
}

func identitiesHere(ids ...ios.Identity) func(context.Context) ([]ios.Identity, error) {
	return func(context.Context) ([]ios.Identity, error) { return ids, nil }
}

// theOperatorsIdentity is that Mac's one identity, as `security` prints it and as its certificate's subject reads.
func theOperatorsIdentity() ios.Identity {
	return ios.Identity{Name: theIdentity, CertCode: declaredCertCode, TeamID: realTeam}
}

func checkNamed(r Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{Name: "<" + name + " not in report>"}
}

// ── seal 1 — say WHERE you read it ──────────────────────────────────────────────────────────────────────────

func TestSeal1_ATeamFromTheEnvironmentIsReportedWithTheVariablesName(t *testing.T) {
	env := Env{
		IosTeamID:       declaredCertCode,
		IosTeamIDSource: "NTH_AGENT_IOS_TEAM_ID",
		IosWdaBundleID:  "com.example.WebDriverAgentRunner",
		IosProfiles: func(context.Context) ([]ios.Profile, error) {
			return []ios.Profile{{Name: "iOS Team Provisioning Profile", AppID: realTeam + ".com.example.WebDriverAgentRunner.xctrunner", TeamID: realTeam, Expires: "2026-09-01T21:07:57Z"}}, nil
		},
	}

	c := iosProvisioningProfile(context.Background(), env, iphoneSeen())

	if !strings.Contains(c.Detail, "from NTH_AGENT_IOS_TEAM_ID") {
		t.Fatalf("the sentence must say WHERE the value was read; got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, declaredCertCode) {
		t.Fatalf("and still say WHAT was declared; got %q", c.Detail)
	}
}

func TestSeal1_ATeamFromTheFlagIsReportedAsTheFlag(t *testing.T) {
	env := Env{
		IosTeamID:       declaredCertCode,
		IosTeamIDSource: "--ios-team-id",
		IosWdaBundleID:  "com.example.WebDriverAgentRunner",
		IosProfiles: func(context.Context) ([]ios.Profile, error) {
			return []ios.Profile{{Name: "iOS Team Provisioning Profile", AppID: realTeam + ".com.example.WebDriverAgentRunner.xctrunner", TeamID: realTeam}}, nil
		},
	}

	c := iosProvisioningProfile(context.Background(), env, iphoneSeen())

	if !strings.Contains(c.Detail, "from --ios-team-id") {
		t.Fatalf("a flag-given team must be reported as the flag; got %q", c.Detail)
	}
	if strings.Contains(c.Detail, "NTH_AGENT_IOS_TEAM_ID") {
		t.Fatalf("and must not name the variable it did not come from; got %q", c.Detail)
	}
}

// ── seal 2 — the remedy names the variable THIS program reads ───────────────────────────────────────────────

func TestSeal2_TheProvisioningRemedyNamesTheVariableTheAgentActuallyReads(t *testing.T) {
	env := Env{
		IosTeamID:       declaredCertCode,
		IosTeamIDSource: "NTH_AGENT_IOS_TEAM_ID",
		IosWdaBundleID:  "com.example.WebDriverAgentRunner",
		IosProfiles: func(context.Context) ([]ios.Profile, error) {
			return []ios.Profile{{Name: "iOS Team Provisioning Profile", AppID: realTeam + ".com.example.WebDriverAgentRunner.xctrunner", TeamID: realTeam}}, nil
		},
	}

	c := iosProvisioningProfile(context.Background(), env, iphoneSeen())

	if !strings.Contains(c.Remedy, "NTH_AGENT_IOS_TEAM_ID") {
		t.Fatalf("the remedy must name the variable the agent reads; got %q", c.Remedy)
	}
	if strings.Contains(c.Remedy, "IOS_XCODE_ORG_ID") {
		t.Fatalf("it must not offer a variable this program never reads — set it and nothing changes; got %q", c.Remedy)
	}
	if !strings.Contains(c.Remedy, realTeam) {
		t.Fatalf("and it must still name the real team the profile carries; got %q", c.Remedy)
	}
}

// ── seal 3 — no remedy names a variable this program does not read (source walk) ────────────────────────────

// envVarShaped matches an ALL_CAPS token of the shape an environment variable takes. Deliberately loose: the seal
// is that a FOURTH variable cannot slip into a sentence, so the walk must catch names nobody has thought of yet.
var envVarShaped = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+){1,}\b`)

func TestSeal3_NoRemedyNamesAVariableThisProgramDoesNotRead(t *testing.T) {
	root := filepath.Join("..", "..")
	read := envVarsThisProgramReads(t, root)
	// INSTRUMENT GUARD (population, not classifier): the sweep must really have found the reads.
	if !read["NTH_AGENT_IOS_TEAM_ID"] || !read["NTH_AGENT_TOKEN"] {
		t.Fatalf("instrument guard: the os.Getenv sweep found %v — it is reading the wrong tree", read)
	}

	// Words that are ALL_CAPS_SHAPED and are not environment variables. Each is here because it appears in a real
	// sentence, and each is a NAME OF SOMETHING ELSE — a build setting, a plist key, a product name.
	notVariables := map[string]bool{
		"DEVELOPMENT_TEAM": true, // an xcodebuild build setting, named because a person sets it in Xcode
	}

	source := readFile(t, filepath.Join(root, "internal", "doctor", "checks.go"))
	for _, line := range strings.Split(stripGoComments(source), "\n") {
		for _, token := range envVarShaped.FindAllString(line, -1) {
			if notVariables[token] || read[token] {
				continue
			}
			t.Fatalf("a sentence names %q, which this program never reads — a remedy that names a variable nobody "+
				"reads is the defect this seal exists for (line: %s)", token, strings.TrimSpace(line))
		}
	}
}

// envVarsThisProgramReads is every name passed to os.Getenv anywhere in the agent — the population a sentence may
// name. An AST walk rather than a regex: a name built by concatenation is not a name this program reads.
func envVarsThisProgramReads(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Getenv" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				found[strings.Trim(lit.Value, `"`)] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found
}

// ── seals 4, 5, 6 — the identity read: what it claims, what it refuses, and how absence is reported ─────────

func TestSeal5_ATeamIdThatIsReallyACertificatesCodeIsNamedAsOne(t *testing.T) {
	env := Env{
		IosTeamID:       declaredCertCode,
		IosTeamIDSource: "NTH_AGENT_IOS_TEAM_ID",
		IosIdentities:   identitiesHere(theOperatorsIdentity()),
	}

	c := iosSigningIdentity(context.Background(), env, iphoneSeen())

	if !strings.Contains(c.Detail, "CERTIFICATE's code, not an Apple team id") {
		t.Fatalf("the sentence that ends the evening was not said; got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "from NTH_AGENT_IOS_TEAM_ID") {
		t.Fatalf("and it must say where the wrong value was read; got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, theIdentity) {
		t.Fatalf("and what this Mac can sign as; got %q", c.Detail)
	}
}

func TestSeal4_ADeclaredTeamWithNoIdentityHereIsReportedWithoutClaimingItIsNotTheirs(t *testing.T) {
	// A team this Mac holds no certificate for — and the certificate it DOES hold belongs to another team.
	env := Env{
		IosTeamID:       "ZZ9PLURAL9",
		IosTeamIDSource: "--ios-team-id",
		IosIdentities:   identitiesHere(theOperatorsIdentity()),
	}

	c := iosSigningIdentity(context.Background(), env, iphoneSeen())

	if c.State != Missing {
		t.Fatalf("Xcode cannot sign as it here, so this stops a run: got %q", c.State)
	}
	if !strings.Contains(c.Detail, "no certificate on this Mac belongs to") {
		t.Fatalf("the absence must be reported as such; got %q", c.Detail)
	}
	// THE LINE THIS SEAL EXISTS FOR: a keychain read may not become a verdict on somebody's Apple account.
	if !strings.Contains(c.Detail, "says nothing about which teams the Apple account owns") {
		t.Fatalf("the sentence must refuse the claim it is not entitled to make; got %q", c.Detail)
	}
	for _, forbidden := range []string{"is not your team", "not yours", "does not belong to you", "you do not own"} {
		if strings.Contains(strings.ToLower(c.Detail+" "+c.Remedy), forbidden) {
			t.Fatalf("the sentence claims the team is not the person's (%q); got %q / %q", forbidden, c.Detail, c.Remedy)
		}
	}
}

func TestSeal4_AnUnreadableCertificateSubjectIsNotAMismatch(t *testing.T) {
	// `security` answered, but no certificate's OU could be read — so which team it belongs to is UNESTABLISHED,
	// and "no identity for your team" would be a verdict this Mac never reached.
	env := Env{
		IosTeamID:       realTeam,
		IosTeamIDSource: "NTH_AGENT_IOS_TEAM_ID",
		IosIdentities:   identitiesHere(ios.Identity{Name: theIdentity, CertCode: declaredCertCode}),
	}

	c := iosSigningIdentity(context.Background(), env, iphoneSeen())

	if c.State != Unknown {
		t.Fatalf("an unestablished answer is Unknown, never a refusal: got %q — %q", c.State, c.Detail)
	}
	if !strings.Contains(c.Detail, "could not be read") {
		t.Fatalf("and it must say what it could not establish; got %q", c.Detail)
	}
}

func TestSeal6_SecurityUnavailableOrRefusingIsUnknownAndSaysWhich(t *testing.T) {
	for _, tc := range []struct{ name, err, want string }{
		{"not installed", "exec: \"security\": executable file not found in $PATH", "executable file not found"},
		{"refused", "SecKeychainSearchCopyNext: User interaction is not allowed.", "User interaction is not allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Env{
				IosTeamID:     declaredCertCode,
				IosIdentities: func(context.Context) ([]ios.Identity, error) { return nil, errors.New(tc.err) },
			}

			c := iosSigningIdentity(context.Background(), env, iphoneSeen())

			if c.State != Unknown {
				t.Fatalf("a machine that could not be asked has not answered no: got %q", c.State)
			}
			if c.State.Blocking() {
				t.Fatalf("and it must not be counted as a blocker")
			}
			if !strings.Contains(c.Detail, tc.want) {
				t.Fatalf("it must say WHICH: got %q, want it to name %q", c.Detail, tc.want)
			}
		})
	}
}

func TestSeal6_ABuildThatCannotAskIsUnknownToo(t *testing.T) {
	c := iosSigningIdentity(context.Background(), Env{IosTeamID: declaredCertCode}, iphoneSeen())

	if c.State != Unknown {
		t.Fatalf("no seam ⇒ Unknown, never Missing: got %q", c.State)
	}
}

// ── seal 7 — the WDA-project check's state and the blocker count agree ──────────────────────────────────────

func TestSeal7_TheWdaProjectCheckStatesAFactAndIsNotCountedAsABlocker(t *testing.T) {
	// The project stores no team on the library target, and a team IS declared — so both real builds pass it on
	// the command line and the stored value cannot stop a run here.
	env := Env{
		IosTeamID:       realTeam,
		IosTeamIDSource: "NTH_AGENT_IOS_TEAM_ID",
		WdaProjectPath:  "/tmp/WebDriverAgent.xcodeproj",
		Run: func(_ context.Context, _ string, _ ...string) (string, error) {
			return "    DEVELOPMENT_TEAM = \n", nil
		},
	}

	c := iosWdaProjectTargets(context.Background(), env, iphoneSeen())

	if c.State != Missing {
		t.Fatalf("the FACT is unchanged — no team is on that target: got %q", c.State)
	}
	if c.Blocks() {
		t.Fatalf("but it does not stop a run here, and must not be counted as one")
	}
	if !strings.Contains(c.Detail, "does not stop a run here") {
		t.Fatalf("and the output must SAY so rather than leaving a reader to reconcile the count with the limits; got %q", c.Detail)
	}
	// …and the count agrees, which is the half that was contradicting itself.
	r := Report{Checks: []Check{c}}
	if len(r.Blocking()) != 0 {
		t.Fatalf("the blocker count must agree with the decision: got %d", len(r.Blocking()))
	}
}

func TestSeal7_WithNoTeamDeclaredThereIsNothingToOverrideWithSoItStillBlocks(t *testing.T) {
	env := Env{
		WdaProjectPath: "/tmp/WebDriverAgent.xcodeproj",
		Run: func(_ context.Context, _ string, _ ...string) (string, error) {
			return "    DEVELOPMENT_TEAM = \n", nil
		},
	}

	c := iosWdaProjectTargets(context.Background(), env, iphoneSeen())

	if !c.Blocks() {
		t.Fatalf("with no declared team nothing overrides the project, so the stored value IS what a build uses")
	}
}

// ── seal 8 (producer half) — the provisioning finding carries the expiry ────────────────────────────────────

func TestSeal8_TheProvisioningFindingCarriesTheProfilesExpiry(t *testing.T) {
	// The evening's real profile: issued on a PAID membership, expiring in six days.
	const expires = "2026-09-01T21:07:57Z"
	udid := "00008130-0000C0FFEE0000C3"
	env := Env{
		IosWdaBundleID: "com.example.WebDriverAgentRunner",
		IosProfiles: func(context.Context) ([]ios.Profile, error) {
			return []ios.Profile{{
				Name:  "iOS Team Provisioning Profile: com.example.WebDriverAgentRunner.xctrunner",
				AppID: realTeam + ".com.example.WebDriverAgentRunner.xctrunner", TeamID: realTeam,
				Expires: expires, ProvisionedDevices: []string{udid},
			}}, nil
		},
		IosTeamID: realTeam,
	}
	now := time.Date(2026, 8, 26, 21, 0, 0, 0, time.UTC)

	f := profileFindings(context.Background(), env, now)(udid)

	if f.State != Satisfied {
		t.Fatalf("the fixture must be the covered case: got %q", f.State)
	}
	if f.ExpiresAtUtc == nil {
		t.Fatal("the date the machine read must ride — without it the platform guesses at an account tier")
	}
	want, _ := time.Parse(time.RFC3339, expires)
	if !f.ExpiresAtUtc.Equal(want) {
		t.Fatalf("expiry: got %v, want %v", f.ExpiresAtUtc, want)
	}
}

func TestSeal8_AnUnreadableExpiryRidesAsAbsentRatherThanGuessed(t *testing.T) {
	udid := "00008130-0000C0FFEE0000C3"
	env := Env{
		IosWdaBundleID: "com.example.WebDriverAgentRunner",
		IosTeamID:      realTeam,
		IosProfiles: func(context.Context) ([]ios.Profile, error) {
			return []ios.Profile{{
				AppID: realTeam + ".com.example.WebDriverAgentRunner.xctrunner", TeamID: realTeam,
				Expires: "not a date", ProvisionedDevices: []string{udid},
			}}, nil
		},
	}

	f := profileFindings(context.Background(), env, time.Now().UTC())(udid)

	if f.ExpiresAtUtc != nil {
		t.Fatalf("a value that does not parse must be absent, never guessed: got %v", f.ExpiresAtUtc)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────────────────

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// stripGoComments removes // lines before a source census. PROSE IS NOT A SENTENCE THE PROGRAM PRINTS: this
// file's own docblocks name IOS_XCODE_ORG_ID to record why it was removed, and a walk that did not strip them
// would be measuring the explanation instead of the remedy.
func stripGoComments(source string) string {
	var kept []string
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
