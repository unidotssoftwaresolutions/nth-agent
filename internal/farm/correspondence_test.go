package farm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── A ROW THAT CANNOT BE FILTERED CANNOT BE CHOSEN ──────────────────────────────────────────────────────────

// wireJSONFields lists the json names WireDescriptor publishes.
func wireJSONFields() map[string]bool {
	fields := map[string]bool{}
	t := reflect.TypeOf(farm.WireDescriptor{})
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		fields[strings.ToLower(name)] = true
	}
	return fields
}

// leaseFilterColumns DISCOVERS the columns the farm's lease SQL filters on, from the farm's own source:
//
//	AND (@osVersion = '' OR lower(d."OsVersion") = lower(@osVersion))
//
// A hand-written list here would be the defect restated.
func leaseFilterColumns(t *testing.T) []string {
	t.Helper()
	// CHANGED 2026-08-25 (THE_CHECK_THAT_ALREADY_KNEW, found in passing): the farm moved the lease SQL into a file of its
	// own, and this walk kept reading the old one — zero columns found, so the seal was red on any machine with the server's
	// source beside it (and silently skipping in CI, which has none). The walk now reads EVERY file the server's sources
	// name for the lease SQL and unions: the population is wherever the farm keeps the filter, not the file somebody
	// remembered.
	var sources []string
	for _, path := range farmtest.ServerSources(t, farmtest.FarmLeaseQueries) {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sources = append(sources, string(src))
	}
	if len(sources) == 0 {
		t.Fatalf("%s names the farm's lease SQL, and none of its files can be read", farmtest.SourcesFile)
	}
	re := regexp.MustCompile(`lower\(d\."(\w+)"\)\s*=\s*lower\(@`)
	seen := map[string]bool{}
	var cols []string
	for _, src := range sources {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				cols = append(cols, m[1])
			}
		}
	}
	return cols
}

