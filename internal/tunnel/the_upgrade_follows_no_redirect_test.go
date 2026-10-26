package tunnel_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// THE UPGRADE CARRIES X-Agent-Token, and Go copies a custom header to wherever a redirect points — any host, any scheme.
// Before this slice the dial used the default client and followed a 3xx; the review found it (2026-10-03). The farm's
// tunnel never redirects, so a redirect is answered as the refusal it is, and the token goes nowhere else.
func TestTheUpgradeFollowsNoRedirectAndTheTokenGoesNowhereElse(t *testing.T) {
	var reached atomic.Int32
	var carried atomic.Value
	carried.Store("")
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		carried.Store(r.Header.Get("X-Agent-Token"))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer elsewhere.Close()
	farm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	defer farm.Close()

	var out syncWriter
	liveTunnel(t, farm.URL, &out, nil)
	waitFor(t, "a refused upgrade", func() bool { return strings.Contains(out.String(), "not connected yet") })
	time.Sleep(100 * time.Millisecond)
	if reached.Load() != 0 {
		t.Fatalf("the upgrade followed a redirect to %s and carried the token %q", elsewhere.URL, carried.Load())
	}
	if !strings.Contains(out.String(), "302") {
		t.Fatalf("the refusal names what the farm answered:\n%s", out.String())
	}
}
