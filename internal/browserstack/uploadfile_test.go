package browserstack_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
)

// ── A_BUILD_THAT_NEVER_LEAVES_THE_BUILDING ─────────────────────────────────────────────────────────────────
//
// The product had no way to obtain a bs:// id, and the one upload it did have asked the WRONG MACHINE to fetch:
// UploadByURL posts an address that BrowserStack resolve from their own network, which is exactly what a
// pre-release build behind a customer's NAT is not. These seals hold the other direction — the bytes leave this
// machine — and the four local refusals that stop a mistake before it costs an upload.

// filePart is what actually arrived in one multipart part.
type filePart struct {
	FieldName string
	FileName  string
	Bytes     []byte
}

// fileStub records the whole shape of what arrived: every field, every file part, and — the property Item 3
// turns on — whether the request declared its length in advance.
type fileStub struct {
	fields        map[string]string
	files         []filePart
	contentLength int64
	transferEnc   []string
	authUser      string
	authKey       string
	status        int
	reply         string
}

func (f *fileStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	f.fields = map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.authUser, f.authKey, _ = r.BasicAuth()
		f.contentLength = r.ContentLength
		f.transferEnc = append([]string(nil), r.TransferEncoding...)

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				value, _ := io.ReadAll(part)
				if part.FileName() != "" {
					f.files = append(f.files, filePart{FieldName: part.FormName(), FileName: part.FileName(), Bytes: value})
					continue
				}
				f.fields[part.FormName()] = string(value)
			}
		}
		status := f.status
		if status == 0 {
			status = http.StatusOK
		}
		reply := f.reply
		if reply == "" {
			reply = `{"app_url":"bs://a1b2c3d4e5f6"}`
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server
}

