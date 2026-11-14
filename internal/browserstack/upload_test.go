package browserstack_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

const sampleBuild = "https://www.browserstack.com/app-automate/sample-apps/android/WikipediaSample.apk"

// uploadStub records what arrived and answers whatever the test wants.
type uploadStub struct {
	path        string
	authUser    string
	authKey     string
	authPresent bool
	fields      map[string]string
	status      int
	reply       string
}

func (u *uploadStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	u.fields = map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.path = r.URL.Path
		u.authUser, u.authKey, u.authPresent = r.BasicAuth()

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				value, _ := io.ReadAll(part)
				u.fields[part.FormName()] = string(value)
			}
		}

		status, reply := u.status, u.reply
		if status == 0 {
			status = http.StatusOK
		}
		if reply == "" {
			reply = `{"app_url":"bs://f00dfeedf00dfeedf00dfeedf00dfeedf00dfeed","custom_id":"NthAgentProbe"}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server
}

func upload(t *testing.T, stub *uploadStub, customID string) (browserstack.App, error) {
	t.Helper()
	server := stub.server(t)
	uploader := browserstack.Uploader{Client: server.Client(), BaseURL: server.URL}
	return uploader.UploadByURL(context.Background(), creds(), sampleBuild, customID)
}

// ── the agent can put a build on the customer's account ────────────────────────────────────────────────────

func TestAnUploadIsBasicAuthenticatedAndNamesTheBuildByURL(t *testing.T) {
	stub := &uploadStub{}
	app, err := upload(t, stub, "NthAgentProbe")
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}

	if stub.path != "/app-automate/upload" {
		t.Fatalf("wrong endpoint: %s", stub.path)
	}
	// A DIFFERENT MECHANISM FROM THE HUB'S, and that is the point of asserting it: the hub takes credentials
	// inside the session body; this takes HTTP basic auth. Both exist in this package now.
	if !stub.authPresent || stub.authUser != "acme-user" || stub.authKey != "SUPERSECRETKEY" {
		t.Fatal("the upload must authenticate with HTTP basic auth")
	}
	// THE url FIELD, NOT file — BrowserStack fetches the build itself, which keeps a binary out of this
	// repository and out of CI entirely.
	if stub.fields["url"] != sampleBuild {
		t.Fatalf("the build must be named by public URL, got %q", stub.fields["url"])
	}
	if _, sentFile := stub.fields["file"]; sentFile {
		t.Fatal("a local-file upload was deliberately not shipped — it brings four new failure modes (missing " +
			"path, unreadable file, size limit, extension validation) and the product need is not yet known")
	}
	if stub.fields["custom_id"] != "NthAgentProbe" {
		t.Fatalf("a custom id makes repeat runs traceable in the dashboard, got %q", stub.fields["custom_id"])
	}

	if app.URL != "bs://f00dfeedf00dfeedf00dfeedf00dfeedf00dfeed" {
		t.Fatalf("the bs:// id is the whole product of an upload, got %q", app.URL)
	}
	if app.CustomID != "NthAgentProbe" {
		t.Fatalf("the echoed custom id is kept, got %q", app.CustomID)
	}
}

func TestACustomIdIsOptionalAndOmittedWhenEmpty(t *testing.T) {
	stub := &uploadStub{}
	if _, err := upload(t, stub, ""); err != nil {
		t.Fatalf("uploading: %v", err)
	}
	if _, present := stub.fields["custom_id"]; present {
		t.Fatal("an empty custom id must not be sent as an empty field — BrowserStack would name the build \"\"")
	}
}

// ── a 200 is not a verdict, applied here BEFORE it costs anything ──────────────────────────────────────────

// THE LESSON THIS PACKAGE PAID FOR AT THE HUB. A session creation answered HTTP 200 while failing, and the live
// test walked past two checks before noticing. An upload that answers 200 with no app_url has uploaded nothing,
// and returning "" with a nil error would hand the caller an empty id to put in a capability set — where it
// would fail as "Platform and Browser not valid.", one indirection away from the truth.
func TestATwoHundredWithNoAppIdIsAFailure(t *testing.T) {
	for name, reply := range map[string]string{
		"empty object":      `{}`,
		"an error in words": `{"error":"file format not supported"}`,
		"a message instead": `{"message":"could not fetch the url"}`,
		"not JSON at all":   `<html>gateway</html>`,
		"app_url but blank": `{"app_url":""}`,
	} {
		stub := &uploadStub{status: http.StatusOK, reply: reply}
		app, err := upload(t, stub, "")
		if !errors.Is(err, browserstack.ErrNoAppURL) {
			t.Fatalf("%s: a 200 with no app id must be an error, got app=%q err=%v", name, app.URL, err)
		}
		if app.URL != "" {
			t.Fatalf("%s: no id must come back with the error", name)
		}
	}
}

// THE ACCOUNT'S OWN WORDS SURVIVE into the error, because "the upload failed" without them sends a person to
// guess. The key does not.
func TestTheUploadFailureCarriesWhatTheAccountSaidAndNeverTheKey(t *testing.T) {
	stub := &uploadStub{status: http.StatusUnprocessableEntity, reply: `{"error":"file format not supported"}`}
	_, err := upload(t, stub, "")
	if err == nil || !strings.Contains(err.Error(), "file format not supported") {
		t.Fatalf("the account's own words must reach the reader, got %v", err)
	}
	if strings.Contains(err.Error(), "SUPERSECRETKEY") {
		t.Fatalf("the upload error leaked the key: %v", err)
	}
}

func TestARejectedUploadIsNamedAsACredentialProblem(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		stub := &uploadStub{status: status, reply: `{"message":"Unauthorized"}`}
		_, err := upload(t, stub, "")
		if err == nil || !strings.Contains(err.Error(), "refused the credentials") {
			t.Fatalf("HTTP %d must be named as a credential problem, got %v", status, err)
		}
		if strings.Contains(err.Error(), "SUPERSECRETKEY") {
			t.Fatalf("HTTP %d leaked the key", status)
		}
	}
}

func TestAnUploadRefusesWithoutBothHalvesOrWithoutABuild(t *testing.T) {
	server := (&uploadStub{}).server(t)
	uploader := browserstack.Uploader{Client: server.Client(), BaseURL: server.URL}

	if _, err := uploader.UploadByURL(context.Background(), browserstack.Credentials{User: "u"}, sampleBuild, ""); err == nil {
		t.Fatal("half a credential must never reach the account")
	}
	if _, err := uploader.UploadByURL(context.Background(), creds(), "  ", ""); err == nil {
		t.Fatal("an upload with nothing to upload is a mistake, not a request")
	}
}

// THE bs:// ID IS NOT A SECRET, and this is asserted so nobody later "protects" it into uselessness: it names a
// build inside an account that already belongs to the customer and authenticates nothing on its own. It has to
// be printable — the whole point is that a person reads it and passes it to --browserstack-app.
func TestTheAppIdIsReturnedAndPrintable(t *testing.T) {
	stub := &uploadStub{}
	app, err := upload(t, stub, "")
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	rendered, _ := json.Marshal(app)
	if !strings.Contains(string(rendered), "bs://") {
		t.Fatal("the id must survive being rendered — it is the one thing a caller needs from an upload")
	}
	if strings.Contains(string(rendered), "SUPERSECRETKEY") || strings.Contains(string(rendered), "acme-user") {
		t.Fatal("…and it must not bring a credential with it")
	}
}
