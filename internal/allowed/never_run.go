package allowed

// ── THREE COMMANDS THIS AGENT DOES NOT RUN (WHAT_THE_FIRST_TRY_SHOWED, decision A7) ──────────────────────────────────
//
// `mobile: startActivity`, `mobile: broadcast` and `mobile: deepLink` start an activity, send a broadcast or open an
// address on the phone. The drivers build an `am start` or `am broadcast` command on the phone from their arguments, among
// them free text (a deep link's address, extras, an identifier), and a phone's shell reads what the driver does not quote.
// The farm's runner sends none of them, so they are refused outright, whatever their arguments, before anything reaches a
// driver; so is the older route that reaches startActivity (appium-android-driver 13.2.3's method map). If a run comes to
// need one, it comes back held to the app under test's package, by a new decision.

// notRun are the three, by lower-case name, with what each does.
var notRun = map[string]string{
	"startactivity": "starts any activity on the phone",
	"broadcast":     "sends any broadcast on the phone",
	"deeplink":      "opens any address on the phone",
}

// startActivityRoute is the older route to startActivity, as routeOf reads it.
const startActivityRoute = "/appium/device/start_activity"

// notRunRefusal is the sentence one of the three is refused with; name is the command as the farm spelled it.
func notRunRefusal(name, what string) string {
	return "the farm asked the Appium on this machine for " + name + ", which " + what + " — this agent does not run " +
		"startActivity, broadcast or deepLink, whatever their arguments, so it was refused, and nothing was sent to Appium"
}
