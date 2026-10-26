package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── `nth-agent capture`: nothing happens without ENABLE, and disable leaves nothing behind ───────────────────────

// noPhones is an adb with nothing attached: these tests never reach a phone, and a command that tried would be answered
// as a phone that is not there.
func noPhones(_ context.Context, args ...string) (string, string, int, error) {
	if len(args) == 1 && args[0] == "devices" {
		return "List of devices attached\n\n", "", 0, nil
	}
	return "", "adb: no devices/emulators found\n", 1, nil
}

func benchSeams(configDir string) captureSeams {
	return captureSeams{
		configDir: func() (string, error) { return configDir, nil },
		adb:       noPhones,
		now:       time.Now,
		goos:      "darwin",
		hostname:  func() (string, error) { return "bench-mac", nil },
		lockWait:  time.Second,
	}
}

func runCaptureCommand(t *testing.T, seams captureSeams, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	inv, code := parse(append([]string{"capture"}, args...), verbs, &stdout, &stderr)
	if inv == nil {
		return code, stdout.String(), stderr.String()
	}
	code = runCapture(inv, strings.NewReader(stdin), &stdout, &stderr, seams)
	return code, stdout.String(), stderr.String()
}

func TestCaptureEnableChangesNothingUnlessENABLEIsTyped(t *testing.T) {
	for _, answer := range []string{"", "\n", "enable\n", "Enable\n", "yes\n", "ENABLE please\n", "ENABLED\n", "y\n"} {
		dir := t.TempDir()
		code, out, _ := runCaptureCommand(t, benchSeams(dir), answer, "enable")
		if code != exitMistake {
			t.Fatalf("answer %q: exit %d", answer, code)
		}
		if !strings.Contains(out, capture.EnableExplanation()) || !strings.Contains(out, capture.ConfirmPrompt) || !strings.Contains(out, capture.NotEnabled) {
			t.Fatalf("answer %q: it must explain, ask, and say nothing changed:\n%s", answer, out)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("answer %q: something was made: %v %v", answer, entries, err)
		}
	}
}

func TestCaptureEnableWithENABLEMakesTheCAAndTheMark(t *testing.T) {
	dir := t.TempDir()
	store := capture.StoreIn(dir)
	code, out, errOut := runCaptureCommand(t, benchSeams(dir), "ENABLE\n", "enable")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if _, on := store.Enabled(); !on {
		t.Fatal("the mark must be written")
	}
	info, err := os.Lstat(netcap.KeyPathIn(store.CADir()))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key must be there, owner-only: %v %v", info, err)
	}
	cert, err := netcap.ReadCertificate(store.CADir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, capture.Enabled(cert.Subject.CommonName, cert.NotAfter)) {
		t.Fatalf("it must say what it made and until when:\n%s", out)
	}

	// A SECOND enable explains nothing new and replaces nothing.
	code, again, _ := runCaptureCommand(t, benchSeams(dir), "", "enable")
	if code != 0 || !strings.Contains(again, capture.AlreadyEnabled(cert.Subject.CommonName, cert.NotAfter)) {
		t.Fatalf("exit %d:\n%s", code, again)
	}
	if now, _ := netcap.ReadCertificate(store.CADir()); netcap.Fingerprint(now) != netcap.Fingerprint(cert) {
		t.Fatal("the CA was replaced")
	}
}

func TestCaptureEnableOnASystemThatCannotKeepTheKeyMakesNothing(t *testing.T) {
	dir := t.TempDir()
	seams := benchSeams(dir)
	seams.goos = "plan9"
	code, out, _ := runCaptureCommand(t, seams, "ENABLE\n", "enable")
	if code != 1 || !strings.Contains(out, capture.UnsupportedSystem("plan9")) {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("something was made: %v", entries)
	}
}

