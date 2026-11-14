package allowed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// WHAT A LEASE CAN ASK. A lease holder reaches this machine only through the farm, and the farm relays a session's
// requests as they were written; so the allow-list reads each request the way Appium will, never more loosely:
//
//   - a body is read by its exact member names, as Appium reads it. A body that names a member twice, or in two spellings
//     that differ only in their letters, is refused: Go's decoder takes a member in any letters and keeps the last, Appium
//     takes the exact name, and the agent cannot tell which value Appium would act on;
//   - a `mobile:` script is read once, the way the drivers read it (trimmed as JavaScript trims, which counts U+FEFF as
//     white space where Go does not), and a command's name holds letters and digits only — every command the drivers have
//     is named so, and a name with anything else in it is not one this agent can judge;
//   - the emulator commands Appium sends through `adb emu` write their text to the emulator's console on this computer as
//     one line, so a control character in their text — a line break above all, which starts a second console command — is
//     refused;
//   - language, locale, appPackage and appActivity are held to the shape their kind of value has: a driver may put them in a
//     shell command on the phone, where a ; or a $( would start another.

// exactMembers reads body as one JSON object, by its exact member names, and refuses one that names a member twice, or in
// two spellings with the same letters in another case, in any object inside it. ok is false for a body that is not one
// JSON object; refusal says which name was given twice.
func exactMembers(body []byte) (members map[string]json.RawMessage, refusal string, ok bool) {
	if name, twice := namedTwice(body); twice {
		return nil, fmt.Sprintf("a request that names %q twice, or in two spellings that differ only in their letters — "+
			"Appium reads one of them, and this agent cannot tell which", safe(name)), true
	}
	if json.Unmarshal(body, &members) != nil || members == nil {
		return nil, "", false
	}
	return members, "", true
}

// namedTwice walks every object in body and gives the first member name that object already holds, compared without case
// (Unicode's simple folding, as Go's decoder compares a name with a field's). A body that is not JSON names nothing twice.
func namedTwice(body []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	type level struct {
		object bool
		key    bool // the next string in this object is a member's name
		seen   map[string]bool
	}
	var stack []*level
	for {
		part, err := decoder.Token()
		if err == io.EOF {
			return "", false
		}
		if err != nil {
			return "", false
		}
		var top *level
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if name, isName := part.(string); isName && top != nil && top.object && top.key {
			folded := foldName(name)
			if top.seen[folded] {
				return name, true
			}
			top.seen[folded] = true
			top.key = false
			continue
		}
		switch part {
		case json.Delim('{'):
			stack = append(stack, &level{object: true, key: true, seen: map[string]bool{}})
			continue
		case json.Delim('['):
			stack = append(stack, &level{})
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].key = true // a value ended: the next string is a name
		}
	}
}

// foldName is name with each letter in the one case Go's decoder matches it in: every letter of its folding orbit maps to
// the same rune, so "script", "SCRIPT" and "ſcript" are one name.
func foldName(name string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < lowest {
				lowest = f
			}
		}
		return lowest
	}, name)
}

// stringMember is members[name] as text: given says the member is there, ok that it is text.
func stringMember(members map[string]json.RawMessage, name string) (value string, given, ok bool) {
	raw, given := members[name]
	if !given {
		return "", false, true
	}
	ok = json.Unmarshal(raw, &value) == nil
	return value, true, ok
}

// isScriptSpace is the white space a driver trims from a script before it looks the command up: JavaScript's, which is
// Go's and U+FEFF. U+180E, white space in older JavaScript, is trimmed too.
func isScriptSpace(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' || r == '\u180e' }

// mobileName reads a script as the drivers do: trimmed, then `mobile:` in any letters, then the command's name trimmed
// again. isMobile is false for any other script (JavaScript in a web view on the phone).
func mobileName(script string) (name string, isMobile bool) {
	script = strings.TrimFunc(script, isScriptSpace)
	if len(script) < len("mobile:") || !strings.EqualFold(script[:len("mobile:")], "mobile:") {
		return "", false
	}
	return strings.TrimFunc(script[len("mobile:"):], isScriptSpace), true
}

// aCommandsName is what every command a driver has is named with: letters and digits.
var aCommandsName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// throughTheEmulatorsConsole are the `mobile:` commands, by lower-case name, that Appium sends to an Android emulator's
// console on this computer through `adb emu`, which writes their text there as one line (appium-adb, under UiAutomator2
// 7.5.2). The runner sends sensorSet, to shake an emulator.
var throughTheEmulatorsConsole = map[string]bool{
	"sendsms": true, "gsmcall": true, "gsmsignal": true, "gsmvoice": true, "sensorset": true, "powerac": true,
	"powercapacity": true, "networkspeed": true, "fingerprint": true,
}

// moreThanOneLine says whether any text inside v holds a control character: a line break, which would start a second
// console command, or any other.
func moreThanOneLine(v any) bool {
	switch typed := v.(type) {
	case string:
		return strings.IndexFunc(typed, unicode.IsControl) >= 0
	case []any:
		for _, item := range typed {
			if moreThanOneLine(item) {
				return true
			}
		}
	case map[string]any:
		for name, item := range typed {
			if moreThanOneLine(name) || moreThanOneLine(item) {
				return true
			}
		}
	}
	return false
}

// THE SHAPES A FREE-TEXT CAPABILITY MAY HAVE. Each is the shape its kind of value has where it is defined, and nothing a
// shell reads as more than one word:
//
//   - a language or a locale: a BCP 47 tag or a POSIX locale — letters and digits in parts joined by - or _ (en, US, sv_SE,
//     zh-Hans-CN), with at most one @key=value after it (ja_JP@calendar=japanese);
//   - an app's package: Android's package name — parts of letters, digits and _, each starting with a letter, joined by dots;
//   - an activity: a class name inside that package — parts of letters, digits, _ and $ (an inner class), joined by dots,
//     perhaps starting with a dot (".MainActivity"), perhaps after its package and a / (com.example/.MainActivity).
var (
	aLanguageOrLocale = regexp.MustCompile(`^[A-Za-z0-9]{1,8}(?:[-_][A-Za-z0-9]{1,8}){0,7}(?:@[A-Za-z0-9]{1,16}=[A-Za-z0-9_-]{1,32})?$`)
	aPackageName      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)*$`)
	anActivityName    = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)*/)?\.?[A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)
)

// shapeOf is the shape a free-text capability's kind must have, and the words its refusal says it in.
func shapeOf(k kind) (*regexp.Regexp, string) {
	switch k {
	case aLanguage:
		return aLanguageOrLocale, "a language or a locale (letters and digits, in parts joined by - or _, such as en, sv_SE or zh-Hans)"
	case aPackage:
		return aPackageName, "an app's package name (letters, digits and _, in parts joined by dots, such as com.example.app)"
	case anActivity:
		return anActivityName, "an activity's name (letters, digits, _ and $, in parts joined by dots, such as .MainActivity)"
	}
	return nil, ""
}

// PhonesNamed is every phone a session's create names, in any of its sets, by either spelling, and inside appium:options:
// the phones Appium may start it on.
func PhonesNamed(body []byte) []string {
	var top map[string]any
	if decodeNumbers(body, &top) != nil {
		return nil
	}
	seen := map[string]bool{}
	var phones []string
	for _, set := range setsOf(top) {
		for _, m := range []map[string]any{set, optionsOf(set)} {
			for _, key := range []string{"udid", "appium:udid"} {
				if udid, ok := m[key].(string); ok && !seen[udid] {
					seen[udid] = true
					phones = append(phones, udid)
				}
			}
		}
	}
	return phones
}
