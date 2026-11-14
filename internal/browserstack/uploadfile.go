package browserstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── THE BUILD THAT NEVER LEAVES THE BUILDING ───────────────────────────────────────────────────────────────
//
// UploadByURL hands BrowserStack an address and THEY fetch it. That works for a published sample and is useless
// for the case this agent exists for: a pre-release build behind a company's NAT is not reachable from a
// vendor's data centre, and nobody publishes an unreleased app to the open web so that it can be tested before
// release. The agent is already INSIDE that boundary — that is the whole reason it runs on the customer's
// machine — and for the one operation that needed to be inside, it was made to act like an outsider.
//
// Everything below sends the bytes FROM HERE.

// BuildFormats are the shapes BrowserStack's App Automate upload accepts, per their documentation.
//
// ⚠️ THIS LIST IS A COPY OF SOMEBODY ELSE'S FACT and can drift from it — see InspectBuild for why it is checked
// here anyway, and for the sentence that makes a stale refusal say so instead of blaming the person's file.
var BuildFormats = []string{".apk", ".aab", ".xapk", ".ipa"}

// MaxBuildBytes is BrowserStack's documented per-app upload limit (1 GB). Same drift caveat as BuildFormats.
const MaxBuildBytes int64 = 1 << 30

// UploadResponseHeaderTimeout bounds THE SERVER'S THINKING TIME, not the transfer.
//
// Go starts this clock after the request body has been fully written, so a 900 MB build crawling up a domestic
// uplink does not spend it — only a server that has taken the whole build and then gone quiet does. That
// distinction is the entire reason this is not an http.Client.Timeout: an overall client deadline bounds the
// transfer too, and would punish exactly the case this file exists to serve.
const UploadResponseHeaderTimeout = 10 * time.Minute

// UploadCeiling is the outer bound on one upload, applied as a context deadline rather than a client timeout.
//
// It exists so a wedged connection ends rather than hanging until somebody notices, and it is deliberately far
// larger than any reasonable upload: BrowserStack's own limit is 1 GB, and 1 GB at 150 KB/s — a bad hotel
// wifi — is about one hour fifty. Two hours is that with room, and a person who genuinely needs longer has a
// network problem this agent should not paper over.
const UploadCeiling = 2 * time.Hour

// The four ways a local path is not a build. Each is its own error because the REMEDIES DIFFER, and a caller
// that wrapped them into one sentence would be the generic failure this whole file was written to avoid.
var (
	// ErrBuildNotFound — nothing at that path. Remedy: check the path.
	ErrBuildNotFound = errors.New("browserstack: no file at that path")
	// ErrBuildUnreadable — something is there and this process may not read it. Remedy: check the permissions.
	// A DIFFERENT SENTENCE FROM THE ONE ABOVE ON PURPOSE: "no such file" sends a person to look for a typo, and
	// they will not find one.
	ErrBuildUnreadable = errors.New("browserstack: that file cannot be read")
	// ErrBuildIsDirectory — the tab-completion mistake. Remedy: name the file inside it.
	ErrBuildIsDirectory = errors.New("browserstack: that is a directory, not a build")
	// ErrBuildShape — an extension BrowserStack does not accept.
	ErrBuildShape = errors.New("browserstack: that is not a shape BrowserStack accepts")
	// ErrBuildTooLarge — over their documented limit.
	ErrBuildTooLarge = errors.New("browserstack: that build is larger than BrowserStack accepts")
)

// InspectBuild answers whether a local path is a build worth spending an upload on, and returns its size.
//
// ⚠️ WHICH OF THESE CHECKS BELONG HERE, ONE LINE EACH — because a check that duplicates the server's and can
// drift from it is worse than no check, and two of these four do duplicate one.
//
//   - NOT FOUND / NOT READABLE — belongs here beyond argument: the server never sees a path on this machine, so
//     there is no remote check to duplicate and nothing that can drift.
//   - A DIRECTORY — the same: purely local, unduplicated, and the mistake a person makes with tab-completion.
//   - THE EXTENSION — DUPLICATES BrowserStack's own rule and CAN drift from it. Kept anyway, because being wrong
//     costs a refusal a person can read in one second while being right saves an entire upload; and the drift is
//     paid for in the sentence, which says whose list this is so a stale refusal blames us rather than the file.
//   - THE SIZE — duplicates their limit and can drift the same way, and is the one that saves the most: a
//     gigabyte pushed up a domestic uplink to be told "too large" at the far end is twenty minutes for nothing.
//
// Neither of the last two has an override flag. A person whose valid build we wrongly refuse is far rarer than a
// person who tab-completed the wrong file, and a flag that turns a check off gets typed by the second person.
func InspectBuild(path string) (int64, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return 0, fmt.Errorf("%w: %s", ErrBuildNotFound, path)
	case errors.Is(err, os.ErrPermission):
		return 0, fmt.Errorf("%w: %s — check the permissions on it and on the directories above it", ErrBuildUnreadable, path)
	case err != nil:
		return 0, fmt.Errorf("%w: %s: %v", ErrBuildUnreadable, path, err)
	case info.IsDir():
		return 0, fmt.Errorf("%w: %s — name the build inside it", ErrBuildIsDirectory, path)
	}

	// READABILITY IS PROVED BY OPENING IT, not inferred from the mode bits. A file can be stat-able and
	// unopenable, and finding that out after the upload has started is finding it out in the worst place.
	handle, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("%w: %s — check the permissions on it and on the directories above it", ErrBuildUnreadable, path)
	}
	_ = handle.Close()

	if !acceptedFormat(path) {
		return 0, fmt.Errorf("%w: %s accepts %s. If BrowserStack has started accepting %q since this agent was "+
			"built, this refusal is ours and not theirs",
			ErrBuildShape, filepath.Base(path), strings.Join(BuildFormats, ", "), strings.ToLower(filepath.Ext(path)))
	}
	if info.Size() > MaxBuildBytes {
		return 0, fmt.Errorf("%w: %s is %s and their documented limit is %s. Refused here rather than after the "+
			"upload, which is the point of checking",
			ErrBuildTooLarge, filepath.Base(path), HumanBytes(info.Size()), HumanBytes(MaxBuildBytes))
	}
	return info.Size(), nil
}

