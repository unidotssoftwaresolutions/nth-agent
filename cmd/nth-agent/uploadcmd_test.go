package main

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// ── A_BUILD_THAT_NEVER_LEAVES_THE_BUILDING — the verb ──────────────────────────────────────────────────────
//
// `connect` refuses to start a cloud session without a build id, and there was no way to obtain one through
// this product: the uploader was a library function reachable only from a build-tagged test. These seals hold
// the verb a person types — including the half that is easy to forget, which is that an id nobody knows what to
// do with is not a fix.

const theKey = "SECRET-ACCESS-KEY-MUST-NOT-BE-PRINTED"

// uploadServer stands in for BrowserStack and records what actually arrived.
type uploadServer struct {
	fields   map[string]string
	fileName string
	fileLen  int
	reached  bool
	reply    string
}

// point aims the verb's uploader at this stub — the SAME verb the binary runs, with one address changed.
func (u *uploadServer) point(t *testing.T) {
	t.Helper()
	u.fields = map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.reached = true
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				value, _ := io.ReadAll(part)
				if part.FileName() != "" {
					u.fileName, u.fileLen = part.FileName(), len(value)
					continue
				}
				u.fields[part.FormName()] = string(value)
			}
		}
		reply := u.reply
		if reply == "" {
			reply = `{"app_url":"bs://9f8e7d6c5b4a"}`
		}
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)

	previous := uploaderFor
	uploaderFor = func() browserstack.Uploader {
		return browserstack.Uploader{Client: browserstack.FileUploadClient(), BaseURL: server.URL}
	}
	t.Cleanup(func() { uploaderFor = previous })
}

func apkAt(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// upload runs the real verb with credentials in the environment, and NO farm and NO token — which is itself
// part of the claim: an id must be obtainable before a workspace exists.
func upload(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("NTH_AGENT_BROWSERSTACK_USER", "acme-user")
	t.Setenv("NTH_AGENT_BROWSERSTACK_KEY", theKey)
	var out, errOut strings.Builder
	code = run(append([]string{"upload"}, args...), strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// ── ITEM 1: the id, and what to do with it ─────────────────────────────────────────────────────────────────

func TestAFileUploadPrintsTheIdAndTheNextCommand(t *testing.T) {
	// ⚠️ THE SECOND HALF IS THE POINT. The finding was that a person cannot GET a build id; a verb that prints a
	// hash and stops has fixed one half of that and left them holding a value with no stated use.
	stub := &uploadServer{}
	stub.point(t)
	path := apkAt(t, "app-release.apk", 2048)

	code, stdout, stderr := upload(t, "--file", path)

	if code != exitOK {
		t.Fatalf("want exit 0, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stdout, "bs://9f8e7d6c5b4a") {
		t.Fatalf("the id must be printed — it is not a secret and it is the whole output: %q", stdout)
	}
	if !strings.Contains(stdout, "--browserstack-app") {
		t.Fatalf("the output must say where the id goes; an id with no stated use is half the fix: %q", stdout)
	}
	if !strings.Contains(stdout, "NTH_AGENT_BROWSERSTACK_APP") {
		t.Fatalf("the environment twin must be named too — it is the form the README tells people to use: %q", stdout)
	}
	// …and the bytes really went.
	if stub.fileName != "app-release.apk" || stub.fileLen != 2048 {
		t.Fatalf("the build's own bytes must have been sent, got name=%q len=%d", stub.fileName, stub.fileLen)
	}
	if _, asked := stub.fields["url"]; asked {
		t.Fatal("a --file upload must not ask BrowserStack to fetch anything")
	}
}

func TestTheVerbSaysSomethingBeforeTheWaitAndNamesTheSize(t *testing.T) {
	// A 200 MB upload with no output reads as a hang, and a person who thinks a command has hung kills it —
	// which on this path means an upload started and abandoned.
	stub := &uploadServer{}
	stub.point(t)
	path := apkAt(t, "big.apk", 1500000)

	_, stdout, _ := upload(t, "--file", path)

	before, _, found := strings.Cut(stdout, "uploaded:")
	if !found {
		t.Fatalf("want the success line, got %q", stdout)
	}
	if !strings.Contains(before, path) {
		t.Fatalf("the line before the wait must name what is being sent: %q", before)
	}
	if !strings.Contains(before, "1.4 MB") {
		t.Fatalf("…and how big it is, so the wait is expected rather than suspicious: %q", before)
	}
}

func TestACustomIdIsPassedThroughAndEchoed(t *testing.T) {
	stub := &uploadServer{reply: `{"app_url":"bs://9f8e7d6c5b4a","custom_id":"acme-nightly"}`}
	stub.point(t)

	_, stdout, _ := upload(t, "--file", apkAt(t, "app.apk", 32), "--custom-id", "acme-nightly")

	if stub.fields["custom_id"] != "acme-nightly" {
		t.Fatalf("the custom id must reach BrowserStack, got %q", stub.fields["custom_id"])
	}
	if !strings.Contains(stdout, "acme-nightly") {
		t.Fatalf("…and be echoed, since it is what makes repeat uploads traceable: %q", stdout)
	}
}

func TestAUrlUploadSaysWhoseNetworkHasToReachTheAddress(t *testing.T) {
	// ⚠️ THE ASSUMPTION THAT MADE THE OPERATOR'S OWN METHOD STOP WORKING. His build is served by a small HTTP
	// server on his machine: perfect in his browser, unreachable from a vendor's data centre. The verb says which
	// machine does the fetching rather than leaving it to be discovered.
	stub := &uploadServer{}
	stub.point(t)

	code, stdout, stderr := upload(t, "--url", "https://example.test/app.apk")

	if code != exitOK {
		t.Fatalf("want exit 0, got %d (%s)", code, stderr)
	}
	if stub.fields["url"] != "https://example.test/app.apk" {
		t.Fatalf("a --url upload posts the address for BrowserStack to fetch, got %q", stub.fields["url"])
	}
	said := strings.ToLower(stdout)
	if !strings.Contains(said, "reachable from") || !strings.Contains(said, "not from this machine") {
		t.Fatalf("the --url path must say the address is resolved from BROWSERSTACK's network: %q", stdout)
	}
}

// ── INJECTION 2: one of the two, never both, never a silent preference ─────────────────────────────────────

func TestFileAndUrlTogetherIsRefusedRatherThanSilentlyPreferred(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)

	code, _, stderr := upload(t, "--file", apkAt(t, "app.apk", 8), "--url", "https://example.test/app.apk")

	if code != exitMistake {
		t.Fatalf("want exit 2, got %d", code)
	}
	if stub.reached {
		t.Fatal("nothing may be uploaded while it is unclear which of the two machines was meant")
	}
	for _, name := range []string{"--file", "--url"} {
		if !strings.Contains(stderr, name) {
			t.Fatalf("the refusal must name both flags, got %q", stderr)
		}
	}
}

func TestNeitherFileNorUrlSaysWhichIsMissingRatherThanUploadingNothing(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)

	code, _, stderr := upload(t)

	if code != exitMistake {
		t.Fatalf("want exit 2, got %d", code)
	}
	if stub.reached {
		t.Fatal("an upload with no build must not reach the account")
	}
	for _, name := range []string{"--file", "--url"} {
		if !strings.Contains(stderr, name) {
			t.Fatalf("the refusal must name both options so a person can choose, got %q", stderr)
		}
	}
}

// ── INJECTION 5: a shape BrowserStack does not accept ──────────────────────────────────────────────────────

func TestAFileThatIsNotABuildIsRefusedBeforeAnythingIsUploaded(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)

	code, _, stderr := upload(t, "--file", apkAt(t, "notes.txt", 16))

	if code != exitMistake {
		t.Fatalf("want exit 2, got %d", code)
	}
	if stub.reached {
		t.Fatal("refusing after the transfer is not refusing — the check exists to save the upload")
	}
	if !strings.Contains(stderr, ".apk") {
		t.Fatalf("the refusal must name what IS accepted, got %q", stderr)
	}
}

func TestEachLocalMistakeReachesThePersonAsItsOwnSentence(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)
	dir := t.TempDir()

	for _, c := range []struct{ name, path, mustSay string }{
		{"missing", filepath.Join(dir, "nope.apk"), "no file at that path"},
		{"a directory", dir, "directory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, stderr := upload(t, "--file", c.path)
			if code != exitMistake {
				t.Fatalf("want exit 2, got %d", code)
			}
			if !strings.Contains(stderr, c.mustSay) {
				t.Fatalf("want a sentence containing %q, got %q", c.mustSay, stderr)
			}
		})
	}
}

