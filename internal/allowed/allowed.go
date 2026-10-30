// Package allowed is WHAT THE FARM MAY ASK OF THIS MACHINE, in one place.
//
// A session the farm starts on a phone plugged into this machine reaches the customer's own Appium. Appium reads the
// session's capabilities as instructions, and some of them name a program to run, a path to read or an address to
// connect to, on this computer. So a session-create passes only when:
//
//   - every capability in it is on Capabilities, below, with a value of the kind its entry allows;
//   - every set of capabilities Appium could start it from names a phone, and each phone it names is one this agent
//     offers right now (Phones);
//   - its build, if it names one, is an https address on the farm's own host (the one given to --farm).
//
// Inside a session, a route or a `mobile:` command that runs a program, or reads or writes files, on this computer rather
// than on the phone is refused (InSession). What acts on the phone and the app under test is left alone.
//
// Everything else is refused with a sentence: the agent prints it, and the farm gets it back as the answer. Nothing that
// is refused reaches Appium.
//
// A NEW CAPABILITY IS ADDED TO Capabilities ON PURPOSE, WITH ITS REASON. The farm's runner sends exactly the entries below
// today; one it starts sending later is refused, in words, until somebody adds it here and says why it is safe.
package allowed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// kind is what a capability's value may be.
type kind int

const (
	aBool          kind = iota + 1
	aNumber             // a JSON number, 0 or more
	aPort               // a whole number from 1 to 65535
	aString             // any string
	aStringList         // a list of strings
	onePlatform         // Android or iOS
	oneAutomation       // UiAutomator2 or XCUITest
	oneOrientation      // PORTRAIT or LANDSCAPE
	thePhone            // a phone this agent offers right now
	theBuild            // an https address on the farm's own host
	onlyFalse           // false: asks for nothing
)

// Capability is one entry of the allow-list: its name without the `appium:` prefix (both spellings are read), the kind of
// value it may carry, and why the farm may send it.
type Capability struct {
	Name string
	Why  string
	kind kind
}

// Capabilities is the allow-list. Every entry is one the farm's runner sends when it starts a session on a customer's
// phone, and none of them names a program, a path or an address on this computer.
var Capabilities = []Capability{
	{"platformName", "which platform the session is for", onePlatform},
	{"automationName", "which of the two Appium drivers drives the phone", oneAutomation},
	{"udid", "which phone — it must be one this agent offers right now", thePhone},
	{"platformVersion", "the phone's OS version, which the driver checks against the phone", aString},
	{"deviceName", "a label for the phone; the udid, which every set must carry, is what picks it", aString},
	{"orientation", "which way up the session starts", oneOrientation},
	{"language", "the language the phone is set to for the session", aString},
	{"locale", "the region the phone is set to for the session", aString},
	{"skipSyncUiDialogTranslation", "whether the driver waits for a language change to reach the phone's dialogs", aBool},
	{"app", "the build to install: an https address on the farm's own host, and nothing else", theBuild},
	{"appPackage", "the app on the phone to start, when the build is already installed", aString},
	{"appActivity", "the screen of that app to start", aString},
	{"appWaitActivity", "which screen counts as the app having started", aString},
	{"bundleId", "the app on the phone to start, on iOS", aString},
	{"autoLaunch", "whether the driver starts the app itself", aBool},
	{"clearDeviceLogsOnStart", "whether the phone's log is cleared, so the last session's lines are not this one's", aBool},
	{"fullReset", "whether the app and its data are removed from the phone before the session", aBool},
	{"noReset", "whether the app's data on the phone is kept", aBool},
	{"autoGrantPermissions", "whether the app's permissions are granted on the phone when it is installed", aBool},
	{"autoAcceptAlerts", "whether the phone's permission dialogs are accepted", aBool},
	{"newCommandTimeout", "how long Appium waits for the next command before it ends the session", aNumber},
	{"waitForIdleTimeout", "how long the driver waits for the app to go quiet", aNumber},
	{"waitForQuiescence", "the older spelling of the same wait", aBool},
	{"useNewWDA", "whether WebDriverAgent is started afresh on the phone", aBool},
	{"wdaLaunchTimeout", "how long the driver waits for WebDriverAgent to start on the phone", aNumber},
	{"wdaLocalPort", "the loopback port this agent declared for the phone's WebDriverAgent", aPort},
	{"mjpegServerPort", "the loopback port this agent reserved for the phone's live view", aPort},
	{"xcodeOrgId", "the Apple team this machine declared, which signs WebDriverAgent here", aString},
	{"xcodeSigningId", "the signing identity this machine declared", aString},
	{"updatedWDABundleId", "the WebDriverAgent bundle id this machine declared", aString},
	{"additionalWebviewBundleIds", "which apps' web views the driver may look into, on the phone", aStringList},
	{"chromedriverAutodownload", "lets Appium fetch a matching chromedriver for a web view; Appium does it only when " +
		"started with its chromedriver_autodownload insecure feature, which this agent's README asks you not to turn on", aBool},
	{"allowProvisioningDeviceRegistration", "false only, which asks for nothing; true would register the phone with your " +
		"Apple team, which only the person at this machine may do", onlyFalse},
}

