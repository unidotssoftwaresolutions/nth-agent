package doctor

// appiumLogLevelName is the check's name: what a person would search for.
const appiumLogLevelName = "Appium log level"

// AppiumLogLevelRemedy is the command that starts Appium at info. Said as Appium's own flags: this agent never starts Appium.
const AppiumLogLevelRemedy = "start Appium at info: appium --address 127.0.0.1 --log-level info (with --log, info:info keeps its file at info too) — " +
	"use debug only while you look into a session that went wrong, then start it at info again"

// appiumDebugWrites is WHY THE LEVEL MATTERS: what Appium writes at debug, its default, that no request can keep out of it.
const appiumDebugWrites = "At debug, Appium's log on this computer also keeps what no request can mask: each answer from the phone " +
	"(a field's text read back), each command's result, and an error that quotes a value typed into the app — a saved login " +
	"among them, and on an iPhone the web view's own script arguments. At info those lines are not written; a request that " +
	"types a value is masked at both."

// appiumLogLevel is THE_HEADER_THAT_REACHES_APPIUM, item 3: the level the Appium this agent is given logs at.
//
// WHY IT IS A CHECK. Each request that types a value into the app reaches the Appium on this computer with
// X-Appium-Is-Sensitive (internal/tunnel/sensitive.go), and Appium writes that request's body as **SECURE**. Some of Appium's
// own lines no header masks, and every one of them is written at debug, Appium's default (base driver 10.6.0: a driver's
// answer, "Responding to client with … result", "Encountered internal error"; the iOS remote debugger's "Executing atom …
// with 'args=[…]'"). An error answer a driver gives back is written at info too, and only Appium itself can mask it.
//
// WHY IT IS UNKNOWN, AND STOPS NOTHING. This agent never starts Appium — it is given one (--appium) — and Appium tells no
// client the level it logs at: GET /status answers ready, a message and its build, nothing more (Appium 3.5.0). So the level
// is not established, said as such, and a run is driven the same at any level: Advisory. The doctor asks Appium nothing to
// find out, and changes nothing about it.
func appiumLogLevel() Check {
	return Check{
		Name: appiumLogLevelName,
		Need: "a run types into the app through the Appium given with --appium; each request that types a value carries " +
			"X-Appium-Is-Sensitive (internal/tunnel), which Appium masks in its log at every level — the lines it never masks are written at debug",
		State:    Unknown,
		Advisory: true,
		Detail:   "Appium does not tell a client the level it logs at, so this was not read; started without --log-level, Appium logs at debug",
		Remedy:   AppiumLogLevelRemedy,
		Limits: "this check asks Appium nothing: a level given on Appium's command line or in its config file (log-level) is " +
			"not seen here, and it says the same whatever Appium runs at",
	}
}
