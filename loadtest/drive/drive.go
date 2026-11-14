// Package drive starts runs of one scenario through the product's public API, step by step — so many at once, then more —
// and says how each step went: passed, failed and errored runs, queue wait and duration (p50, p95, max), and why runs
// failed. Every run is pinned to one fake phone of the load-test fleet by its model name, so no run can land on a phone a
// person owns or on the platform's own phones.
//
// It never retries a refused start: a refusal is counted and printed with the product's own words. It refuses to run at
// all without --i-know-this-is-production, because every run it starts is a real run, charged to a real workspace.
package drive

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BaseVariable names the product's API address (https://…, the host only).
const BaseVariable = "NTH_AGENT_LOADTEST_API"

// KeysVariable holds the API keys, one per load-test workspace, as `workspace-id=key` pairs separated by spaces. It is
// read from the environment only: an API key on a command line is visible to every account on the machine.
const KeysVariable = "NTH_AGENT_LOADTEST_API_KEYS"

// KeyVariable is the name the usage prints.
const KeyVariable = KeysVariable

// PhoneModel is the model name of the fleet's n-th fake phone (from 1) — the fleet names them so, and nothing else does.
func PhoneModel(n int) string { return fmt.Sprintf("LoadTest Phone %02d", n) }

// ErrNotConfirmed is the refusal without --i-know-this-is-production.
var ErrNotConfirmed = errors.New("refusing to start: every run this starts is a real run on production, charged to the " +
	"load-test workspace — pass --i-know-this-is-production to say you know")

// Workspace is one load-test workspace and its key. The key cannot be printed: every fmt verb answers with its length.
type Workspace struct {
	ID  string
	key string
}

func (w Workspace) String() string {
	return fmt.Sprintf("workspace %s (key: %d characters)", w.ID, len(w.key))
}
func (w Workspace) GoString() string           { return w.String() }
func (w Workspace) Format(f fmt.State, _ rune) { fmt.Fprint(f, w.String()) }

// Keys is the workspaces the keys variable named.
type Keys struct {
	Workspaces []Workspace
	problem    string
}

// KeyFromEnvironment reads KeysVariable, and nowhere else.
func KeyFromEnvironment(getenv func(string) string) Keys {
	if getenv == nil {
		getenv = os.Getenv
	}
	return ParseKeys(getenv(KeysVariable))
}

// ParseKeys reads `workspace-id=key workspace-id=key`. A problem is said without the key.
func ParseKeys(raw string) Keys {
	var keys Keys
	for i, pair := range strings.Fields(raw) {
		id, key, found := strings.Cut(pair, "=")
		if !found || strings.TrimSpace(id) == "" || strings.TrimSpace(key) == "" {
			keys.problem = fmt.Sprintf("%s: entry %d is not workspace-id=key", KeysVariable, i+1)
			return keys
		}
		keys.Workspaces = append(keys.Workspaces, Workspace{ID: id, key: key})
	}
	if len(keys.Workspaces) == 0 {
		keys.problem = KeysVariable + " is not set: it holds one workspace-id=key pair per load-test workspace"
	}
	return keys
}

// Config is one drive.
type Config struct {
	Production bool
	BaseURL    string
	Keys       Keys
	// ScenarioID is the scenario every run replays: one id when the steps run in one workspace, or `workspace-id=scenario-id`
	// pairs separated by commas, one per workspace — a scenario belongs to one workspace, so each has its own copy.
	ScenarioID string
	Steps      []int
	StepBound  time.Duration
	Poll       time.Duration
	// PlanVersion pins the plan the runs replay; 0 = the scenario's own.
	PlanVersion int
	// BaselineP95 is the 4-at-once step's p95 duration, when the steps run one invocation at a time; 0 = the first step's.
	BaselineP95 time.Duration
	// Pause waits for a line on this reader before every step after the first (the operator raises the worker's lanes
	// there); nil = no pause.
	Pause io.Reader
	Out   io.Writer
	// ProvisionalPasses counts a run that ended provisional WITH NO FAILURE CLASS as passed, in a column of its own — a
	// recorded scenario with no check steps ends provisional when nothing failed. A provisional run that carries a failure
	// class is never counted as passed. false = a provisional run stops the drive, as any run that did not pass does.
	ProvisionalPasses bool

	// TEST SEAMS — no flag reaches them.
	HTTP *http.Client
	Now  func() time.Time
}

// ParseSteps reads "4,8,12".
func ParseSteps(raw string) ([]int, error) {
	var steps []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 200 {
			return nil, fmt.Errorf("--steps wants numbers of runs at once from 1 to 200 separated by commas, like 4,8,12 — not %q", raw)
		}
		steps = append(steps, n)
	}
	return steps, nil
}