func acceptedFormat(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, accepted := range BuildFormats {
		if ext == accepted {
			return true
		}
	}
	return false
}

// HumanBytes is the size a person reads. Exported because the verb narrates the same number before the wait.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// FileUploadClient is the HTTP client a FILE upload wants, and it is deliberately not the one the two short-form
// calls use. See UploadResponseHeaderTimeout: no overall Client.Timeout, because that clock runs during the
// transfer and a slow link is the case this exists to serve, not a fault to abort.
func FileUploadClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = UploadResponseHeaderTimeout
	return &http.Client{Transport: transport}
}

// UploadFile sends the bytes of a build ON THIS MACHINE to BrowserStack and returns the bs:// id.
//
// ⚠️ THE BYTES STREAM AND THAT IS A REQUIREMENT, NOT A REFINEMENT. UploadByURL builds its body in a
// bytes.Buffer, which is right for two short form fields and wrong for a file: a 500 MB build would sit in
// memory in full, and twice over once the request copied it. Here the multipart writer runs on the writing end
// of an io.Pipe and the request reads the other end, so the file moves through in the copy buffer's worth at a
// time however large it is. Nothing in this function reads the build into memory, and a seal asserts that as a
// property of the source rather than of a run.
//
// THE KEY GOES INTO AN Authorization HEADER THIS FUNCTION BUILDS, exactly as its sibling does, and nowhere else.
func (u Uploader) UploadFile(ctx context.Context, creds Credentials, path, customID string) (App, error) {
	if !creds.Configured() {
		return App{}, errors.New("browserstack: an upload needs both a user and a key")
	}
	if strings.TrimSpace(path) == "" {
		return App{}, errors.New("browserstack: an upload needs the path of a build to send")
	}
	if _, err := InspectBuild(path); err != nil {
		return App{}, err
	}

	file, err := os.Open(path)
	if err != nil {
		return App{}, fmt.Errorf("%w: %s", ErrBuildUnreadable, path)
	}
	defer file.Close()

	// THE WRITING END RUNS IN ITS OWN GOROUTINE and closes the pipe with whatever error it hit, so a failure
	// mid-file surfaces as the request's own error rather than as a truncated upload the far end accepts.
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		writer.CloseWithError(func() error {
			part, err := form.CreateFormFile("file", filepath.Base(path))
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, file); err != nil {
				return err
			}
			if customID != "" {
				if err := form.WriteField("custom_id", customID); err != nil {
					return err
				}
			}
			return form.Close()
		}())
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint(), reader)
	if err != nil {
		_ = reader.CloseWithError(err)
		return App{}, fmt.Errorf("browserstack: building the upload request: %w", err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.SetBasicAuth(creds.User, creds.Key)

	response, err := u.client().Do(request)
	if err != nil {
		return App{}, fmt.Errorf("browserstack: the upload did not complete, so nothing was added to your account: %w", err)
	}
	defer response.Body.Close()
	return decodeUpload(response)
}

// decodeUpload is the response half, SHARED with UploadByURL so the two paths cannot disagree about what counts
// as success. A 200 is not a verdict here either: the app id is.
func decodeUpload(response *http.Response) (App, error) {
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return App{}, fmt.Errorf("browserstack: reading the upload response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return App{}, errors.New(FailureRejected.Describe())
	}

	var decoded struct {
		AppURL   string `json:"app_url"`
		CustomID string `json:"custom_id"`
		Error    string `json:"error"`
		Message  string `json:"message"`
	}
	_ = json.Unmarshal(payload, &decoded)

	if decoded.AppURL == "" {
		said := firstNonBlank(decoded.Error, decoded.Message)
		if said == "" {
			said = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return App{}, fmt.Errorf("%w: %s", ErrNoAppURL, said)
	}
	return App{URL: decoded.AppURL, CustomID: decoded.CustomID}, nil
}
