package farm_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// AN_IPHONE_YOU_CAN_ACTUALLY_RUN — a phone we offer must be a phone we can drive, and the signature that drives
// it belongs to the machine it is plugged into.
//
// Driving a real iPhone means Appium building and SIGNING WebDriverAgent on THIS machine. A Mac that has no
// signing identity cannot do that, so a leasable row for its iPhone is a promise it cannot keep — measured
// through the product's own surface: both of the operator's iPhones listed Available, and a run aimed at either
// ended Error with "signing your device with our Apple team is not something this platform will do".

// The two phones actually on the operator's desk, as his own agent reported them a few minutes before this seal
// was written (`nth-agent devices --json`): a Samsung and an iPhone on one machine.
func iosAndAndroid() []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{
			{ID: "R5CT64M346B", Model: "SM-M346B1", Manufacturer: "samsung", OsVersion: "16", FormFactor: "phone"},
		}, nil),
		inventory.Answered(inventory.IOS, []inventory.Offerable{
			{ID: "00008140-0000C0FFEE0000A1", Model: "Ada's iPhone Lab", Manufacturer: "Apple", OsVersion: "26.6"},
		}, nil),
	}
}

func offeredIDs(report farm.Report) []string {
	ids := make([]string, 0, len(report.Devices))
	for _, d := range report.Devices {
		ids = append(ids, d.LocalID)
	}
	return ids
}

// THE OFFER SEAL, BOTH DIRECTIONS. A machine with no identity keeps its iPhone PRESENT and does not offer it; a
// machine that declared one offers it. The Android phone is untouched either way — this gate is about what
// signing an iOS session needs, not about a machine being unhealthy.
func TestAnIphoneIsOfferedOnlyByAMachineThatCanSignForIt(t *testing.T) {
	outcomes := iosAndAndroid()

	silent := farm.AttachDevices(farm.BuildReport(outcomes), outcomes, "http://127.0.0.1:4723", nil, nil, nil)
	if got := offeredIDs(silent); len(got) != 1 || got[0] != "R5CT64M346B" {
		t.Fatalf("a machine with no signing identity offers its Android phone and NOT its iPhone, got %v", got)
	}
	if silent.Present == nil || len(*silent.Present) != 2 {
		t.Fatalf("the iPhone is still PRESENT — the machine really does see it; got %v", silent.Present)
	}
	// …and the WHY rides the platform outcome, in the vocabulary that already reaches the Devices screen.
	var iosOutcome *farm.WireOutcome
	for i := range silent.Outcomes {
		if silent.Outcomes[i].Platform == farm.WireIos {
			iosOutcome = &silent.Outcomes[i]
		}
	}
	if iosOutcome == nil || iosOutcome.Remedy == nil || !strings.Contains(*iosOutcome.Remedy, "--ios-team-id") {
		t.Fatalf("the withheld iPhone must carry the machine's own remedy, got %+v", iosOutcome)
	}
	if iosOutcome.Status != farm.WireAnswered {
		t.Fatalf("the platform ANSWERED — the offerable half is established and the farm's reconcile must act on it, got %v", iosOutcome.Status)
	}
	if silent.Unusable == nil || len(*silent.Unusable) != 0 {
		t.Fatalf("a phone we choose not to offer is NOT the platform's own word for unusable, got %v", silent.Unusable)
	}

	declared := farm.DeclareIosSigning(farm.BuildReport(outcomes), "TEAM123456", "com.example.WebDriverAgentRunner", "")
	declared = farm.AttachDevices(declared, outcomes, "http://127.0.0.1:4723", nil, nil, nil)
	if got := offeredIDs(declared); len(got) != 2 {
		t.Fatalf("a machine that declared an identity offers both phones, got %v", got)
	}
	if declared.IosSigning == nil || declared.IosSigning.SigningID != farm.DefaultIosSigningID {
		t.Fatalf("an unnamed signing id defaults to Appium's own, got %+v", declared.IosSigning)
	}
}

// A HALF IDENTITY IS NOT AN IDENTITY. A team with no bundle id cannot sign anything, so declaring one must not
// open the gate — the alternative is a phone offered on the strength of a value that fails at the WDA build.
func TestAHalfIdentityDeclaresNothing(t *testing.T) {
	for _, c := range []struct{ team, bundle string }{{"TEAM123456", ""}, {"", "com.example.WebDriverAgentRunner"}, {"", ""}} {
		outcomes := iosAndAndroid()
		report := farm.DeclareIosSigning(farm.BuildReport(outcomes), c.team, c.bundle, "")
		if report.IosSigning != nil {
			t.Fatalf("team=%q bundle=%q must declare nothing, got %+v", c.team, c.bundle, report.IosSigning)
		}
		report = farm.AttachDevices(report, outcomes, "http://127.0.0.1:4723", nil, nil, nil)
		for _, d := range report.Devices {
			if d.Descriptor.Platform == farm.WireIos {
				t.Fatalf("team=%q bundle=%q offered an iPhone, got %+v", c.team, c.bundle, d)
			}
		}
	}
}

