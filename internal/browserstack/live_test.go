//go:build browserstack_live

// A BUILD TAG, NOT AN ENVIRONMENT GATE, AND THE DIFFERENCE IS MONEY.
//
// An env gate compiles this file into every test binary and leaves one variable between a routine `go test ./...`
// and a real device-minute. Variables leak into CI images, into shells, into a developer's profile — and the
// failure mode is silent spending nobody attributes. A build tag means the code is NOT IN THE BINARY unless
// somebody types the tag: `go test -tags browserstack_live ./internal/browserstack/`. There is no configuration
// that can turn it on by accident, which is the property worth having when the account is a trial with 100
// device-minutes in total.
//
// THE RULE NOW COVERS THE UPLOAD TOO, and not because an upload costs device-minutes — it does not. It WRITES TO
// THE CUSTOMER'S ACCOUNT, which is reason enough for the same tag.
//
// It also means `go build ./...`, `go vet ./...` and `go test ./...` cannot touch BrowserStack, because this
// file is not part of any of them. `go build -tags browserstack_live ./...` and its vet twin exist so that a
// file the default commands never compile is still a file whose breakage somebody sees.

package browserstack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// sessionsStarted counts what this run actually cost. Reported at the end, because "roughly all of them remain"
// is not a measurement.
var sessionsStarted atomic.Int32

// liveSpec is the device the live run asks for. It replaces the two constants this slice deleted: the customer
// names devices now, and a test is a customer like any other.
var liveSpec = browserstack.DeviceSpec{DeviceName: "Google Pixel 9", PlatformVersion: "15.0"}

func liveCredentials(t *testing.T) browserstack.Credentials {
	t.Helper()
	creds := browserstack.Credentials{
		User: os.Getenv("NTH_AGENT_BROWSERSTACK_USER"),
		Key:  os.Getenv("NTH_AGENT_BROWSERSTACK_KEY"),
	}
	// NOT A SKIP. Somebody typed the tag, so they meant to touch the account; returning quietly would report a
	// pass for work that never happened — the silent-skip defect this programme keeps removing.
	if !creds.Configured() {
		t.Fatal("set NTH_AGENT_BROWSERSTACK_USER and NTH_AGENT_BROWSERSTACK_KEY — these tests touch a real account and will not pretend to have run")
	}
	return creds
}

// device is one row of the account's device list.
type device struct {
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Device    string `json:"device"`
	Real      bool   `json:"realMobile"`
}

// accountDevices answers item 0(a). Deliberately implemented HERE and not in the package: a device query is
// production code the REGISTRY slice will need — with a cache, an error model and a place in the vocabulary —
// and inventing that surface now would commit its decisions early. This is a live test asking a question.
func accountDevices(t *testing.T, creds browserstack.Credentials) []device {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, "https://api-cloud.browserstack.com/app-automate/devices.json", nil)
	request.SetBasicAuth(creds.User, creds.Key)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s: %v", browserstack.FailureUnreachable.Describe(), err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the device list answered HTTP %d: %s", response.StatusCode, string(payload))
	}
	var devices []device
	if err := json.Unmarshal(payload, &devices); err != nil {
		t.Fatalf("the device list was not the shape we expected: %v", err)
	}
	return devices
}

// ITEM 0(a): the configured spec was written from a plausible guess and never checked. This says what is
// actually there — and it is free, so there is no reason it was ever a guess.
func TestTheConfiguredDeviceExistsOnThisAccount(t *testing.T) {
	creds := liveCredentials(t)
	devices := accountDevices(t, creds)
	t.Logf("the account offers %d device entries", len(devices))

	for _, d := range devices {
		if d.Device == liveSpec.DeviceName && d.OSVersion == liveSpec.PlatformVersion {
			t.Logf("CONFIGURED SPEC EXISTS: %s / %s (os=%s realMobile=%v)", d.Device, d.OSVersion, d.OS, d.Real)
			return
		}
	}

	// A SESSION-CREATION THAT WAS NEVER GOING TO WORK IS A WORSE MESSAGE THAN A LIST OF WHAT EXISTS.
	var nearest []string
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Device), "pixel") {
			nearest = append(nearest, fmt.Sprintf("%s / %s", d.Device, d.OSVersion))
		}
	}
	sort.Strings(nearest)
	t.Fatalf("the configured spec %s / %s is NOT on this account. Nearest by name:\n  %s",
		liveSpec.DeviceName, liveSpec.PlatformVersion, strings.Join(nearest, "\n  "))
}

