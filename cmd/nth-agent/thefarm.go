package main

import (
	"context"
	"net/http"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ios"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/state"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// theFarm is the farm connect connects to: --farm, then NTH_AGENT_FARM, then the one farm a customer has
// (WHAT_THE_FIRST_TRY_SHOWED). Whichever is used goes through the same check (farm.ValidateBaseURL) at the first call,
// and the start banner names it.
func theFarm(flag, env string) string {
	return firstNonEmpty(flag, env, farm.DefaultBaseURL)
}

// stateFolder is the folder doctor names as this account's agent state on this machine; "" when it cannot be found.
func stateFolder() string {
	configDir, err := state.ConfigDir()
	if err != nil {
		return ""
	}
	return state.Folder(configDir)
}

// identifyWda asks what answers on one iPhone's WebDriverAgent port ON THE PHONE, through a forward that lives as long as the
// question (ios.WithWdaPort, as the idle probe asks), judged against the build this machine declared (decision A6,
// internal/connect/wdaidentity.go). A forward that cannot be opened is a question not asked.
func identifyWda(client *http.Client, declaredBundle string) func(ctx context.Context, udid string, port uint16) wda.Identity {
	return func(ctx context.Context, udid string, port uint16) wda.Identity {
		var id wda.Identity
		if err := ios.WithWdaPort(udid, port, func(baseURL string) error {
			id = wda.Identify(ctx, client, baseURL, declaredBundle)
			return nil
		}); err != nil {
			return wda.Identity{Is: wda.IdentityNotAsked, Found: err.Error()}
		}
		return id
	}
}
