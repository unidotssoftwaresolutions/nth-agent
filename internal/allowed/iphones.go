package allowed

import (
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