func (c Config) check() error {
	if !c.Production {
		return ErrNotConfirmed
	}
	if c.Keys.problem != "" {
		return errors.New(c.Keys.problem)
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s must be the product's API address, like https://api.example.com — it is %q", BaseVariable, c.BaseURL)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost")) {
		return fmt.Errorf("%s must be https (an API key travels on it), or http to this machine's loopback — it is %q", BaseVariable, c.BaseURL)
	}
	if _, err := c.scenarios(); err != nil {
		return err
	}
	if len(c.Steps) == 0 {
		return errors.New("--steps names no step")
	}
	return nil
}

// scenarios is each workspace's scenario.
func (c Config) scenarios() (map[string]string, error) {
	out := map[string]string{}
	raw := strings.TrimSpace(c.ScenarioID)
	if !strings.Contains(raw, "=") {
		if _, err := parseUUID(raw); err != nil {
			return nil, fmt.Errorf("--scenario must be the scenario's id, or workspace-id=scenario-id pairs: %v", err)
		}
		for _, w := range c.Keys.Workspaces {
			out[w.ID] = raw
		}
		return out, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		ws, id, _ := strings.Cut(strings.TrimSpace(pair), "=")
		if _, err := parseUUID(id); err != nil {
			return nil, fmt.Errorf("--scenario pair %q: %v", pair, err)
		}
		out[strings.TrimSpace(ws)] = id
	}
	for _, w := range c.Keys.Workspaces {
		if out[w.ID] == "" {
			return nil, fmt.Errorf("--scenario names no scenario for workspace %s", w.ID)
		}
	}
	return out, nil
}

func parseUUID(s string) (string, error) {
	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 32 {
		return "", fmt.Errorf("%q is not an id", s)
	}
	if _, err := hex.DecodeString(clean); err != nil {
		return "", fmt.Errorf("%q is not an id", s)
	}
	return s, nil
}

// run is one started run, as the driver follows it.
type run struct {
	workspace Workspace
	phone     string
	id        string
	status    string
	failure   string
	ended     string
	created   time.Time
	started   time.Time
	finished  time.Time
	duration  time.Duration
	terminal  bool
}

// Result is one step's outcome.
type Result struct {
	AtOnce  int
	Started int
	Refused map[string]int // "HTTP 400 queue_limit_reached: …" → count
	Passed  int
	// ProvisionalPassed is the runs that ended provisional with no failure class, counted as passed (Config.ProvisionalPasses).
	ProvisionalPassed int
	Failed            int
	Errored           int
	Other             map[string]int
	Unfinished        int
	QueueWait         []time.Duration
	Duration          []time.Duration
	Failures          map[string]int
	ReadsLimit        int // reads the API answered 429; each was read again at the next poll

	provisionalColumn bool // the drive counts provisional runs with no failure class as passed, so the line says how many
}