// ITEM 6: the whole flow, in order, against the real account.
func TestAnUploadedBuildRunsOnARealDevice(t *testing.T) {
	creds := liveCredentials(t)

	// 1 — THE DEVICE, BEFORE ANY SESSION. Failing here costs nothing; failing at the hub costs a confusing
	// message ("Platform and Browser not valid.") that names neither of its two causes.
	devices := accountDevices(t, creds)
	found := false
	for _, d := range devices {
		if d.Device == liveSpec.DeviceName && d.OSVersion == liveSpec.PlatformVersion {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("refusing to start a session on %s / %s: this account does not offer it — see "+
			"TestTheConfiguredDeviceExistsOnThisAccount for what it does offer",
			liveSpec.DeviceName, liveSpec.PlatformVersion)
	}

	// 2 — THE BUILD. BrowserStack's own published sample, fetched by BrowserStack from their own site: it is
	// not ours to host and not ours to check in. The custom id makes repeat runs traceable in their dashboard.
	uploader := browserstack.Uploader{Client: &http.Client{Timeout: 5 * time.Minute}}
	app, err := uploader.UploadByURL(context.Background(), creds,
		"https://www.browserstack.com/app-automate/sample-apps/android/WikipediaSample.apk", "NthAgentSkeleton")
	if err != nil {
		t.Fatalf("uploading the sample build: %v", err)
	}
	t.Logf("UPLOADED: %s (custom_id=%q) — retained by BrowserStack for 30 days", app.URL, app.CustomID)

	// 3 — THE SESSION, FROM THE PLATFORM'S REAL BODY THROUGH THE AGENT'S OWN MERGE. A live test that hand-wrote
	// its capabilities would prove the hub works and say nothing about this agent.
	body, err := browserstack.Merge([]byte(TheRealBody), creds, app.URL, liveSpec)
	if err != nil {
		t.Fatalf("merging the platform's real body: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	started := time.Now()
	response, err := client.Post(browserstack.HubURL+"/session", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", browserstack.FailureUnreachable.Describe(), err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)

	// THE PAYLOAD IS THE VERDICT, NOT THE TRANSPORT — the correction this slice exists for. The previous version
	// of this test checked `response.StatusCode != http.StatusOK` and walked straight past a failure that
	// answered HTTP 200.
	if failure := browserstack.ClassifyResponse(response.StatusCode, payload); failure != "" {
		t.Fatalf("%s\n  HTTP %d, verbatim: %s", failure.Describe(), response.StatusCode, string(payload))
	}

	var created struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(payload, &created); err != nil || created.Value.SessionID == "" {
		t.Fatalf("no session id came back (HTTP %d): %v / %s", response.StatusCode, err, string(payload))
	}
	sessionID := created.Value.SessionID
	sessionsStarted.Add(1)

	// 5 — ENDED IN A defer, ON EVERY PATH. A session nobody ends bills until idleTimeout expires, and this runs
	// on the failure path too — which is the whole reason it is a defer and not a line at the end.
	defer func() {
		request, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/session/%s", browserstack.HubURL, sessionID), nil)
		ended, err := client.Do(request)
		if err != nil {
			t.Errorf("THE SESSION WAS NOT ENDED and is billing until it times out — end %s by hand: %v", sessionID, err)
			return
		}
		defer ended.Body.Close()
		if ended.StatusCode != http.StatusOK {
			t.Errorf("ending the session answered HTTP %d — check %s in the dashboard", ended.StatusCode, sessionID)
		}
		// 6 — WHAT IT COST, measured rather than estimated.
		t.Logf("COST: session %s ran %s; %d session(s) started by this test run",
			sessionID, time.Since(started).Round(time.Second), sessionsStarted.Load())
	}()

	// 4 — ONE TRIVIAL COMMAND. A standard W3C read every driver answers, so this proves the session is
	// DRIVEABLE without doing anything that costs time or touches the app.
	timeouts, err := client.Get(fmt.Sprintf("%s/session/%s/timeouts", browserstack.HubURL, sessionID))
	if err != nil {
		t.Fatalf("the session was created but could not be driven: %v", err)
	}
	defer timeouts.Body.Close()
	if timeouts.StatusCode != http.StatusOK {
		t.Fatalf("a trivial command answered HTTP %d on a session that was created", timeouts.StatusCode)
	}

	t.Logf("LIVE: session %s created from an uploaded build and driven on %s / %s",
		sessionID, liveSpec.DeviceName, liveSpec.PlatformVersion)
}

// ITEM 6: THE LAST GUESS IN THIS PACKAGE, SETTLED FOR NOTHING.
//
// FailureRejected's arms were written from documented prose and no rejected authentication has ever been seen on
// the wire. This function has already been wrong once by trusting prose — it returned "" for a real failure
// because it read the transport instead of the payload — so the arms are not left standing on the same footing.
//
// ONE ATTEMPT, NOT A LOOP: repeated rejected authentications are how an account gets rate-limited.
// THE REAL USERNAME WITH A CORRUPTED KEY: a username invented for a test may belong to somebody else.
// NO SESSION STARTS, so this costs no device-minutes.
func TestWhatARejectedAuthenticationActuallyLooksLike(t *testing.T) {
	creds := liveCredentials(t)
	corrupted := browserstack.Credentials{User: creds.User, Key: creds.Key + "-corrupted"}

	body, err := browserstack.Merge([]byte(TheRealBody), corrupted, "bs://0000000000000000000000000000000000000000", liveSpec)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Post(browserstack.HubURL+"/session", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", browserstack.FailureUnreachable.Describe(), err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)

	// RECORDED VERBATIM WHETHER OR NOT THE ASSERTION HOLDS — the bytes are the point of this test, and a failing
	// assertion that hid them would leave the next author guessing exactly as this one did.
	t.Logf("REJECTED-KEY ENVELOPE, verbatim:\n  HTTP %d\n  %s", response.StatusCode, string(payload))

	// A SESSION MUST NOT HAVE STARTED. If a corrupted key somehow produced one, that is a far bigger finding
	// than the classification — and it would be billing.
	var created struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	_ = json.Unmarshal(payload, &created)
	if created.Value.SessionID != "" {
		t.Fatalf("A CORRUPTED KEY STARTED A SESSION (%s) — ending it now is the least of the problem", created.Value.SessionID)
	}

	got := browserstack.ClassifyResponse(response.StatusCode, payload)
	if got != browserstack.FailureRejected {
		t.Fatalf("a rejected authentication classified as %q, not %q. The envelope is logged above — rewrite "+
			"FailureRejected's arms from THOSE BYTES, and keep the documented arms alongside rather than "+
			"replacing them: this function has been wrong once by trusting prose and the observed shape is only "+
			"one account on one day", got, browserstack.FailureRejected)
	}
}

// TestAFileOnThisMachineIsUploadedFromHere is the ONE case the default suite cannot prove: that BrowserStack's
// real endpoint accepts the multipart shape this agent streams, with the credentials this agent sends.
//
// ⚠️ WRITTEN, NOT RUN. It is behind the same build tag as everything else in this file, and for the same reason
// that tag exists at all: an upload costs no device-minutes and WRITES TO THE CUSTOMER'S ACCOUNT, which is the
// rule. The operator runs it deliberately, on his own clock:
//
//	printf 'BrowserStack access key: ' && read -rs NTH_AGENT_BROWSERSTACK_KEY && echo && export NTH_AGENT_BROWSERSTACK_KEY  # paste this line ALONE
//	export NTH_AGENT_BROWSERSTACK_USER=
//	export NTH_AGENT_BROWSERSTACK_BUILD=/absolute/path/to/app-release.apk
//	go test -tags browserstack_live -run TestAFileOnThisMachineIsUploadedFromHere ./internal/browserstack/
//
// (The `read -rs` form is deliberate: the value is never an argument, so it is not in `ps`, and never a command,
// so it is not in the shell's history file. Both exposures are named in the README for the same reason.)
//
// WHAT IT PROVES THAT THE STUB CANNOT: the field name BrowserStack actually reads, that a chunked request with
// no declared Content-Length is accepted by their front end (the stub is Go's own server, which accepts one by
// construction — a real load balancer need not), and that the id comes back in the field this package parses.
func TestAFileOnThisMachineIsUploadedFromHere(t *testing.T) {
	creds := liveCredentials(t)

	path := strings.TrimSpace(os.Getenv("NTH_AGENT_BROWSERSTACK_BUILD"))
	if path == "" {
		t.Skip("set NTH_AGENT_BROWSERSTACK_BUILD to the absolute path of a build on this machine")
	}
	// REFUSED HERE FIRST, exactly as the verb does it: a live test that spent an upload to be told the file was
	// the wrong shape would have proved nothing except that the check is missing.
	size, err := browserstack.InspectBuild(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Logf("uploading %s (%s) — this WRITES to the account and the build is retained for 30 days",
		path, browserstack.HumanBytes(size))

	uploader := browserstack.Uploader{Client: browserstack.FileUploadClient()}
	ctx, cancel := context.WithTimeout(context.Background(), browserstack.UploadCeiling)
	defer cancel()

	app, err := uploader.UploadFile(ctx, creds, path, "NthAgentFileUpload")
	if err != nil {
		t.Fatalf("uploading %s: %v", path, err)
	}
	if app.URL == "" {
		t.Fatal("the upload answered without an app id, which UploadFile should already have refused")
	}
	t.Logf("UPLOADED FROM THIS MACHINE: %s (custom_id=%q). Use it with: "+
		"nth-agent connect --browserstack-app %s", app.URL, app.CustomID, app.URL)
}
