// Package fleet stands in for many customers' machines at once: K connections to the farm, each the agent's own
// connect loop and drive tunnel (internal/connect, internal/tunnel — the same code a customer runs, not a copy of it),
// each offering D fake phones (loadtest/fakephone) as the workspace's own devices.
//
// WHAT IT CAN REACH, AND WHAT IT CANNOT. A connection's Appium address is the loopback listener this package opened for
// its own fake phones, and nothing else: there is no flag, variable or field that names another one. It never runs the
// agent's probes (no adb, no usbmuxd), never starts a live view (no mirror is offered), never captures, and never runs a
// program. The only addresses it dials are the farm's, and its own listeners.
package fleet

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/inventory"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/state"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
	"github.com/unidotssoftwaresolutions/nth-agent/loadtest/fakephone"
)

// Manufacturer is what every fake phone says made it: a name no person could take for a real maker.
const Manufacturer = "nthcase-loadtest"

// AgentVersion is what each connection declares to the farm, so an operator reading the farm's node list sees which
// nodes are the fleet's.
const AgentVersion = "loadtest"

// MaxConnections and MaxPhonesEach are the most this fleet will start: the farm's own per-workspace limits (ten nodes, ten
// tunnels, fifty devices a node), so a fleet that asks for more is refused here rather than measured against the farm's
// guard.
const (
	MaxConnections = 10
	MaxPhonesEach  = 50
)

// Config is one fleet.
type Config struct {
	FarmURL     string
	Credential  Credential // the workspace agent token, read from the environment only (secrets.go)
	Connections int        // K
	PhonesEach  int        // D
	// StateDir holds one folder per connection, each the agent's own state folder for that connection (its node id and its
	// lock). The same folder on the next start keeps the same node ids, so restarts do not count new nodes at the farm.
	StateDir string
	Phone    fakephone.Options
	Out      io.Writer

	// TEST SEAMS — no flag and no variable reaches them. Zero = the agent's own values.
	TLS        *tls.Config // the farm's TLS, for a test farm with its own certificate
	Interval   time.Duration
	LeaveBound time.Duration
	// StartGap is the wait between one connection's start and the next: the farm's door takes one registration a second
	// from one address (a burst of five), and a fleet that registered all at once would be measuring that door. Zero =
	// DefaultStartGap.
	StartGap time.Duration
}

// tlsCopy is the test farm's TLS for one user of it: an HTTP transport writes into the config it is given as it starts,
// so two transports must not share one.
func (c Config) tlsCopy() *tls.Config {
	if c.TLS == nil {
		return nil
	}
	return c.TLS.Clone()
}

// DefaultStartGap keeps the fleet's registrations under the farm door's pace for one address.
const DefaultStartGap = 1500 * time.Millisecond

// Phone is one fake phone as the farm will see it.
type Phone struct {
	Serial string
	Name   string
}

// PhonesFor names the phones of connection index k (from 1): numbered across the fleet, "LoadTest Phone 01" onward, and
// with serials that carry the connection's own node id, so two fleets never offer the same serial.
func PhonesFor(k, phonesEach int, nodeID string) []fakephone.Identity {
	short := strings.ReplaceAll(nodeID, "-", "")
	if len(short) > 8 {
		short = short[:8]
	}
	var phones []fakephone.Identity
	for d := 1; d <= phonesEach; d++ {
		n := (k-1)*phonesEach + d
		phones = append(phones, fakephone.Identity{
			Serial:       fmt.Sprintf("loadtest-%s-%02d", short, d),
			Model:        fmt.Sprintf("LoadTest Phone %02d", n),
			Manufacturer: Manufacturer,
			OsVersion:    fakephone.OsVersion,
		})
	}
	return phones
}