// THE DECLARATION IS ON THE WIRE, AND ABSENT WHEN THERE IS NONE. Omitted rather than sent empty: a farm that has
// never heard of the field and an agent with nothing to say must produce the same bytes.
func TestTheDeclarationRidesRegistrationAndIsOmittedWhenThereIsNone(t *testing.T) {
	with, err := json.Marshal(farm.RegisterRequest{
		IosSigning: &farm.WireIosSigning{TeamID: "TEAM123456", WdaBundleID: "com.example.WDA", SigningID: "Apple Development"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with), `"iosSigning":{"teamId":"TEAM123456","wdaBundleId":"com.example.WDA","signingId":"Apple Development"}`) {
		t.Fatalf("the declaration must ride registration verbatim, got %s", with)
	}
	without, err := json.Marshal(farm.RegisterRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "iosSigning") {
		t.Fatalf("a machine with nothing to say sends no field at all, got %s", without)
	}
}

// ── THE LEAK CENSUS: every read of the signing identity is classified ────────────────────────────────────────

// signingReadsUnder walks ONE TREE — the root is a PARAMETER, which is the whole guard. Five slices running, an
// instrument guard has exercised a different call than the census it guards, every time because the census's
// population was a separate hard-coded call the guard could not reach. Here the guard plants a tree and this same
// function reads it.
func signingReadsUnder(t *testing.T, root string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	names := map[string]bool{"IosTeamID": true, "IosWdaBundleID": true, "TeamID": true, "WdaBundleID": true, "IosSigning": true}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				if names[node.Sel.Name] {
					found[filepath.Base(path)] = append(found[filepath.Base(path)], node.Sel.Name)
				}
			case *ast.Ident:
				if names[node.Name] && node.Obj != nil {
					found[filepath.Base(path)] = append(found[filepath.Base(path)], node.Name)
				}
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

// THE IDENTITY GOES WHERE THE TOKEN GOES AND NOWHERE ELSE. An Apple team id is not a secret — it is printed in
// Xcode and carried in every signed build — but it is a fact about somebody else's development account, so every
// place production code touches it must say which exit it feeds. A new read fails until its author classifies it,
// and "it is printed to the transcript" is not a classification this list will accept.
func TestEveryReadOfTheSigningIdentityIsClassified(t *testing.T) {
	classified := map[string]string{
		"wire.go":     "DECLARES the fields on RegisterRequest — the one intended exit, over the same authenticated https call the token rides (ValidateBaseURL + the forced redirect policy)",
		"client.go":   "copies report.IosSigning onto the registration body; never logged, never a URL component",
		"report.go":   "the declaration and the offer gate — one field, read to decide whether an iPhone may become a leasable row. The gate's own sentences (IosSigningReason/IosSigningRemedy) name the FLAGS, never the values",
		"loop.go":     "stamps the declaration from Config; the narrator is not given it (a transcript is the one place a person pastes into a bug report)",
		"main.go":     "reads --ios-team-id / NTH_AGENT_IOS_TEAM_ID into memory and hands it to the loop; never echoed",
		"doctor.go":   "holds the WDA BUNDLE ID for the provisioning-profile check — no team id, and the bundle id is echoed only back to the person who typed it, in their own terminal",
		"checks.go":   "asks whether a profile covers <bundle id>.xctrunner FOR THE DECLARED TEAM; the detail names the bundle id and the team the person passed, which are theirs and on their screen, and the remedy names the team their own profile carries — the one sentence that turns a failed build into a fixed one",
		"profiles.go": "READS the team id off this machine's .mobileprovision files (TeamIdentifier) — a local decode with the OS's own `security`, never sent anywhere; only the check above consumes it",
		"setup.go":    "reads --ios-team-id / NTH_AGENT_IOS_TEAM_ID into the build plan; PRINTED ON PURPOSE, in the plan a person must say yes to and in the exact command they may run themselves — a build that hid which team it signs with is the tool this verb replaces",
		"wda.go":      "carries the team into the xcodebuild argv and into the plan's printed statement; the statement IS the intended exit — see setup.go",
		"findings.go": "reads the declared team + bundle id to REDUCE the profile/project checks to state words (THE_CHECK_THAT_ALREADY_KNEW); only the check NAME, the state, a timestamp and (since A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS) the profile's EXPIRY DATE ride the wire — WireFinding still carries no prose and no identity, so the team feeds no new exit",
		// A_DOCTOR_THAT_KNOWS_MORE_THAN_IT_SAYS item 3, classified rather than exempted: this reads the team out of
		// a CERTIFICATE's subject (OU) with the OS's own `security`, exactly as profiles.go reads it out of a
		// .mobileprovision. It is a LOCAL read with a LOCAL exit — the doctor's own terminal, on the machine that
		// holds the keychain — and it is what lets the check say "the code you declared is your certificate's, not
		// your team's". It is sent nowhere: no finding, no registration, no URL.
		"identities.go": "READS this Mac's code-signing identities (`security find-identity`) and each certificate's OU — the team; consumed only by the doctor check that names the certificate-code trap, printed only in the operator's own terminal, never sent",
	}
	found := signingReadsUnder(t, filepath.Join("..", ".."))
	// INSTRUMENT GUARD (population, not classifier): the walk must have found the one intended exit.
	if len(found["client.go"]) == 0 {
		t.Fatal("instrument guard: the sweep did not find client.go's registration copy — it is reading the wrong tree")
	}
	for file := range found {
		if _, ok := classified[file]; !ok {
			t.Fatalf("%s reads the signing identity and is not classified — say which exit it feeds", file)
		}
	}
}

// THE INSTRUMENT GUARD: the census reads THE TREE IT IS GIVEN. A census that ignored its root and answered about
// the repository would pass every planted case while proving nothing about the population it claims to walk.
func TestTheSigningCensusReadsTheTreeItIsGiven(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted_leak.go")
	if err := os.WriteFile(planted, []byte("package planted\n\ntype x struct{ IosTeamID string }\n\nfunc f(v x) string { return v.IosTeamID }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found := signingReadsUnder(t, dir)
	if len(found["planted_leak.go"]) == 0 {
		t.Fatalf("the census must read the tree it is given: planted file not found, got %v", found)
	}
	if len(found) != 1 {
		t.Fatalf("and ONLY that tree — a census answering about the repository proves nothing about a planted one, got %v", found)
	}
}

// ── THE_MACHINE_SAYS_WHICH_PORT: the declaration is per DEVICE and it is on the wire ──────────────────────────

func TestTheDeclaredPortRidesEachDevicesDescriptor(t *testing.T) {
	outcomes := iosAndAndroid()
	declared := farm.DeclareIosSigning(farm.BuildReport(outcomes), "TEAM123456", "com.example.WebDriverAgentRunner", "")
	declared = farm.AttachDevices(declared, outcomes, "http://127.0.0.1:4723", func(udid string) *int {
		port := 26903
		if udid != "00008140-0000C0FFEE0000A1" {
			port = 27532
		}
		return &port
	}, nil, nil)

	body, err := json.Marshal(farm.RegisterRequest{Devices: declared.Devices})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"udid":"00008140-0000C0FFEE0000A1","appiumUrl":"http://127.0.0.1:4723","status":"Available","manufacturer":"Apple","model":"Ada's iPhone Lab","wdaPort":26903`) {
		t.Fatalf("the port must ride the DEVICE'S descriptor, verbatim: %s", body)
	}
	// THE ANDROID PHONE GETS NONE. WebDriverAgent is an iOS thing, and a number nobody reads pretending to be a
	// fact is exactly what this slice removed from the worker.
	for _, d := range declared.Devices {
		if d.Descriptor.Platform == farm.WireAndroid && d.Descriptor.WdaPort != nil {
			t.Fatalf("android must declare no WebDriverAgent port, got %d", *d.Descriptor.WdaPort)
		}
	}
}

func TestAMachineThatDeclaresNoPortSendsNoFieldAtAll(t *testing.T) {
	outcomes := iosAndAndroid()
	declared := farm.DeclareIosSigning(farm.BuildReport(outcomes), "TEAM123456", "com.example.WebDriverAgentRunner", "")
	declared = farm.AttachDevices(declared, outcomes, "http://127.0.0.1:4723", nil, nil, nil)

	body, err := json.Marshal(farm.RegisterRequest{Devices: declared.Devices})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "wdaPort") {
		t.Fatalf("a machine with nothing to say sends no field — absent and 0 are not the same thing: %s", body)
	}
}
