package wda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ── WHAT WE LEAVE RUNNING ON SOMEBODY'S PHONE ────────────────────────────────────────────────────────────────
//
// After a run finishes, WebDriverAgent keeps running on the device and the phone keeps wearing an
// **Automation Running** banner. That is not a leak: Appium's XCUITest driver defaults `useNewWDA` to false so it
// can REUSE a listener from a previous session, which on our own rack is free speed — the operator measured 6702 ms
// cold against 1693 ms warm. On a phone somebody lent us it is our software still attached to their device, under a
// banner nobody disclosed, after the test they asked for finished.
//
// THE QUESTION HAD TO BE ESTABLISHED, NOT ASSUMED, and three candidates were measured on 2026-08-24:
//
//	Appium's own session list   GET /sessions → 404 on Appium 3.5.0; GET /appium/sessions → 500,
//	                           "Potentially insecure feature 'session_discovery' has not been enabled".
//	                           Asking Appium would mean asking the customer to restart THEIR server with
//	                           --allow-insecure. We do not reconfigure a machine we do not own.
//	go-ios process list        appservice.ListProcesses / KillProcess need the CoreDevice tunnel:
//	                           "missing tunnel address and RSD port. To start the tunnel, run `ios tunnel start`".
//	                           That is a privileged daemon on their Mac. Same rule, same answer.
//	WDA ITSELF                 reachable over a plain usbmuxd forward — no tunnel, no reconfiguration — and it
//	                           answers the exact question. THIS IS THE ONE.
//
// `GET /status` is served `.withoutSession` (FBSessionCommands.m:48) and every WDA response carries
// `sessionId = [FBSession activeSession].identifier ?: NSNull.null` (FBResponsePayload.m:94). So a null sessionId is
// WebDriverAgent saying, about itself, that nothing is driving it — which is the question, asked of the thing that
// knows, rather than the easiest thing to code. `GET /wda/shutdown` (FBWebServer.m:265) is its own stop: we ask it
// to end itself rather than killing a process on somebody's phone.

// AppiumDefaultAgentPort is the port WebDriverAgent listens on when NOBODY SAYS OTHERWISE — Appium's
// `WDA_AGENT_PORT` (lib/webdriveragent.ts:34).
//
// IT IS NOT THE PORT THIS PLATFORM'S PHONES LISTEN ON, and a docblock here used to say it was: *"Appium's
// wdaLocalPort capability moves the HOST end of the forward, never this one."* The driver resolves the device
// port as `((realDevice ? wdaRemotePort : null) ?? wdaLocalPort) || WDA_AGENT_PORT` (webdriveragent.ts:90-91),
// and this platform sends a wdaLocalPort and no wdaRemotePort — so the device follows the capability. Kept as a
// named constant because it is the value the rule REPLACES, and because a reader who finds 8100 in a log should
// find the sentence that says why it is not ours. See PortFor.
const AppiumDefaultAgentPort = 8100

// RunnerState is what one probe established about the WebDriverAgent on one device. FOUR ANSWERS, because the
// difference between them is the difference between a cleanup and a kill.
type RunnerState int

const (
	// RunnerUnknown: the question could not be asked or answered. NEVER a reason to stop anything — an unanswered
	// question is not evidence of idleness, and this is the state a flaky cable produces.
	RunnerUnknown RunnerState = iota
	// RunnerNotListening: nothing is accepting on the device's WDA port. There is nothing to stop, and nothing to
	// worry about: this is the state a phone is in when we have already cleaned up, or never started.
	RunnerNotListening
	// RunnerIdle: WebDriverAgent is up and NO session is attached — it says so itself.
	RunnerIdle
	// RunnerInUse: WebDriverAgent is up and something has a session on it right now. Stopping it here is the
	// surprise-mid-suite failure, which is the same failure as leaving it running, wearing the opposite sign.
	RunnerInUse
)

func (s RunnerState) String() string {
	switch s {
	case RunnerNotListening:
		return "not-listening"
	case RunnerIdle:
		return "idle"
	case RunnerInUse:
		return "in-use"
	default:
		return "unknown"
	}
}

// RunnerStatus is one probe's result: the state, and — when it could not be established — why. NOT `Runner`,
// which this package already uses for the xcodebuild process seam.
type RunnerStatus struct {
	State     RunnerState
	SessionID string // non-empty only for RunnerInUse
	Reason    string // non-empty only for RunnerUnknown
}

// statusEnvelope is the part of WDA's response this cares about. `sessionId` is a JSON null when nothing holds a
// session, which is why it is a pointer: a MISSING field and an EXPLICIT null are the same answer here, but neither
// is the empty string, and decoding into a plain string would quietly turn "no session" and "a session whose id is
// empty" into one value.
type statusEnvelope struct {
	SessionID *string `json:"sessionId"`
	Value     struct {
		Ready *bool `json:"ready"`
	} `json:"value"`
}

// ReadStatus asks one WebDriverAgent about itself. The HTTP client is a parameter so a seal can answer for it.
func ReadStatus(ctx context.Context, client *http.Client, baseURL string) RunnerStatus {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/status", nil)
	if err != nil {
		return RunnerStatus{State: RunnerUnknown, Reason: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		// NOTHING IS LISTENING is a real, useful answer and it is what a closed port produces over a forward —
		// measured on both of the operator's phones today: "read tcp …: connection reset by peer". It is reported
		// as its own state rather than as an error, because "there is nothing to stop" is not "I could not tell".
		return RunnerStatus{State: RunnerNotListening, Reason: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return RunnerStatus{State: RunnerUnknown, Reason: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return RunnerStatus{State: RunnerUnknown, Reason: fmt.Sprintf("WebDriverAgent answered %d", resp.StatusCode)}
	}

	var env statusEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return RunnerStatus{State: RunnerUnknown, Reason: "WebDriverAgent's status was not readable: " + err.Error()}
	}
	if env.SessionID != nil && *env.SessionID != "" {
		return RunnerStatus{State: RunnerInUse, SessionID: *env.SessionID}
	}
	return RunnerStatus{State: RunnerIdle}
}

// Shutdown asks WebDriverAgent to end itself — its own route, not a process kill. We stop the thing we did not
// start by asking it, which is the only way to do it without touching anything else on somebody's machine.
func Shutdown(ctx context.Context, client *http.Client, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/wda/shutdown", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		// WDA tears its own server down while answering, so a dropped connection here is the SUCCESS case as often
		// as it is a failure. The caller's next probe is what settles it — this never claims more than it did.
		return nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return nil
}

// ProbeTimeout bounds one status read. Short on purpose: this runs on every heartbeat and a phone that does not
// answer promptly is a phone this tick knows nothing about, which is a state the decision already handles.
const ProbeTimeout = 3 * time.Second
