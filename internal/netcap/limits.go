package netcap

import (
	"fmt"
	"strings"
)

// ── WHAT A HOST THE PHONE REACHES, OR AN APP ON THE PHONE, CAN MAKE A CAPTURE KEEP OR SPEND (ANOTHER_ACCOUNT_AND_LIMITS) ──
//
// Each number is chosen so a real run never meets it, and a run that does is told in words: the phone in its answer, the
// record the farm pulls, and the terminal.
const (
	// MaxResponseHeaderBytes bounds the headers of one answer the capture proxy reads from a host: Chrome's own bound,
	// past which a browser refuses the answer (ERR_RESPONSE_HEADERS_TOO_BIG), so no site that works in a browser sends
	// more. Go's default was 10 MiB, each masked and encoded twice.
	MaxResponseHeaderBytes = 256 << 10
	// MaxRequestHeaderBytes bounds the headers of one request the phone sends the capture proxy: the same 256 KiB, where
	// the web servers a request is bound for refuse a few dozen KiB (nginx's 32 KiB, Node's 16 KiB).
	MaxRequestHeaderBytes = 256 << 10
	// MaxHeaderValueBytes is the longest header value the capture masks and keeps (cut to MaxHeaderChars after masking).
	// A cookie is at most 4 KiB and a token a few; one longer than this is left out whole rather than cut and then masked,
	// which could keep part of a credential.
	MaxHeaderValueBytes = 64 << 10
	// MaxRememberedHosts bounds the hosts a capture remembers as refusing its certificate (passed through afterwards, and
	// recorded once each): an app reaches a few dozen hosts, and the capture's other bounds are 4096 (its leaf cache, the
	// controller's said-once set).
	MaxRememberedHosts = 4096
	// maxAuthorityChars is the longest host:port a capture remembers: a host name is at most 253 characters.
	maxAuthorityChars = 260
)

// HeadersTooLarge is what the phone and the record are told when a host's answer has more headers than MaxResponseHeaderBytes.
func HeadersTooLarge() string {
	return fmt.Sprintf("the host answered with more than %d KiB of headers, more than a browser accepts, so the capture proxy did not read it or pass it on", MaxResponseHeaderBytes>>10)
}

// HeaderLeftOut is a header value past MaxHeaderValueBytes, in the record.
func HeaderLeftOut(size int) string {
	return fmt.Sprintf("(left out: %d bytes, longer than any header this agent records (%d KiB), so it was not masked and kept)", size, MaxHeaderValueBytes>>10)
}

// RememberedEnough is said, once, when a capture stops remembering hosts that refuse its certificate.
func RememberedEnough(bound int) string {
	return fmt.Sprintf("the phone refused the capture's certificate on more than %d hosts, more than any app reaches; later ones are passed through no faster and not recorded one by one", bound)
}

func headersTooLarge(err error) bool {
	return err != nil && strings.Contains(err.Error(), "response headers exceeded")
}

// unlockKeepingBound gives the proxy's lock back, after forgetting hostPort again when it is past the bound — too many
// hosts, or a name longer than a host's can be. Called with mu held, right after hostPort was stored.
func (p *Proxy) unlockKeepingBound(hostPort string) {
	bound, heard := p.opts.Recording.remembering()
	if len(hostPort) > maxAuthorityChars || len(p.passThrough) > bound {
		delete(p.passThrough, hostPort)
		if len(p.passThrough) >= bound && !p.saidBound && heard != nil {
			p.saidBound = true
			defer heard(RememberedEnough(bound))
		}
	}
	p.mu.Unlock()
}