// Check refuses a fleet that cannot be started, before anything is dialled.
func (c Config) Check() error {
	switch {
	case c.Connections < 1 || c.Connections > MaxConnections:
		return fmt.Errorf("--connections must be 1 to %d (the farm counts at most %d nodes and %d tunnels for one workspace), not %d",
			MaxConnections, MaxConnections, MaxConnections, c.Connections)
	case c.PhonesEach < 1 || c.PhonesEach > MaxPhonesEach:
		return fmt.Errorf("--phones must be 1 to %d (the farm takes at most %d devices from one node of a workspace), not %d",
			MaxPhonesEach, MaxPhonesEach, c.PhonesEach)
	case c.StateDir == "":
		return errors.New("--state names the folder that keeps each connection's node id; it is required")
	}
	if err := c.Credential.Check(); err != nil {
		return err
	}
	if err := farm.ValidateBaseURL(c.FarmURL); err != nil {
		return err
	}
	return refuseTheAgentsOwnFolder(c.StateDir)
}

// refuseTheAgentsOwnFolder keeps the fleet out of the folder a real agent on this machine keeps its node id in: a fleet
// connection that took that id would take that machine's place at the farm.
func refuseTheAgentsOwnFolder(stateDir string) error {
	own, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return err
	}
	if abs == own || strings.HasPrefix(own+string(filepath.Separator), abs+string(filepath.Separator)) {
		return fmt.Errorf("--state %s holds this machine's own agent folder; give the fleet a folder of its own", stateDir)
	}
	return nil
}

// Run starts every connection, and returns once ctx ends and every connection has stopped and said goodbye.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.Check(); err != nil {
		return err
	}
	out := &lockedWriter{w: cfg.Out}
	fmt.Fprintf(out, "fleet: %d connection(s) × %d fake phone(s) to %s (the token is %d characters; it is never printed)\n",
		cfg.Connections, cfg.PhonesEach, cfg.FarmURL, cfg.Credential.Length())

	var conns []*connection
	stopAll := func() {
		for _, c := range conns {
			c.close()
		}
	}
	for k := 1; k <= cfg.Connections; k++ {
		c, err := open(cfg, k, out)
		if err != nil {
			stopAll()
			return err
		}
		conns = append(conns, c)
	}
	gap := cfg.StartGap
	if gap <= 0 {
		gap = DefaultStartGap
	}
	var wg sync.WaitGroup
	errs := make([]error, len(conns))
	for i, c := range conns {
		if i > 0 {
			timer := time.NewTimer(gap)
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
			timer.Stop()
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.run(ctx, cfg)
		}()
	}
	wg.Wait()
	for _, c := range conns {
		if summary := c.appium.Stats().Summary(); summary != "" {
			fmt.Fprintf(c.out, "what its fake phones were asked: %s\n", summary)
		}
	}
	stopAll()
	fmt.Fprintln(out, "fleet: stopped")
	return errors.Join(errs...)
}

// connection is one stand-in machine: a state folder, a node id, a fake Appium with D phones, a connect loop and a tunnel.
type connection struct {
	index    int
	name     string
	nodeID   string
	release  func()
	listener net.Listener
	server   *http.Server
	appium   *fakephone.Server
	out      io.Writer
}

