package allowed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// A BUILD IT HAS COUNTED. Appium on this machine installs only a build this agent has fetched, measured and checked, and it
// gets that build as a file (internal/builds fetches and counts it; the tunnel hands it over). The list's half:
//
//   - a build is an .ipa or an .apk, by the last part of its address's path, in whatever letters. A .zip or an .apks is
//     refused: Appium unpacks either on this computer (a .zip for an iPhone is streamed through an unpacker, an .apks is
//     extracted), and no phone this agent drives installs one — an iPhone takes an .ipa, an Android phone an .apk. Anything
//     else is refused too: it is not a build this agent can count;
//   - one request names at most one build, so one fetch and one count stand behind what Appium installs;
//   - after the hand-over the request names a path on this computer, and it is judged again: that path passes only when it
//     is the very file the agent wrote for this request (own). It is the one exception to "never a path on this machine",
//     and no request the farm sends can use it, because the farm's request is judged before the agent writes anything.

// BuildKind is the kind of build address names, by the last part of its path: ".ipa" or ".apk", whatever letters spell it,
// or "" with the sentence the build is refused with.
func BuildKind(address string) (kind, refusal string) {
	where := address
	if u, err := url.Parse(address); err == nil {
		where = u.Path
	}
	switch ext := strings.ToLower(path.Ext(where)); ext {
	case ".ipa", ".apk":
		return ext, ""
	case ".zip", ".apks":
		return "", fmt.Sprintf("the farm asked the Appium on this machine to install a build from %s, which is a %s — Appium "+
			"unpacks one on this computer, and no phone this agent drives installs one (an iPhone takes an .ipa, an Android "+
			"phone an .apk) — %s", shownAddress(address), ext, nothingSent)
	}
	return "", fmt.Sprintf("the farm asked the Appium on this machine to install a build from %s, which is neither an .ipa "+
		"nor an .apk — this agent hands Appium only a build it has fetched and counted itself, and those are the two kinds a "+
		"phone it drives installs — %s", shownAddress(address), nothingSent)
}

// NotHandedOn is the sentence for a build the list let through and the agent then did not hand to Appium, for reason.
func NotHandedOn(address, reason string) string {
	return fmt.Sprintf("the farm asked the Appium on this machine to install a build from %s, and this agent did not hand "+
		"it on: %s — %s", shownAddress(address), reason, nothingSent)
}

// CreateHandedOver judges a session-create after the hand-over: as Create does, except that a build may be a path own says
// the agent wrote for this request.
func CreateHandedOver(body []byte, farmURL string, phones *Phones, own func(string) bool) string {
	return create(body, farmURL, phones, own)
}

// InSessionHandedOver judges an in-session request after the hand-over, as CreateHandedOver judges a create.
func InSessionHandedOver(method, path string, body []byte, farmURL string, own func(string) bool) string {
	return inSession(method, path, body, farmURL, own)
}

// judgeBuild holds one build to the rule: the farm's own host over https, or the agent's own file for this request; and an
// .ipa or an .apk either way.
func judgeBuild(address, farmURL string, own func(string) bool) string {
	if !(own != nil && own(address)) && !OnTheFarm(address, farmURL) {
		return buildRefusal(address, farmURL)
	}
	_, refusal := BuildKind(address)
	return refusal
}

const moreThanOneBuild = "the farm asked the Appium on this machine to install more than one build in one request — this " +
	"agent fetches and counts one build at a time — " + nothingSent

// BuildsNamed is every build a request names, each once, in the order they are first named: a create's app in each of its
// sets (both spellings, and inside appium:options), or an install's build. Nil for a request that installs nothing.
func BuildsNamed(create bool, method, path string, body []byte) []string {
	var named []string
	add := func(v any) {
		s, _ := v.(string)
		for _, n := range named {
			if n == s {
				return
			}
		}
		named = append(named, s)
	}
	if create {
		var top map[string]any
		if decodeNumbers(body, &top) != nil {
			return nil
		}
		for _, set := range setsOf(top) {
			for _, at := range buildPlacesIn(set) {
				add(at.m[at.key])
			}
		}
		return named
	}
	var ask map[string]any
	if decodeNumbers(body, &ask) != nil {
		return nil
	}
	for _, at := range installPlacesIn(routeOf(path), ask) {
		if at.list != nil {
			add(at.list[at.index])
		} else {
			add(at.m[at.key])
		}
	}
	return named
}

