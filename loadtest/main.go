// Command loadtest is the phone path's load harness: `fleet` stands in for customers' machines and phones, and `drive`
// starts runs against them through the product's public API. It is built on its own (go build -o nth-loadtest
// ./loadtest) and is never part of the published agent: the release builds ./cmd/nth-agent only, and the npm package
// carries that binary only (a seal in this directory holds both).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/drive"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fakephone"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fleet"
)

const usage = `loadtest — the phone path's load harness. Nothing here is the agent a customer runs.

  loadtest fleet [--connections K] [--phones D] [--state DIR] [--delay-scale X]
      Connects K stand-in machines to the farm in ` + fleet.FarmVariable + `, each offering D fake phones as the
      workspace's own devices, with the workspace agent token in ` + fleet.TokenVariable + ` (read from the
      environment only, never printed). Stops on SIGTERM or Ctrl-C and takes its phones off the farm.

  loadtest drive --i-know-this-is-production --scenario ID|WS=ID,WS=ID [--steps 4,8,12,16,20] [--step-bound 20m]
      Starts runs of one scenario through the product's public API at ` + drive.BaseVariable + `, with the API key in
      ` + drive.KeyVariable + ` (environment only, never printed), step by step, and prints each step's results.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "fleet":
		return runFleet(ctx, args[1:], stdout, stderr, getenv)
	case "drive":
		return runDrive(ctx, args[1:], stdout, stderr, getenv)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}

func runFleet(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("fleet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home, _ := os.UserHomeDir()
	connections := flags.Int("connections", 2, "stand-in machines (each one node and one tunnel at the farm)")
	phones := flags.Int("phones", 5, "fake phones each machine offers")
	stateDir := flags.String("state", filepath.Join(home, ".nth-loadtest"), "folder that keeps each connection's node id")
	scale := flags.Float64("delay-scale", 1, "multiplies every fake phone's answer time (1 = a real phone's)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	options := fakephone.DefaultOptions()
	options.DelayScale = *scale
	err := fleet.Run(ctx, fleet.Config{
		FarmURL:     getenv(fleet.FarmVariable),
		Credential:  fleet.CredentialFromEnvironment(getenv),
		Connections: *connections,
		PhonesEach:  *phones,
		StateDir:    *stateDir,
		Phone:       options,
		Out:         stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runDrive(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("drive", flag.ContinueOnError)
	flags.SetOutput(stderr)
	production := flags.Bool("i-know-this-is-production", false, "required: every run started is a real run, charged to the workspace")
	scenario := flags.String("scenario", "", "the scenario to run: its id, or workspace-id=scenario-id pairs separated by commas (one per workspace)")
	steps := flags.String("steps", "4,8,12,16,20", "how many runs at once, step by step")
	bound := flags.Duration("step-bound", 20*time.Minute, "the longest one step may take before its unfinished runs are counted as such")
	poll := flags.Duration("poll", 15*time.Second, "how often each run is read (each read counts against the key's 120 a minute)")
	planVersion := flags.Int("plan-version", 0, "the plan version the runs replay (0 = the scenario's own)")
	baseline := flags.Duration("baseline-p95", 0, "the 4-at-once step's p95 duration, when the steps run one invocation at a time")
	noPause := flags.Bool("no-pause", false, "go to the next step without waiting for Enter")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	plan, err := drive.ParseSteps(*steps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var pause io.Reader = os.Stdin
	if *noPause {
		pause = nil
	}
	err = drive.Run(ctx, drive.Config{
		Production:  *production,
		BaseURL:     getenv(drive.BaseVariable),
		Keys:        drive.KeyFromEnvironment(getenv),
		ScenarioID:  *scenario,
		Steps:       plan,
		StepBound:   *bound,
		Poll:        *poll,
		PlanVersion: *planVersion,
		BaselineP95: *baseline,
		Pause:       pause,
		Out:         stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, drive.ErrNotConfirmed) {
			return 2
		}
		return 1
	}
	return 0
}