func TestCaptureDisableDeletesTheKeyTheFilesAndTheMark(t *testing.T) {
	dir := t.TempDir()
	store := capture.StoreIn(dir)
	if code, out, _ := runCaptureCommand(t, benchSeams(dir), "ENABLE\n", "enable"); code != 0 {
		t.Fatalf("enable: %s", out)
	}
	file := filepath.Join(store.FilesDir(), "0f3c2a91-5b7e-4d21-9a3c-6e8f10b2c4d5.har")
	if err := os.MkdirAll(store.FilesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runCaptureCommand(t, benchSeams(dir), "", "disable")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, gone := range []string{netcap.KeyPathIn(store.CADir()), store.CADir(), file} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s must be gone: %v", gone, err)
		}
	}
	if _, on := store.Enabled(); on {
		t.Fatal("the mark must be gone")
	}
	for _, want := range []string{"Network capture is off for this machine.", "it is listed in two places; remove it in both", capture.SixThings()} {
		if !strings.Contains(out, want) {
			t.Fatalf("disable must say %q:\n%s", want, out)
		}
	}
}

func TestCaptureSetupWithCaptureOffTouchesNoPhone(t *testing.T) {
	dir := t.TempDir()
	seams := benchSeams(dir)
	asked := 0
	seams.adb = func(context.Context, ...string) (string, string, int, error) { asked++; return "", "", 0, nil }
	code, out, _ := runCaptureCommand(t, seams, "", "setup", "R5CT30ABCDE")
	if code != 1 || asked != 0 || !strings.Contains(out, "npx nthcase-agent@latest capture enable") {
		t.Fatalf("exit %d, adb asked %d times:\n%s", code, asked, out)
	}
}

func TestTheCaptureVerbTakesExactlyItsOwnWords(t *testing.T) {
	for _, c := range []struct {
		args []string
		says string
	}{
		{nil, "capture needs one of: enable, setup, disable"},
		{[]string{"frobnicate"}, `unknown capture command "frobnicate" — it is one of: enable, setup, disable`},
		{[]string{"enable", "now"}, `capture enable takes nothing more, and was given "now"`},
		{[]string{"setup", "R5CT30ABCDE", "R5CT30OTHER"}, `capture setup takes nothing more, and was given "R5CT30OTHER"`},
		{[]string{"disable", "--json"}, `unknown option "--json"`},
	} {
		code, _, errOut := runCaptureCommand(t, benchSeams(t.TempDir()), "", c.args...)
		if code != exitMistake || !strings.Contains(errOut, c.says) {
			t.Fatalf("capture %v: exit %d, %q", c.args, code, errOut)
		}
	}
}

// ── THE SIX THINGS, IN EVERY TEXT ───────────────────────────────────────────────────────────────────────────────
//
// AN_AGENT_THAT_CAN_RECORD_ITS_PHONE rule 10: every text about capture says the same six things, each at its own length.
// Each thing is a pair of marks a text must carry, read with its line breaks and indentation folded to single spaces.

var sixThings = []struct {
	thing string
	marks []*regexp.Regexp
}{
	{"the whole phone's traffic goes through this machine — use a test phone",
		[]*regexp.Regexp{regexp.MustCompile(`(?i)whole phone`), regexp.MustCompile(`(?i)test phone`)}},
	{"only debug builds that trust user certificates are readable",
		[]*regexp.Regexp{regexp.MustCompile(`(?i)debug builds`), regexp.MustCompile(`(?i)trust user(-installed)? certificates`)}},
	{"what is masked here, and that it is masked again on arrival",
		[]*regexp.Regexp{regexp.MustCompile(`(?i)masked (on this machine|here)`), regexp.MustCompile(`(?i)mask(ed)? again`)}},
	{"what reaches us is kept one hour, and nothing raw is kept here",
		[]*regexp.Regexp{regexp.MustCompile(`(?i)one hour`), regexp.MustCompile(`(?i)nothing raw`)}},
	// A_MACHINE_THAT_STAYS_THE_CUSTOMERS, "the key never leaves this machine" was not true of a backup's copy. Every
	// text now says the three true things instead, and each is held: never sent, kept out of backups, and that a backup
	// made before that may still hold a copy.
	{"the agent never sends the CA's key anywhere and keeps it out of backups, an earlier backup may hold a copy, and the CA lasts one year",
		[]*regexp.Regexp{regexp.MustCompile(`(?i)never sends (the capture CA's|its private|its) key anywhere`),
			regexp.MustCompile(`(?i)out of backups`), regexp.MustCompile(`(?i)backup made before`), regexp.MustCompile(`(?i)one year`)}},
	{"how to turn it all off and remove the certificate — both places on a Samsung",
		[]*regexp.Regexp{regexp.MustCompile("(`|npx \\S+ )capture disable"), regexp.MustCompile(`(?i)both places|remove it in both`)}},
}

