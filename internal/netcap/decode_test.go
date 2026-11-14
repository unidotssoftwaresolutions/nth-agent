package netcap

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write(data)
	_ = w.Close()
	return b.Bytes()
}

// A BODY THIS AGENT CANNOT DECODE OR READ IS NEVER SENT — not even its base64 — and the entry says why.
func TestABodyThisAgentCannotDecodeOrReadIsLeftOutWithItsReason(t *testing.T) {
	secret := `{"accessToken":"UNDECODABLEHIDDEN","note":"x"}`
	cases := []struct {
		name, encoding, contentType string
		body                        []byte
		want                        string
	}{
		{"an encoding it does not decode", "x-custom-pack", "application/json", []byte(secret), "encoded as “x-custom-pack”, which this agent does not decode"},
		{"two encodings at once", "gzip, br", "application/json", gzipped(t, []byte(secret)), "encoded as something, which this agent does not decode"},
		{"a gzip body that is not gzip", "gzip", "application/json", []byte("plainly not compressed " + secret), "does not decode as gzip"},
		{"a br body that is not br", "br", "application/json", []byte(secret), "does not decode as br"},
		{"a zstd body that is not zstd", "zstd", "application/json", []byte(secret), "does not decode as zstd"},
		{"a gzip body whose stream ends early on the wire", "gzip", "application/json", gzipped(t, []byte(secret))[:20], "does not decode as gzip"},
		{"a binary body", "", "image/png", append([]byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0x0d}, bytes.Repeat([]byte{0, 1, 2, 0xfe}, 40)...), "it is not text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := readOneBody(c.body, int64(len(c.body)), c.encoding, c.contentType)
			if body.text != "" || !strings.Contains(body.leftOut, c.want) {
				t.Fatalf("the body must be left out saying %q, got leftOut=%q text=%q", c.want, body.leftOut, body.text)
			}
			entry := entryFor(Flow{Method: "GET", URL: "https://host.example/", Status: 200,
				RespHeaders: []Header{{"Content-Type", c.contentType}, {"Content-Encoding", c.encoding}}, RespBody: c.body, RespBodySize: int64(len(c.body))})
			encoded, err := encodeEntry(entry)
			if err != nil {
				t.Fatal(err)
			}
			if entry.Response.Content.Text != "" || entry.Response.Content.Comment != body.leftOut {
				t.Fatalf("the entry keeps no body and says why: %+v", entry.Response.Content)
			}
			if strings.Contains(string(encoded), "UNDECODABLEHIDDEN") || strings.Contains(string(encoded), `"encoding"`) {
				t.Fatalf("nothing of a body left out reaches the record, as text or as base64: %s", encoded)
			}
			if entry.Response.Content.Size != int64(len(c.body)) || entry.Response.Status != 200 {
				t.Fatalf("the entry keeps its status and sizes: %+v", entry.Response)
			}
		})
	}
}

// THE SERVER'S ENCODING NAME IS ITS TEXT, AND IS REPEATED ONLY WHEN IT IS A PLAIN TOKEN.
func TestAnEncodingNameIsRepeatedOnlyWhenItIsAPlainToken(t *testing.T) {
	body := readOneBody([]byte("x"), 1, "token=SECRETINENCODING", "text/plain")
	if strings.Contains(body.leftOut, "SECRETINENCODING") || !strings.Contains(body.leftOut, "something") {
		t.Fatalf("a header value that is not an encoding's name is not repeated into the record, got %q", body.leftOut)
	}
}

// A BODY CUT BY THIS AGENT'S OWN BOUND decodes to its start, which is the preview — it is not a corrupt body.
func TestABodyLargerThanWhatIsKeptIsAPreviewOfItsStart(t *testing.T) {
	text := []byte(`{"items":[` + strings.Repeat(`{"title":"keepTHIS","n":12345},`, 120000) + `{}]}`)
	compressed := gzipped(t, text)
	kept := compressed[:len(compressed)/2] // the stream cut in the middle, as when a big body is cut at the kept bound
	body := readOneBody(kept, int64(len(compressed)), "gzip", "application/json")
	if body.leftOut != "" {
		t.Fatalf("a body cut at the kept bound is a preview, not a fault: %s", body.leftOut)
	}
	if !strings.HasPrefix(string(text), body.text) || len(body.text) == 0 {
		t.Fatalf("the preview is the body's start")
	}
	if body.decodedSize != -1 {
		t.Fatalf("a body not decoded to its end has no decoded size, got %d", body.decodedSize)
	}
}

