package netcap

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// ── ONE EXCHANGE, AS THE RUNNER WILL READ IT ────────────────────────────────────────────────────────────────────────
//
// A Flow is what the proxy saw — RAW, and in memory only. The one way it reaches the disk is entryFor: every value in the
// entry is the masked one, and a body is there only as decoded, masked text (decode.go). Nothing else about a Flow is
// ever written, logged or sent.
//
// THE ENTRY IS mitmproxy's SHAPE FOR THE FIELDS THE RUNNER READS (HarRedactor.TryRedactEntry: startedDateTime, time,
// request.method/url/headers/bodySize/postData.text/postData.mimeType, response.status/headers and
// content.mimeType/size/text/encoding) with the meanings mitmproxy 12.2.3's savehar.py gives them, read on disk:
// content.size is the body's size ON THE WIRE, compression what decoding added, text the decoded text. A text body is
// never base64 here, so content.encoding is never written.
//
// WHAT IS WRITTEN THAT THE RUNNER DOES NOT READ YET, AND SAYS SOMETHING: a body left out carries its reason in its
// `comment` (HAR 1.2's own field), and an exchange that never got a response carries this agent's sentence in
// `_error`, mitmproxy's field. WHAT IS NOT WRITTEN AT ALL: cookies, queryString and postData.params, which would be a
// second copy of what the headers, the address and the body already carry — empty lists, as mitmproxy writes when
// there are none — and the server's address.

// Header is one header line, in order, repeats kept.
type Header struct {
	Name  string
	Value string
}

// Flow is one exchange as the proxy saw it. RAW, IN MEMORY ONLY — see the file's docblock.
type Flow struct {
	Started  time.Time
	Method   string
	URL      string
	ReqProto string

	ReqHeaders []Header
	// ReqBody is the request body's first keptBytes; ReqBodySize its whole size on the wire.
	ReqBody     []byte
	ReqBodySize int64

	Status      int
	Reason      string
	RespProto   string
	RespHeaders []Header
	// RespBody is the response body's first keptBytes; RespBodySize its whole size on the wire.
	RespBody     []byte
	RespBodySize int64

	// The timings, in milliseconds.
	SendMs, WaitMs, ReceiveMs float64

	// WebSocket marks an upgrade: the exchange that opened a WebSocket is recorded; its messages are not.
	WebSocket bool
	// Err is this agent's own sentence for an exchange that never got a response. Never a library's error text, which
	// can carry the very address being masked.
	Err string
}

type harPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harEntry struct {
	StartedDateTime   string      `json:"startedDateTime"`
	Time              float64     `json:"time"`
	Request           harRequest  `json:"request"`
	Response          harResponse `json:"response"`
	Cache             struct{}    `json:"cache"`
	Timings           harTimings  `json:"timings"`
	ResourceType      string      `json:"_resourceType,omitempty"`
	WebSocketMessages *[]struct{} `json:"_webSocketMessages,omitempty"`
}

type harRequest struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	HTTPVersion string       `json:"httpVersion"`
	Cookies     []harPair    `json:"cookies"`
	Headers     []harPair    `json:"headers"`
	QueryString []harPair    `json:"queryString"`
	HeadersSize int          `json:"headersSize"`
	BodySize    int64        `json:"bodySize"`
	PostData    *harPostData `json:"postData,omitempty"`
}

type harPostData struct {
	MimeType string    `json:"mimeType"`
	Text     string    `json:"text"`
	Params   []harPair `json:"params"`
	Comment  string    `json:"comment,omitempty"`
}

type harResponse struct {
	Status      int        `json:"status"`
	StatusText  string     `json:"statusText"`
	HTTPVersion string     `json:"httpVersion"`
	Cookies     []harPair  `json:"cookies"`
	Headers     []harPair  `json:"headers"`
	Content     harContent `json:"content"`
	RedirectURL string     `json:"redirectURL"`
	HeadersSize int        `json:"headersSize"`
	BodySize    int64      `json:"bodySize"`
	Error       string     `json:"_error,omitempty"`
}

type harContent struct {
	Size        int64  `json:"size"`
	Compression *int64 `json:"compression,omitempty"`
	MimeType    string `json:"mimeType"`
	Text        string `json:"text"`
	Comment     string `json:"comment,omitempty"`
}

type harTimings struct {
	Connect float64 `json:"connect"`
	SSL     float64 `json:"ssl"`
	Send    float64 `json:"send"`
	Receive float64 `json:"receive"`
	Wait    float64 `json:"wait"`
}

