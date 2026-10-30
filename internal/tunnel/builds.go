package tunnel

import (
	"context"
	"fmt"
	"net/http"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/builds"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// A BUILD THIS AGENT HAS COUNTED. Appium on this machine installs only a build this agent has fetched, measured and checked,
// and it gets that build as a file. A session's app, and every install inside a session, passes the allow-list as the farm
// sent it (an .ipa or an .apk, https, on the farm's own host); then the agent fetches it into its own folder and counts it
// (internal/builds), writes the request again with its own file in the build's place (allowed.HandOver), and judges that
// request once more, where the one path that may pass is the file it wrote for this request. Only then does Appium hear of
// it. Appium installs a local .ipa or .apk as it is, so it neither fetches, nor keeps, nor unpacks anything of its own.
//
// The file is held for the session it was fetched for, and removed when that session ends: its DELETE (forget), the
// release of a session nobody ended (releaseIfSilent), or the agent stopping (Run). A create that started no session leaves
// no file. A sweep when the tunnel is made removes what a crash left behind.

// BuildShelf is where this tunnel keeps the builds it hands Appium.
type BuildShelf struct {
	// In is the agent's config folder: the builds live in In/nth-agent/builds. "" = no folder: every build is refused, in
	// words, and every session without one still starts.
	In string
	// Client, FreeBytes and MaxBytes are TEST SEAMS (builds.Options): no flag and no environment variable reaches them, and
	// each zero value is the farm's transport rule with this tunnel's TLS, the file system's own answer, and the platform's
	// own cap on a build.
	Client    *http.Client
	FreeBytes func(dir string) (uint64, error)
	MaxBytes  int64
}

// shelfState is this tunnel's shelf, or why it has none.
type shelfState struct {
	shelf *builds.Shelf
	why   string
}

// openBuilds opens the shelf the config names, sweeping what a crash left in it, and answers the tunnel.
func (t *Tunnel) openBuilds() *Tunnel {
	if t.cfg.Builds.In == "" {
		t.shelf.why = "this agent has no folder of its own to fetch it into"
		return t
	}
	shelf, swept, err := builds.Open(t.cfg.Builds.In, builds.Options{Client: t.cfg.Builds.Client, TLS: t.cfg.TLSClientConfig,
		MaxBytes: t.cfg.Builds.MaxBytes, FreeBytes: t.cfg.Builds.FreeBytes})
	if err != nil {
		t.shelf.why = "this agent could not make its own folder for builds (" + farm.SafeForTerminal(err.Error(), "") + ")"
		t.say("this agent could not make its own folder for the builds it hands Appium, so every session that installs a " +
			"build will be refused until it can (" + farm.SafeForTerminal(err.Error(), "") + ")\n")
		return t
	}
	t.shelf.shelf = shelf
	if swept > 0 {
		t.say(fmt.Sprintf("removed %d build file(s) an earlier run of this agent left in %s\n", swept, shelf.Dir()))
	}
	return t
}

// handOverBuilds fetches and counts the build a request the allow-list let through names, and writes body again with the
// agent's own file in its place. It answers the files it wrote, or the sentence the request is refused with. A request that
// names no build is left as it came.
func (t *Tunnel) handOverBuilds(ctx context.Context, create bool, method, path string, body *[]byte) ([]string, string) {
	named := allowed.BuildsNamed(create, method, path, *body)
	if len(named) == 0 {
		return nil, ""
	}
	address := named[0] // the allow-list lets one build through, and no more
	if t.shelf.shelf == nil {
		return nil, allowed.NotHandedOn(address, t.shelf.why)
	}
	file, refusal := t.shelf.shelf.Fetch(ctx, address, t.cfg.FarmURL)
	if refusal != "" {
		return nil, refusal
	}
	fetched := []string{file}
	// THE ONE PATH THAT MAY PASS: the file written for this request, still the agent's. Nothing else on this computer.
	own := func(p string) bool { return p == file && t.shelf.shelf.Wrote(p) }
	rewritten, err := allowed.HandOver(create, path, *body, map[string]string{address: file})
	judged := ""
	switch {
	case err != nil:
		judged = allowed.NotHandedOn(address, "this agent could not write the request again with its own file in it")
	case create:
		judged = allowed.CreateHandedOver(rewritten, t.cfg.FarmURL, t.cfg.Phones, own)
	default:
		judged = allowed.InSessionHandedOver(method, path, rewritten, t.cfg.FarmURL, own)
	}
	if judged != "" {
		t.shelf.shelf.Forget(fetched)
		return nil, judged
	}
	if !create {
		t.holdBuilds(sessionIDInPath(path), fetched)
	}
	*body = rewritten
	return fetched, ""
}

// holdBuilds keeps files for as long as session lives.
func (t *Tunnel) holdBuilds(session string, files []string) {
	if t.shelf.shelf != nil && session != "" {
		t.shelf.shelf.Hold(session, files)
	}
}

// dropBuilds removes the files held for session, which has ended.
func (t *Tunnel) dropBuilds(session string) {
	if t.shelf.shelf != nil {
		t.shelf.shelf.Drop(session)
	}
}

// forgetBuilds removes each of files that no session holds: the request they were fetched for started none.
func (t *Tunnel) forgetBuilds(files []string) {
	if t.shelf.shelf != nil && len(files) > 0 {
		t.shelf.shelf.Forget(files)
	}
}

// removeAllBuilds removes every file the shelf holds: the agent is stopping.
func (t *Tunnel) removeAllBuilds() {
	if t.shelf.shelf != nil {
		t.shelf.shelf.RemoveAll()
	}
}
