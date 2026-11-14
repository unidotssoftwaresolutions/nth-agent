package allowed_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
)

// WHAT_THE_FIRST_TRY_SHOWED, decision A7. `mobile: startActivity`, `mobile: broadcast` and `mobile: deepLink` start an
// activity, send a broadcast or open an address on the phone, and the drivers build an `am` command on the phone from free
// text in their arguments. The farm's runner sends none of them, so they are refused outright, whatever their arguments,
// in every spelling Appium reads, and through the older route that reaches startActivity; the farm's answer says the agent
// does not run them. A command whose name merely holds one of the words (startLogsBroadcast) is not one of them.
func TestTheThreeCommandsThatStartThingsOnThePhoneAreRefused(t *testing.T) {
	const farmURL = "https://farm.nthcase.com"
	refused := map[string][2]string{
		"startActivity, no arguments":        {"/session/s1/execute/sync", `{"script":"mobile: startActivity","args":[]}`},
		"startActivity, the app's own":       {"/session/s1/execute/sync", `{"script":"mobile: startActivity","args":[{"intent":"com.example.app/.MainActivity"}]}`},
		"startActivity, no space":            {"/session/s1/execute/sync", `{"script":"mobile:startActivity","args":[{}]}`},
		"startActivity, other letters":       {"/session/s1/execute", `{"script":"mobile: STARTACTIVITY","args":[{}]}`},
		"startActivity, async":               {"/session/s1/execute/async", `{"script":"  mobile: startActivity  ","args":[{}]}`},
		"broadcast":                          {"/session/s1/execute/sync", `{"script":"mobile: broadcast","args":[{"action":"com.example.PING"}]}`},
		"broadcast, no arguments at all":     {"/session/s1/execute/sync", `{"script":"mobile: broadcast"}`},
		"deepLink":                           {"/session/s1/execute/sync", `{"script":"mobile: deepLink","args":[{"url":"example://home","package":"com.example.app"}]}`},
		"deepLink, other letters":            {"/session/s1/execute/sync", `{"script":"Mobile: DeepLink","args":[{"url":"example://home"}]}`},
		"the older route to startActivity":   {"/session/s1/appium/device/start_activity", `{"appPackage":"com.example.app","appActivity":".MainActivity"}`},
		"the older route, in other letters":  {"/session/s1/APPIUM/Device/Start_Activity/", `{"appPackage":"com.example.app","appActivity":".MainActivity"}`},
		"the older route, a body none reads": {"/session/s1/appium/device/start_activity", `not json`},
	}
	for name, c := range refused {
		said := allowed.InSession("POST", c[0], []byte(c[1]), farmURL)
		if said == "" {
			t.Errorf("%s: let through, and it must be refused whatever its arguments", name)
			continue
		}
		for _, must := range []string{"this agent does not run", "nothing was sent to Appium"} {
			if !strings.Contains(said, must) {
				t.Errorf("%s: the farm's answer must say %q: %q", name, must, said)
			}
		}
	}
	for name, body := range map[string]string{
		"a log broadcast, whose name holds the word": `{"script":"mobile: startLogsBroadcast","args":[]}`,
		"the runner's own":                           `{"script":"mobile: activeAppInfo","args":[]}`,
		"JavaScript in a web view":                   `{"script":"return document.title","args":[]}`,
	} {
		if said := allowed.InSession("POST", "/session/s1/execute/sync", []byte(body), farmURL); said != "" {
			t.Errorf("%s: refused, and must pass as before: %q", name, said)
		}
	}
}