func buildFile(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func goodCreds() browserstack.Credentials {
	return browserstack.Credentials{User: "acme-user", Key: "acme-key-must-not-appear"}
}

// ── ITEM 1 / INJECTION 1: the bytes go, and the path does not ──────────────────────────────────────────────

func TestAFileUploadSendsTheBYTESAndNeverThePathAsAUrl(t *testing.T) {
	// THE WHOLE SLICE, INVERTED, IS THE INJECTION THIS CATCHES: posting the local path in the `url` field would
	// "succeed" against a stub and ask BrowserStack to fetch a path on somebody's laptop. "An upload happened" is
	// not the property — WHICH MACHINE READS THE BUILD is.
	path := buildFile(t, "app-release.apk", 4096)
	stub := &fileStub{}
	server := stub.server(t)

	app, err := browserstack.Uploader{BaseURL: server.URL}.UploadFile(context.Background(), goodCreds(), path, "")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if len(stub.files) != 1 {
		t.Fatalf("want exactly one file part, got %d", len(stub.files))
	}
	part := stub.files[0]
	if part.FieldName != "file" {
		t.Fatalf("BrowserStack's app upload reads the `file` field; got %q", part.FieldName)
	}
	if part.FileName != "app-release.apk" {
		t.Fatalf("the part must carry the build's own name, got %q", part.FileName)
	}
	if len(part.Bytes) != 4096 {
		t.Fatalf("the build's bytes must arrive whole: sent 4096, server saw %d", len(part.Bytes))
	}
	// …and NOTHING asked BrowserStack to go and fetch anything.
	if _, asked := stub.fields["url"]; asked {
		t.Fatalf("a --file upload sent a `url` field (%q) — that asks BROWSERSTACK to fetch, which is the "+
			"assumption this verb exists to remove", stub.fields["url"])
	}
	for name, value := range stub.fields {
		if strings.Contains(value, path) {
			t.Fatalf("the local path travelled in the %q field — it names a place only this machine can reach", name)
		}
	}
	if app.URL != "bs://a1b2c3d4e5f6" {
		t.Fatalf("the id must come back, got %q", app.URL)
	}
	if stub.authUser != "acme-user" || stub.authKey != "acme-key-must-not-appear" {
		t.Fatal("the upload must be basic-authenticated with the account's own credentials")
	}
}

func TestACustomIdRidesTheFileUploadAndIsOmittedWhenEmpty(t *testing.T) {
	path := buildFile(t, "app.apk", 64)

	with := &fileStub{}
	if _, err := (browserstack.Uploader{BaseURL: with.server(t).URL}).
		UploadFile(context.Background(), goodCreds(), path, "acme-nightly"); err != nil {
		t.Fatal(err)
	}
	if with.fields["custom_id"] != "acme-nightly" {
		t.Fatalf("custom_id must ride the upload, got %q", with.fields["custom_id"])
	}

	without := &fileStub{}
	if _, err := (browserstack.Uploader{BaseURL: without.server(t).URL}).
		UploadFile(context.Background(), goodCreds(), path, ""); err != nil {
		t.Fatal(err)
	}
	if _, present := without.fields["custom_id"]; present {
		t.Fatal("an empty custom id must be omitted, not sent as an empty field")
	}
}

// ── ITEM 3 / INJECTION 3: the bytes stream ─────────────────────────────────────────────────────────────────

func TestTheRequestDoesNotKnowItsOwnLengthWhichIsWhatProvesItIsNotHoldingTheFile(t *testing.T) {
	// ⚠️ WHY THIS SEAL AND NOT A MEMORY MEASUREMENT. A test that uploads 500 MB is a test nobody keeps, and a
	// test that watches the heap is a test that fails on a busy machine for reasons that are not the code's. This
	// asserts a PROTOCOL CONSEQUENCE that only holds when the client did not have the bytes: Go sets
	// Content-Length from a body whose size it can see (bytes.Buffer, bytes.Reader, strings.Reader) and falls
	// back to chunked transfer encoding for a body it cannot measure — which an io.Pipe is. So a request that
	// arrives chunked, with no declared length, is a request whose body did not exist yet when it was sent.
	//
	// It is honest because it cannot be satisfied by accident: to buffer the file and still arrive chunked you
	// would have to wrap the buffer specifically to hide its length, which is what the structural seal below
	// catches.
	path := buildFile(t, "big.apk", 3<<20)
	stub := &fileStub{}
	server := stub.server(t)

	if _, err := (browserstack.Uploader{BaseURL: server.URL}).
		UploadFile(context.Background(), goodCreds(), path, ""); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if stub.contentLength != -1 {
		t.Fatalf("the request declared Content-Length %d, so the client knew the whole size before sending — it "+
			"was holding the build in memory. A streamed body has no length to declare.", stub.contentLength)
	}
	chunked := false
	for _, enc := range stub.transferEnc {
		if enc == "chunked" {
			chunked = true
		}
	}
	if !chunked {
		t.Fatalf("the request was not chunked (%v) — a body Go can measure is a body the client already had", stub.transferEnc)
	}
	// …and the bytes still arrive intact, which is the half a length check alone would not prove.
	if len(stub.files) != 1 || len(stub.files[0].Bytes) != 3<<20 {
		t.Fatalf("streaming must not lose bytes: server saw %d parts", len(stub.files))
	}
}

// declBody returns the source text of one top-level func or method declaration, sliced by the DECLARATION
// rather than by a name match — a file-wide search for a call finds the call site of a sibling and reports it
// as this function's.
func declBody(t *testing.T, path, receiver, name string) string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		if receiver != "" {
			if fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			ident, isIdent := fn.Recv.List[0].Type.(*ast.Ident)
			if !isIdent || ident.Name != receiver {
				continue
			}
		}
		start := fset.Position(fn.Pos()).Offset
		end := fset.Position(fn.End()).Offset
		return string(source[start:end])
	}
	t.Fatalf("declaration %s.%s not found in %s — the seal is reading the wrong file", receiver, name, path)
	return ""
}

