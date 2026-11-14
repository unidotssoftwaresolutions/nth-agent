package connect_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
)

// ── AN_AGENT_THAT_SPEAKS_PLAINLY: connect's start, as golden text ─────────────────────────────────────────────────────
//
// Two screens — a machine ready to offer its phones, and one with the one thing that stops runs — each in two variants:
// plain (a pipe, a log, NO_COLOR; this file) and coloured on a terminal (golden_colour_test.go), escape codes and carriage
// returns written out as `\x1b` and `\r`. The farm is the stub farm on 127.0.0.1, written as https://farm.example so a
// golden does not hold a port. `-update` rewrites them.

var update = flag.Bool("update", false, "rewrite the golden screens under testdata")

const goldenNode = "11111111-2222-3333-4444-555555555555" // the stub farm answers with this id, so no "different id" line

func golden(t *testing.T, name, got, farmURL string) {
	t.Helper()
	visible := strings.NewReplacer("\x1b", `\x1b`, "\r", `\r`, farmURL, "https://farm.example").Replace(got)
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(visible), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden screen %s (run with -update to write it): %v", path, err)
	}
	if visible != string(want) {
		t.Fatalf("%s: the screen changed.\n got:\n%s\nwant:\n%s", name, visible, want)
	}
}

// twoAndroidPhones is a machine with two Android phones on its cable, and no iPhone.
func twoAndroidPhones(context.Context) []inventory.Outcome {
	return []inventory.Outcome{
		inventory.Answered(inventory.Android, []inventory.Offerable{
			{ID: "R5CT30ABCDE", Model: "SM-A546B", OsVersion: "14"},
			{ID: "39051FDJH00ABC", Model: "Pixel 8", OsVersion: "15"},
		}, nil),
		inventory.Answered(inventory.IOS, nil, nil),
	}
}

// goldenConfig is the stub farm, one heartbeat after the registration, a clock that stands still, and the agent's version.
func goldenConfig(t *testing.T, out *strings.Builder, ready bool) (connect.Config, string) {
	t.Helper()
	_, server := newStubFarm(t)
	cfg := config(server, out, 1)
	cfg.NodeID = goldenNode
	cfg.AgentVersion = "v0.5.4"
	cfg.Probes = twoAndroidPhones
	cfg.Now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 10, 0, time.UTC) }
	if ready {
		cfg.Driver = &connect.Driver{AppiumURL: "http://127.0.0.1:4723", Probe: func(context.Context) error { return nil },
			TunnelUp: func() bool { return true }, TunnelEverUp: func() bool { return true }}
	}
	return cfg, server.URL
}

func TestGoldenConnectReadyPlain(t *testing.T) {
	var out strings.Builder
	cfg, farmURL := goldenConfig(t, &out, true)
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	golden(t, "connect-ready.plain.golden", out.String(), farmURL)
}

func TestGoldenConnectWithOneBlockingProblemPlain(t *testing.T) {
	var out strings.Builder
	cfg, farmURL := goldenConfig(t, &out, false)
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	golden(t, "connect-blocked.plain.golden", out.String(), farmURL)
}