// neverPassed are capabilities that name a program, a path or an address on this computer, with what each would do. None
// is on the list, so each is refused like any capability not on it; this names them so that the sentence says what it was
// and so that adding one to Capabilities fails a seal instead of passing quietly.
var neverPassed = map[string]string{
	"chromedriverexecutable":    "a program on this computer for Appium to start",
	"chromedriverexecutabledir": "a folder on this computer to start a program from",
	"webdriveragenturl":         "an address for Appium to connect to from this computer",
	"deriveddatapath":           "a folder on this computer to build or run WebDriverAgent from",
	"prebuiltwdapath":           "a WebDriverAgent build on this computer to run",
	"keychainpath":              "a keychain file on this computer",
	"keychainpassword":          "the password of a keychain on this computer",
}

const nothingSent = "so the session was refused, and nothing was sent to Appium"

// byName is Capabilities by name, built once.
var byName = func() map[string]Capability {
	m := map[string]Capability{}
	for _, c := range Capabilities {
		m[c.Name] = c
	}
	return m
}()

// Phones is which phones this agent offers right now: the connect loop rewrites it from the rows it publishes every tick,
// and the tunnel reads it before a session-create reaches Appium. A phone left out with --skip, one on Wi-Fi or an emulator
// the agent does not offer, or one unplugged since the last tick, is not in it. nil offers none.
type Phones struct {
	mu    sync.Mutex
	udids map[string]bool
}

// Offering is a set that offers exactly these phones.
func Offering(udids ...string) *Phones {
	p := &Phones{}
	p.Offer(udids)
	return p
}

// Offer replaces what is on offer.
func (p *Phones) Offer(udids []string) {
	set := make(map[string]bool, len(udids))
	for _, u := range udids {
		set[u] = true
	}
	p.mu.Lock()
	p.udids = set
	p.mu.Unlock()
}

// Offers says whether this phone is on offer right now.
func (p *Phones) Offers(udid string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.udids[udid]
}

// Create judges a session-create's body for a phone on this machine: "" lets it through unchanged, anything else is the
// sentence it is refused with. farmURL is the farm this agent was started with (--farm).
func Create(body []byte, farmURL string, phones *Phones) string {
	var top map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&top); err != nil || top == nil {
		return "the farm asked to start a session whose capabilities are not a JSON object this agent can read — " + nothingSent
	}
	var sets []map[string]any
	for key, value := range top {
		switch key {
		case "capabilities":
			w3c, ok := value.(map[string]any)
			if !ok {
				return "the farm asked to start a session whose capabilities are not an object — " + nothingSent
			}
			always := map[string]any{}
			firsts := []any{map[string]any{}}
			for part, inner := range w3c {
				switch part {
				case "alwaysMatch":
					m, ok := inner.(map[string]any)
					if !ok {
						return "the farm asked to start a session whose alwaysMatch is not an object — " + nothingSent
					}
					always = m
				case "firstMatch":
					list, ok := inner.([]any)
					if !ok || len(list) == 0 {
						return "the farm asked to start a session whose firstMatch is not a list of capability sets — " + nothingSent
					}
					firsts = list
				default:
					return fmt.Sprintf("the farm asked to start a session carrying %q beside alwaysMatch and firstMatch, "+
						"which Appium may read and this agent does not pass on — %s", safe(part), nothingSent)
				}
			}
			for _, first := range firsts {
				m, ok := first.(map[string]any)
				if !ok {
					return "the farm asked to start a session with a firstMatch entry that is not an object — " + nothingSent
				}
				// The two halves of one set are judged as one, the way Appium merges them.
				set := map[string]any{}
				for k, v := range always {
					set[k] = v
				}
				for k, v := range m {
					if _, twice := set[k]; twice {
						return fmt.Sprintf("the farm asked to start a session naming %q in both alwaysMatch and firstMatch — %s", safe(k), nothingSent)
					}
					set[k] = v
				}
				sets = append(sets, set)
			}
		case "desiredCapabilities":
			m, ok := value.(map[string]any)
			if !ok {
				return "the farm asked to start a session whose desiredCapabilities are not an object — " + nothingSent
			}
			sets = append(sets, m)
		default:
			return fmt.Sprintf("the farm asked to start a session carrying %q, which Appium may read and this agent does not "+
				"pass on — %s", safe(key), nothingSent)
		}
	}
	if len(sets) == 0 {
		return "the farm asked to start a session with no capabilities at all, so Appium would pick a phone itself — " + nothingSent
	}
	for _, set := range sets {
		if refusal := judgeSet(set, farmURL, phones); refusal != "" {
			return refusal
		}
	}
	return ""
}

