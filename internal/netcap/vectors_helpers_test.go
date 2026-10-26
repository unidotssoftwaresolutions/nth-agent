package netcap

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// placedFlow puts one case's input where the case says, the way the proxy would hand that exchange to the record: a
// header under its name, a body (encoded as the case says it crossed the wire) under its content type, or the address.
func placedFlow(t *testing.T, location, name, contentType, contentEncoding string, body []byte, input string) Flow {
	t.Helper()
	f := Flow{Started: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), Method: "GET", URL: "https://host.example/", ReqProto: "HTTP/1.1"}
	switch location {
	case "reqHeader":
		f.ReqHeaders = []Header{{Name: name, Value: input}}
	case "respHeader":
		f.Status, f.RespProto = 200, "HTTP/1.1"
		f.RespHeaders = []Header{{Name: name, Value: input}}
	case "reqBody":
		f.Method = "POST"
		f.ReqHeaders = []Header{{Name: "Content-Type", Value: contentType}}
		if contentEncoding != "" {
			f.ReqHeaders = append(f.ReqHeaders, Header{Name: "Content-Encoding", Value: contentEncoding})
		}
		f.ReqBody, f.ReqBodySize = body, int64(len(body))
	case "respBody":
		f.Status, f.RespProto = 200, "HTTP/1.1"
		f.RespHeaders = []Header{{Name: "Content-Type", Value: contentType}}
		if contentEncoding != "" {
			f.RespHeaders = append(f.RespHeaders, Header{Name: "Content-Encoding", Value: contentEncoding})
		}
		f.RespBody, f.RespBodySize = body, int64(len(body))
	case "url":
		f.URL = input
	default:
		t.Fatalf("a case sits in %q, which is not a place this reader knows", location)
	}
	return f
}

// fieldOf reads back, from the masked entry, the field a case placed its input in.
func fieldOf(t *testing.T, entry harEntry, location, name string) string {
	t.Helper()
	switch location {
	case "reqHeader":
		return pairValue(t, entry.Request.Headers, name)
	case "respHeader":
		return pairValue(t, entry.Response.Headers, name)
	case "reqBody":
		if entry.Request.PostData == nil {
			t.Fatalf("the request body was not recorded at all")
		}
		if entry.Request.PostData.Comment != "" {
			t.Fatalf("the request body was left out: %s", entry.Request.PostData.Comment)
		}
		return entry.Request.PostData.Text
	case "respBody":
		if entry.Response.Content.Comment != "" {
			t.Fatalf("the response body was left out: %s", entry.Response.Content.Comment)
		}
		return entry.Response.Content.Text
	default:
		return entry.Request.URL
	}
}

func pairValue(t *testing.T, pairs []harPair, name string) string {
	t.Helper()
	for _, p := range pairs {
		if p.Name == name {
			return p.Value
		}
	}
	t.Fatalf("the header %q the case placed is not in the record: %v", name, pairs)
	return ""
}

// encodeAs compresses text the way a server would have sent it, with the encoders this side has.
func encodeAs(t *testing.T, encoding, text string) []byte {
	t.Helper()
	var b bytes.Buffer
	switch encoding {
	case "":
		return []byte(text)
	case "gzip":
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(text))
		_ = w.Close()
	case "deflate":
		w := zlib.NewWriter(&b)
		_, _ = w.Write([]byte(text))
		_ = w.Close()
	case "br":
		w := brotli.NewWriter(&b)
		_, _ = w.Write([]byte(text))
		_ = w.Close()
	default:
		t.Fatalf("this side has no %s encoder: carry such a case as an agent-only vector, compressed by the reference library", encoding)
	}
	return b.Bytes()
}

func quoted(s string) string {
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return fmt.Sprintf("%q", s)
}
