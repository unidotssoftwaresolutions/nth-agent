package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── NETWORK CAPTURE, AS THE FARM ASKS FOR IT OVER THE TUNNEL ───────────────────────────────────────────────────
//
// The farm speaks to a capturing node in three ways, all on the tunnel the agent already keeps (the farm's rack node's
// RelayTunnelClient, ported):
//
//   - capture-start / capture-stop — control frames, no reply (RelayCaptureStart / RelayCaptureStop). Handed to this
//     machine's capture controller, off the read loop: a start runs adb, and a ping must always be answered.
//   - the handback question — a request on the farm-only path gurufarm-node/android/capture-handback, answered HERE, not
//     forwarded to Appium, and only when it carries the farm's mark (X-GuruFarm-Asks: phone-state), which no consumer can
//     put on a frame. Answered off the request worker, as the node answers it: a handback can take a while, and Appium's
//     requests must not wait behind it.
//   - the pull — a stream-start whose path is gfhar:<lease> (stream.go): the lease's masked capture, sent in chunks, and
//     its file deleted once the last chunk has left this machine.

// CaptureControl is what the tunnel asks of this machine's network capture (capture.Controller).
type CaptureControl interface {
	Start(ctx context.Context, lease, serial string)
	Stop(ctx context.Context, lease string)
	HandBackForFarm(ctx context.Context, udid, lease string) (clean bool, observation string)
	Pull(lease string) (data []byte, refusal string)
	Pulled(lease string)
}

const (
	// captureHandbackRoute is NodePhoneStateRoutes.CaptureHandback.
	captureHandbackRoute = "gurufarm-node/android/capture-handback"
	// farmAsks and farmAsksValue are NodePhoneStateRoutes.FarmHeader and its value: the farm's mark on its own questions.
	farmAsks      = "X-GuruFarm-Asks"
	farmAsksValue = "phone-state"
)

// isCaptureHandback: the farm's handback question — the path as the farm sends it (/gurufarm-node/…) or without its
// slash, a query string ignored, case not hiding it (NodePhoneStateRoutes.Which).
func isCaptureHandback(path string) bool {
	trimmed := strings.TrimLeft(path, "/")
	if i := strings.IndexByte(trimmed, '?'); i >= 0 {
		trimmed = trimmed[:i]
	}
	return strings.EqualFold(trimmed, captureHandbackRoute)
}

func askedByTheFarm(headers map[string]string) bool {
	for name, value := range headers {
		if strings.EqualFold(name, farmAsks) && value == farmAsksValue {
			return true
		}
	}
	return false
}

// captureControl hands a capture-start or capture-stop to the controller, off the read loop and under the agent's own
// context: a start or a stop must finish with the phone even when the tunnel drops under it.
func (l *link) captureControl(kind string, data []byte) {
	if l.t.cfg.Capture == nil {
		if l.t.saidNoCapture.CompareAndSwap(false, true) {
			l.t.say("the farm asked for network capture, and this agent was started without it\n")
		}
		return
	}
	var frame struct {
		LeaseID string `json:"leaseId"`
		Serial  string `json:"serial"`
	}
	if json.Unmarshal(data, &frame) != nil {
		return
	}
	switch kind {
	case "capture-start":
		go l.t.cfg.Capture.Start(l.agent, frame.LeaseID, frame.Serial)
	case "capture-stop":
		go l.t.cfg.Capture.Stop(l.agent, frame.LeaseID)
	}
}

// answerHandback answers the farm's handback question (NodeCaptureHandbackRequest → NodeCaptureHandbackResponse), the
// node's way: 200 with the answer when it could be asked, 400 for a question with no phone or lease in it, and 404 — with
// nothing about any phone — for a frame without the farm's mark.
func (l *link) answerHandback(frame requestFrame) {
	reply := func(status int, payload any) {
		body, err := json.Marshal(payload)
		if err != nil {
			return
		}
		encoded := base64.StdEncoding.EncodeToString(body)
		if err := l.queue.WaitBelow(l.life, l.t.responseHighWater()); err != nil {
			return
		}
		_ = l.send(responseFrame{ID: frame.ID, Status: status, Headers: map[string]string{"Content-Type": "application/json"}, BodyBase64: &encoded})
	}
	if !askedByTheFarm(frame.Headers) {
		reply(404, map[string]string{"failure": "This path is answered for the farm only."})
		return
	}
	var ask struct {
		Udid    string `json:"udid"`
		LeaseID string `json:"leaseId"`
	}
	ok := frame.BodyBase64 != nil
	if ok {
		raw, err := base64.StdEncoding.DecodeString(*frame.BodyBase64)
		ok = err == nil && json.Unmarshal(raw, &ask) == nil
	}
	if !ok || !capture.IsAdbSerial(ask.Udid) || !netcap.IsLease(strings.ToLower(ask.LeaseID)) {
		reply(400, map[string]string{"failure": "A capture handback was asked for without a phone and the lease that captured on it."})
		return
	}
	clean, observation := l.t.cfg.Capture.HandBackForFarm(l.agent, ask.Udid, ask.LeaseID)
	answer := struct {
		Clean       bool    `json:"clean"`
		Observation *string `json:"observation"`
	}{Clean: clean}
	if observation != "" {
		answer.Observation = &observation
	}
	reply(200, answer)
}

// pullLease reads the lease a capture pull may open — the farm's CapturePull rule, and one step stricter: the token must
// be exactly gfhar:<lease id>, the frame must say which lease the pull was made on, and the two must be one lease. An
// older farm that names no lease never pulled a capture from this agent, so a pull that names none is refused.
func pullLease(path, frameLease string) (string, string) {
	const prefix = "gfhar:"
	if !strings.HasPrefix(path, prefix) || len(path) != len(prefix)+36 || !netcap.IsLease(strings.ToLower(path[len(prefix):])) {
		return "", "A lease opens only its own network capture, and this pull does not name one the way the farm hands it out (gfhar:<lease id>), so it was not opened."
	}
	lease := strings.ToLower(path[len(prefix):])
	if strings.TrimSpace(frameLease) == "" {
		return "", "A lease opens only its own network capture, and this pull did not say which lease it was made on, so it was not opened."
	}
	if !strings.EqualFold(frameLease, lease) {
		return "", "A lease opens only its own network capture, and this pull named another lease's, so it was not opened."
	}
	return lease, ""
}