// judgeSet judges one set of capabilities Appium could start the session from, with its appium:options read in.
func judgeSet(set map[string]any, farmURL string, phones *Phones) string {
	entries := map[string]any{}
	for key, value := range set {
		if key == "appium:options" {
			options, ok := value.(map[string]any)
			if !ok {
				return "the farm asked to start a session whose appium:options is not an object — " + nothingSent
			}
			for inner, v := range options {
				if inner == "appium:options" {
					return "the farm asked to start a session with appium:options inside appium:options — " + nothingSent
				}
				entries[inner] = v
			}
			continue
		}
		entries[key] = value
	}
	names := make([]string, 0, len(entries))
	for key := range entries {
		names = append(names, key)
	}
	sort.Strings(names)
	namedAPhone := false
	for _, key := range names {
		value := entries[key]
		name := strings.TrimPrefix(key, "appium:")
		if what, never := neverPassed[strings.ToLower(name)]; never {
			return fmt.Sprintf("the farm asked the Appium on this machine for %s, which names %s — this agent never passes "+
				"that on, whoever asks — %s", safe(key), what, nothingSent)
		}
		c, listed := byName[name]
		if !listed {
			return fmt.Sprintf("the farm asked the Appium on this machine for %s, which is not on this agent's list of "+
				"capabilities a session may carry — %s", safe(key), nothingSent)
		}
		if refusal := judgeValue(key, c, value, farmURL, phones); refusal != "" {
			return refusal
		}
		if c.kind == thePhone {
			namedAPhone = true
		}
	}
	if !namedAPhone {
		return "the farm asked to start a session that names no phone (appium:udid) in one of its sets of capabilities, " +
			"so Appium would pick a phone itself, perhaps one this agent does not offer — " + nothingSent
	}
	return ""
}

func judgeValue(key string, c Capability, value any, farmURL string, phones *Phones) string {
	wrong := func(want string) string {
		return fmt.Sprintf("the farm asked the Appium on this machine for %s with a value that is not %s — %s", safe(key), want, nothingSent)
	}
	switch c.kind {
	case aBool:
		if _, ok := value.(bool); !ok {
			return wrong("true or false")
		}
	case onlyFalse:
		if value != false {
			return wrong("false")
		}
	case aNumber:
		n, ok := value.(json.Number)
		if !ok {
			return wrong("a number")
		}
		f, err := n.Float64()
		if err != nil || f < 0 || math.IsInf(f, 0) {
			return wrong("a number, 0 or more")
		}
	case aPort:
		n, ok := value.(json.Number)
		if !ok {
			return wrong("a port number")
		}
		i, err := n.Int64()
		if err != nil || i < 1 || i > 65535 {
			return wrong("a port number from 1 to 65535")
		}
	case aString:
		if _, ok := value.(string); !ok {
			return wrong("text")
		}
	case aStringList:
		list, ok := value.([]any)
		if !ok {
			return wrong("a list of text")
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return wrong("a list of text")
			}
		}
	case onePlatform, oneAutomation, oneOrientation:
		s, _ := value.(string)
		choices := map[kind][]string{onePlatform: {"Android", "iOS"}, oneAutomation: {"UiAutomator2", "XCUITest"},
			oneOrientation: {"PORTRAIT", "LANDSCAPE"}}[c.kind]
		for _, choice := range choices {
			if strings.EqualFold(s, choice) {
				return ""
			}
		}
		return wrong(strings.Join(choices, " or "))
	case thePhone:
		udid, ok := value.(string)
		if !ok {
			return "the farm asked to start a session whose phone (" + safe(key) + ") is not text, so it names no phone this " +
				"agent offers — " + nothingSent
		}
		if !phones.Offers(udid) {
			return fmt.Sprintf("the farm asked to start a session on phone %q, which this agent does not offer — only the "+
				"phones `connect` says it offers can be driven from the farm (a phone left out with --skip, one reached over "+
				"Wi-Fi or an emulator is not, unless you offered it) — %s", safe(udid), nothingSent)
		}
	case theBuild:
		address, _ := value.(string)
		if !OnTheFarm(address, farmURL) {
			return buildRefusal(address, farmURL)
		}
	}
	return ""
}

