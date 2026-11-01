package drive_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/drive"
)

const scenario = "00000000-0000-4000-8000-000000000042"

// api stands in for the product's public API: it starts runs (refusing the ones refuse names), and answers each run's
// read with what outcome says. It holds every start it was asked for.
type api struct {
	t       *testing.T
	mu      sync.Mutex
	starts  []map[string]any
	keys    map[string]string // workspace → the key it takes
	refuse  func(n int) bool
	outcome func(run int) string // "" = still running
	reads   map[string]int
	cancels int
	server  *httptest.Server
	created time.Time
}

func newAPI(t *testing.T, keys map[string]string) *api {
	a := &api{t: t, keys: keys, reads: map[string]int{}, created: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		refuse: func(int) bool { return false }, outcome: func(int) string { return "Passed" }}
	a.server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.server.Close)
	return a
}

func (a *api) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // api workspaces {ws} runs [{id} [cancel]]
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "workspaces" || parts[3] != "runs" {
		w.WriteHeader(404)
		return
	}
	ws := parts[2]
	reply := func(status int, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}
	if r.Header.Get("Authorization") != "Bearer "+a.keys[ws] {
		// A server that echoes what it was sent: the driver must still print no key.
		reply(401, map[string]any{"success": false, "message": "the key " + r.Header.Get("Authorization") + " is not this workspace's"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && len(parts) == 4:
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		body["workspace"] = ws
		a.starts = append(a.starts, body)
		n := len(a.starts)
		if a.refuse(n) {
			reply(400, map[string]any{"success": false, "code": "queue_limit_reached", "message": "The plan's queue is full."})
			return
		}
		reply(200, map[string]any{"success": true, "data": map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", n), "status": "Queued"}})
	case r.Method == http.MethodGet && len(parts) == 5:
		id := parts[4]
		a.reads[id]++
		var n int
		fmt.Sscanf(id[len(id)-12:], "%d", &n)
		status := a.outcome(n)
		data := map[string]any{"id": id, "status": "Running", "createdAt": a.created, "startedAt": a.created.Add(time.Duration(n) * time.Second)}
		if status != "" && a.reads[id] > 1 {
			data["status"] = status
			data["finishedAt"] = a.created.Add(time.Duration(n)*time.Second + time.Minute)
			data["durationMs"] = 60000 + n*1000
			if status != "Passed" {
				data["failureClass"] = "ElementNotFound"
			}
		}
		reply(200, map[string]any{"success": true, "data": data})
	case r.Method == http.MethodPost && len(parts) == 6 && parts[5] == "cancel":
		a.cancels++
		reply(200, map[string]any{"success": true})
	default:
		w.WriteHeader(404)
	}
}

func config(a *api, keys string, steps ...int) (drive.Config, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return drive.Config{Production: true, BaseURL: a.server.URL, Keys: drive.ParseKeys(keys), ScenarioID: scenario, Steps: steps,
		StepBound: 5 * time.Second, Poll: 5 * time.Millisecond, Out: out}, out
}

func TestEachStepStartsItsRunsOnTheirOwnFakePhonesAndSaysHowTheyWent(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "gp_key_a_secret", "ws-b": "gp_key_b_secret"})
	cfg, out := config(a, "ws-a=gp_key_a_secret ws-b=gp_key_b_secret", 2, 4)
	if err := drive.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(a.starts) != 6 {
		t.Fatalf("2 then 4 at once is 6 starts; the API was asked %d", len(a.starts))
	}
	seen := map[string]bool{}
	for _, s := range a.starts[2:] {
		var spec map[string]string
		json.Unmarshal([]byte(s["deviceSpecJson"].(string)), &spec)
		key := s["workspace"].(string) + " " + spec["model"]
		if seen[key] || !strings.HasPrefix(spec["model"], "LoadTest Phone ") {
			t.Fatalf("a run is pinned to %q in %s — each run of a step needs its own fake phone", spec["model"], s["workspace"])
		}
		seen[key] = true
		if s["scenarioId"] != scenario || s["operationId"] == "" {
			t.Fatalf("a start without the scenario or its own operation id: %v", s)
		}
	}
	text := out.String()
	for _, want := range []string{"── 2 at once ──", "── 4 at once ──", "passed 4, failed 0", "queue wait", "duration", "p50", "p95", "max", "every step ran"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
	for _, key := range []string{"gp_key_a_secret", "gp_key_b_secret"} {
		if strings.Contains(text, key) {
			t.Fatal("the driver printed a key")
		}
	}
}

// EACH WORKSPACE RUNS ITS OWN COPY OF THE SCENARIO: a scenario belongs to one workspace.
func TestEachWorkspaceRunsItsOwnScenario(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "ka", "ws-b": "kb"})
	cfg, _ := config(a, "ws-a=ka ws-b=kb", 4)
	other := "00000000-0000-4000-8000-0000000000bb"
	cfg.ScenarioID = "ws-a=" + scenario + ", ws-b=" + other
	if err := drive.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, s := range a.starts {
		want := scenario
		if s["workspace"] == "ws-b" {
			want = other
		}
		if s["scenarioId"] != want {
			t.Fatalf("workspace %s started scenario %v", s["workspace"], s["scenarioId"])
		}
	}
}

