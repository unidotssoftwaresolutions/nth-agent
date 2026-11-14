// Package appium probes the LOCAL Appium endpoint a customer points the agent at. A FLAG IS A CLAIM,
// NOT A CAPABILITY: the endpoint is probed before any device is offered, so `--appium http://nothing`
// cannot re-create the phantom by configuration.
package appium

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Status asks Appium's own status route — GET /status, the W3C-standard liveness every Appium serves
// (1.x and 2.x alike), chosen over inventing a probe because it is the route Appium itself documents
// as "am I ready". Any 2xx is an answer; anything else is a probe that could not ask.
func Status(ctx context.Context, client *http.Client, baseURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/status", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("the Appium status route answered %d", response.StatusCode)
	}
	return nil
}