// OnTheFarm says whether address is an https address on the farm's own host and port, with no user name: the only place a
// build may come from. The farm hands a phone's Appium its builds at /app-cache/ on that host, with a signed grant.
func OnTheFarm(address, farmURL string) bool {
	build, err := url.Parse(address)
	if err != nil || build.Scheme != "https" || build.User != nil || build.Opaque != "" || build.Host == "" {
		return false
	}
	farmAt, err := url.Parse(farmURL)
	if err != nil || farmAt.Host == "" {
		return false
	}
	return strings.EqualFold(build.Hostname(), farmAt.Hostname()) && portOf(build) == portOf(farmAt)
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}

func buildRefusal(address, farmURL string) string {
	host := "the farm's host"
	if u, err := url.Parse(farmURL); err == nil && u.Host != "" {
		host = u.Host
	}
	shown := safe(address)
	if shown == "" {
		shown = "an empty address"
	}
	return fmt.Sprintf("the farm asked the Appium on this machine to install a build from %s, which is not an https "+
		"address on the farm's own host (%s) — a path or another address would have Appium read a file on this computer "+
		"or fetch from somewhere nobody checked — %s", shown, safe(host), nothingSent)
}

// runsOnThisComputer are the `mobile:` commands, by lower-case name, that run a program or read or write files on this
// computer rather than on the phone, with what each does. Read from the drivers Appium installs (UiAutomator2 7.5.2 with
// appium-android-driver 13.2.3, XCUITest 11.9.0). The farm's runner sends none of them.
var runsOnThisComputer = map[string]string{
	"shell":                         "runs a command through adb",
	"execemuconsolecommand":         "sends any command to an emulator's console",
	"startscreenstreaming":          "starts a GStreamer program on this computer",
	"stopscreenstreaming":           "stops a GStreamer program on this computer",
	"injectemulatorcameraimage":     "writes a file into an emulator's files on this computer",
	"startmediaprojectionrecording": "records the screen to this computer and can upload it from here",
	"stopmediaprojectionrecording":  "uploads a recording from this computer to any address",
	"startperfrecord":               "runs xctrace on this computer",
	"stopperfrecord":                "uploads a trace from this computer to any address",
	"startaudiorecording":           "runs ffmpeg on this computer",
	"stopaudiorecording":            "returns a recording made on this computer",
	"startscreenrecording":          "runs ffmpeg on this computer",
	"stopscreenrecording":           "uploads a recording from this computer to any address",
	"runxctest":                     "runs an XCTest bundle from this computer",
	"installxctestbundle":           "installs an XCTest bundle from this computer",
	"listxctestbundles":             "runs a tool on this computer",
	"simctl":                        "runs any simctl command on this computer",
	"installcertificate":            "starts a web server on this computer to hand the phone a certificate",
}

// MaxTreeDepth is the deepest tree a session may ask Appium to read (its snapshotMaxDepth setting): the runner refuses a
// page source nested deeper than 128 levels, and a source holds two levels of its own around the app's tree, so 126 is the
// deepest whose source the runner still reads. Appium's own defaults are 70 on Android and 50 on iOS.
const MaxTreeDepth = 126

// installs are the `mobile:` commands that install a build; their address is held to the same rule as a session's app.
var installs = map[string]bool{"installapp": true, "installmultipleapks": true}

