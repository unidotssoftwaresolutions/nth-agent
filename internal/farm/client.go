package farm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CallStatus is how one farm call resolved — each of the four wire answers gets its own name, so the
// loop can give each its own behaviour (and a test can drive each).
type CallStatus int

const (
	CallOK CallStatus = iota
	// CallUnauthorized — 401. TERMINAL: a refused token — whatever the cause, and the agent does not guess it — does
	// not heal by retrying, and hammering an auth failure is how a customer's laptop becomes our incident. The error
	// beside it is a *RefusedError carrying the farm's own words for why, when it gave any.
	CallUnauthorized
	// CallNotFound — 404 on heartbeat: the node row is gone (purged). Re-register with the SAME id —
	// that is what the sentence tells us to do, and GF2 idempotent registration makes it safe.
	CallNotFound
	// CallBusy — 503: the farm applied NOTHING and says the next tick retries. Doing anything more
	// (like re-registering) would undo the farm's own contention fix. A 503 with no envelope is the farm's address
	// answering in its place (a *GatewayError), and waits for the tick the same way.
	CallBusy
	// CallTransport — DNS, refused, timeout, or a non-2xx this client has no name for. Backoff: these
	// heal (a router reboots, a deploy finishes). A non-2xx still carries what the farm said (an *AnswerError), or that
	// the farm's address answered in its place (a *GatewayError: a 502 or 504 with no envelope).
	CallTransport
	// CallUnsafeTransport — the base URL itself would expose the token (plaintext to a non-loopback
	// host). TERMINAL: no amount of retrying makes http become https — backing off against it forever
	// was treating an unfixable condition like a flaky one, while the equally-unfixable revoked token
	// was correctly terminal.
	CallUnsafeTransport
	// CallTooMany — 429: the farm ANSWERED, and this workspace's machine is past one of its limits (machines, devices on one
	// machine; AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO). Backoff as for a transport failure, never a re-registration, never
	// a stop — the farm's NodesController asks exactly that. It was a CallTransport, so the person read "cannot reach the
	// farm" about a farm that had answered, and the tunnel's state heard the farm had not.
	CallTooMany
)

// AnswerBodyLimit is the most of a register or heartbeat answer this agent reads (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST):
// 1 MiB. Today's farm answers a registration with a node id and a heartbeat with three lists — work it assigned,
// which no customer's machine is ever given, and the ids of this node's leased devices, at most NodeReportLimits.MaxDevices
// (200) of them — so its largest real answer is a few tens of kilobytes. The bound is on the bytes as the agent reads them,
// after Go's transport has opened a gzip answer, so a small compressed body cannot unfold past it.
const AnswerBodyLimit = 1 << 20

// Client speaks to the farm's node plane. Outbound-only: it opens no port, listens on nothing.
type Client struct {
	BaseURL string
	// Token is the workspace agent token. IT IS NEVER WRITTEN ANYWHERE — not logged, not persisted,
	// not echoed into an error, never a URL component. It exists in memory and on the X-Agent-Token
	// header — and that header's TRANSPORT is enforced, not assumed: ValidateBaseURL refuses plaintext
	// to any non-loopback host before the first request, and the forced redirect policy (httpClient)
	// refuses to carry the header to any hop that rule was never checked against.
	Token string
	HTTP  *http.Client
}

// HeartbeatWork is what the farm asked of us. This slice cannot perform any of it — the loop prints
// that fact rather than silently discarding it.
type HeartbeatWork struct {
	ProvisionTasks int
	Commands       int
}

// HeartbeatAnswer is the farm's answer to one heartbeat: the work it asked for, and what it says about this agent's release.
type HeartbeatAnswer struct {
	HeartbeatWork
	// NewerAgentVersion is the newest agent release, when the farm says this agent is older than it (newerAgentVersion,
	// AN_AGENT_THAT_KNOWS_IT_IS_OLD); "" when it says nothing. See NewerAgentVersion for what is read as one.
	NewerAgentVersion string
}

// Registration is the farm's answer to a registration.
type Registration struct {
	// NodeID is the id the farm confirmed.
	NodeID string
	// NewerAgentVersion is what HeartbeatAnswer's is, said at the door.
	NewerAgentVersion string
}

// Register announces the node. On success returns the farm-confirmed node id.
// (A `liveViewAvailability = LiveViewUnavailable` var stood here, sent by every registration, "a constant for the same
// reason: every stream-start frame is refused on every host until the stream family is ported". It was ported, so it
// stopped being a constant: the live view is now the report's declaration — see DeclareLiveView.)

func (c *Client) Register(ctx context.Context, nodeID, hostName string, report Report) (Registration, CallStatus, error) {
	// WHAT THIS MACHINE SAYS ABOUT CAPTURE: the report's declaration (internal/capture decides it), and Disabled when a
	// report carries none — never nothing.
	captureAvailability := CaptureDisabled
	if report.Capture != nil {
		captureAvailability = *report.Capture
	}
	body := RegisterRequest{
		HostName: hostName, Devices: report.Devices, NodeID: nodeID,
		UnusableDevices: report.Unusable, PlatformOutcomes: report.Outcomes,
		Reachability: report.Reachability,
		// THE PHONES THIS MACHINE HAS, named — sent at the door and every tick after it, so a first
		// registration never knows less than the heartbeats that follow.
		PresentDevices: report.Present,
		// THIS NODE IS NOT SILENT ABOUT CAPTURE: off, on, or on and unable — so the farm withholds the HAR pull URL with a
		// named reason, or hands it out, instead of promising one this machine would only refuse (see wire.go).
		CaptureAvailability: &captureAvailability,
		// AND IT IS NOT SILENT ABOUT THE LIVE VIEW EITHER: Available when a local Appium can start a phone's screen
		// server, Unavailable otherwise, so the farm can tell a consumer "no mirror, and why" rather than handing over
		// an endpoint that will never produce a frame (DeclareLiveView; per phone, the descriptor's MirrorPort).
		LiveViewAvailability: report.LiveView,
		// THE SIGNATURE BELONGS TO THE MACHINE. nil when this machine declared none — omitted from the JSON, so a
		// farm that has never heard of the field and an agent that has nothing to say produce the same bytes.
		// Registration-only; see RegisterRequest.IosSigning for why, and why that differs from Reachability.
		IosSigning: report.IosSigning,
		// WHICH BUILD IS TALKING. Nobody is ever standing at the machine, so `--version` answers a question the
		// only person who can act on it never gets to ask. nil when the caller did not say — see wire.go.
		AgentVersion: report.AgentVersion,
	}
	var data registerData
	status, err := c.post(ctx, "/v1/nodes/register", body, &data)
	if status != CallOK {
		return Registration{}, status, err
	}
	return Registration{NodeID: data.NodeID, NewerAgentVersion: NewerAgentVersion(data.NewerAgentVersion)}, CallOK, nil
}

