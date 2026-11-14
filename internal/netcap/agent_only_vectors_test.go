package netcap

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── BODIES THAT CROSSED THE WIRE COMPRESSED: DECODED, THEN MASKED ────────────────────────────────────────────────
//
// The runner never sees a compressed body — mitmproxy decodes before it writes a HAR — so these cases are the agent's alone:
// testdata/agent-only-mask-vectors.json, every body compressed by a REFERENCE implementation (Python's zlib, Google's
// brotli, libzstd — the libraries mitmproxy's own decoders call), so the Go decoders here are checked against encoders
// that are not their own. Each case must decode to exactly its `decoded` text, and then mask like any other body.

type agentCase struct {
	ID              string   `json:"id"`
	Location        string   `json:"location"`
	ContentType     string   `json:"contentType"`
	ContentEncoding string   `json:"contentEncoding"`
	Decoded         string   `json:"decoded"`
	EncodedBase64   string   `json:"encodedBase64"`
	ProducedBy      string   `json:"producedBy"`
	Absent          []string `json:"absent"`
	Present         []string `json:"present"`
}

func TestEveryEncodingDecodesAsTheReferenceEncodedItAndIsMasked(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "agent-only-mask-vectors.json"))
	if err != nil {
		t.Fatalf("the agent-only vectors are missing: %v", err)
	}
	var file struct {
		Cases []agentCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range file.Cases {
		seen[c.ContentEncoding] = true
		t.Run(c.ID, func(t *testing.T) {
			encoded, err := base64.StdEncoding.DecodeString(c.EncodedBase64)
			if err != nil {
				t.Fatal(err)
			}
			body := readOneBody(encoded, int64(len(encoded)), c.ContentEncoding, c.ContentType)
			if body.leftOut != "" {
				t.Fatalf("a %s body from %s was left out: %s", c.ContentEncoding, c.ProducedBy, body.leftOut)
			}
			if body.text != c.Decoded {
				t.Fatalf("a %s body from %s decoded to %s, not %s", c.ContentEncoding, c.ProducedBy, quoted(body.text), quoted(c.Decoded))
			}
			if body.decodedSize != int64(len(c.Decoded)) {
				t.Fatalf("the decoded size is %d and the body is %d bytes", body.decodedSize, len(c.Decoded))
			}
			flow := placedFlow(t, c.Location, "", c.ContentType, c.ContentEncoding, encoded, "")
			entry := entryFor(flow)
			got := fieldOf(t, entry, c.Location, "")
			for _, gone := range c.Absent {
				if strings.Contains(got, gone) {
					t.Errorf("%s must be masked after decoding, and survived in %s", quoted(gone), quoted(got))
				}
			}
			for _, kept := range c.Present {
				if !strings.Contains(got, kept) {
					t.Errorf("%s must survive, and was lost from %s", quoted(kept), quoted(got))
				}
			}
			// THE RECORD STATES THE BODY'S SIZE ON THE WIRE, and what decoding added — mitmproxy's two numbers.
			if c.Location == "respBody" {
				if entry.Response.Content.Size != int64(len(encoded)) || entry.Response.BodySize != int64(len(encoded)) {
					t.Fatalf("content.size and bodySize are the size on the wire (%d), got %d and %d", len(encoded),
						entry.Response.Content.Size, entry.Response.BodySize)
				}
				if entry.Response.Content.Compression == nil || *entry.Response.Content.Compression != int64(len(c.Decoded)-len(encoded)) {
					t.Fatalf("content.compression is what decoding added (%d)", len(c.Decoded)-len(encoded))
				}
			}
		})
	}
	for _, encoding := range []string{"gzip", "deflate", "deflateraw", "br", "zstd"} {
		if !seen[encoding] {
			t.Fatalf("no agent-only case carries a %s body — each encoding the agent decodes is checked against its reference", encoding)
		}
	}
}