// entryFor masks one flow into the entry the capture file holds.
func entryFor(f Flow) harEntry {
	reqHeaders := maskHeaders(f.ReqHeaders)
	entry := harEntry{
		StartedDateTime: f.Started.UTC().Format("2006-01-02T15:04:05.000000-07:00"),
		Request: harRequest{
			Method:      stripNul(f.Method),
			URL:         maskAddress(stripNul(f.URL)),
			HTTPVersion: f.ReqProto,
			Cookies:     []harPair{},
			Headers:     pairsOf(reqHeaders),
			QueryString: []harPair{},
			HeadersSize: -1,
			BodySize:    f.ReqBodySize,
		},
		Timings: harTimings{Connect: -1, SSL: -1, Send: f.SendMs, Receive: f.ReceiveMs, Wait: f.WaitMs},
	}
	for _, t := range []float64{f.SendMs, f.WaitMs, f.ReceiveMs} {
		if t > 0 {
			entry.Time += t
		}
	}

	switch f.Method {
	case "POST", "PUT", "PATCH":
		contentType := headerValue(f.ReqHeaders, "Content-Type")
		body := readOneBody(f.ReqBody, f.ReqBodySize, headerValue(f.ReqHeaders, "Content-Encoding"), contentType)
		post := &harPostData{MimeType: maskText(contentType), Params: []harPair{}}
		if body.leftOut != "" {
			post.Comment = body.leftOut
		} else {
			post.Text = maskBodyText(body.text, contentType)
		}
		entry.Request.PostData = post
	}

	if f.Status == 0 {
		entry.Response = harResponse{Cookies: []harPair{}, Headers: []harPair{}, HeadersSize: -1, BodySize: -1, Error: f.Err}
		return entry
	}
	respHeaders := maskHeaders(f.RespHeaders)
	contentType := headerValue(f.RespHeaders, "Content-Type")
	content := harContent{Size: f.RespBodySize, MimeType: maskText(contentType)}
	if !f.WebSocket {
		body := readOneBody(f.RespBody, f.RespBodySize, headerValue(f.RespHeaders, "Content-Encoding"), contentType)
		if body.decodedSize >= 0 {
			added := body.decodedSize - f.RespBodySize
			content.Compression = &added
		}
		if body.leftOut != "" {
			content.Comment = body.leftOut
		} else {
			content.Text = maskBodyText(body.text, contentType)
		}
	}
	entry.Response = harResponse{
		Status:      f.Status,
		StatusText:  maskText(f.Reason),
		HTTPVersion: f.RespProto,
		Cookies:     []harPair{},
		Headers:     pairsOf(respHeaders),
		Content:     content,
		RedirectURL: headerValue(respHeaders, "Location"),
		HeadersSize: -1,
		BodySize:    f.RespBodySize,
		Error:       f.Err,
	}
	if f.WebSocket {
		entry.ResourceType = "websocket"
		entry.WebSocketMessages = &[]struct{}{}
	}
	return entry
}

// refusalEntry is the one entry a host that refused the capture certificate gets: the address the phone asked for, in
// mitmproxy's shape for a CONNECT (https://host:port/), no response, and why.
func refusalEntry(started time.Time, hostPort string) harEntry {
	return harEntry{
		StartedDateTime: started.UTC().Format("2006-01-02T15:04:05.000000-07:00"),
		Request: harRequest{
			Method: "CONNECT", URL: maskAddress("https://" + hostPort + "/"),
			Cookies: []harPair{}, Headers: []harPair{}, QueryString: []harPair{}, HeadersSize: -1,
		},
		Response: harResponse{Cookies: []harPair{}, Headers: []harPair{}, HeadersSize: -1, BodySize: -1, Error: NotReadableRefused},
		Timings:  harTimings{Connect: -1, SSL: -1},
	}
}

// NotReadableRefused is what an entry for a host that refused the capture certificate says: from then on, for the rest of
// the capture, that host's traffic passes through untouched and unread. It names THE HOST, never the app (AN_AGENT_CAPTURE_READY_TO_SHIP,
// rule 5): an app that trusts user certificates may still refuse one host — the one it pins, or one an SDK reaches through a
// library that trusts only its own certificates — and "the app does not trust" read as if the whole app had refused.
const NotReadableRefused = "not readable: this host did not accept the capture certificate (the app pins it, or its library trusts only its own certificates)"

// encodeEntry is an entry's JSON as it goes into the capture file — HTML characters as written, as Python's json writes them.
func encodeEntry(entry harEntry) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entry); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func pairsOf(headers []Header) []harPair {
	out := make([]harPair, len(headers))
	for i, h := range headers {
		out[i] = harPair{Name: h.Name, Value: h.Value}
	}
	return out
}

func headerValue(headers []Header, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
