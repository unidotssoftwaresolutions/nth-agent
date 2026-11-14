package netcap

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"mime"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andybalholm/brotli"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/zstd"
)

// ── A BODY, AS THE RECORD READS IT ──────────────────────────────────────────────────────────────────────────────
//
// THE PHONE ALWAYS GETS THE ORIGINAL BYTES. What is decided here is only what the RECORD holds: the decoded text, as
// mitmproxy writes it (its net/encoding.py and savehar.py, 12.2.3, read on disk), or no body at all and the reason.
//
//   - Decoded for the record: gzip (and the zlib stream some servers label gzip — mitmproxy's zlib auto-detection),
//     deflate (zlib, then raw deflate when the header is missing — mitmproxy's fallback), deflateraw, br and zstd.
//   - A BODY THIS AGENT CANNOT DECODE OR READ IS NEVER SENT — not even as base64, because the masker cannot see inside
//     it. That is a body in an encoding this agent does not decode, one that does not decode as it says, and one that
//     decodes to something that is not text (an image, protobuf — mitmproxy's own is_mostly_bin test, ported below).
//     That test reads a body's first hundred bytes, and a string-heavy protobuf or MessagePack body passes it; so a body
//     is also left out when its media type names a binary or schema-less encoding (unreadableFormat), and when ANY byte
//     of it is a control character or, unless it declares Latin-1, not UTF-8 (readsAsText).
//     The entry keeps its method, address, status, headers and sizes, and its comment says the body was left out and why.
//   - THE PREVIEW IS THE RUNNER'S OWN BOUND PLUS THE CHARACTER THAT CROSSES IT, counted the way the runner counts (UTF-16 code units), so a body
//     longer than the preview reaches the runner longer than its bound and is marked truncated there exactly as a node's is.

const (
	// runnerBodyCap is the runner's preview bound (HarRedactor.DefaultBodyCap), in UTF-16 code units.
	runnerBodyCap = 256 * 1024
	// previewUnits is the most the record keeps of a text body: the runner's bound and the one character that crosses it (one
	// or two units), so that the runner sees a body cut here as cut.
	previewUnits = runnerBodyCap + 2
	// keptBytes is how much of a body, raw or decoded, is held in memory for the record. A megabyte is enough for
	// previewUnits of any UTF-8 text (three bytes to a unit at the most), and for a compressed body that decodes to that.
	// Everything past it is streamed to the phone and not held.
	keptBytes = 1 << 20
)

// readBody is what the record holds of one body.
type readBody struct {
	// text is the decoded body as text, cut to previewUnits; empty for an empty body or one that was left out.
	text string
	// decodedSize is the whole body's decoded size when this agent decoded all of it, else -1.
	decodedSize int64
	// leftOut says why the record holds no body; empty when it holds one.
	leftOut string
}

// readOneBody decodes the kept bytes of a body (its first keptBytes) whose whole size on the wire was total, as its
// Content-Encoding and Content-Type say, for the record.
func readOneBody(kept []byte, total int64, contentEncoding, contentType string) (body readBody) {
	if total == 0 {
		return readBody{decodedSize: 0}
	}
	complete := int64(len(kept)) == total
	body.decodedSize = -1

	encoding := strings.ToLower(strings.TrimSpace(contentEncoding))
	var decoded []byte
	switch encoding {
	case "", "identity", "none":
		decoded, body.decodedSize = kept, total // not encoded: what crossed the wire is the body
	case "gzip", "deflate", "deflateraw", "br", "zstd":
		var whole bool
		var reason string
		decoded, whole, reason = decodeKept(encoding, kept, complete)
		if reason != "" {
			return readBody{decodedSize: -1, leftOut: reason}
		}
		if whole {
			body.decodedSize = int64(len(decoded))
		}
	default:
		return readBody{decodedSize: -1, leftOut: "the body was left out: it is encoded as " + nameOfEncoding(encoding) +
			", which this agent does not decode, so it could not be masked"}
	}
	// mitmproxy's test over the first hundred bytes, then WHAT THE BODY SAYS IT IS, then WHAT EVERY BYTE OF IT IS: a
	// body that passes the first is not yet text.
	notText := func() readBody {
		// A SIZE, NEVER THE BODY, goes into the sentence — computed first, so no printing call is handed a body.
		size := describeSize(body.decodedSize, total)
		return readBody{decodedSize: body.decodedSize, leftOut: "the body was left out: it is not text (" + size + "), " +
			"and this agent sends only bodies it can read and mask"}
	}
	if isMostlyBinary(decoded) {
		return notText()
	}
	if format := unreadableFormat(contentType); format != "" {
		return readBody{decodedSize: body.decodedSize, leftOut: "the body was left out: it is " + format +
			", which this agent cannot read field by field, so it could not be masked"}
	}
	if !readsAsText(decoded, contentType, body.decodedSize == int64(len(decoded))) {
		return notText()
	}
	body.text = previewText(decoded, contentType)
	return body
}