func open(cfg Config, k int, out io.Writer) (*connection, error) {
	name := fmt.Sprintf("conn %02d", k)
	dir := filepath.Join(cfg.StateDir, fmt.Sprintf("conn-%02d", k))
	release, err := state.HoldConnect(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	nodeID, err := state.LoadOrCreateNodeID(dir)
	if err != nil {
		release()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	// THE ONLY APPIUM THIS CONNECTION KNOWS: a listener it opened itself, on loopback, serving its fake phones.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		release()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	appium := fakephone.NewServer(PhonesFor(k, cfg.PhonesEach, nodeID), cfg.Phone)
	server := &http.Server{Handler: appium, ReadHeaderTimeout: 10 * time.Second}
	go server.Serve(listener)
	return &connection{index: k, name: name, nodeID: nodeID, release: release, listener: listener, server: server,
		appium: appium, out: &prefixWriter{prefix: name + ": ", w: out}}, nil
}

func (c *connection) appiumURL() string { return "http://" + c.listener.Addr().String() }

func (c *connection) close() {
	if c.server != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		c.server.Shutdown(shutdown)
		cancel()
		c.server = nil
	}
	if c.release != nil {
		c.release()
		c.release = nil
	}
}

// offered is this connection's fake phones as the agent's probes would report them: Android answered, every phone offerable;
// iOS not asked.
func (c *connection) offered() []inventory.Outcome {
	var phones []inventory.Offerable
	for _, p := range c.appium.Phones() {
		phones = append(phones, inventory.Offerable{ID: p.Serial, Model: p.Model, Manufacturer: p.Manufacturer,
			OsVersion: p.OsVersion, FormFactor: "phone"})
	}
	return []inventory.Outcome{inventory.Answered(inventory.Android, phones, nil), inventory.NotAsked(inventory.IOS)}
}

func (c *connection) run(ctx context.Context, cfg Config) error {
	sleep := func(ctx context.Context, d time.Duration) {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	phones := &allowed.Phones{}
	tunnelState := tunnel.NewState()
	appiumURL := c.appiumURL()
	drive := tunnel.New(tunnel.Config{
		FarmURL: cfg.FarmURL, NodeID: c.nodeID, Token: cfg.Credential.secret(), AppiumURL: appiumURL,
		State: tunnelState, Phones: phones, Sleep: sleep, Out: c.out,
		Builds:          tunnel.BuildShelf{In: filepath.Join(cfg.StateDir, fmt.Sprintf("conn-%02d", c.index))},
		TLSClientConfig: cfg.tlsCopy(),
	})
	var dialOnce sync.Once
	var registered bool
	var mu sync.Mutex
	interval := cfg.Interval
	if interval <= 0 {
		interval = connect.DefaultInterval
	}
	// THE AGENT'S OWN CLIENT, as main builds it; only a test farm's certificate needs a transport of its own.
	httpClient := &http.Client{Timeout: 15 * time.Second}
	if cfg.TLS != nil {
		httpClient.Transport = &http.Transport{TLSClientConfig: cfg.tlsCopy(), Proxy: http.ProxyFromEnvironment}
	}
	client := &farm.Client{BaseURL: cfg.FarmURL, Token: cfg.Credential.secret(), HTTP: httpClient}
	err := connect.Run(ctx, connect.Config{
		AgentVersion:   AgentVersion,
		Client:         client,
		NodeID:         c.nodeID,
		HostName:       fmt.Sprintf("nthcase-loadtest-%02d", c.index),
		Probes:         func(context.Context) []inventory.Outcome { return c.offered() },
		Interval:       interval,
		BackoffBase:    time.Second,
		BackoffCeiling: time.Minute,
		Sleep:          sleep,
		Out:            c.out,
		Driver: &connect.Driver{
			AppiumURL:    appiumURL,
			Probe:        func(context.Context) error { return c.appium.Alive() },
			TunnelUp:     drive.Connected,
			TunnelEverUp: drive.EverConnected,
			Evidence:     tunnelState,
		},
		OnRegistered: func() {
			mu.Lock()
			registered = true
			mu.Unlock()
			dialOnce.Do(func() { go drive.Run(ctx) })
		},
		Phones: phones,
	})
	mu.Lock()
	said := registered
	mu.Unlock()
	if said {
		// THE FLEET TAKES ITS PHONES OFF THE FARM WHEN IT STOPS, as the agent does: a goodbye, bounded, and the farm's
		// heartbeat window covers one it does not take.
		bound := cfg.LeaveBound
		if bound <= 0 {
			bound = 3 * time.Second
		}
		leave, cancel := context.WithTimeout(context.Background(), bound)
		status, leaveErr := client.Leave(leave, c.nodeID)
		cancel()
		if status == farm.CallOK {
			fmt.Fprintln(c.out, "told the farm this node is leaving — its fake phones are off offer there now")
		} else {
			fmt.Fprintf(c.out, "the farm did not take the goodbye (%v); its phones go off offer when its heartbeat window lapses\n", leaveErr)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", c.name, err)
	}
	return nil
}

// lockedWriter keeps the connections' lines whole.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// prefixWriter puts the connection's name in front of each line it says.
type prefixWriter struct {
	mu      sync.Mutex
	prefix  string
	w       io.Writer
	pending []byte
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = append(p.pending, b...)
	for {
		i := strings.IndexByte(string(p.pending), '\n')
		if i < 0 {
			break
		}
		if _, err := io.WriteString(p.w, p.prefix+string(p.pending[:i+1])); err != nil {
			return 0, err
		}
		p.pending = p.pending[i+1:]
	}
	return len(b), nil
}
