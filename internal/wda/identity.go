package wda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ── WHAT ANSWERS ON AN IPHONE'S WEBDRIVERAGENT PORT (WHAT_THE_FIRST_TRY_SHOWED, decision A6) ─────────────────────────
//
// The port this machine declares for an iPhone is the port ON THE PHONE the driver reaches WebDriverAgent on (the device
// follows wdaLocalPort; see AppiumDefaultAgentPort). ReadStatus takes any JSON answer there for WebDriverAgent, and the
// agent stops WebDriverAgent after five idle minutes, so another app on the phone could hold the port by then and be taken
// for it by the next session, which would send it the session's commands and typed values.
//
// WebDriverAgent says which build it is: its /status carries value.build.productBundleIdentifier, the
// WDA_PRODUCT_BUNDLE_IDENTIFIER it was started with (appium-webdriveragent 13.2.0, FBSessionCommands.m:269-281). Appium
// starts it with the updatedWDABundleId the session names (lib/xcodebuild.ts:446), which the farm's runner takes from this
// machine's --ios-wda-bundle-id; a preinstalled launch adds the .xctrunner suffix (lib/webdriveragent.ts:180). Every
// release from 4.10.2, where the bundled changelog starts, to 13.2.0 carries the field, and no entry removes it. So the
// answer is taken for this machine's WebDriverAgent only when it says it is the declared build. It is what WebDriverAgent
// says about itself, not a proof: a program that copies its answer word for word passes.

// IdentityKind is what answers on a phone's WebDriverAgent port.
type IdentityKind int

const (
	// IdentityNotAsked: the question could not be asked (no forward to the phone). Found says why.
	IdentityNotAsked IdentityKind = iota
	// IdentityNothing: nothing listens there — a connection to it is refused or reset. Appium starts WebDriverAgent there.
	IdentityNothing
	// IdentityDeclared: the WebDriverAgent build this machine declared answers, holding SessionID ("" = none).
	IdentityDeclared
	// IdentityStranger: something answers that does not say it is the declared build. Found says what it did.
	IdentityStranger
)

// Identity is one answer to the question.
type Identity struct {
	Is        IdentityKind
	Bundle    string // the build it says it is, as it said it ("" when it said none)
	SessionID string // the session the declared WebDriverAgent holds, "" for none
	Found     string // for a stranger, what it did, said after "it": "says it is the WebDriverAgent build \"…\""
}

// Identify asks what answers at baseURL (a forward to the phone's port) and judges it against the declared bundle id.
// The client's timeout bounds the ask; a listener that takes the connection and does not answer within it is a stranger.
func Identify(ctx context.Context, client *http.Client, baseURL, declaredBundle string) Identity {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/status", nil)
	if err != nil {
		return Identity{Is: IdentityNotAsked, Found: err.Error()}
	}
	response, err := client.Do(request)
	if err != nil {
		if timedOut(err) {
			return Identity{Is: IdentityStranger, Found: "takes the connection and does not answer"}
		}
		return Identity{Is: IdentityNothing}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	switch {
	case err != nil:
		return Identity{Is: IdentityStranger, Found: "answers, and its answer could not be read"}
	case response.StatusCode != http.StatusOK:
		return Identity{Is: IdentityStranger, Found: fmt.Sprintf("answers HTTP %d where WebDriverAgent's status answers 200", response.StatusCode)}
	}
	var status struct {
		SessionID *string `json:"sessionId"`
		Value     struct {
			Build struct {
				Bundle string `json:"productBundleIdentifier"`
			} `json:"build"`
		} `json:"value"`
	}
	if json.Unmarshal(body, &status) != nil {
		return Identity{Is: IdentityStranger, Found: "answers with something that is not WebDriverAgent's status"}
	}
	bundle := status.Value.Build.Bundle
	if bundle == "" {
		return Identity{Is: IdentityStranger, Found: "answers, and does not say which WebDriverAgent build it is"}
	}
	if !IsTheDeclaredBuild(bundle, declaredBundle) {
		shown := bundle
		if len(shown) > 200 {
			shown = shown[:200] + "…"
		}
		return Identity{Is: IdentityStranger, Bundle: bundle, Found: "says it is the WebDriverAgent build " + strconv.Quote(shown)}
	}
	session := ""
	if status.SessionID != nil {
		session = *status.SessionID
	}
	return Identity{Is: IdentityDeclared, Bundle: bundle, SessionID: session}
}

// IsTheDeclaredBuild says whether the bundle a WebDriverAgent reports is the one declared: the same, or the same with the
// .xctrunner suffix a preinstalled launch adds, either way round. Nothing is declared: nothing is the declared build.
func IsTheDeclaredBuild(reported, declared string) bool {
	if declared == "" {
		return false
	}
	const suffix = ".xctrunner"
	base := strings.TrimSuffix(declared, suffix)
	return reported == base || reported == base+suffix
}

func timedOut(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}