// unreadableFormat names the binary or schema-less encoding a body's declared media type says it is in, or is empty for
// a type the masker may read. Such a body holds its values beside their names with no `:`, `=` or quote between them, or
// with no name at all (protobuf's fields are numbers), so a held name cannot be paired with its value and a value with
// no published shape cannot be told from the app's content. gRPC-web-text is base64 of the same frames: printable, and
// just as closed to the masker.
func unreadableFormat(contentType string) string {
	media := mediaType(contentType)
	if media == "" {
		return ""
	}
	top, sub, _ := strings.Cut(media, "/")
	switch top {
	case "image":
		if sub == "svg+xml" {
			return "" // SVG is XML, which the text rule reads
		}
		return "an image"
	case "audio", "video":
		return top
	case "font":
		return "a font"
	}
	if top != "application" {
		return ""
	}
	suffix := ""
	if i := strings.LastIndexByte(sub, '+'); i >= 0 {
		suffix = sub[i+1:]
	}
	switch {
	case strings.HasPrefix(sub, "grpc"):
		return "gRPC (" + media + ")"
	case sub == "x-protobuf" || sub == "protobuf" || sub == "x-google-protobuf" || sub == "vnd.google.protobuf" ||
		sub == "proto" || sub == "x-protobuffer" || suffix == "proto" || suffix == "protobuf":
		return "protobuf (" + media + ")"
	case sub == "msgpack" || sub == "x-msgpack" || sub == "vnd.msgpack" || suffix == "msgpack":
		return "MessagePack (" + media + ")"
	case sub == "cbor" || sub == "cbor-seq" || suffix == "cbor" || suffix == "cbor-seq":
		return "CBOR (" + media + ")"
	case sub == "x-thrift" || sub == "thrift" || strings.HasPrefix(sub, "vnd.apache.thrift") || suffix == "thrift":
		return "Thrift (" + media + ")"
	case sub == "avro" || sub == "x-avro" || sub == "avro-binary" || strings.HasPrefix(sub, "vnd.apache.avro") || suffix == "avro":
		return "Avro (" + media + ")"
	case sub == "bson" || sub == "x-bson" || suffix == "bson":
		return "BSON (" + media + ")"
	case sub == "x-flatbuffers" || sub == "flatbuffers" || suffix == "flatbuffers":
		return "FlatBuffers (" + media + ")"
	case sub == "octet-stream":
		return "raw bytes (application/octet-stream)"
	}
	return ""
}

// mediaType is a Content-Type's media type in lower case, its parameters dropped — read leniently, so a value Go's
// parser refuses is still judged by what it names.
func mediaType(contentType string) string {
	if media, _, err := mime.ParseMediaType(contentType); err == nil {
		return media
	}
	media, _, _ := strings.Cut(contentType, ";")
	media = strings.ToLower(strings.TrimSpace(media))
	if !encodingName.MatchString(strings.ReplaceAll(media, "/", "")) || strings.Count(media, "/") != 1 {
		return "" // not a media type at all: judged by its bytes alone
	}
	return media
}

// readsAsText is the rest of the body's own test, over EVERY byte the record would read: no control character but tab,
// line feed, form feed and carriage return; and, unless it declares Latin-1, nothing that is not UTF-8 — MessagePack's
// and CBOR's headers are bytes 0x80-0xBF, which no C0 test sees. In a Latin-1 body the C1 controls (0x80-0x9F) count
// as control characters. whole says the bytes are the whole body; when they are not, a character cut at the kept bound is
// the preview's end, not a fault.
func readsAsText(decoded []byte, contentType string, whole bool) bool {
	latin1 := isLatin1(contentType)
	for _, b := range decoded {
		if (b < 0x20 && b != '\t' && b != '\n' && b != '\f' && b != '\r') || b == 0x7f || (latin1 && b >= 0x80 && b <= 0x9f) {
			return false
		}
	}
	if latin1 {
		return true
	}
	if !whole {
		for cut := 0; cut < utf8.UTFMax && cut < len(decoded); cut++ {
			if utf8.RuneStart(decoded[len(decoded)-1-cut]) {
				if !utf8.FullRune(decoded[len(decoded)-1-cut:]) {
					decoded = decoded[:len(decoded)-1-cut]
				}
				break
			}
		}
	}
	return utf8.Valid(decoded)
}

