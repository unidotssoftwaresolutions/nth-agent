package connect_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 5: "devices are NOT being offered" read as an alarm ───────────────────────
//
// Filmed on 2026-10-20 at the start of `connect --appium …`. main dials the drive tunnel from OnRegistered, so the
// registration's own report is always built with a tunnel that has not connected yet: every start-up with --appium said
// "devices are NOT being offered", with "what to do: nothing to do" under it, and seconds later that the drive path was
// ready. That is the order things start in, not a fault, and it may not read as one.

// startedLikeMain wires a driver the way main does: Appium answers, and the tunnel connects only once a registration has
// been accepted.
func startedLikeMain(cfg *connect.Config) {
	var dialled atomic.Bool
	cfg.Driver = &connect.Driver{
		AppiumURL:    "http://127.0.0.1:4723",
		Probe:        func(context.Context) error { return nil },
		TunnelUp:     dialled.Load,
		TunnelEverUp: dialled.Load,
	}
	cfg.OnRegistered = func() { dialled.Store(true) }
}

func TestAStartWithAppiumDoesNotSayDevicesAreNotOffered(t *testing.T) {
	_, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = driverOutcomes
	startedLikeMain(&cfg)
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	transcript := out.String()
	if strings.Contains(strings.ToLower(transcript), "not being offered") || strings.Contains(transcript, "nothing to do") {
		t.Fatalf("a start with --appium says its devices are not offered while the tunnel it dials after registering has "+
			"not opened yet — that is the order things start in, not a fault:\n%s", transcript)
	}
	if !strings.Contains(transcript, "has not connected yet") || !strings.Contains(transcript, "the drive path is ready") {
		t.Fatalf("the start still says the tunnel opens after registration, and then that the drive path is ready:\n%s", transcript)
	}
}

// allCaps is a word written in capitals to shout. URL is the one placeholder these sentences use.
var allCaps = regexp.MustCompile(`\b[A-Z]{2,}\b`)

// Where the devices really are not offered, the fact and the remedy stay, said plainly.
func TestDevicesNotOfferedIsSaidPlainly(t *testing.T) {
	cases := map[string]func(cfg *connect.Config){
		"started without --appium": func(cfg *connect.Config) { cfg.Driver = nil },
		"Appium not answering": func(cfg *connect.Config) {
			cfg.Driver = &connect.Driver{AppiumURL: "http://127.0.0.1:4723",
				Probe:    func(context.Context) error { return errors.New("connection refused") },
				TunnelUp: func() bool { return true }}
		},
		"the tunnel dropped": func(cfg *connect.Config) {
			cfg.Driver = &connect.Driver{AppiumURL: "http://127.0.0.1:4723", Probe: func(context.Context) error { return nil },
				TunnelUp: func() bool { return false }, TunnelEverUp: func() bool { return true }}
		},
	}
	for name, set := range cases {
		_, server := newStubFarm(t)
		var out strings.Builder
		cfg := config(server, &out, 1)
		cfg.Probes = driverOutcomes
		set(&cfg)
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("%s: run: %v", name, err)
		}
		lines := strings.Split(out.String(), "\n")
		said := false
		for i, line := range lines {
			if !strings.Contains(strings.ToLower(line), "not being offered") {
				continue
			}
			said = true
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "  what to do: ") {
				t.Errorf("%s: the fact keeps its remedy on the next line:\n%s", name, out.String())
			}
			block := []string{line, lines[i+1]}
			if i+2 < len(lines) && strings.HasPrefix(lines[i+2], "  your phones") {
				block = append(block, lines[i+2])
			}
			for _, l := range block {
				for _, word := range allCaps.FindAllString(l, -1) {
					if word != "URL" {
						t.Errorf("%s: %q is shouted in capitals: %q", name, word, l)
					}
				}
			}
		}
		if !said {
			t.Errorf("%s: the devices are not offered, and that fact must still be said:\n%s", name, out.String())
		}
	}
}

// ── AN_AGENT_THAT_KNOWS_IT_IS_OLD item 1: say when a newer agent exists ─────────────────────────────────────────
//
// The farm answers a registration and every heartbeat with newerAgentVersion when this agent is older than the release it
// was told about (the farm's wire says so), and leaves the field out otherwise.

