package browserstack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
)

// UploadURL is BrowserStack's App Automate app-upload endpoint. A DIFFERENT HOST AND A DIFFERENT AUTHENTICATION
// MECHANISM from the hub: the hub takes credentials inside the session body, this takes HTTP basic auth. Both
// now exist in this package, and the key-read census names this file for exactly that reason.
const UploadURL = "https://api-cloud.browserstack.com/app-automate/upload"

// ErrNoAppURL is a 200 that carried no app id.
//
// IT HAS ITS OWN ERROR BECAUSE OF WHAT THIS PACKAGE LEARNED THE HARD WAY. On 2026-09-01 the hub answered
// HTTP 200 to a session creation that had failed, and the live test walked past two checks before noticing. A
// transport status is not a verdict here either: an upload that answers 200 without an app_url has not uploaded
// anything, and returning "" with a nil error would hand the caller an empty app id to put in a capability set.
var ErrNoAppURL = errors.New("browserstack: the upload answered without an app id")

// App is what an upload leaves on the customer's account.
//
// NONE OF THIS IS SECRET. A bs:// id names a build inside an account that already belongs to the customer; it
// authenticates nothing on its own, so it may be printed, logged and returned — unlike the access key, which may
// not appear in a return value, an error, a log line or a String().
//
// RETAINED FOR 30 DAYS. BrowserStack deletes uploaded apps after 30 days, so an id that works today answers 404
// in six weeks. That is a support call nobody would diagnose from the code, which is why it is written here
// beside the field that carries the id rather than in a document.
type App struct {
	// URL is the bs://… id a session's `app` capability names.
	URL string
	// CustomID is the caller's own name for this build, echoed back when one was supplied. Uploading again with
	// the same custom id points it at the new build, which is what makes repeat runs traceable in the dashboard.
	CustomID string
}

// Uploader puts builds on the customer's account.
//
// BaseURL is a TEST SEAM and deliberately not a customer surface: no flag and no environment variable reaches
// it, and empty means UploadURL. It exists so the stub-server tests drive the SAME code the live run does — a
// path proved against a different path is a path nobody proved.
type Uploader struct {
	Client  *http.Client
	BaseURL string
}

func (u Uploader) endpoint() string {
	if u.BaseURL != "" {
		return trimSlash(u.BaseURL) + "/app-automate/upload"
	}
	return UploadURL
}

func trimSlash(base string) string { return strings.TrimRight(base, "/") }

func (u Uploader) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

// UploadByURL asks BROWSERSTACK to fetch a build and keep it on the customer's account.
//
// ⚠️ THE ADDRESS MUST BE REACHABLE FROM BROWSERSTACK, NOT FROM HERE, and that is the whole difference between
// this function and UploadFile beside it. We send a string; a machine in a vendor's data centre resolves it and
// makes the request. So a URL that works perfectly in a browser on the customer's own laptop — a build served by
// a local HTTP server, a file on a company intranet, anything behind a NAT or a VPN — fails here, and fails with
// whatever BrowserStack's fetcher says rather than with anything this agent could have predicted.
//
// That is fine for a published sample and wrong for the case this agent exists to serve. **The agent is already
// on the customer's side of that line.** When the bytes are on this machine, UploadFile sends them from here and
// nothing has to be publishable. Use this one only when the build genuinely is on the public internet.
//
// THE KEY GOES INTO AN Authorization HEADER THIS FUNCTION BUILDS, and nowhere else: not into the returned App,
// not into any error this function produces, not into a narration. The failures below name the status and the
// account's own words; none of them formats a credential.
func (u Uploader) UploadByURL(ctx context.Context, creds Credentials, buildURL, customID string) (App, error) {
	if !creds.Configured() {
		return App{}, errors.New("browserstack: an upload needs both a user and a key")
	}
	if strings.TrimSpace(buildURL) == "" {
		return App{}, errors.New("browserstack: an upload needs the URL of a build to fetch")
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	if err := writer.WriteField("url", buildURL); err != nil {
		return App{}, fmt.Errorf("browserstack: building the upload request: %w", err)
	}
	if customID != "" {
		if err := writer.WriteField("custom_id", customID); err != nil {
			return App{}, fmt.Errorf("browserstack: building the upload request: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return App{}, fmt.Errorf("browserstack: building the upload request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint(), &form)
	if err != nil {
		return App{}, fmt.Errorf("browserstack: building the upload request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetBasicAuth(creds.User, creds.Key)

	response, err := u.client().Do(request)
	if err != nil {
		// OUR SIDE, and it says so: nothing was written to the account.
		return App{}, fmt.Errorf("browserstack: could not reach the upload endpoint, so nothing was uploaded: %w", err)
	}
	defer response.Body.Close()
	// ONE RESPONSE HALF FOR BOTH PATHS (see decodeUpload) — a 200 is not a verdict here either; the app id is.
	// Shared rather than copied, because two decoders would be two definitions of "the upload worked".
	return decodeUpload(response)
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
