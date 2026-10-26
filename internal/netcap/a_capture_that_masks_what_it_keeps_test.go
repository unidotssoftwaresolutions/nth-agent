package netcap

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

// ── A CAPTURE THAT MASKS WHAT IT KEEPS ──────────────────────────────────────────────────────────────────
//
// The masker reads JSON, form and multipart syntax and text patterns, and nothing else. A body in a binary or schema-less
// encoding — protobuf, gRPC and gRPC-web, MessagePack, CBOR, Thrift — holds its credentials beside their names with no
// `:`, `=` or quote between them, or with no name at all, so the masker cannot tell a password from a page title. Such a
// body was judged by mitmproxy's printable-ratio test over its first hundred bytes alone, and a string-heavy one passed as
// text and was kept with its values. It is now judged by what it SAYS it is and by EVERY byte the record would read, and a
// body the masker cannot read is left out, with the comment saying why — as every other unreadable body already is.

// leftOutCase is one body that must not reach the record, and the word its comment must carry.
type leftOutCase struct {
	name, contentType string
	body              []byte
	secrets           []string
	reason            string
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func utf16le(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func unreadableBodies() []leftOutCase {
	token200 := strings.Repeat("Zq9", 67)[:200]
	token40 := strings.Repeat("Wx7", 14)[:40]
	protoLogin := cat([]byte{0x0A, 0x12}, []byte("tester@example.com"), []byte{0x12, 0x0E}, []byte("hunter2hunter2"))
	grpcFramed := cat([]byte{0, 0, 0, 0, 0x24}, protoLogin)
	msgpack := cat([]byte{0x81, 0xA8}, []byte("password"), []byte{0xA7}, []byte("hunter2"))
	cbor := cat([]byte{0xA1, 0x68}, []byte("password"), []byte{0x67}, []byte("hunter2"))
	textFirst := cat([]byte("--B\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\n"+strings.Repeat("abcdefghij", 12)+
		"\r\n--B\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n"), utf16le(`{"password":"UTF16HIDDEN"}`), []byte("\r\n--B--\r\n"))
	lateNul := cat([]byte(strings.Repeat("plain readable words ", 30)), []byte{0}, []byte("LATENULHIDDEN"))
	return []leftOutCase{
		// What the content says it is: a binary or schema-less encoding, however printable its bytes.
		{"REST protobuf, mostly printable", "application/x-protobuf", protoLogin, []string{"hunter2hunter2", "tester@example.com"}, "protobuf"},
		{"protobuf, an opaque token", "application/x-protobuf", cat([]byte{0x0A, 0xC8, 0x01}, []byte(token200)), []string{token200}, "protobuf"},
		{"application/protobuf", "application/protobuf", protoLogin, []string{"hunter2hunter2"}, "protobuf"},
		{"Connect unary", "application/connect+proto", protoLogin, []string{"hunter2hunter2"}, "protobuf"},
		{"gRPC-web", "application/grpc-web+proto", cat([]byte{0, 0, 0, 0, 0x2A, 0x0A, 0x28}, []byte(token40)), []string{token40}, "gRPC"},
		{"gRPC-web-text, base64 the masker cannot see inside", "application/grpc-web-text", []byte(base64.StdEncoding.EncodeToString(grpcFramed)), []string{base64.StdEncoding.EncodeToString(grpcFramed)}, "gRPC"},
		{"gRPC", "application/grpc", grpcFramed, []string{"hunter2hunter2"}, "gRPC"},
		{"MessagePack", "application/msgpack", msgpack, []string{"hunter2"}, "MessagePack"},
		{"MessagePack, x-", "application/x-msgpack", msgpack, []string{"hunter2"}, "MessagePack"},
		{"CBOR", "application/cbor", cbor, []string{"hunter2"}, "CBOR"},
		{"Thrift", "application/vnd.apache.thrift.binary", protoLogin, []string{"hunter2hunter2"}, "Thrift"},
		{"a +proto suffix", "application/vnd.example.login+proto", protoLogin, []string{"hunter2hunter2"}, "protobuf"},
		{"a +cbor suffix", "application/vnd.example+cbor", cbor, []string{"hunter2"}, "CBOR"},
		{"octet-stream", "application/octet-stream", []byte("password=OCTETHIDDEN"), []string{"OCTETHIDDEN"}, "octet-stream"},
		{"a media type in capitals, with parameters", "Application/X-Protobuf; charset=binary", protoLogin, []string{"hunter2hunter2"}, "protobuf"},
		// What the content IS, whatever it says: control bytes anywhere, or bytes that are not the text it claims to be.
		{"MessagePack labelled JSON", "application/json", msgpack, []string{"hunter2"}, "not text"},
		{"CBOR with no type at all", "", cbor, []string{"hunter2"}, "not text"},
		{"protobuf labelled text", "text/plain", protoLogin, []string{"hunter2hunter2"}, "not text"},
		{"a NUL past the first hundred bytes", "text/plain", lateNul, []string{"LATENULHIDDEN"}, "not text"},
		{"a text-first multipart with a UTF-16 part", "multipart/form-data; boundary=B", textFirst, []string{"UTF16HIDDEN"}, "not text"},
		{"UTF-16 JSON labelled JSON", "application/json", utf16le(`{"password":"UTF16JSONHIDDEN"}`), []string{"UTF16JSONHIDDEN"}, "not text"},
	}
}

func TestABodyTheMaskerCannotReadIsLeftOutHoweverPrintableItsStart(t *testing.T) {
	for _, c := range unreadableBodies() {
		t.Run(c.name, func(t *testing.T) {
			for _, where := range []string{"reqBody", "respBody"} {
				flow := placedFlow(t, where, "", c.contentType, "", c.body, "")
				entry := entryFor(flow)
				encoded, err := encodeEntry(entry)
				if err != nil {
					t.Fatal(err)
				}
				comment, text := entry.Response.Content.Comment, entry.Response.Content.Text
				if where == "reqBody" {
					comment, text = entry.Request.PostData.Comment, entry.Request.PostData.Text
				}
				if text != "" || !strings.HasPrefix(comment, "the body was left out: ") || !strings.Contains(comment, c.reason) {
					t.Fatalf("%s: a %q body must be left out saying %q; comment=%q text=%s", where, c.contentType, c.reason, comment, quoted(text))
				}
				for _, secret := range c.secrets {
					if strings.Contains(string(encoded), secret) {
						t.Fatalf("%s: %s reached the record: %s", where, quoted(secret), quoted(string(encoded)))
					}
				}
				if strings.Contains(string(encoded), `"encoding"`) {
					t.Fatalf("%s: a body left out is never written as base64 either", where)
				}
			}
		})
	}
}

// TEXT THE MASKER CAN READ IS KEPT AND MASKED, AS IT WAS — the gate leaves out what it cannot read and nothing else.
func TestTextTheMaskerCanReadIsStillKeptAndMasked(t *testing.T) {
	cases := []struct {
		name, contentType, body string
		gone, kept              []string
	}{
		{"JSON", "application/json", `{"password":"JSONHIDDEN","title":"keepTITLE"}`, []string{"JSONHIDDEN"}, []string{"keepTITLE"}},
		{"Connect's JSON", "application/connect+json", `{"token":"CONNHIDDEN","n":1}`, []string{"CONNHIDDEN"}, []string{`"n":1`}},
		{"a form", "application/x-www-form-urlencoded", "user=keepU&password=FORMHIDDEN", []string{"FORMHIDDEN"}, []string{"keepU"}},
		{"plain text with tabs and line ends", "text/plain", "line one\tkeepTAB\r\nline two\f\n", nil, []string{"keepTAB"}},
		{"UTF-8 beyond ASCII", "text/plain; charset=utf-8", "ünïcödé 日本語 😀 keepUTF8", nil, []string{"keepUTF8"}},
		{"Latin-1, declared", "text/plain; charset=ISO-8859-1", "caf\xe9 keepLATIN", nil, []string{"café keepLATIN"}},
		{"SVG is text", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><title>keepSVG</title></svg>`, nil, []string{"keepSVG"}},
		{"XML", "application/xml", "<r><k>keepXML</k></r>", nil, []string{"keepXML"}},
		{"no type, plain words", "", "just words keepNOTYPE", nil, []string{"keepNOTYPE"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fieldOf(t, entryFor(placedFlow(t, "respBody", "", c.contentType, "", []byte(c.body), "")), "respBody", "")
			for _, gone := range c.gone {
				if strings.Contains(got, gone) {
					t.Fatalf("%s must be masked: %s", gone, got)
				}
			}
			for _, kept := range c.kept {
				if !strings.Contains(got, kept) {
					t.Fatalf("%s must be kept: %s", kept, got)
				}
			}
		})
	}
}

// A BODY CUT BY THIS AGENT'S OWN BOUND in the middle of a character is still text: only a body whose end was kept can
// be said to end badly.
func TestABodyCutInsideACharacterIsStillText(t *testing.T) {
	text := []byte(strings.Repeat("ş", keptBytes)) // two bytes a character: the kept bytes end on a boundary...
	kept := text[:keptBytes-1]                     // ...and one byte short of it, inside a character
	body := readOneBody(kept, int64(len(text)), "", "text/plain")
	if body.leftOut != "" || body.text == "" {
		t.Fatalf("a body cut by the kept bound inside a character is a preview, not binary: %q", body.leftOut)
	}
}