// InSession judges one request inside a session this agent created, for a phone on this machine: "" lets it through
// unchanged, anything else is the sentence it is refused with.
func InSession(method, path string, body []byte, farmURL string) string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	rest := path
	if i := strings.Index(path, "/session/"); i >= 0 {
		rest = path[i+len("/session/"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[j:]
		} else {
			rest = ""
		}
	}
	// Appium routes without regard to case (its server is an Express app, and Appium leaves case-sensitive routing off), so
	// a route spelled in other letters is judged as the route it reaches.
	rest = strings.ToLower(strings.TrimRight(rest, "/"))
	refuse := func(what string) string {
		return "the farm asked the Appium on this machine for " + what + " — this agent never passes on what runs a " +
			"program, or reads or writes files, on this computer rather than on the phone, so it was refused, and nothing " +
			"was sent to Appium"
	}
	switch rest {
	case "/appium/settings":
		if strings.EqualFold(method, "POST") {
			return deeperThanARunReads(body)
		}
	case "/appium/execute_driver":
		return refuse("execute_driver, a script Appium runs in its own process on this computer")
	case "/appium/start_recording_screen", "/appium/stop_recording_screen":
		return refuse("a screen recording (" + strings.TrimPrefix(rest, "/appium/") + "), which is made on this computer and can be uploaded from it to any address")
	case "/se/log", "/log":
		var ask struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(body, &ask) != nil {
			return refuse("a log whose kind this agent cannot read")
		}
		if strings.EqualFold(strings.TrimSpace(ask.Type), "server") {
			return refuse("Appium's own log on this computer, which holds other sessions' lines")
		}
	case "/appium/device/install_app":
		var ask struct {
			AppPath string `json:"appPath"`
		}
		if json.Unmarshal(body, &ask) != nil {
			return refuse("an install whose build this agent cannot read")
		}
		if !OnTheFarm(ask.AppPath, farmURL) {
			return buildRefusal(ask.AppPath, farmURL)
		}
	case "/execute/sync", "/execute/async", "/execute", "/execute_async":
		var ask struct {
			Script string `json:"script"`
			Args   []any  `json:"args"`
		}
		if json.Unmarshal(body, &ask) != nil {
			return refuse("a script this agent cannot read")
		}
		script := strings.TrimSpace(ask.Script)
		if len(script) < len("mobile:") || !strings.EqualFold(script[:len("mobile:")], "mobile:") {
			return "" // JavaScript in a web view on the phone
		}
		command := strings.ToLower(strings.TrimSpace(script[len("mobile:"):]))
		if what, ok := runsOnThisComputer[command]; ok {
			return refuse(fmt.Sprintf("mobile: %s, which %s", safe(strings.TrimSpace(script[len("mobile:"):])), what))
		}
		if installs[command] {
			for _, address := range buildsNamedIn(ask.Args) {
				if !OnTheFarm(address, farmURL) {
					return buildRefusal(address, farmURL)
				}
			}
		}
	}
	return ""
}

// deeperThanARunReads judges a settings call: "" lets it through unchanged, anything else is the sentence it is refused with.
// Only the tree depth is judged — by whatever letters it is spelled in, and as Appium reads a body, the last of a name given
// twice — and only a whole number from 1 to MaxTreeDepth, written as one, passes: iOS reads a depth given as text as the
// number in it. Every other setting passes as it came.
func deeperThanARunReads(body []byte) string {
	refused := func(what string) string {
		return "the farm asked the Appium on this machine " + what + " — so it was refused, and nothing was sent to Appium"
	}
	var ask map[string]json.RawMessage
	if json.Unmarshal(body, &ask) != nil {
		return refused("for settings this agent cannot read")
	}
	raw, given := ask["settings"]
	if !given {
		return ""
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(raw, &settings) != nil || settings == nil {
		return refused("for settings this agent cannot read")
	}
	for name, value := range settings {
		if !strings.EqualFold(name, "snapshotMaxDepth") {
			continue
		}
		depth := string(bytes.TrimSpace(value))
		if n, err := strconv.Atoi(depth); err != nil || n < 1 || n > MaxTreeDepth || depth != strconv.Itoa(n) {
			return refused(fmt.Sprintf("to read each screen to a depth of %s, where a run reads a screen at most %d levels deep "+
				"(a whole number from 1 to %d)", safe(depth), MaxTreeDepth, MaxTreeDepth))
		}
	}
	return ""
}

// buildsNamedIn reads every build address a mobile: install names: app, appPath, and each of apks.
func buildsNamedIn(args []any) []string {
	var out []string
	for _, arg := range args {
		m, ok := arg.(map[string]any)
		if !ok {
			out = append(out, "") // an install whose build is not where this agent reads it is refused
			continue
		}
		found := false
		for _, key := range []string{"app", "appPath"} {
			if v, ok := m[key]; ok {
				s, _ := v.(string)
				out = append(out, s)
				found = true
			}
		}
		if v, ok := m["apks"]; ok {
			found = true
			list, ok := v.([]any)
			if !ok {
				out = append(out, "")
			}
			for _, item := range list {
				s, _ := item.(string)
				out = append(out, s)
			}
		}
		if !found {
			out = append(out, "")
		}
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// safe is a value the farm chose, folded to one line and cut, before it stands in a sentence a terminal prints.
func safe(s string) string { return farm.SafeForTerminal(s, "") }