// Heartbeat sends the same facts as registration for this tick.
func (c *Client) Heartbeat(ctx context.Context, nodeID string, report Report) (HeartbeatAnswer, CallStatus, error) {
	body := HeartbeatRequest{
		Devices: report.Devices, UnusableDevices: report.Unusable, PlatformOutcomes: report.Outcomes,
		Reachability: report.Reachability, PresentDevices: report.Present,
	}
	var data heartbeatData
	status, err := c.post(ctx, "/v1/nodes/"+nodeID+"/heartbeat", body, &data)
	if status != CallOK {
		return HeartbeatAnswer{}, status, err
	}
	return HeartbeatAnswer{
		HeartbeatWork:     HeartbeatWork{ProvisionTasks: len(data.ProvisionTasks), Commands: len(data.Commands)},
		NewerAgentVersion: NewerAgentVersion(data.NewerAgentVersion),
	}, CallOK, nil
}

// Leave tells the farm this node is leaving, so it takes the node's devices off offer at once instead of when its heartbeat
// window lapses (POST /v1/nodes/{id}/leave, AN_AGENT_THAT_KNOWS_IT_IS_OLD). The farm reads no body. CallOK means the node is
// offline there now, whichever of its three outcomes the farm answered; any other status means the goodbye was not taken —
// a farm older than the door answers 404 — and the heartbeat window still covers it.
//
// Send it only once the loop has stopped: a registration after it brings the node back. The caller bounds it with ctx.
func (c *Client) Leave(ctx context.Context, nodeID string) (CallStatus, error) {
	return c.post(ctx, "/v1/nodes/"+nodeID+"/leave", nil, nil)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) (CallStatus, error) {
	// THE AUTHORITY IS AT THE POINT OF USE: the scheme is validated on the same call that attaches the
	// header, so a caller that constructs a Client directly — skipping the loop's early, friendly
	// startup check — still cannot hand the token to a plaintext transport. The startup check stays as
	// the early refusal; this is the guarantee (the filter-and-write relationship the farm settled).
	if err := ValidateBaseURL(c.BaseURL); err != nil {
		return CallUnsafeTransport, err
	}
	// A nil body sends none (the leave).
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return CallTransport, fmt.Errorf("encoding the request: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return CallTransport, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-Agent-Token", c.Token)

	response, err := c.httpClient().Do(request)
	if err != nil {
		// The error may quote the URL — never the token, which rides a header. Passed through as-is.
		return CallTransport, err
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized:
		// THE FARM SAYS WHY — read it (a little of it), make it safe to print, and carry it out. The status is
		// unchanged: still CallUnauthorized, still terminal. See refusal.go.
		return CallUnauthorized, &RefusedError{Reason: readRefusal(response.Body, c.Token)}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		// EVERY OTHER ANSWER IS READ THE SAME WAY (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO): the farm's sentence, through the
		// same door and the same bound as a 401's, out beside the status. Only a 401 was read before. AND A 502, 503 OR 504
		// WITH NO ENVELOPE IS NOT THE FARM'S (NTH_AGENT_0_4_4): its address answered in its place. The status stays as below.
		raw := ReadAnswerBody(response.Body)
		answered := GatewayOr(response.StatusCode, raw, &AnswerError{Status: response.StatusCode, Reason: readRefusal(bytes.NewReader(raw), c.Token)})
		switch response.StatusCode {
		case http.StatusNotFound:
			return CallNotFound, answered
		case http.StatusTooManyRequests:
			return CallTooMany, answered
		case http.StatusServiceUnavailable:
			return CallBusy, answered
		}
		return CallTransport, answered
	}

	// AN ANSWER THIS AGENT CANNOT USE is still an answer (NTH_AGENT_0_4_3 item 5): the status stays CallTransport, so the
	// backoff and the tunnel's evidence are as before, and the error says the farm answered.
	unusable := func(what string, err error) (CallStatus, error) {
		return CallTransport, &UnusableAnswerError{Status: response.StatusCode, What: what, Err: err}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, AnswerBodyLimit+1))
	if err != nil {
		return unusable("could not be read", err)
	}
	if len(raw) > AnswerBodyLimit {
		return unusable(fmt.Sprintf("was larger than the %d bytes this agent reads", AnswerBodyLimit), nil)
	}
	var wrapped envelope[json.RawMessage]
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return unusable("was not readable", err)
	}
	if out != nil && wrapped.Data != nil {
		if err := json.Unmarshal(wrapped.Data, out); err != nil {
			return unusable("had an unexpected shape", err)
		}
	}
	return CallOK, nil
}