func TestNothingOnTheFileUploadPathReadsTheBuildIntoMemory(t *testing.T) {
	// THE STRUCTURAL HALF, and it catches what the protocol seal cannot: a buffering implementation deliberately
	// wrapped to hide its length. The property is stated as source rather than behaviour because "how much memory
	// did that use" has no honest assertion at this size.
	body := declBody(t, filepath.Join("uploadfile.go"), "Uploader", "UploadFile")

	// INSTRUMENT GUARD: the slice must really be the function, not an empty string that passes everything.
	for _, expected := range []string{"io.Pipe()", "io.Copy(part, file)", "CreateFormFile"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("instrument guard: the sliced declaration does not contain %q — the seal is reading the wrong "+
				"thing and its green means nothing", expected)
		}
	}

	for _, forbidden := range []string{"io.ReadAll", "os.ReadFile", "bytes.Buffer", "bytes.NewReader", "ioutil.ReadFile"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("UploadFile names %s — a 500 MB build read into memory is held in full, and twice over once "+
				"the request copies it. The body must be produced lazily.", forbidden)
		}
	}
}

func TestAFileUploadDoesNotCarryTheClientDeadlineThatWouldPunishASlowLink(t *testing.T) {
	// The bound is on the SERVER'S thinking time, not on the transfer: Go starts ResponseHeaderTimeout after the
	// request body is fully written. An http.Client.Timeout would run during the upload and abort exactly the
	// case this verb exists to serve — a large build on a slow uplink.
	client := browserstack.FileUploadClient()
	if client.Timeout != 0 {
		t.Fatalf("the file-upload client set an overall timeout of %v — that clock runs during the transfer", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("want an *http.Transport to carry the response-header bound, got %T", client.Transport)
	}
	if transport.ResponseHeaderTimeout != browserstack.UploadResponseHeaderTimeout {
		t.Fatalf("the server's thinking time must still be bounded, got %v", transport.ResponseHeaderTimeout)
	}
}

// ── ITEM 2: the four local refusals, each with its own sentence ────────────────────────────────────────────

func TestEachWayAPathIsNotABuildHasItsOwnSentence(t *testing.T) {
	dir := t.TempDir()
	tooBig := filepath.Join(dir, "huge.apk")
	// A SPARSE FILE, so the size check is exercised without writing a gigabyte: Truncate sets the length and the
	// filesystem allocates nothing. os.Stat reports the full size, which is the only thing InspectBuild reads.
	handle, err := os.Create(tooBig)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Truncate(browserstack.MaxBuildBytes + 1); err != nil {
		t.Skipf("this filesystem will not make a sparse file: %v", err)
	}
	_ = handle.Close()

	for _, c := range []struct {
		name    string
		path    string
		want    error
		mustSay []string
	}{
		{
			name: "not there at all", path: filepath.Join(dir, "nope.apk"), want: browserstack.ErrBuildNotFound,
			mustSay: []string{"nope.apk"},
		},
		{
			name: "a directory", path: dir, want: browserstack.ErrBuildIsDirectory,
			mustSay: []string{"directory", "inside it"},
		},
		{
			name: "a shape they do not take", path: buildFile(t, "notes.txt", 10), want: browserstack.ErrBuildShape,
			// THE DRIFT IS PAID FOR IN THE SENTENCE: this check copies BrowserStack's own list, so a stale
			// refusal must say the list is ours rather than leave a person staring at a valid file.
			mustSay: []string{".apk", ".ipa", "this refusal is ours and not theirs"},
		},
		{
			name: "larger than they take", path: tooBig, want: browserstack.ErrBuildTooLarge,
			mustSay: []string{"1.0 GB", "rather than after the"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := browserstack.InspectBuild(c.path)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			for _, phrase := range c.mustSay {
				if !strings.Contains(err.Error(), phrase) {
					t.Fatalf("the sentence must contain %q — four failures wrapped into one generic error is the "+
						"thing this was written to avoid. Got: %s", phrase, err)
				}
			}
		})
	}
}

func TestAnUnreadableBuildSaysSoRatherThanSayingItIsMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything, so this machine cannot produce an unreadable file")
	}
	path := buildFile(t, "locked.apk", 32)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot remove read permission here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := browserstack.InspectBuild(path)
	if !errors.Is(err, browserstack.ErrBuildUnreadable) {
		t.Fatalf("want the unreadable error, got %v", err)
	}
	// THE REMEDIES DIFFER, WHICH IS WHY THE SENTENCES DO. "No such file" sends a person hunting for a typo they
	// will not find.
	if errors.Is(err, browserstack.ErrBuildNotFound) {
		t.Fatal("an unreadable file must not be reported as a missing one")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("the sentence must name the remedy, got: %v", err)
	}
}

