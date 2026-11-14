package connect_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// The coloured variants of connect's start (golden_test.go): a 100-column terminal, colour on, the updating line kept.
func goldenOnATerminal(t *testing.T, name string, ready bool) {
	t.Helper()
	var out strings.Builder
	cfg, farmURL := goldenConfig(t, &out, ready)
	cfg.Console = ui.NewConsole(&out, ui.Mode{Color: true, Terminal: true, Columns: 100})
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Console.Done()
	golden(t, name, out.String(), farmURL)
}

func TestGoldenConnectReadyColour(t *testing.T) {
	goldenOnATerminal(t, "connect-ready.colour.golden", true)
}

func TestGoldenConnectWithOneBlockingProblemColour(t *testing.T) {
	goldenOnATerminal(t, "connect-blocked.colour.golden", false)
}

// THE RAW ERROR IS SHOWN ONLY WHEN ASKED: under --verbose, dim, after the three parts; never on its own and never by default.
func TestTheRawErrorIsShownOnlyWhenAsked(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		var out strings.Builder
		cfg, _ := goldenConfig(t, &out, false)
		cfg.Driver = &connect.Driver{AppiumURL: "http://127.0.0.1:4723",
			Probe:    func(context.Context) error { return errDial },
			TunnelUp: func() bool { return true }}
		cfg.Console = ui.NewConsole(&out, ui.Mode{})
		cfg.Console.Verbose = verbose
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if !strings.Contains(text, "is not answering (nothing is listening at its address)") {
			t.Fatalf("verbose=%v: the line says what the dial found, in words:\n%s", verbose, text)
		}
		raw := "  as it came: " + errDial.Error()
		if verbose != strings.Contains(text, raw) {
			t.Fatalf("verbose=%v: the raw error is shown exactly when asked for:\n%s", verbose, text)
		}
		if strings.Count(text, errDial.Error()) > 1 || (verbose && !strings.Contains(text, "  → start Appium there")) {
			t.Fatalf("verbose=%v: the raw error rides under the three parts, once:\n%s", verbose, text)
		}
	}
}

var errDial = dialError("dial tcp 127.0.0.1:4723: connect: connection refused")

type dialError string

func (e dialError) Error() string { return string(e) }
