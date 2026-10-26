package farm

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ValidateBaseURL refuses a farm URL whose transport would expose the workspace token — BEFORE any
// request is made, so a customer never discovers this after the token has already crossed the wire
// once. A CREDENTIAL IS ONLY AS PRIVATE AS ITS TRANSPORT: X-Agent-Token over plain http is readable
// by every hop of whatever network the laptop is on — a café, a hotel, a corporate span port.
//
// THE EXCEPTION, EXPLICIT AND NARROW: http:// to a LOOPBACK host (localhost, ::1, 127.0.0.0/8) is
// allowed, because a loopback packet never leaves this machine — there is no wire for the token to
// cross. That is the whole argument; anyone widening this exception (a LAN address "because it's our
// LAN", a .internal name "because it's behind the VPN") must meet it: those packets DO leave the
// machine, and the token travels with them.
func ValidateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("the farm URL %q is not a valid URL: %v", raw, err)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		// EXACTLY "localhost", NOT A SPELLING OF IT (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST). Go's proxy rule lets only
		// the lower-case name and loopback addresses past HTTP_PROXY; LOCALHOST was let through here and then handed to the
		// customer's proxy, token and all (measured on Go 1.27.0). One reading of "loopback", and it is the stricter one.
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
			return fmt.Errorf("the farm URL %q is plain http to a name written other than localhost — this agent sends plain http only to exactly localhost or a loopback address, because any other spelling can be sent through a proxy with your workspace token on it: write it as http://localhost (lower case, no trailing dot) or use https://", raw)
		}
		return fmt.Errorf("the farm URL %q is plain http, which would send your workspace token in cleartext across the network — use https:// (http is allowed only for loopback hosts like 127.0.0.1, where the token never leaves this machine)", raw)
	default:
		return fmt.Errorf("the farm URL %q must be https:// (or http:// to a loopback host for local development)", raw)
	}
}

// httpClient returns the caller's client with THE REDIRECT POLICY FORCED — a shallow copy, so the
// caller's Timeout and Transport survive and the policy cannot be lost by constructing a client
// without it.
//
// WHY REFUSE REDIRECTS ENTIRELY: this API does not redirect, and Go's default policy follows up to
// ten — stripping only the Authorization and Cookie families on a cross-host hop. A CUSTOM header
// like X-Agent-Token is preserved, so an https:// farm answering `302 Location: http://elsewhere/`
// would hand the workspace token to another host, in cleartext, and report success. A guard
// evaluated against the URL you typed is not a guard evaluated against the request that carries the
// credential — the transport that matters is the one on the LAST hop, and the only redirect this
// API could send is one somebody else injected.
func (c *Client) httpClient() *http.Client {
	client := *c.HTTP
	// NO PROXY FOR PLAIN HTTP: a caller that names no transport gets Go's default one with the farm's proxy rule (Proxy).
	if client.Transport == nil {
		client.Transport = farmTransport
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return fmt.Errorf("the farm answered with a redirect to %s — this API never redirects, so the request was refused rather than carrying the token to a hop nobody checked", req.URL)
	}
	return &client
}

// Proxy is the proxy rule for both of the farm's channels (this client and the tunnel's upgrade): an https or wss request
// follows this machine's proxy settings, as it always did; a plain one never goes through a proxy. ValidateBaseURL lets
// plain http through only to this machine's loopback, on the ground that the token never leaves the machine — a proxy
// would be the one way it could (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST).
func Proxy(request *http.Request) (*url.URL, error) {
	if request.URL.Scheme != "https" && request.URL.Scheme != "wss" {
		return nil, nil
	}
	return http.ProxyFromEnvironment(request)
}

// farmTransport is Go's default transport with Proxy as its proxy rule — one, so its connections are reused across calls.
var farmTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = Proxy
	return transport
}()
