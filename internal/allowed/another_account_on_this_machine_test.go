package allowed_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
)

// AN IPHONE'S WEBDRIVERAGENT PORT IS ASKED ABOUT AS ITS SESSION STARTS (ANOTHER_ACCOUNT_AND_LIMITS). The question
// itself is the connect loop's (internal/connect/wdaports.go); a phone it says no about is refused in its words, and
// nothing is sent.
func TestAPhoneWhoseWebDriverAgentPortIsHeldIsRefusedInItsWords(t *testing.T) {
	phones := &allowed.Phones{}
	phones.OfferHeld([]string{"00008110-0000c0ffee0000b2"}, func(udid string, port int) string {
		if port == 0 {
			port = 21000
		}
		return "port " + strings.Repeat("x", 0) + itoa(port) + " is held by a program another account runs"
	})
	body := `{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:udid":"00008110-0000c0ffee0000b2","appium:wdaLocalPort":21000}}}`
	refusal := allowed.Create([]byte(body), "https://farm.example", phones)
	if !strings.Contains(refusal, "another account") || !strings.Contains(refusal, "21000") {
		t.Fatalf("the session must be refused in the guard's words, got %q", refusal)
	}
	open := &allowed.Phones{}
	open.OfferHeld([]string{"00008110-0000c0ffee0000b2"}, func(string, int) string { return "" })
	if refusal := allowed.Create([]byte(body), "https://farm.example", open); refusal != "" {
		t.Fatalf("a phone the guard says nothing about starts as before: %q", refusal)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
