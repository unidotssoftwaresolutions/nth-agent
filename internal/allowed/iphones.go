package allowed

import (
	"context"
	"encoding/json"
	"strings"
)

// ── THE QUESTION ASKED OF A PHONE AS ITS SESSION STARTS (ANOTHER_ACCOUNT_AND_LIMITS) ─────────────────────────────────
//
// Which phones are on offer is rewritten every tick; some facts about a phone must be asked when its session starts,
// not ten seconds before. The connect loop gives the set its question with the offer (OfferHeld): for an iPhone,
// whether the port its WebDriverAgent will be reached on is held by a program another account runs, or is another
// phone's (internal/connect/wdaports.go). A phone the question says no about is refused in its words, and nothing is
// sent.

// startedQuestion is the question asked of a phone once its session has started: "" or why the session must be ended.
type startedQuestion func(ctx context.Context, udid string) string

// OfferAsking is OfferHeld with a second question, asked once a session on one of the phones has started
// (WHAT_THE_FIRST_TRY_SHOWED): for an iPhone, whether the session is driven through the WebDriverAgent this machine
// declared (internal/connect/wdaidentity.go). nil asks nothing.
func (p *Phones) OfferAsking(udids []string, held func(udid string, wdaPort int) string, started func(ctx context.Context, udid string) string) {
	p.OfferHeld(udids, held)
	p.mu.Lock()
	p.started = started
	p.mu.Unlock()
}

// Started asks the set's second question of every phone a session that has started names, from the create that started
// it: "" when there is none or every answer is yes, otherwise why the session must be ended.
func (p *Phones) Started(ctx context.Context, createBody []byte) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if started == nil {
		return ""
	}
	for _, udid := range PhonesNamed(createBody) {
		if why := started(ctx, udid); why != "" {
			return why
		}
	}
	return ""
}

// askHeld asks the set's question of the phone a set of capabilities names, with the wdaLocalPort it names (0 = none).
// "" when there is no question, or its answer is yes.
func (p *Phones) askHeld(set map[string]any) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	held := p.held
	p.mu.Unlock()
	if held == nil {
		return ""
	}
	udid, port := "", 0
	read := func(m map[string]any) {
		for key, value := range m {
			switch strings.TrimPrefix(key, "appium:") {
			case "udid":
				if s, ok := value.(string); ok {
					udid = s
				}
			case "wdaLocalPort":
				if n, ok := value.(json.Number); ok {
					if v, err := n.Int64(); err == nil {
						port = int(v)
					}
				}
			}
		}
	}
	read(set)
	if options, ok := set["appium:options"].(map[string]any); ok {
		read(options)
	}
	if why := held(udid, port); why != "" {
		return why + " — " + nothingSent
	}
	return ""
}