// A DECOMPRESSION BOMB costs the record at most what it keeps.
func TestABombDecodesNoFurtherThanWhatIsKept(t *testing.T) {
	bomb := gzipped(t, bytes.Repeat([]byte("a"), 64<<20))
	body := readOneBody(bomb, int64(len(bomb)), "gzip", "text/plain")
	if body.leftOut != "" {
		t.Fatal(body.leftOut)
	}
	if units := len(utf16.Encode([]rune(body.text))); units != runnerBodyCap+1 {
		t.Fatalf("the preview is the runner's bound plus the character that crosses it (%d), got %d", runnerBodyCap+1, units)
	}
	if body.decodedSize != -1 {
		t.Fatal("a body decoded only as far as is kept has no decoded size")
	}
}

// THE PREVIEW IS COUNTED AS THE RUNNER COUNTS — UTF-16 code units — and cut after the character that crosses the runner's bound,
// so a body longer than the bound reaches the runner longer than it, and is marked truncated there — emoji included, whose
// characters are two units each and once ended a preview exactly at the bound.
func TestThePreviewIsTheRunnersBoundPlusOneCountedInItsUnits(t *testing.T) {
	for _, unit := range []string{"a", "ş", "日", "😀"} {
		text := strings.Repeat(unit, runnerBodyCap+10)
		body := readOneBody([]byte(text), int64(len(text)), "", "text/plain; charset=utf-8")
		units := len(utf16.Encode([]rune(body.text)))
		if units <= runnerBodyCap || units > previewUnits {
			t.Fatalf("%q: the preview must be longer than the runner's bound (%d) by one character at most, got %d", unit, runnerBodyCap, units)
		}
		if !strings.HasPrefix(text, body.text) {
			t.Fatalf("%q: the preview is the body's start, cut on a character", unit)
		}
	}
}

func TestALatin1BodyIsReadAsLatin1AndAnythingElseAsUTF8(t *testing.T) {
	body := readOneBody([]byte{'c', 'a', 'f', 0xe9}, 4, "", "text/plain; charset=ISO-8859-1")
	if body.text != "café" {
		t.Fatalf("Latin-1 is read as Latin-1, got %q", body.text)
	}
	body = readOneBody([]byte("café"), 5, "", "text/plain")
	if body.text != "café" {
		t.Fatalf("anything else is read as UTF-8, got %q", body.text)
	}
	// A BYTE THAT IS NOT UTF-8, IN A BODY THAT DOES NOT DECLARE LATIN-1, IS NOT TEXT THE MASKER CAN READ. It used to
	// become U+FFFD and be kept; MessagePack's and CBOR's headers are such bytes, and nothing tells a stray Latin-1 letter
	// from them, so the body is left out — the safer side of the same promise: the masker sees all the text that is written.
	body = readOneBody([]byte{'c', 'a', 'f', 0xe9}, 4, "", "text/plain")
	if body.text != "" || !strings.Contains(body.leftOut, "it is not text") || strings.Contains(body.text, string(utf8.RuneError)) {
		t.Fatalf("a body that is not the UTF-8 it is read as is left out, got text=%q leftOut=%q", body.text, body.leftOut)
	}
}

func TestIsMostlyBinaryIsMitmproxys(t *testing.T) {
	cases := map[string]bool{
		"":                                  false,
		`{"a":1}`:                           false,
		"ünïcödé text, mostly ascii":        false,
		"日本語のテキスト":                          false,
		string([]byte{0, 0, 0, 0, 1, 2, 3}): true,
		string(bytes.Repeat([]byte{0xff, 0xfe}, 60)): true,
	}
	for text, want := range cases {
		if got := isMostlyBinary([]byte(text)); got != want {
			t.Fatalf("is_mostly_bin(%q) = %v, mitmproxy says %v", text, got, want)
		}
	}
}