// ── INJECTION 4 / ITEM 4: the key has nowhere to go ────────────────────────────────────────────────────────

func TestTheAccessKeyAppearsInNoOutputOfTheUploadVerb(t *testing.T) {
	// The census in internal/browserstack proves no credential-named value reaches a printing call anywhere in
	// the tree. This is the same claim executed end to end on the one verb whose whole job is to print something.
	stub := &uploadServer{}
	stub.point(t)

	for _, args := range [][]string{
		{"--file", apkAt(t, "app.apk", 64)},
		{"--file", apkAt(t, "notes.txt", 64)},       // a refusal
		{"--file", filepath.Join(t.TempDir(), "x")}, // another refusal
		{"--url", "https://example.test/app.apk"},
	} {
		_, stdout, stderr := upload(t, args...)
		if strings.Contains(stdout, theKey) || strings.Contains(stderr, theKey) {
			t.Fatalf("the access key reached the output of %v", args)
		}
	}
}

func TestTheUploadVerbNeedsCredentialsAndSaysSoWithoutMentioningAFarm(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)
	t.Setenv("NTH_AGENT_BROWSERSTACK_USER", "")
	t.Setenv("NTH_AGENT_BROWSERSTACK_KEY", "")

	var out, errOut strings.Builder
	code := run([]string{"upload", "--file", apkAt(t, "app.apk", 8)}, strings.NewReader(""), &out, &errOut)

	if code != exitMistake {
		t.Fatalf("want exit 2, got %d", code)
	}
	if stub.reached {
		t.Fatal("nothing may be sent without credentials")
	}
	for _, name := range []string{"--browserstack-user", "--browserstack-key"} {
		if !strings.Contains(errOut.String(), name) {
			t.Fatalf("the refusal must name both halves, got %q", errOut.String())
		}
	}
	// ⚠️ AND IT MUST NOT SEND THEM TO GET A WORKSPACE FIRST. connect refuses to start without the id this verb
	// produces, so requiring a farm or a token here would make the two steps circular.
	said := errOut.String()
	if strings.Contains(said, "--farm") || strings.Contains(said, "--token") {
		t.Fatalf("the upload verb needs no farm and no token, and its refusal must not ask for one: %q", said)
	}
}

func TestHalfACredentialIsRefusedOnTheUploadVerbToo(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)
	t.Setenv("NTH_AGENT_BROWSERSTACK_USER", "")
	t.Setenv("NTH_AGENT_BROWSERSTACK_KEY", "")

	var out, errOut strings.Builder
	code := run([]string{"upload", "--browserstack-user", "acme", "--file", apkAt(t, "app.apk", 8)},
		strings.NewReader(""), &out, &errOut)

	if code != exitMistake {
		t.Fatalf("half a credential must be a mistake, got exit %d", code)
	}
	if stub.reached {
		t.Fatal("a half-configured account must not be contacted")
	}
}