func TestTheUploadRefusesABadPathBeforeItOpensAConnection(t *testing.T) {
	// REFUSING AFTER THE TRANSFER IS NOT REFUSING. The stub records whether it was reached at all.
	stub := &fileStub{}
	server := stub.server(t)
	reached := false
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		http.Redirect(w, r, server.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(guard.Close)

	_, err := browserstack.Uploader{BaseURL: guard.URL}.
		UploadFile(context.Background(), goodCreds(), buildFile(t, "notes.txt", 8), "")
	if !errors.Is(err, browserstack.ErrBuildShape) {
		t.Fatalf("want the shape refusal, got %v", err)
	}
	if reached {
		t.Fatal("the upload contacted BrowserStack before deciding the file was not a build — the whole point of " +
			"checking here is that a person does not spend the transfer to be told")
	}
}

func TestAFileUploadRefusesWithoutBothCredentialHalvesOrWithoutAPath(t *testing.T) {
	for _, c := range []struct {
		name  string
		creds browserstack.Credentials
		path  string
	}{
		{"no key", browserstack.Credentials{User: "u"}, "x.apk"},
		{"no user", browserstack.Credentials{Key: "k"}, "x.apk"},
		{"no path", goodCreds(), "   "},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := (browserstack.Uploader{BaseURL: "http://127.0.0.1:1"}).
				UploadFile(context.Background(), c.creds, c.path, ""); err == nil {
				t.Fatal("want a refusal before anything is attempted")
			}
		})
	}
}

// ── the response half is SHARED, so both paths agree about what "worked" means ─────────────────────────────

func TestAFileUploadTreatsATwoHundredWithNoAppIdAsAFailureLikeItsSibling(t *testing.T) {
	stub := &fileStub{reply: `{"error":"Access denied for that app"}`}
	_, err := browserstack.Uploader{BaseURL: stub.server(t).URL}.
		UploadFile(context.Background(), goodCreds(), buildFile(t, "app.apk", 16), "")

	if !errors.Is(err, browserstack.ErrNoAppURL) {
		t.Fatalf("a 200 is not a verdict — the app id is; got %v", err)
	}
	if !strings.Contains(err.Error(), "Access denied for that app") {
		t.Fatalf("the account's own words must survive, got %v", err)
	}
	if strings.Contains(err.Error(), "acme-key-must-not-appear") {
		t.Fatal("the access key reached an error string")
	}
}

func TestARejectedFileUploadIsNamedAsACredentialProblem(t *testing.T) {
	stub := &fileStub{status: http.StatusUnauthorized, reply: `{"error":"Invalid username or password"}`}
	_, err := browserstack.Uploader{BaseURL: stub.server(t).URL}.
		UploadFile(context.Background(), goodCreds(), buildFile(t, "app.apk", 16), "")

	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "credential") {
		t.Fatalf("a 401 must be named as a credential problem, got %v", err)
	}
	if strings.Contains(err.Error(), "acme-key-must-not-appear") {
		t.Fatal("the access key reached an error string")
	}
}
