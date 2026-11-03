package doctor

import "strings"

// WhatItStops is WHY A CHECK MATTERS, in one short line: what it stops (AN_AGENT_THAT_SPEAKS_PLAINLY). It is the second
// of the three parts every problem is said in — after what is wrong, before what to do — and it is never printed for a
// check that is ready or not needed here.
//
// A BLOCKER SAYS WHAT IT STOPS, by its own name: a sentence written for adb would be wrong about a provisioning profile.
// A check that could not be established says it may stop a run, and one that stops nothing says what it still costs.
// "" means this build has no sentence for a blocker of that name, which the census reads as a defect.
func WhatItStops(c Check) string {
	switch {
	case c.State == Unknown:
		return "The agent could not tell whether this stops a run here."
	case !c.Blocks():
		return whatItCosts(c)
	}
	for _, s := range stops {
		if c.Name == s.name || (strings.HasSuffix(s.name, " ") && strings.HasPrefix(c.Name, s.name)) {
			return s.sentence
		}
	}
	return ""
}

// stops is each blocker's sentence, by check name. A name ending in a space is a prefix: "Appium driver " is every driver.
var stops = []struct{ name, sentence string }{
	{"adb", "Without adb, this machine's Android phones cannot be listed or driven."},
	{"adb server", "Until it answers, this machine's Android phones cannot be listed or driven."},
	{"Appium", "Without Appium, no phone on this machine can be driven in a run."},
	{"Appium endpoint", "Until this Appium answers, no phone on this machine is offered for a run."},
	{"Appium driver ", "Without this driver, Appium cannot drive these phones in a run."},
	{"Xcode command-line tools", "Without them, WebDriverAgent cannot be built, so no run on this machine's iPhones can start."},
	{"iOS Developer Mode", "An iPhone with Developer Mode off cannot be driven in a run."},
	{"iOS provisioning profile", "Without one, WebDriverAgent cannot be signed, so no run on this machine's iPhones can start."},
	{"iOS WebDriverAgent project", "WebDriverAgent cannot be built from it, so no run on this machine's iPhones can start."},
	{"iOS WebDriverAgent port", "A run on that iPhone cannot start while something else holds the port."},
	{"iOS signing identity", "Without it, WebDriverAgent cannot be signed on this Mac, so no run on its iPhones can start."},
	{"unusable devices", "Those phones are not offered for a run until this is done."},
	{"devices left out", "Those phones are not offered for a run."},
	{"live view", "A run on this phone cannot be watched live."},
	{"live view ", "A run on this phone cannot be watched live."},
}

// whatItCosts is what a true fact that stops nothing still costs — the advisory checks' "why it matters".
func whatItCosts(c Check) string {
	switch {
	case c.Name == "devices left out":
		return "They are not offered for a run; the phones on the cable still are."
	case c.Name == "unusable devices":
		return "Those phones are not offered for a run; the others still are."
	case strings.HasPrefix(c.Name, "live view"):
		return "A run on this phone is still driven; it cannot be watched live."
	case c.Name == "iOS WebDriverAgent project":
		return "Both builds that run here pass your team themselves, which overrides what the project stores."
	}
	return "Worth knowing; it stops nothing here."
}
