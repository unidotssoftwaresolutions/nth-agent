package browserstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// DevicesURL is the account's own device list. A REST read on the API host — it starts no session and costs no
// device-minutes, which is what makes validating a spec at startup affordable at all.
const DevicesURL = "https://api-cloud.browserstack.com/app-automate/devices.json"

// Catalogue asks the customer's account what it offers.
//
// THE KEY GOES INTO AN Authorization HEADER THIS FUNCTION BUILDS and nowhere else — the third and last exit in
// this package, and the key-read census names this file for it. Nothing here returns, formats or narrates it;
// the failures below carry the HTTP status and the account's own words.
func (u Uploader) Catalogue(ctx context.Context, creds Credentials) ([]CatalogueDevice, error) {
	if !creds.Configured() {
		return nil, errors.New("browserstack: reading the device list needs both a user and a key")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.devicesEndpoint(), nil)
	if err != nil {
		return nil, fmt.Errorf("browserstack: building the device-list request: %w", err)
	}
	request.SetBasicAuth(creds.User, creds.Key)

	response, err := u.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("browserstack: could not reach the device list, so nothing was checked: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("browserstack: reading the device list: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, errors.New(FailureRejected.Describe())
	}

	var devices []CatalogueDevice
	if err := json.Unmarshal(payload, &devices); err != nil {
		return nil, fmt.Errorf("browserstack: the device list was not the shape we expected (HTTP %d): %w",
			response.StatusCode, err)
	}
	// A 200 IS NOT A VERDICT — the lesson this package paid for at the hub, applied a third time. An empty list
	// is not a catalogue; treating it as one would refuse every spec the customer has.
	if len(devices) == 0 {
		return nil, fmt.Errorf("browserstack: the device list came back empty (HTTP %d) — nothing can be checked "+
			"against it, so no spec is refused on its evidence", response.StatusCode)
	}
	return devices, nil
}

func (u Uploader) devicesEndpoint() string {
	if u.BaseURL != "" {
		return trimSlash(u.BaseURL) + "/app-automate/devices.json"
	}
	return DevicesURL
}
