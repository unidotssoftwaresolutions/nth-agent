package netcap

import (
	"strings"
	"testing"
)

// THE JSON READER READS WHAT the runner's Utf8JsonReader READS, and masks where the value stands.
func TestTheJSONReaderReadsWhatTheRunnersReaderReads(t *testing.T) {
	cases := []struct {
		name, body string
		gone, kept []string
	}{
		{"two values, the credential in the second", "{\"a\":1}\n{\"credentials\":{\"user\":\"U1HIDDEN\"}}", []string{"U1HIDDEN"}, []string{`{"a":1}`}},
		{"cut short inside a string", `{"credentials":{"user":"U2HIDDEN"},"rest":"never clo`, []string{"U2HIDDEN"}, []string{`"rest":"never clo`}},
		{"comments and trailing commas", "{// c\n\"tokens\":[\"T3HIDDEN\",],/* x */\"keep\":\"K3\",}", []string{"T3HIDDEN"}, []string{`"keep":"K3"`, "// c"}},
		{"a strong name over a list of objects", `{"secrets":[{"a":"S4HIDDEN"},{"b":[1,"S5HIDDEN"]}],"k":"K4"}`, []string{"S4HIDDEN", "S5HIDDEN"}, []string{`"k":"K4"`}},
		{"a value name over an object reads it name by name", `{"session":{"id":"KEEPSESSIONID","token":"S6HIDDEN"}}`, []string{"S6HIDDEN"}, []string{"KEEPSESSIONID"}},
		{"a name written with escapes", `{"pass\u0077ord":"S7HIDDEN"}`, []string{"S7HIDDEN"}, nil},
		{"a number under a credential's name", `{"pin":123456,"page":2}`, []string{"123456"}, []string{`"page":2`}},
		{"literals stay, as the runner leaves them", `{"password":true,"otp":null}`, nil, []string{`"password":true`, `"otp":null`}},
		{"a JSON string holding JSON", `{"payload":"{\"refresh_token\":\"S8HIDDEN\"}","k":1}`, []string{"S8HIDDEN"}, []string{`"k":1`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskBodyText(c.body, "application/json")
			for _, gone := range c.gone {
				if strings.Contains(got, gone) {
					t.Fatalf("%s must be masked: %s", gone, got)
				}
			}
			for _, kept := range c.kept {
				if !strings.Contains(got, kept) {
					t.Fatalf("%s must stay as written: %s", kept, got)
				}
			}
		})
	}
}

// MASKED WHERE IT STANDS: every byte not masked, the layout included, stays as the app sent it — the runner's splice.
func TestAMaskedJSONBodyKeepsItsOwnBytes(t *testing.T) {
	body := "{\n  \"user\" : \"keep\",\n  \"accessToken\":   \"HIDDEN\",\n  \"list\": [ 1, 2 ]\n}"
	want := "{\n  \"user\" : \"keep\",\n  \"accessToken\":   \"" + Mask + "\",\n  \"list\": [ 1, 2 ]\n}"
	if got := maskJSONFields(body); got != want {
		t.Fatalf("only the masked value changes:\n got %q\nwant %q", got, want)
	}
}

// A NAME IS READ AS THE RUNNER READS IT, and its percent-encoding the way .NET decodes it: an escape that is not UTF-8 stays as
// written, and the rest of the name is still read.
func TestANameIsReadTheWayTheRunnerReadsIt(t *testing.T) {
	for name, want := range map[string]reach{
		"accessToken": reachEverything, "ACCESS_TOKEN": reachEverything, "x-amz-signature": reachEverything,
		"user[password]": reachEverything, "data[0][token]": reachEverything, "password[]": reachEverything,
		"token_type": reachNone, "tokenExpiresAt": reachNone, "cvv2": reachValue, "X-Hub-Signature-256": reachEverything,
		"pin": reachValue, "pinCode": reachNone, "tokens": reachEverything, "credentials": reachEverything, "": reachNone,
	} {
		if got := nameReach(name); got != want {
			t.Fatalf("%q reads as %v, the runner reads it as %v", name, got, want)
		}
	}
	if got := formDecoded("pass%77ord%FF"); got != "password%FF" {
		t.Fatalf(".NET keeps an escape that is not UTF-8 as written and decodes the rest, got %q", got)
	}
	if !nameSensitive(formDecoded("pass%C3%A9word")) {
		t.Fatal("a multi-byte escape decodes to its character, and the name is still read")
	}
	if got := maskPairs("?a=1&pass%77ord=PAIRHIDDEN&z=2", false); strings.Contains(got, "PAIRHIDDEN") {
		t.Fatalf("an escaped name is read decoded: %s", got)
	}
}

// .NET's \s IS EVERY UNICODE SPACE — a no-break space ends a pair's value there as it does in the runner.
func TestWhiteSpaceIsDotNetsWhiteSpace(t *testing.T) {
	nbsp := string(rune(0xA0))
	got := maskPairs("?token=AHIDDEN"+nbsp+"tail", false)
	if strings.Contains(got, "AHIDDEN") || !strings.Contains(got, nbsp+"tail") {
		t.Fatalf("the value ends at the no-break space and is masked up to it: %q", got)
	}
	if !isNetSpace(0x2028) || !isNetSpace(0x85) || isNetSpace('x') {
		t.Fatal("char.IsWhiteSpace counts the separators and U+0085")
	}
}

func TestAMultipartPartKeepsTheBoundaryItEndsAt(t *testing.T) {
	body := "--B\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\nMPHIDDEN\r\n--B\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\nkeepNOTE\r\n--B--\r\n"
	got := maskBodyText(body, "multipart/form-data; boundary=B")
	if strings.Contains(got, "MPHIDDEN") || !strings.Contains(got, "keepNOTE") || strings.Count(got, "--B") != strings.Count(body, "--B") {
		t.Fatalf("the part is masked and every boundary stays: %q", got)
	}
}

func TestTheHeaderRuleIsTheRunners(t *testing.T) {
	for name, sensitive := range map[string]bool{
		"Authorization": true, "proxy-authorization": true, "Cookie": true, "Set-Cookie": true,
		"X-Goog-Api-Key": true, "X-Auth-Token": true, "X-Session-Id": true, "X-Hub-Signature-256": true,
		"X-Request-Id": false, "Content-Type": false, "X-Client-Ref": false,
	} {
		if sensitiveHeader(name) != sensitive {
			t.Fatalf("%s: sensitive=%v in the runner", name, sensitive)
		}
	}
	got := maskText("Bearer abc.def.ghi and Basic dXNlcjpwYXNzd29yZDEyMw== and {{secret:Login.password}} and AKIAABCDEFGHIJKLMNOP")
	for _, gone := range []string{"abc.def.ghi", "dXNlcjpwYXNzd29yZDEyMw", "Login.password", "AKIAABCDEFGHIJKLMNOP"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%s must be masked in text: %s", gone, got)
		}
	}
	if stripNul("a"+string(rune(0))+"b") != "ab" {
		t.Fatal("a NUL never reaches the record")
	}
}