// Run drives every step and prints each one. It stops after a step with any run that did not pass, or whose p95 duration
// is past 1.5 times the first step's: the runbook's stop rule, as far as the API can see it.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.check(); err != nil {
		return err
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 15 * time.Second
	}
	out := cfg.Out
	var names []string
	for _, w := range cfg.Keys.Workspaces {
		names = append(names, w.String())
	}
	fmt.Fprintf(out, "drive: scenario %s, steps %v, through %s, %s\n", cfg.ScenarioID, cfg.Steps, cfg.BaseURL, strings.Join(names, ", "))
	baseline := cfg.BaselineP95
	var pause *bufio.Reader
	if cfg.Pause != nil {
		pause = bufio.NewReader(cfg.Pause)
	}
	for i, n := range cfg.Steps {
		if i > 0 && pause != nil {
			fmt.Fprintf(out, "next: %d at once. Set the device worker's mobile lanes for it, then press Enter (Ctrl-C stops here).\n", n)
			if _, err := pause.ReadString('\n'); err != nil {
				return fmt.Errorf("stopped before the %d-at-once step: %w", n, err)
			}
		}
		result := step(ctx, cfg, n)
		print(out, result)
		p95 := percentile(result.Duration, 95)
		if baseline == 0 && i == 0 {
			baseline = p95
		}
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case result.Started < n || result.Failed+result.Errored+result.Unfinished+sum(result.Other) > 0:
			fmt.Fprintln(out, "STOP: this step had a run that did not pass, or a start that was refused — read it before the next one.")
			return nil
		case baseline > 0 && float64(p95) > 1.5*float64(baseline):
			fmt.Fprintf(out, "STOP: p95 duration %s is past 1.5 × %s, the first step's.\n", p95.Round(time.Second), baseline.Round(time.Second))
			return nil
		}
	}
	fmt.Fprintln(out, "drive: every step ran")
	return nil
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// step starts n runs at once — spread over the workspaces in turn, each on its own fake phone — and follows them to their
// ends or to the step's bound, when every one still going is cancelled.
func step(ctx context.Context, cfg Config, n int) Result {
	result := Result{AtOnce: n, Refused: map[string]int{}, Other: map[string]int{}, Failures: map[string]int{},
		provisionalColumn: cfg.ProvisionalPasses}
	var runs []*run
	perWorkspace := map[string]int{}
	for i := 0; i < n; i++ {
		w := cfg.Keys.Workspaces[i%len(cfg.Keys.Workspaces)]
		perWorkspace[w.ID]++
		phone := PhoneModel(perWorkspace[w.ID])
		id, refusal := trigger(ctx, cfg, w, phone)
		if refusal != "" {
			result.Refused[refusal]++
			continue
		}
		runs = append(runs, &run{workspace: w, phone: phone, id: id})
	}
	result.Started = len(runs)
	deadline := cfg.Now().Add(cfg.StepBound)
	for {
		open := 0
		for _, r := range runs {
			if r.terminal {
				continue
			}
			if limited := read(ctx, cfg, r); limited {
				result.ReadsLimit++
			}
			if !r.terminal {
				open++
			}
		}
		if open == 0 || ctx.Err() != nil || !cfg.Now().Before(deadline) {
			break
		}
		wait(ctx, cfg.Poll)
	}
	for _, r := range runs {
		if !r.terminal {
			result.Unfinished++
			cancel(cfg, r)
			continue
		}
		switch r.status {
		case "passed", "pass", "passing", "succeeded", "success":
			result.Passed++
		case "provisional":
			// Counted as passed only when the operator asked, and only with nothing that says why it failed or ended: a
			// provisional run carrying a failure class, or an ended-because word, is a run that did not pass.
			if cfg.ProvisionalPasses && r.failure == "" && r.ended == "" {
				result.ProvisionalPassed++
			} else {
				result.Other[r.status]++
			}
		case "failed", "fail":
			result.Failed++
		case "error", "errored":
			result.Errored++
		default:
			result.Other[r.status]++
		}
		if !r.started.IsZero() && !r.created.IsZero() {
			result.QueueWait = append(result.QueueWait, r.started.Sub(r.created))
		}
		if r.duration > 0 {
			result.Duration = append(result.Duration, r.duration)
		}
		if why := strings.TrimSpace(r.failure + " " + r.ended); why != "" && r.status != "passed" {
			result.Failures[why]++
		}
	}
	return result
}

func wait(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Code    string          `json:"code"`
	Errors  []string        `json:"errors"`
	Data    json.RawMessage `json:"data"`
}

func (cfg Config) request(ctx context.Context, w Workspace, method, path string, body any) (int, envelope, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, envelope{}, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.BaseURL, "/")+path, reader)
	if err != nil {
		return 0, envelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+w.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "nthcase-loadtest")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cfg.HTTP.Do(req)
	if err != nil {
		return 0, envelope{}, errors.New(withoutTheKey(err.Error(), w.key))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var e envelope
	_ = json.Unmarshal(raw, &e)
	if e.Message == "" && resp.StatusCode >= 300 {
		e.Message = strings.TrimSpace(string(raw))
	}
	e.Message = withoutTheKey(e.Message, w.key)
	return resp.StatusCode, e, nil
}

// withoutTheKey keeps the key out of anything printed, should a server or a transport error ever echo it.
func withoutTheKey(text, key string) string {
	if key == "" {
		return text
	}
	return strings.ReplaceAll(text, key, "«key»")
}

func newOperationID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func scenarioFor(cfg Config, w Workspace) string {
	scenarios, _ := cfg.scenarios()
	return scenarios[w.ID]
}

// trigger starts one run; a refusal comes back as one line, the product's own words, and is never tried again.
func trigger(ctx context.Context, cfg Config, w Workspace, phone string) (string, string) {
	spec, _ := json.Marshal(map[string]string{"model": phone})
	body := map[string]any{
		"scenarioId":     scenarioFor(cfg, w),
		"triggeredBy":    "load test",
		"operationId":    newOperationID(),
		"deviceSpecJson": string(spec),
	}
	if cfg.PlanVersion > 0 {
		body["requestedPlanVersion"] = cfg.PlanVersion
	}
	status, e, err := cfg.request(ctx, w, http.MethodPost, "/api/workspaces/"+url.PathEscape(w.ID)+"/runs", body)
	if err != nil {
		return "", "no answer: " + err.Error()
	}
	if status/100 != 2 || !e.Success {
		code := e.Code
		if code == "" {
			code = "no code"
		}
		message := e.Message
		if len(message) > 200 {
			message = message[:200] + "…"
		}
		return "", fmt.Sprintf("HTTP %d %s: %s", status, code, message)
	}
	var data struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil || data.ID == "" {
		return "", fmt.Sprintf("HTTP %d with no run id in the answer", status)
	}
	return data.ID, ""
}