var folded = regexp.MustCompile(`\s+`)

func fold(text string) string { return folded.ReplaceAllString(text, " ") }

// readmeSection is a README section from its heading to the next of the same depth.
func readmeSection(t *testing.T, heading string) string {
	t.Helper()
	readme := readmeText(t)
	start := strings.Index(readme, heading+"\n")
	if start < 0 {
		t.Fatalf("the README has no %q section", heading)
	}
	rest := readme[start+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return heading + rest
}

func helpCaptureSection(t *testing.T) string {
	t.Helper()
	start := strings.Index(usage(), "network capture — OFF until you turn it on here")
	end := strings.Index(usage(), "Exit code 0 means")
	if start < 0 || end < start {
		t.Fatal("--help has no network capture section")
	}
	return usage()[start:end]
}

func TestEveryCaptureTextSaysTheSixThings(t *testing.T) {
	until := time.Date(2027, 10, 4, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	off := capture.ReadStatus(capture.StoreIn(dir), time.Now())
	if _, err := capture.Enable(capture.StoreIn(dir), "bench-mac", "v-test", time.Now(), "darwin"); err != nil {
		t.Fatal(err)
	}
	on := capture.ReadStatus(capture.StoreIn(dir), time.Now())

	texts := map[string]string{
		"the enable explanation and its question": capture.EnableExplanation() + capture.ConfirmPrompt,
		"enable, done":                      capture.Enabled("NthCase agent CA — bench-mac", until),
		"enable, already on":                capture.AlreadyEnabled("NthCase agent CA — bench-mac", until),
		"enable, on a system without it":    capture.UnsupportedSystem("plan9"),
		"setup's steps, Samsung":            capture.SetupSteps("R5CT30ABCDE", "SM-A546B", capture.VendorSamsung),
		"setup's steps, stock Android":      capture.SetupSteps("39051FDJH00ABC", "Pixel 8", capture.VendorStock),
		"the check passed":                  capture.TrustTrusted("R5CT30ABCDE", true),
		"the check refused":                 capture.TrustRefused("R5CT30ABCDE", true),
		"the check never answered":          capture.TrustNoAnswer("R5CT30ABCDE", false),
		"doctor, capture off":               strings.Join(off.Lines, "\n"),
		"doctor, capture on":                strings.Join(on.Lines, "\n"),
		"disable":                           capture.DisableSummary(capture.DisableResult{CAName: "NthCase agent CA — bench-mac"}, "/x/ca"),
		"a phone that could not be reached": capture.Unreachable("R5CT30ABCDE", "127.0.0.1:41234"),
		"the README section":                readmeSection(t, "## Network capture — off until you turn it on"),
		"--help":                            helpCaptureSection(t),
	}
	for name, text := range texts {
		text = fold(text)
		for _, thing := range sixThings {
			for _, mark := range thing.marks {
				if !mark.MatchString(text) {
					t.Errorf("%s does not say %q (no %s):\n%s", name, thing.thing, mark, text)
				}
			}
		}
	}
}

// THE PROMISE IS ONE SENTENCE IN THREE PLACES: README, --help and doctor say capture.ThePromise, word for word — doctor up
// to where it would say SixThings' key, backups, year and removal a second time (AN_AGENT_THAT_KNOWS_IT_IS_OLD item 3), and
// those it says once, in SixThings, which it prints just before.
func TestThePromiseIsTheSameInTheReadmeHelpAndDoctor(t *testing.T) {
	promise := fold(capture.ThePromise())
	ownHalf, repeated, cut := strings.Cut(capture.ThePromise(), " The agent never sends its private key anywhere")
	doctorSays := strings.Join(capture.ReadStatus(capture.StoreIn(t.TempDir()), time.Now()).Lines, "\n")
	if !cut || !strings.Contains(fold(doctorSays), fold(ownHalf)) || !strings.Contains(fold(doctorSays), fold(capture.SixThings())) {
		t.Errorf("doctor does not say the promise as written, up to where SixThings says the rest:\n%s", doctorSays)
	}
	for _, fact := range []string{"out of backups", "one year", "capture disable"} {
		if !strings.Contains(repeated, fact) || strings.Count(fold(doctorSays), fact) != 1 {
			t.Errorf("doctor must say %q once, in SixThings — the half of the promise it leaves out says it too:\n%s", fact, doctorSays)
		}
	}
	customer, _, _ := strings.Cut(readmeText(t), readmeBoundary)
	// The README as the publish stamps it, which is what a customer reads: the promise names `npx <package> capture
	// disable`, and the README may only spell the package {{PACKAGE}}.
	customer = strings.ReplaceAll(customer, "{{PACKAGE}}", packageName(t))
	for name, text := range map[string]string{
		"the README (the half customers read)": customer,
		"--help":                               usage(),
	} {
		if !strings.Contains(fold(text), promise) {
			t.Errorf("%s does not say the promise as written:\n%s", name, capture.ThePromise())
		}
	}
}

// THE SHARED MACHINE, SAID IN BOTH PLACES (AN_AGENT_CAPTURE_THE_CUSTOMER_CAN_SEE rule 2): what another account on this
// machine can and cannot do while a capture runs is one sentence, capture.SharedMachine, and `capture enable`'s
// explanation and the README's capture section say it word for word.
func TestTheSharedMachineIsSaidInTheExplanationAndTheReadme(t *testing.T) {
	sentence := fold(capture.SharedMachine)
	for name, text := range map[string]string{
		"capture enable's explanation": capture.EnableExplanation(),
		"the README's capture section": readmeSection(t, "## Network capture — off until you turn it on"),
	} {
		if !strings.Contains(fold(text), sentence) {
			t.Errorf("%s does not say what another account on this machine can do, as written:\n%s", name, capture.SharedMachine)
		}
	}
}

// WHERE TO READ MORE (AN_AGENT_THAT_SHIPS_ITS_NOTICES): the capture section ends by naming the two pages that say what the
// workspace shows and keeps of a capture — the docs section and the privacy section, each by its own anchor.
func TestTheCaptureSectionSaysWhereToReadMore(t *testing.T) {
	section := readmeSection(t, "## Network capture — off until you turn it on")
	customer, _, _ := strings.Cut(readmeText(t), readmeBoundary)
	if !strings.Contains(customer, section) {
		t.Fatal("the capture section is below the registry boundary — a customer never reads it")
	}
	for _, page := range []string{"https://nthcase.com/docs/your-devices#network-capture", "https://nthcase.com/privacy#network-capture"} {
		if !strings.Contains(section, "("+page+")") {
			t.Errorf("the capture section does not link %s", page)
		}
	}
}

// THE README SEAL, EXTENDED TO A VERB'S OWN WORDS: `npx {{PACKAGE}}@latest capture enable` is a command a person types, so
// the README documents it as typed, with @latest (NTH_AGENT_0_4_3).
func TestEveryVerbsOwnWordsAreDocumentedInTheReadme(t *testing.T) {
	readme := readmeText(t)
	documented := 0
	for _, verb := range verbs {
		for _, sub := range verb.Subcommands {
			command := "npx {{PACKAGE}}@latest " + verb.Name + " " + sub.Name
			if !strings.Contains(readme, command) {
				t.Errorf("the binary has `%s` and the README does not document it", command)
			}
			documented++
		}
	}
	if documented < 3 {
		t.Fatalf("read %d subcommands — the table is not the one this binary runs", documented)
	}
}