func TestTheDescriptorCarriesEveryFieldTheLeaseFilterReads(t *testing.T) {
	// THE DESCRIPTOR-COMPLETENESS SEAL: every column the farm filters an unspecified lease on has a wire field
	// — and the publication fills it from the probe (an empty probe answer is omitted, never "").
	cols := leaseFilterColumns(t)
	if len(cols) < 3 {
		t.Fatalf("the filter discovery is blind: found %v", cols)
	}
	fields := wireJSONFields()
	for _, col := range cols {
		if !fields[strings.ToLower(col)] {
			t.Fatalf("the farm filters leases on %q and the agent's descriptor has no such field — a row without it cannot be chosen", col)
		}
	}

	report := farm.AttachDevices(farm.BuildReport(nil), []inventory.Outcome{inventory.Answered(inventory.Android,
		[]inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1", Manufacturer: "samsung", OsVersion: "16", FormFactor: "phone"}}, nil)},
		"http://127.0.0.1:4723", nil, nil, nil)
	body, _ := json.Marshal(report.Devices[0].Descriptor)
	var published map[string]any
	json.Unmarshal(body, &published)
	for _, col := range cols {
		key := strings.ToLower(col[:1]) + col[1:]
		if published[key] == nil || published[key] == "" {
			t.Fatalf("AttachDevices must publish %s from the probe, got %v in %s", key, published[key], body)
		}
	}
}

func TestAnUnansweredFieldIsOmittedNotSentBlank(t *testing.T) {
	report := farm.AttachDevices(farm.BuildReport(nil), []inventory.Outcome{inventory.Answered(inventory.Android,
		[]inventory.Offerable{{ID: "X", Model: "SM-M346B1"}}, nil)}, "http://127.0.0.1:4723", nil, nil, nil)
	body, _ := json.Marshal(report.Devices[0].Descriptor)
	for _, absent := range []string{`"manufacturer"`, `"formFactor"`} {
		if strings.Contains(string(body), absent) {
			t.Fatalf("an unanswered field must be OMITTED, got %s", body)
		}
	}
	if !strings.Contains(string(body), `"osVersion":null`) {
		t.Fatalf("osVersion rides as null when unanswered (the farm's additive-null convention), got %s", body)
	}
}

// THE LEASE-MATCHES-THE-PROBE SEAL, agent half: the register body the agent sends for the operator's Samsung,
// built from the MEASURED props (see android/describe_test.go), pinned as a golden. The farm half
// (the farm's ARowThatCannotBeFilteredTests, Requires=SiblingRepo) registers this exact file and leases with a
// spec written from the same values — and with a spec written from the RACK node's row for the same phone.
func TestTheSamsungRegisterBodyIsTheGolden(t *testing.T) {
	report := farm.AttachDevices(farm.BuildReport([]inventory.Outcome{inventory.Answered(inventory.Android,
		[]inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1", Manufacturer: "samsung", OsVersion: "16", FormFactor: "phone"}}, nil)}),
		[]inventory.Outcome{inventory.Answered(inventory.Android,
			[]inventory.Offerable{{ID: "R5CT64M346B", Model: "SM-M346B1", Manufacturer: "samsung", OsVersion: "16", FormFactor: "phone"}}, nil)},
		"http://127.0.0.1:4723", nil, nil, nil)
	body, err := json.Marshal(farm.RegisterRequest{
		HostName: "Adas-MacBook-Pro.local", Devices: report.Devices, NodeID: "1fe13529-0000-4000-8000-000000000001",
		UnusableDevices: report.Unusable, PlatformOutcomes: report.Outcomes,
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "samsung-register.golden.json"))
	if err != nil {
		t.Fatalf("golden missing: %v", err)
	}
	if strings.TrimSpace(string(want)) != string(body) {
		t.Fatalf("the Samsung's register body moved:\n got: %s\nwant: %s", body, strings.TrimSpace(string(want)))
	}
	if !strings.Contains(string(body), `"model":"SM-M346B1"`) || strings.Contains(string(body), "SM_M346B1") {
		t.Fatal("the model on the wire must be ro.product.model, never adb's sanitised token")
	}
}

// ── CERTAINTY THAT IS NEVER SENT IS SILENCE ────────────────────────────────────────────────────────────────

func TestTheRegistrationAlwaysDeclaresTheMachinesCapture(t *testing.T) {
	// The farm's rule for a node that says NOTHING about capture is the optimistic HAR pull URL ("said nothing" is not
	// "said no" — correct, and untouched). So this agent is never silent: it says which of the farm's three words is true
	// of this machine (NodeCaptureAvailability), and the farm withholds the URL with a named reason — NodeCaptureDisabled
	// for "off here", NodeCannotCapture for "on and unable" — or hands it out. The farm-side half of this seal lives in
	// The farm (Requires=SiblingRepo).
	//
	// Until AN_AGENT_THAT_CAN_RECORD_ITS_PHONE this test was TestTheRegistrationDeclaresThatThisAgentCannotCapture and
	// pinned "Unavailable" on every machine. The agent can capture now, once a person turns it on, so off is the default
	// and the declaration is the machine's.
	registered := func(t *testing.T, report farm.Report) string {
		t.Helper()
		var received atomic.Value
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			received.Store(string(body))
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":{"nodeId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","apiKey":"k"}}`))
		}))
		defer stub.Close()
		client := &farm.Client{BaseURL: stub.URL, Token: "sk-agent-T", HTTP: stub.Client()}
		if _, status, err := client.Register(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "host", report); status != farm.CallOK {
			t.Fatalf("register: %v %v", status, err)
		}
		return received.Load().(string)
	}
	built := farm.BuildReport([]inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)})

	for _, c := range []struct {
		name   string
		report farm.Report
		want   string
	}{
		{"off until the machine says otherwise", built, `"captureAvailability":"Disabled"`},
		{"on, and this machine can run it", farm.DeclareCapture(built, farm.CaptureAvailable), `"captureAvailability":"Available"`},
		{"on, and this machine cannot run it now", farm.DeclareCapture(built, farm.CaptureUnavailable), `"captureAvailability":"Unavailable"`},
		{"switched off again", farm.DeclareCapture(farm.DeclareCapture(built, farm.CaptureAvailable), farm.CaptureDisabled), `"captureAvailability":"Disabled"`},
		// A word the farm does not know is never sent: the farm binds an unknown name to null — silence — and silence
		// gets the optimistic pull URL. So it is sent as the one answer that promises nothing.
		{"a word the farm does not know", farm.DeclareCapture(built, "Maybe"), `"captureAvailability":"Unavailable"`},
		// A report that carries no declaration at all (one not made by BuildReport) is still not silence.
		{"a report that carries no declaration", farm.Report{Devices: []farm.WireDevice{}}, `"captureAvailability":"Disabled"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := registered(t, c.report)
			if !strings.Contains(body, c.want) || strings.Count(body, "captureAvailability") != 1 {
				t.Fatalf("the agent must DECLARE capture exactly once as %s, got %s", c.want, body)
			}
		})
	}
}

// ── A RUN THAT CANNOT BE WATCHED MUST SAY SO ───────────────────────────────────────────────────────────────

func TestTheRegistrationDeclaresThatThisAgentCannotStream(t *testing.T) {
	// The same sentence as capture, about a different capability. This agent refuses every stream-start frame, so
	// a consumer leasing one of its devices must be told "no live view, and why" instead of being handed a mirror
	// that will never fill. Silence is untouched: an older agent that does not send the field keeps today's answer,
	// which is why the field is a pointer and omitempty on the wire.
	//
	// It is NOT a statement about who owns the device. The two agree today by construction — a customer's phone can
	// only reach the farm through this binary — but ownership is durable while this is a property of this month's
	// build, and the day the stream family is ported this line changes and ownership does not.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":{"nodeId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","apiKey":"k"}}`))
	}))
	defer stub.Close()

	client := &farm.Client{BaseURL: stub.URL, Token: "sk-agent-T", HTTP: stub.Client()}
	report := farm.BuildReport([]inventory.Outcome{inventory.Answered(inventory.Android, nil, nil)})
	if _, status, err := client.Register(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "host", report); status != farm.CallOK {
		t.Fatalf("register: %v %v", status, err)
	}

	body := received.Load().(string)
	if !strings.Contains(body, `"liveViewAvailability":"Unavailable"`) {
		t.Fatalf("the agent must DECLARE that it cannot stream a live view, got %s", body)
	}
	// AND IT MUST STILL DECLARE CAPTURE. Two standing facts, sent together, neither derived from the other — capture
	// is off until the machine turns it on, so the plain report says Disabled.
	if !strings.Contains(body, `"captureAvailability":"Disabled"`) {
		t.Fatalf("the live-view declaration must not have displaced the capture one, got %s", body)
	}
}

// THE OMITTED FIELD IS THE OLD AGENT. A pointer field with omitempty is what makes an older node's silence
// representable at all — if this ever became a plain string, every silent node would start shouting "Available"
// (the zero value is not one of the two answers, but the farm binds an unknown string to null, and a future
// rename to a valued default would be invisible). Pin the shape, not just the value.
func TestAnAgentThatSaysNothingAboutStreamingOmitsTheField(t *testing.T) {
	body, err := json.Marshal(farm.RegisterRequest{HostName: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "liveViewAvailability") {
		t.Fatalf("a request that declares nothing must OMIT the field, got %s", body)
	}
}

var received atomic.Value