// read refreshes one run; it answers whether the API turned the read away at its rate limit.
func read(ctx context.Context, cfg Config, r *run) bool {
	status, e, err := cfg.request(ctx, r.workspace, http.MethodGet,
		"/api/workspaces/"+url.PathEscape(r.workspace.ID)+"/runs/"+url.PathEscape(r.id), nil)
	if err != nil || status == http.StatusTooManyRequests || status/100 != 2 {
		return status == http.StatusTooManyRequests
	}
	var data struct {
		Status        string     `json:"status"`
		FailureClass  string     `json:"failureClass"`
		EndedBecause  string     `json:"endedBecause"`
		CreatedAt     time.Time  `json:"createdAt"`
		StartedAt     *time.Time `json:"startedAt"`
		FinishedAt    *time.Time `json:"finishedAt"`
		DurationMs    *int64     `json:"durationMs"`
		DeviceOwnship string     `json:"deviceOwnership"`
	}
	if json.Unmarshal(e.Data, &data) != nil {
		return false
	}
	r.status = strings.ToLower(strings.TrimSpace(data.Status))
	r.failure, r.ended, r.created = data.FailureClass, data.EndedBecause, data.CreatedAt
	if data.StartedAt != nil {
		r.started = *data.StartedAt
	}
	if data.FinishedAt != nil {
		r.finished = *data.FinishedAt
	}
	switch {
	case data.DurationMs != nil:
		r.duration = time.Duration(*data.DurationMs) * time.Millisecond
	case !r.finished.IsZero() && !r.started.IsZero():
		r.duration = r.finished.Sub(r.started)
	}
	r.terminal = data.FinishedAt != nil || terminalWord(r.status)
	return false
}

func terminalWord(status string) bool {
	switch status {
	case "passed", "pass", "passing", "succeeded", "success", "failed", "fail", "error", "errored", "provisional",
		"unverified", "cancelled", "canceled":
		return true
	}
	return false
}

// cancel ends a run the step's bound left going, so the next step and the cleanup start from no runs of ours.
func cancel(cfg Config, r *run) {
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	status, e, err := cfg.request(ctx, r.workspace, http.MethodPost,
		"/api/workspaces/"+url.PathEscape(r.workspace.ID)+"/runs/"+url.PathEscape(r.id)+"/cancel", nil)
	switch {
	case err != nil:
		fmt.Fprintf(cfg.Out, "  run %s (%s) is still going and could not be cancelled: %v — cancel it on the Runs page\n", r.id, r.phone, err)
	case status/100 != 2:
		fmt.Fprintf(cfg.Out, "  run %s (%s) is still going; the cancel was answered HTTP %d: %s — cancel it on the Runs page\n", r.id, r.phone, status, e.Message)
	default:
		fmt.Fprintf(cfg.Out, "  run %s (%s) was still going at the step's bound and was cancelled\n", r.id, r.phone)
	}
}

func percentile(values []time.Duration, p int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	// nearest rank
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

func print(out io.Writer, r Result) {
	fmt.Fprintf(out, "\n── %d at once ──\n", r.AtOnce)
	fmt.Fprintf(out, "  started %d, refused %d; passed %d, ", r.Started, sum(r.Refused), r.Passed)
	if r.provisionalColumn {
		fmt.Fprintf(out, "provisional with no failure class (counted as passed) %d, ", r.ProvisionalPassed)
	}
	fmt.Fprintf(out, "failed %d, error %d, unfinished at the bound %d", r.Failed, r.Errored, r.Unfinished)
	for status, count := range r.Other {
		fmt.Fprintf(out, ", %s %d", status, count)
	}
	fmt.Fprintln(out)
	line := func(name string, values []time.Duration) {
		if len(values) == 0 {
			fmt.Fprintf(out, "  %-10s none read\n", name)
			return
		}
		fmt.Fprintf(out, "  %-10s p50 %s  p95 %s  max %s  (n=%d)\n", name, percentile(values, 50).Round(100*time.Millisecond),
			percentile(values, 95).Round(100*time.Millisecond), percentile(values, 100).Round(100*time.Millisecond), len(values))
	}
	line("queue wait", r.QueueWait)
	line("duration", r.Duration)
	for _, group := range []struct {
		title string
		m     map[string]int
	}{{"refusals", r.Refused}, {"failure classes", r.Failures}} {
		if len(group.m) == 0 {
			continue
		}
		var keys []string
		for k := range group.m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(out, "  %s:\n", group.title)
		for _, k := range keys {
			fmt.Fprintf(out, "    %d × %s\n", group.m[k], k)
		}
	}
	if r.ReadsLimit > 0 {
		fmt.Fprintf(out, "  %d read(s) were turned away at the API's rate limit and read again at the next poll\n", r.ReadsLimit)
	}
}
