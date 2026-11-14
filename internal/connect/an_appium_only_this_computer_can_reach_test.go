package connect_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
)

// AN APPIUM ONLY THIS COMPUTER CAN REACH. connect drives an Appium that listens on more than loopback — refusing it would stop
// every customer who started Appium the way the README used to say — and says so ONCE, at the start, with the command that
// keeps it on this computer. It asks the kernel's own table where Appium listens (a seam here), never the network.
func TestConnectSaysOnceThatItsAppiumListensBeyondThisComputer(t *testing.T) {
	_, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 3)
	cfg.Probes = driverOutcomes
	asked := 0
	cfg.Driver = &connect.Driver{
		AppiumURL:     "http://127.0.0.1:4723",
		Probe:         func(context.Context) error { return nil },
		TunnelUp:      func() bool { return true },
		AlsoListensOn: func() []string { asked++; return []string{"0.0.0.0 (every address of this computer)"} },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"your Appium also listens on 0.0.0.0 (every address of this computer)",
		"another computer",
		"appium --address 127.0.0.1",
	} {
		if strings.Count(text, want) != 1 {
			t.Fatalf("connect says %q exactly once over three ticks, got:\n%s", want, text)
		}
	}
	if asked != 1 {
		t.Fatalf("where Appium listens is asked once, at the start: asked %d times", asked)
	}
	if !strings.Contains(text, "the drive path is ready") {
		t.Fatalf("it is a look, not a stop: the devices are still offered:\n%s", text)
	}
}

func TestConnectSaysNothingOfAnAppiumOnLoopback(t *testing.T) {
	_, server := newStubFarm(t)
	var out strings.Builder
	cfg := config(server, &out, 2)
	cfg.Probes = driverOutcomes
	cfg.Driver = &connect.Driver{
		AppiumURL:     "http://127.0.0.1:4723",
		Probe:         func(context.Context) error { return nil },
		TunnelUp:      func() bool { return true },
		AlsoListensOn: func() []string { return nil },
	}
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "also listens") {
		t.Fatalf("an Appium on loopback alone is said nothing of:\n%s", out.String())
	}
}