// HandOver is body with every build it names replaced by the file local gives for it: what Appium is sent once the agent
// has fetched and counted the build. Every other value is kept as it came (numbers as written); the keys' order is not.
func HandOver(create bool, path string, body []byte, local map[string]string) ([]byte, error) {
	var top map[string]any
	if err := decodeNumbers(body, &top); err != nil {
		return nil, err
	}
	swap := func(v any) any {
		if s, ok := v.(string); ok {
			if file, ok := local[s]; ok {
				return file
			}
		}
		return v
	}
	if create {
		for _, set := range setsOf(top) {
			for _, at := range buildPlacesIn(set) {
				at.m[at.key] = swap(at.m[at.key])
			}
		}
	} else {
		for _, at := range installPlacesIn(routeOf(path), top) {
			if at.list != nil {
				at.list[at.index] = swap(at.list[at.index])
			} else {
				at.m[at.key] = swap(at.m[at.key])
			}
		}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(top); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// place is where a build is named: a key of an object, or an item of a list.
type place struct {
	m     map[string]any
	key   string
	list  []any
	index int
}

// setsOf is every set of capabilities a create carries, as Create reads them: alwaysMatch, each firstMatch entry, and the
// older desiredCapabilities. Each is the decoded object itself, so writing into it writes into the body.
func setsOf(top map[string]any) []map[string]any {
	var sets []map[string]any
	if w3c, ok := top["capabilities"].(map[string]any); ok {
		if always, ok := w3c["alwaysMatch"].(map[string]any); ok {
			sets = append(sets, always)
		}
		if firsts, ok := w3c["firstMatch"].([]any); ok {
			for _, first := range firsts {
				if m, ok := first.(map[string]any); ok {
					sets = append(sets, m)
				}
			}
		}
	}
	if desired, ok := top["desiredCapabilities"].(map[string]any); ok {
		sets = append(sets, desired)
	}
	return sets
}

// buildPlacesIn is where one set names its build: app or appium:app, in the set or in its appium:options.
func buildPlacesIn(set map[string]any) []place {
	var at []place
	for _, m := range []map[string]any{set, optionsOf(set)} {
		for _, key := range []string{"app", "appium:app"} {
			if _, ok := m[key]; ok {
				at = append(at, place{m: m, key: key})
			}
		}
	}
	return at
}

func optionsOf(set map[string]any) map[string]any {
	options, _ := set["appium:options"].(map[string]any)
	return options
}

// installPlacesIn is where an in-session request names the build it installs: install_app's appPath, or a mobile: install's
// app, appPath and each of its apks.
func installPlacesIn(rest string, ask map[string]any) []place {
	var at []place
	switch rest {
	case "/appium/device/install_app":
		if _, ok := ask["appPath"]; ok {
			at = append(at, place{m: ask, key: "appPath"})
		}
	case "/execute/sync", "/execute/async", "/execute", "/execute_async":
		script, _ := ask["script"].(string)
		if !installs[mobileCommand(script)] {
			return nil
		}
		args, _ := ask["args"].([]any)
		for _, arg := range args {
			m, ok := arg.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"app", "appPath"} {
				if _, ok := m[key]; ok {
					at = append(at, place{m: m, key: key})
				}
			}
			if list, ok := m["apks"].([]any); ok {
				for i := range list {
					at = append(at, place{list: list, index: i})
				}
			}
		}
	}
	return at
}

// mobileCommand is the lower-case name of a `mobile:` script, or "" for any other script.
func mobileCommand(script string) string {
	script = strings.TrimSpace(script)
	if len(script) < len("mobile:") || !strings.EqualFold(script[:len("mobile:")], "mobile:") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(script[len("mobile:"):]))
}

func decodeNumbers(body []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(into)
}

func shownAddress(address string) string {
	if shown := safe(address); shown != "" {
		return shown
	}
	return "an empty address"
}
