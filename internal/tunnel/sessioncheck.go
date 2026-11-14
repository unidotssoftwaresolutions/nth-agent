package tunnel

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ── A SESSION THAT STARTED ON A WEBDRIVERAGENT THIS MACHINE DID NOT DECLARE IS ENDED (WHAT_THE_FIRST_TRY_SHOWED) ─────────
//
// Once Appium has started a session on a phone here, the phone's set is asked whether the session is driven through the
// WebDriverAgent this machine declared (allowed.Phones.Started; internal/connect/wdaidentity.go). When it is not, the agent
// ends the session at the local Appium, keeps nothing of it — no route, no build held, no mirror — and answers the farm's
// create with the reason instead of Appium's 200, so the run stops there and says why.

// endStarted ends session id at the local Appium and answers the create's frame with why.
func (t *Tunnel) endStarted(frameID, id, why string) responseFrame {
	ended := "and ended it"
	ctx, cancel := context.WithTimeout(context.Background(), endStartedBound)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(t.endpointFor(toLocal), "/")+"/session/"+id, nil)
	if err == nil {
		var response *http.Response
		if response, err = t.clientFor(toLocal).Do(request); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			if response.StatusCode >= 300 {
				ended = "and the local Appium did not end it (HTTP " + strconv.Itoa(response.StatusCode) + "), so Appium's own timeout will"
			}
		}
	}
	if err != nil {
		ended = "and the local Appium could not be reached to end it, so Appium's own timeout will"
	}
	t.say(why + " (" + ended + ")\n")
	return errorResponse(frameID, "Relay forward error: "+why)
}

// endStartedBound is the longest the agent waits for Appium to end such a session.
const endStartedBound = 30 * time.Second