// versionFarm answers each call with the newerAgentVersion the test sets for it ("" leaves the field out).
type versionFarm struct {
	register   atomic.Value // string
	heartbeat  atomic.Value // string
	heartbeats atomic.Int32
}

func newVersionFarm(t *testing.T, register, heartbeat string) (*versionFarm, *httptest.Server) {
	t.Helper()
	f := &versionFarm{}
	f.register.Store(register)
	f.heartbeat.Store(heartbeat)
	field := func(v string) string {
		if v == "" {
			return ""
		}
		return `,"newerAgentVersion":"` + v + `"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/register") {
			_, _ = w.Write([]byte(`{"success":true,"data":{"nodeId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","apiKey":""` + field(f.register.Load().(string)) + `}}`))
			return
		}
		f.heartbeats.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"data":{"provisionTasks":[],"commands":[],"leasedDeviceIds":[]` + field(f.heartbeat.Load().(string)) + `}}`))
	}))
	t.Cleanup(server.Close)
	return f, server
}

func runAgainst(t *testing.T, server *httptest.Server, own string, install connect.Install, ticks int) string {
	t.Helper()
	var out strings.Builder
	cfg := config(server, &out, ticks)
	cfg.AgentVersion = own
	cfg.Install = install
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("an older agent keeps working, and this one stopped: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestANewerReleaseIsSaidOnceAndTheAgentKeepsWorking(t *testing.T) {
	farm, server := newVersionFarm(t, "0.4.1", "0.4.1")
	transcript := runAgainst(t, server, "v0.4.0", connect.Install{Started: connect.StartedByNpx}, 5)
	if n := strings.Count(transcript, "v0.4.1 is out"); n != 1 {
		t.Fatalf("the farm named 0.4.1 at the door and on five heartbeats; say it once, got %d:\n%s", n, transcript)
	}
	if !strings.Contains(transcript, "this agent is v0.4.0, and v0.4.1 is out") || !strings.Contains(transcript, "It keeps working as it is.") {
		t.Fatalf("the block says this agent's version, the newer one, and that nothing stops:\n%s", transcript)
	}
	if farm.heartbeats.Load() != 5 {
		t.Fatalf("an older agent keeps working: %d heartbeats of 5", farm.heartbeats.Load())
	}

	// A different version later is said, once.
	farm2, server2 := newVersionFarm(t, "0.4.1", "0.4.1")
	var out strings.Builder
	cfg := config(server2, &out, 4)
	cfg.AgentVersion = "v0.4.0"
	ticks := 0
	probes := cfg.Probes
	cfg.Probes = func(ctx context.Context) []inventory.Outcome {
		ticks++
		if ticks == 3 {
			farm2.heartbeat.Store("0.4.2")
		}
		return probes(ctx)
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "v0.4.1 is out") != 1 || strings.Count(out.String(), "v0.4.2 is out") != 1 {
		t.Fatalf("0.4.1, then 0.4.2: each said once:\n%s", out.String())
	}
}

func TestNothingIsSaidWhenTheFarmSaysNothingNewer(t *testing.T) {
	for _, c := range []struct{ name, own, farm string }{
		{"no field", "v0.4.0", ""},
		{"this agent is the newest", "v0.4.1", "0.4.1"},
		{"this agent is newer than the farm knows", "v0.5.0", "0.4.1"},
		{"a build of its own", "dev", "0.4.1"},
		{"no version", "", "0.4.1"},
		{"not a version", "v0.4.0", "latest"},
		{"a pre-release", "v0.4.0", "0.4.1-rc.1"},
		{"a word after it", "v0.4.0", "0.4.1 now"},
	} {
		_, server := newVersionFarm(t, c.farm, c.farm)
		transcript := runAgainst(t, server, c.own, connect.Install{}, 3)
		if strings.Contains(transcript, " is out") || strings.Contains(transcript, "to update") {
			t.Errorf("%s: nothing newer, so nothing is said:\n%s", c.name, transcript)
		}
	}
}

// HOW TO UPDATE DEPENDS ON HOW IT WAS STARTED: one command when the agent can tell, the two common ones when it cannot.
func TestTheUpdateCommandFitsHowTheAgentWasStarted(t *testing.T) {
	npx := "npx nthcase-agent@latest connect …"
	global := "npm install -g nthcase-agent@latest"
	for _, c := range []struct {
		name    string
		install connect.Install
		say     []string
		not     []string
	}{
		{"from npx's cache", connect.Install{Started: connect.StartedByNpx}, []string{npx}, []string{"npm install"}},
		{"a global install", connect.Install{Started: connect.StartedFromGlobal}, []string{global}, []string{"npx nthcase-agent@latest", "Started through npx"}},
		// Its update is npm install -g, and never the npx form. Every OTHER command it prints is the npx form with @latest
		// (NTH_AGENT_0_4_3): it was started through npx, and that form runs this release or a newer one even where the
		// global install's own command is not on PATH. So the npx form is held out of the update advice, not the transcript.
		{"a global install that npx ran", connect.Install{Started: connect.StartedFromGlobalByNpx},
			[]string{global, "this is the copy installed on this machine with npm install -g, which is older. Started through npx, an installed copy is used before a newer one is fetched.",
				"run `npx nthcase-agent@latest doctor`"},
			[]string{"\n  to update, stop it (Ctrl-C) and start it with the flags you use now: npx nthcase-agent@latest"}},
		{"a project", connect.Install{Started: connect.StartedFromProject, Project: "/work/app"},
			[]string{"npm install nthcase-agent@latest in /work/app"}, []string{"npm install -g", "npx nthcase-agent@latest"}},
		{"cannot tell", connect.Install{}, []string{npx, global}, nil},
	} {
		_, server := newVersionFarm(t, "0.4.1", "")
		transcript := runAgainst(t, server, "v0.4.0", c.install, 1)
		for _, want := range c.say {
			if !strings.Contains(transcript, want) {
				t.Errorf("%s: must say %q:\n%s", c.name, want, transcript)
			}
		}
		for _, not := range c.not {
			if strings.Contains(transcript, not) {
				t.Errorf("%s: must not say %q:\n%s", c.name, not, transcript)
			}
		}
	}
}

func TestHowTheAgentWasStartedIsReadFromWhereItsProgramIs(t *testing.T) {
	files := map[string]bool{"/work/app/package.json": true, "/work/lib/package.json": true}
	isFile := func(p string) bool { return files[p] }
	viaNpx := func(k string) string {
		if k == "npm_lifecycle_event" {
			return "npx"
		}
		return ""
	}
	typed := func(string) string { return "" }
	for _, c := range []struct {
		path   string
		getenv func(string) string
		want   connect.Install
	}{
		{"/Users/ada/.npm/_npx/50ecfe4e91505fe9/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64", viaNpx, connect.Install{Started: connect.StartedByNpx}},
		{"/Users/ada/.npm/_npx/50ecfe4e91505fe9/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64", typed, connect.Install{Started: connect.StartedByNpx}},
		{"/usr/local/lib/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64", typed, connect.Install{Started: connect.StartedFromGlobal}},
		{"/usr/local/lib/node_modules/nthcase-agent/bin/nth-agent-darwin-arm64", viaNpx, connect.Install{Started: connect.StartedFromGlobalByNpx}},
		{"/home/ada/.nvm/versions/node/v22.11.0/lib/node_modules/nthcase-agent/bin/nth-agent-linux-amd64", typed, connect.Install{Started: connect.StartedFromGlobal}},
		{"/work/app/node_modules/nthcase-agent/bin/nth-agent-linux-amd64", viaNpx, connect.Install{Started: connect.StartedFromProject, Project: "/work/app"}},
		{"/work/lib/node_modules/nthcase-agent/bin/nth-agent-linux-amd64", typed, connect.Install{Started: connect.StartedFromProject, Project: "/work/lib"}},
		{"/opt/tools/node_modules/nthcase-agent/bin/nth-agent-linux-amd64", typed, connect.Install{}},
		{"/Users/ada/Downloads/nth-agent", viaNpx, connect.Install{}},
		{"", typed, connect.Install{}},
	} {
		if got := connect.HowStarted(c.path, c.getenv, isFile); got != c.want {
			t.Errorf("%s (npx=%v): got %+v, want %+v", c.path, c.getenv("npm_lifecycle_event") == "npx", got, c.want)
		}
	}
}
