package builds

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// fetchClient is client with no redirect followed, or the farm's transport rule with tlsConfig when client is nil.
func fetchClient(client *http.Client, tlsConfig *tls.Config) *http.Client {
	var c http.Client
	if client != nil {
		c = *client
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = farm.Proxy
		transport.TLSClientConfig = tlsConfig
		c.Transport = transport
	}
	// NO REDIRECT IS FOLLOWED, whoever built the client: the farm's build route and its front door never redirect, so a
	// redirect is an address nobody checked. The answer is read as a refusal (Fetch).
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Timeout = 0 // bounded by FetchWithin, through the request's context
	return &c
}

// Fetch fetches the build at address into the shelf and counts it, and answers the file's path — or "" and the sentence the
// build is refused with. farmURL is the farm this agent was started with (--farm).
//
// In order, and nothing is written before the first four pass:
//   - the address is an .ipa or an .apk, https, on the farm's own host and port (the allow-list's rule, held again here);
//   - one GET, with no credential (a build's address carries its own grant), over the farm's transport rule; no redirect;
//   - the farm answers 200, and its answer does not say the file is a zip archive or name it a .zip or an .apks;
//   - the size it states, or the cap when it states none, fits under the cap and leaves KeepFreeBytes of disk free;
//   - the bytes are written to a file of the agent's own, readable by this account only, and stopped one byte past the cap;
//   - the file is counted (Count). Only then does it take its name, and become a file the shelf hands over.
func (s *Shelf) Fetch(ctx context.Context, address, farmURL string) (string, string) {
	kind, refusal := allowed.BuildKind(address)
	if refusal != "" {
		return "", refusal
	}
	notOn := func(reason string) (string, string) { return "", allowed.NotHandedOn(address, reason) }
	if !allowed.OnTheFarm(address, farmURL) || farm.ValidateBaseURL(farmURL) != nil {
		return notOn("it is not an https address on the farm's own host")
	}

	ctx, cancel := context.WithTimeout(ctx, FetchWithin)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return notOn("its address could not be read")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return notOn("this agent could not fetch it (" + shortly(err) + ")")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return notOn(fmt.Sprintf("the farm answered its address with a redirect (HTTP %d), which this agent does not follow — "+
			"a redirect is an address nobody checked", response.StatusCode))
	}
	if response.StatusCode != http.StatusOK {
		return notOn(fmt.Sprintf("the farm answered its address with HTTP %d", response.StatusCode))
	}
	if says := answerSays(response.Header); says != "" {
		return notOn("the farm's answer says it is " + says + " — Appium unpacks one on this computer, and no phone this " +
			"agent drives installs one")
	}
	if response.ContentLength > s.maxBytes {
		return notOn("it is larger than " + sizeInWords(s.maxBytes) + ", the most a build may be")
	}
	room := s.maxBytes
	if response.ContentLength >= 0 {
		room = response.ContentLength
	}
	release, why := s.reserve(room)
	if why != "" {
		return notOn(why)
	}
	defer release()

	name, err := newName(kind)
	if err != nil {
		return notOn("this agent could not name a file for it")
	}
	final := filepath.Join(s.dir, name)
	part := final + ".part"
	file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return notOn("this agent could not write it to its own folder")
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, s.maxBytes+1))
	closeErr := file.Close()
	fail := func(reason string) (string, string) {
		os.Remove(part)
		return notOn(reason)
	}
	switch {
	case written > s.maxBytes:
		return fail("it is larger than " + sizeInWords(s.maxBytes) + ", the most a build may be")
	case copyErr != nil:
		return fail("the farm's answer ended before the build did (" + shortly(copyErr) + ")")
	case closeErr != nil:
		return fail("this agent could not write it to its own folder")
	}
	if fault := Count(part, kind); fault != Counted {
		return fail(fault.Sentence())
	}
	s.mu.Lock()
	s.wrote[final] = true
	s.mu.Unlock()
	if err := os.Rename(part, final); err != nil {
		s.remove([]string{final})
		return fail("this agent could not write it to its own folder")
	}
	return final, ""
}

// answerSays is what the farm's answer says the file is, when that is a zip archive, a .zip or an .apks; "" otherwise.
func answerSays(header http.Header) string {
	if media, _, err := mime.ParseMediaType(header.Get("Content-Type")); err == nil && strings.Contains(strings.ToLower(media), "zip") {
		return "a zip archive (" + farm.SafeForTerminal(media, "") + ")"
	}
	if _, params, err := mime.ParseMediaType(header.Get("Content-Disposition")); err == nil {
		name := strings.ToLower(params["filename"])
		for _, ext := range []string{".zip", ".apks"} {
			if strings.HasSuffix(name, ext) {
				return "a " + ext
			}
		}
	}
	return ""
}

// shortly is what went wrong, without the address it went wrong at (which the sentence already names).
func shortly(err error) string {
	var u *url.Error
	if errors.As(err, &u) {
		err = u.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "it took longer than " + FetchWithin.String()
	}
	return farm.SafeForTerminal(err.Error(), "")
}