// A REFUSED START IS COUNTED AND PRINTED IN THE PRODUCT'S WORDS, AND NEVER TRIED AGAIN — and it stops the drive, because
// a step that did not start what it says did not measure what it says.
func TestARefusedStartIsCountedNeverRetriedAndStopsTheDrive(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "gp_key_a_secret"})
	a.refuse = func(n int) bool { return n == 2 }
	cfg, out := config(a, "ws-a=gp_key_a_secret", 3, 6)
	if err := drive.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(a.starts) != 3 {
		t.Fatalf("the refused start was tried again, or the drive went on: %d starts", len(a.starts))
	}
	text := out.String()
	for _, want := range []string{"started 2, refused 1", "1 × HTTP 400 queue_limit_reached: The plan's queue is full.", "STOP"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
}

func TestAFailedRunStopsTheDriveAndItsClassIsCounted(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "k"})
	a.outcome = func(run int) string {
		if run == 3 {
			return "Failed"
		}
		return "Passed"
	}
	cfg, out := config(a, "ws-a=k", 3, 6)
	drive.Run(context.Background(), cfg)
	text := out.String()
	if len(a.starts) != 3 || !strings.Contains(text, "passed 2, failed 1") || !strings.Contains(text, "1 × ElementNotFound") || !strings.Contains(text, "STOP") {
		t.Fatalf("%d starts:\n%s", len(a.starts), text)
	}
}

// A RUN STILL GOING AT THE STEP'S BOUND IS COUNTED AS SUCH AND CANCELLED, so the next step and the cleanup start from no
// runs of the driver's.
func TestARunPastTheBoundIsCancelled(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "k"})
	a.outcome = func(run int) string {
		if run == 2 {
			return ""
		}
		return "Passed"
	}
	cfg, out := config(a, "ws-a=k", 2)
	cfg.StepBound = 200 * time.Millisecond
	drive.Run(context.Background(), cfg)
	if a.cancels != 1 || !strings.Contains(out.String(), "unfinished at the bound 1") || !strings.Contains(out.String(), "was cancelled") {
		t.Fatalf("%d cancel(s):\n%s", a.cancels, out.String())
	}
}

func TestTheDriverRefusesWhatItCannotRunSafely(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "k"})
	base, _ := config(a, "ws-a=gp_secret_key", 1)
	cases := map[string]func(*drive.Config){
		"no production flag": func(c *drive.Config) { c.Production = false },
		"no keys":            func(c *drive.Config) { c.Keys = drive.ParseKeys("") },
		"a key without its workspace": func(c *drive.Config) {
			c.Keys = drive.ParseKeys("gp_secret_key")
		},
		"plain http to another host": func(c *drive.Config) { c.BaseURL = "http://api.example.com" },
		"no scenario id":             func(c *drive.Config) { c.ScenarioID = "find the lamp" },
		"no scenario for a workspace": func(c *drive.Config) {
			c.Keys = drive.ParseKeys("ws-a=gp_secret_key ws-b=gp_secret_key")
			c.ScenarioID = "ws-a=" + scenario
		},
	}
	for name, change := range cases {
		cfg := base
		change(&cfg)
		err := drive.Run(context.Background(), cfg)
		if err == nil {
			t.Errorf("%s: not refused", name)
			continue
		}
		if strings.Contains(err.Error(), "gp_secret_key") {
			t.Errorf("%s: the refusal carries the key: %v", name, err)
		}
	}
	if len(a.starts) != 0 {
		t.Fatal("a refused drive started a run")
	}
	keys := drive.ParseKeys("ws-a=gp_secret_key")
	if s := fmt.Sprintf("%v %+v %#v %s %q", keys, keys, keys, keys.Workspaces[0], keys.Workspaces[0]); strings.Contains(s, "gp_secret_key") {
		t.Fatalf("formatting the keys prints one: %s", s)
	}
}

// A KEY THE SERVER ECHOES BACK IS NOT PRINTED EITHER.
func TestAnEchoedKeyIsNotPrinted(t *testing.T) {
	a := newAPI(t, map[string]string{"ws-a": "the-right-key"})
	cfg, out := config(a, "ws-a=gp_wrong_key_echoed", 1)
	drive.Run(context.Background(), cfg)
	if strings.Contains(out.String(), "gp_wrong_key_echoed") || !strings.Contains(out.String(), "«key»") {
		t.Fatalf("the echoed key:\n%s", out.String())
	}
}

func TestStepsAreRead(t *testing.T) {
	if steps, err := drive.ParseSteps("4, 8,12"); err != nil || fmt.Sprint(steps) != "[4 8 12]" {
		t.Fatalf("%v %v", steps, err)
	}
	for _, bad := range []string{"", "0", "4,x", "300"} {
		if _, err := drive.ParseSteps(bad); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}