// decodeKept decodes a compressed body's kept bytes, at most keptBytes of output. whole is true when the stream ended
// within them; reason says why the body cannot be read — empty when it can (a body cut at the kept bound decodes to
// its start, which is the preview, not a fault).
func decodeKept(encoding string, kept []byte, complete bool) (decoded []byte, whole bool, reason string) {
	defer func() {
		if recover() != nil {
			decoded, whole, reason = nil, false, notAsItSays(encoding)
		}
	}()
	attempt := func(open func(io.Reader) (io.Reader, error)) ([]byte, bool, error) {
		reader, err := open(bytes.NewReader(kept))
		if err != nil {
			return nil, false, err
		}
		out, err := io.ReadAll(io.LimitReader(reader, keptBytes))
		if err != nil {
			return out, false, err
		}
		if len(out) < keptBytes {
			return out, true, nil
		}
		var one [1]byte
		n, err := reader.Read(one[:])
		if n == 0 && errors.Is(err, io.EOF) {
			return out, true, nil
		}
		return out, false, nil // more than this agent keeps: the preview is its start
	}

	var err error
	switch encoding {
	case "gzip":
		// ONE MEMBER, as mitmproxy's zlib reads it: what follows the first gzip member is not read (some servers pad).
		decoded, whole, err = attempt(func(r io.Reader) (io.Reader, error) {
			member, err := gzip.NewReader(r)
			if err != nil {
				return nil, err
			}
			member.Multistream(false)
			return member, nil
		})
		if errors.Is(err, gzip.ErrHeader) {
			decoded, whole, err = attempt(func(r io.Reader) (io.Reader, error) { return zlib.NewReader(r) })
		}
	case "deflate", "deflateraw":
		decoded, whole, err = attempt(func(r io.Reader) (io.Reader, error) { return zlib.NewReader(r) })
		if err != nil && !(errors.Is(err, io.ErrUnexpectedEOF) && !complete && len(decoded) > 0) {
			decoded, whole, err = attempt(func(r io.Reader) (io.Reader, error) { return flate.NewReader(r), nil })
		}
	case "br":
		decoded, whole, err = attempt(func(r io.Reader) (io.Reader, error) { return brotli.NewReader(r), nil })
	case "zstd":
		decoded, whole, err = attempt(func(r io.Reader) (io.Reader, error) { return zstd.NewReader(r), nil })
	}
	if err == nil {
		return decoded, whole, ""
	}
	// CUT BY THIS AGENT'S OWN BOUND: the stream runs past the kept bytes, and what decoded before the cut is the preview.
	if errors.Is(err, io.ErrUnexpectedEOF) && !complete && len(decoded) > 0 {
		return decoded, false, ""
	}
	return nil, false, notAsItSays(encoding)
}

func notAsItSays(encoding string) string {
	return "the body was left out: it says it is " + encoding + "-encoded and does not decode as " + encoding +
		", so it could not be masked"
}

// encodingName is the shape of an encoding name this agent will repeat in a sentence; anything else the server sent is
// not repeated, because a header value is the server's text and this sentence is not masked.
var encodingName = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,31}$`)

func nameOfEncoding(encoding string) string {
	if encodingName.MatchString(encoding) {
		return "“" + encoding + "”"
	}
	return "something"
}

func describeSize(decodedSize, wireSize int64) string {
	if decodedSize >= 0 {
		return strconv.FormatInt(decodedSize, 10) + " bytes"
	}
	return strconv.FormatInt(wireSize, 10) + " bytes on the wire"
}

// previewText reads decoded bytes as text and cuts them to previewUnits on a character's boundary. A body in ISO-8859-1
// (Latin-1) is read as Latin-1; anything else as UTF-8. A body that is not UTF-8 never gets here (readsAsText), so the
// one byte that can still become U+FFFD is a character cut at the kept bound — what the record's JSON would make of it
// anyway, so the masker sees exactly the text that is written.
func previewText(decoded []byte, contentType string) string {
	var text string
	if isLatin1(contentType) {
		runes := make([]rune, len(decoded))
		for i, b := range decoded {
			runes[i] = rune(b)
		}
		text = string(runes)
	} else {
		text = strings.ToValidUTF8(string(decoded), string(utf8.RuneError))
	}
	units := 0
	for i, r := range text {
		units += utf16.RuneLen(r)
		if units > runnerBodyCap {
			// THE CHARACTER THAT CROSSES THE BOUND IS KEPT: a character of two units (an emoji) could otherwise end the
			// preview exactly at the runner's bound, which the runner would read as a whole body.
			return text[:i+utf8.RuneLen(r)]
		}
	}
	return text
}

func isLatin1(contentType string) bool {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch strings.ToLower(params["charset"]) {
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "l1", "cp819", "ibm819":
		return true
	}
	return false
}

// isMostlyBinary is mitmproxy's strutils.is_mostly_bin, byte for byte: it looks at the first hundred-odd bytes, and a
// body of mostly printable ASCII, or of valid UTF-8 with few control characters, is text.
func isMostlyBinary(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	if len(s) > 100 {
		cut := 100
		for at := 100; at < min(104, len(s)); at++ {
			if s[at]>>6 != 0b10 { // not a continuation byte: a character starts here
				cut = at
				break
			}
		}
		s = s[:cut]
	}
	low, high := 0, 0
	for _, b := range s {
		switch {
		case b < 9 || (13 < b && b < 32):
			low++
		case b > 126:
			high++
		}
	}
	ascii := len(s) - low - high
	if float64(ascii)/float64(len(s)) > 0.7 {
		return false
	}
	if float64(ascii+high)/float64(len(s)) > 0.95 && utf8.Valid(s) {
		return false
	}
	return true
}
