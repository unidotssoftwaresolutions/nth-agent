package capture

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── THE CAPTURE CONTROLLER: ONLY WHERE EVERY GATE HOLDS, AND ALWAYS GIVING THE PHONE BACK ───────────────────────
//
// The farm's rack node's NetworkCaptureController, rebuilt for a customer's machine. The farm asks; this machine's own gates
// decide:
//
//   GATE 1  the machine has capture ON (`capture enable` — the mark, and a CA it may use);
//   GATE 2  the leased phone was SET UP here (`capture setup`), for this CA;
//   GATE 3  the farm sent a capture start for that lease.
//
// A start that fails a gate touches nothing, and its reason is kept for the lease: when the run pulls its capture, that
// sentence is the answer it gets (the farm hands a node's answer to the run), so a person reading the run learns what
// happened on the machine and what to do there.
//
// THE PHONE IS ALWAYS GIVEN BACK (A_CAPTURE_THAT_ALWAYS_GIVES_THE_PHONE_BACK, ported). Before a phone is touched, what
// will change on it is written down (Store.writeRecord); the record goes only once the phone reads back with no proxy of
// ours. Phones are handed back when a capture stops, when the farm asks, on a timer, at start-up, and when capture is
// turned off — and when the agent is told to exit. This agent never runs adb root and never touches a phone's
// certificate store: the CA a person installed stays until they remove it, and that is not "not clean".

const (
	// MaxConcurrent is how many phones one machine captures at once — the node's number.
	MaxConcurrent = 4
	// MaxCapture is how long a capture may run before it is stopped and its phone handed back — the node's bound for a
	// stop that never came (CaptureOptions.MaxSessionSeconds).
	MaxCapture = 4 * time.Hour
	// Retention is how long a capture file stays on this machine after its capture stops when nobody pulls it — the
	// operator's hour.
	Retention = time.Hour
	// HandbackBound is the whole of one handback the farm asks for — the farm's own bound for the question
	// (NodePhoneStateBounds.CaptureHandbackTimeout), which it waits a margin beyond.
	HandbackBound = 3 * time.Minute

	// sayOnceBound is the most this controller keeps per farm-chosen key — lines said once, outcomes kept for a pull, leases
	// ended before they started — before it starts over: the node's own backstop for its outcomes (4096), for all three.
	sayOnceBound = 4096

	markEvery = 2 * time.Second
	passEvery = 30 * time.Second
)

// Config wires a controller. Every seam is a parameter, so the whole lifecycle runs in a test on a fake adb.
type Config struct {
	Store        Store
	Adb          Adb
	Out          io.Writer // the console: one line when something changes
	AgentVersion string
	Now          func() time.Time
	// LookPath finds adb, for the declaration; nil = exec.LookPath. GOOS is runtime.GOOS unless a test says otherwise.
	LookPath func(string) (string, error)
	GOOS     string
	// PortFree checks a capture port; nil = liveview.LoopbackFree, the agent's own loopback check.
	PortFree func(uint16) bool
	// UpstreamRoots is a TEST SEAM for the proxy's upstream leg; nil = the system's roots.
	UpstreamRoots *x509.CertPool
	// PeerCheck proves, before a phone is touched, that this machine can tell which account opens a connection to the
	// capture proxy; nil = netcap.PeerCheckWorks. PeerOwner names that account for each connection; nil =
	// netcap.OwnerOfPeer. TEST SEAMS: the proxy refuses every other account's connection either way (netcap/peer.go).
	PeerCheck func() error
	PeerOwner netcap.PeerOwner
	// ReachLocal is the customer's netcap.AllowLocalFlag: a capture may reach this machine and its local network. False,
	// the default, keeps it to the internet (netcap/destination.go); true is said on screen at every capture's start.
	ReachLocal bool
	// MarkEvery, PassEvery, Retention and MaxCapture override the constants (test seams; zero = the constant).
	MarkEvery, PassEvery, Retention, MaxCapture time.Duration
}

// Controller runs this machine's captures.
type Controller struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*session
	gates    map[string]*sync.Mutex
	outcomes map[string]outcome
	said     map[string]string
	// ended is every lease a stop or the farm's handback question found no capture for (A_FARM_THE_AGENT_DOES_NOT_TAKE_ON_TRUST):
	// a start for it that arrives later touches nothing. Written and read under mu, in the same section as the
	// reservation, so either the start reserved first and is stopped, or it finds the lease here.
	ended map[string]time.Time
	wasOn bool
}

type session struct {
	lease, serial string
	port          int
	started       time.Time
	stopped       time.Time // zero while it runs
	settled       bool      // the start has finished with the phone
	pulled        bool

	// Under the phone's gate:
	proxy  *netcap.Proxy
	rec    *netcap.Recording
	record Record
	lock   *phoneLock
}

type outcome struct {
	sentence string
	at       time.Time
}

// NewController builds a controller; Run starts its timers.
func NewController(cfg Config) *Controller {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.PortFree == nil {
		cfg.PortFree = liveview.LoopbackFree
	}
	if cfg.PeerCheck == nil {
		cfg.PeerCheck = netcap.PeerCheckWorks
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	// EVERYTHING THIS CONTROLLER SAYS goes through the agent's terminal filter.
	cfg.Out = farm.TerminalWriter(cfg.Out)
	if cfg.MarkEvery <= 0 {
		cfg.MarkEvery = markEvery
	}
	if cfg.PassEvery <= 0 {
		cfg.PassEvery = passEvery
	}
	if cfg.Retention <= 0 {
		cfg.Retention = Retention
	}
	if cfg.MaxCapture <= 0 {
		cfg.MaxCapture = MaxCapture
	}
	return &Controller{cfg: cfg, sessions: map[string]*session{}, gates: map[string]*sync.Mutex{},
		outcomes: map[string]outcome{}, said: map[string]string{}, ended: map[string]time.Time{}}
}

// Supported says whether this system can run capture: the systems this agent ships for, where it can keep the CA's key
// to one user and lock a phone between processes.
func Supported(goos string) bool { return (goos == "darwin" || goos == "linux") && lockingSupported }

// Availability is this machine's capture declaration for the farm, in the farm's own words (NodeCaptureAvailability):
// Disabled when capture is off here; Unavailable when it is on and this machine cannot run it now (no adb, or a CA it
// may not use); Available otherwise. It reads the CA's CERTIFICATE and the key file's mode, never the key.
func (c *Controller) Availability() string {
	if _, on := c.cfg.Store.Enabled(); !on {
		return farm.CaptureDisabled
	}
	if c.caProblem() != "" {
		return farm.CaptureUnavailable
	}
	if _, err := c.cfg.LookPath("adb"); err != nil {
		return farm.CaptureUnavailable
	}
	return farm.CaptureAvailable
}

// Readiness is ONE PHONE's capture readiness for the farm, in the farm's own words (PhoneCaptureReadiness), read from
// the gates a capture start reads — so the farm can withhold the pull URL, and say why, before a run starts:
//
//   - farm.PhoneCaptureReady: this machine's capture is Available, and the phone was set up here, for this CA;
//   - farm.PhoneCaptureNotSetUp: this machine's capture is Available, and the phone was never set up here, or was set up
//     for an earlier CA — either way `nth-agent capture setup <serial>` on this machine is what it needs;
//   - "" (say nothing): a phone capture does not run on (not an adb serial, or an iPhone), and every phone while the
//     machine's own declaration is Disabled or Unavailable — that word already says why nothing is captured here, and it
//     is the one a person acts on first.
//
// It reads the CA's CERTIFICATE and the phones list, never the key — like Availability.
func (c *Controller) Readiness(serial string) string {
	if !IsAdbSerial(serial) || looksLikeAnIPhone(serial) || c.Availability() != farm.CaptureAvailable {
		return ""
	}
	cert, err := netcap.ReadCertificate(c.cfg.Store.CADir())
	if err != nil {
		return ""
	}
	if phone, setUp := c.cfg.Store.Phone(serial); setUp && phone.CAFingerprint == netcap.Fingerprint(cert) {
		return farm.PhoneCaptureReady
	}
	return farm.PhoneCaptureNotSetUp
}

// caProblem says why the CA cannot be used for a capture now, from its certificate and its files' modes alone.
func (c *Controller) caProblem() string {
	if !Supported(c.cfg.GOOS) {
		return "this system cannot run network capture"
	}
	cert, err := netcap.ReadCertificate(c.cfg.Store.CADir())
	if err != nil {
		return "its certificate cannot be read"
	}
	now := c.cfg.Now()
	if now.After(cert.NotAfter) || now.Before(cert.NotBefore) {
		return "it is outside the year it is valid for"
	}
	for _, check := range []struct {
		path   string
		folder bool
	}{{c.cfg.Store.CADir(), true}, {netcap.KeyPathIn(c.cfg.Store.CADir()), false}} {
		info, err := os.Lstat(check.path)
		if err != nil || info.IsDir() != check.folder || info.Mode().Perm()&0o077 != 0 || (!check.folder && !info.Mode().IsRegular()) {
			return "its key or its folder is missing or readable by other users"
		}
	}
	return ""
}

// ── a capture ─────────────────────────────────────────────────────────────────────────────────────────────────

// Start is the farm's capture start for lease on serial. It never touches the phone unless every gate holds, and the
// record of what it will change is on disk first.
func (c *Controller) Start(ctx context.Context, lease, serial string) {
	lease = strings.ToLower(strings.TrimSpace(lease))
	if !netcap.IsLease(lease) {
		return // not a frame the farm sends: there is no lease to answer for
	}
	refuse := func(sentence string) {
		c.keepOutcome(lease, sentence)
		// The serial is the farm's: folded to one line and cut before it is said.
		c.sayOnce("refused "+lease, "a run asked for network capture on phone %s and it was not started — %s", farm.SafeForTerminal(serial, ""), sentence)
	}
	switch {
	case !IsAdbSerial(serial):
		refuse(refusedNotAndroid)
		return
	case looksLikeAnIPhone(serial):
		refuse(refusedNotAndroid)
		return
	}
	if _, on := c.cfg.Store.Enabled(); !on || !Supported(c.cfg.GOOS) {
		refuse(refusedOff())
		return
	}
	cert, err := netcap.ReadCertificate(c.cfg.Store.CADir())
	if err != nil || c.caProblem() != "" {
		refuse(refusedCA())
		return
	}
	phone, setUp := c.cfg.Store.Phone(serial)
	if !setUp {
		refuse(refusedNotSetUp(serial))
		return
	}
	if phone.CAFingerprint != netcap.Fingerprint(cert) {
		refuse(refusedOldCA(serial))
		return
	}
	// THE PROXY ASKS WHO CONNECTS, and on a machine whose kernel cannot be asked it would refuse the phone's every
	// connection — so that is found out here, before anything is reserved or the phone is touched.
	if c.cfg.PeerCheck() != nil {
		refuse(refusedPeerCheck)
		return
	}

	now := c.cfg.Now()
	c.mu.Lock()
	if _, exists := c.sessions[lease]; exists {
		c.mu.Unlock()
		return // a start sent twice is one start
	}
	if _, ended := c.ended[lease]; ended {
		// THE FARM WAS ALREADY TOLD: its handback question was answered, or a stop came, before this start could
		// reserve anything — the phone may be offered again already. Nothing is touched.
		c.mu.Unlock()
		refuse(refusedEnded)
		return
	}
	if c.liveOnLocked(serial) != nil {
		c.mu.Unlock()
		refuse(refusedLive(serial))
		return
	}
	if c.liveCountLocked() >= MaxConcurrent {
		c.mu.Unlock()
		refuse(refusedAt4)
		return
	}
	port := c.choosePortLocked(serial)
	if port == 0 {
		c.mu.Unlock()
		refuse(refusedNoPort)
		return
	}
	s := &session{lease: lease, serial: serial, port: port, started: now}
	c.sessions[lease] = s // reserved: the lease and the port are this start's
	c.mu.Unlock()

	gate := c.gate(serial)
	gate.Lock()
	defer gate.Unlock()
	if c.stoppedBeforeTouching(s) {
		return
	}

	lock, err := c.cfg.Store.tryLockPhone(serial)
	if err != nil {
		c.drop(s)
		refuse(refusedBusy(serial))
		return
	}
	s.lock = lock

	// THE PROXY'S READ OF THE KEY — the one place a capture reads it.
	ca, err := netcap.LoadCA(c.cfg.Store.CADir(), now)
	if err != nil {
		c.abandon(s)
		refuse(refusedCA())
		return
	}
	rec, err := netcap.NewRecording(c.cfg.Store.FilesDir(), lease, c.cfg.AgentVersion, c.cfg.Now)
	if err != nil {
		c.abandon(s)
		refuse(refusedFile)
		return
	}
	s.rec = rec
	proxy := netcap.NewProxy(netcap.Options{CA: ca, Recording: rec, UpstreamRoots: c.cfg.UpstreamRoots, Now: c.cfg.Now,
		ReachLocal: c.cfg.ReachLocal, PeerOwner: c.cfg.PeerOwner, Refused: func(uid int, found bool) {
			if found {
				c.sayOnce("peer "+lease, "network capture on phone %s: a connection to its proxy from another account on this machine (uid %d) was refused — only this account and the administrator may use it", serial, uid)
			} else {
				c.sayOnce("peer "+lease, "network capture on phone %s: a connection to its proxy was refused, because this machine could not tell which account opened it", serial)
			}
		}})
	if _, err := proxy.Listen(uint16(port)); err != nil {
		c.abandon(s)
		refuse(refusedNoPort)
		return
	}
	go proxy.Serve()
	s.proxy = proxy

	// WRITTEN DOWN BEFORE THE PHONE IS TOUCHED: a record that cannot be written is a capture that does not start.
	record := Record{Serial: serial, Lease: lease, Kind: KindCapture, Port: port, Proxy: ProxyValue(port), Reverse: true, Written: now}
	if err := c.cfg.Store.writeRecord(record); err != nil {
		c.abandon(s)
		refuse(refusedNotRecorded)
		return
	}
	s.record = record

	// CAPTURE IS STILL ON, read again now that this start can be seen by every stopper. `capture disable` removes
	// the mark first and then reads the records once: a start that read the mark before that and gets here after it is
	// undone here, before the phone is touched; one that gets here before it has a record disable reads, and a session
	// the next tick of watchMark stops.
	if _, on := c.cfg.Store.Enabled(); !on {
		c.abandon(s)
		refuse(refusedOff())
		return
	}

	// The phone, from here: the reverse first (the proxy it leads to is already listening), then the phone's proxy.
	if _, _, exit, _ := c.cfg.Adb(ctx, reverseAddArgs(serial, port)...); exit != 0 {
		c.failedOnThePhone(ctx, s)
		refuse(refusedReverse)
		return
	}
	if _, _, exit, _ := c.cfg.Adb(ctx, proxySetArgs(serial, port)...); exit != 0 {
		c.failedOnThePhone(ctx, s)
		refuse(refusedProxy)
		return
	}
	c.mu.Lock()
	s.settled = true
	delete(c.outcomes, lease)
	c.mu.Unlock()
	c.say("network capture started on phone %s for a run — while it runs, every app's traffic on that phone goes through this machine (127.0.0.1:%d)", serial, port)
	if c.cfg.ReachLocal {
		c.say(LocalNetworkOpen)
	}
}

// Stop ends lease's capture and hands its phone back — the farm's capture-stop, a capture past its bound, capture
// turned off, the agent exiting. The capture's file is kept for its pull or its hour.
func (c *Controller) Stop(ctx context.Context, lease string) {
	lease = strings.ToLower(strings.TrimSpace(lease))
	c.mu.Lock()
	s := c.sessions[lease]
	if s == nil {
		c.endedLocked(lease)
	}
	c.mu.Unlock()
	if s == nil {
		return
	}
	gate := c.gate(s.serial)
	gate.Lock()
	defer gate.Unlock()
	c.mu.Lock()
	if !s.stopped.IsZero() {
		c.mu.Unlock()
		return
	}
	s.stopped = c.cfg.Now()
	settled := s.settled
	c.mu.Unlock()
	if !settled {
		return // its start has not reached the phone, and sees the stop when it does
	}
	handedBack, observation := c.handBackLocked(ctx, s.record, s)
	entries := recordedAndMasked(0, 0)
	if s.rec != nil {
		entries = recordedAndMasked(s.rec.Recorded(), s.rec.Seen())
	}
	if handedBack {
		c.say("network capture on phone %s stopped: %s; the phone was handed back", s.serial, entries)
	} else {
		c.say("network capture on phone %s stopped: %s; the phone could not be handed back yet (%s)", s.serial, entries, observation)
	}
}

// stoppedBeforeTouching ends a start that a stop reached first: nothing was set on the phone, so nothing is handed back.
func (c *Controller) stoppedBeforeTouching(s *session) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.stopped.IsZero() {
		return false
	}
	s.settled = true
	delete(c.sessions, s.lease)
	return true
}

// abandon undoes a start that did not reach the phone: its proxy, its file (nothing was captured into it), its record
// and its lock.
func (c *Controller) abandon(s *session) {
	if s.proxy != nil {
		_ = s.proxy.Close()
	}
	if s.rec != nil {
		_ = s.rec.Remove()
	}
	_ = c.cfg.Store.removeRecord(s.lease)
	c.drop(s)
}

// failedOnThePhone undoes a start the phone refused part-way: the phone is handed back exactly as a stop would, the empty
// capture file goes, and the start is forgotten. The record stays only if the phone could not be handed back.
func (c *Controller) failedOnThePhone(ctx context.Context, s *session) {
	rec := s.rec
	c.handBackLocked(ctx, s.record, s)
	if rec != nil {
		_ = rec.Remove()
	}
	c.drop(s)
}

// drop forgets a start that did not become a capture.
func (c *Controller) drop(s *session) {
	if s.lock != nil {
		s.lock.unlock()
		s.lock = nil
	}
	c.mu.Lock()
	delete(c.sessions, s.lease)
	c.mu.Unlock()
}

// ── the handback ─────────────────────────────────────────────────────────────────────────────────────────────

// handBackLocked hands back one record for this controller: THE ONE SEQUENCE (handBackSteps), with the session's proxy
// stopped in the middle of it, and what could not be finished said once. The caller holds the phone's gate.
func (c *Controller) handBackLocked(ctx context.Context, r Record, s *session) (bool, string) {
	defer c.release(s)
	ok, observation := handBackSteps(ctx, c.cfg.Store, c.cfg.Adb, c.cfg.Now, r, func() { c.release(s) })
	if ok {
		c.mu.Lock()
		delete(c.said, "kept "+r.Lease)
		c.mu.Unlock()
		return true, ""
	}
	if strings.HasPrefix(observation, "the phone was not reachable") {
		c.sayOnce("kept "+r.Lease, "%s", strings.TrimRight(Unreachable(r.Serial, r.Proxy), "\n"))
	} else {
		c.sayOnce("kept "+r.Lease, "phone %s is not handed back yet: %s — this machine keeps trying", r.Serial, observation)
	}
	return false, observation
}

// handbackTally is one handback's phones: those a record was handed back for, and those still kept. A phone is said to be
// handed back once — after its records are, and only when none of them was kept — whichever path handed it back: start-up and
// the timer (HandBackOrphans), or the farm's question (HandBackForFarm). A capture's own stop says it in its own line.
type handbackTally map[string]bool

func (t handbackTally) add(serial string, handedBack bool) {
	if kept, seen := t[serial]; !seen || !kept {
		t[serial] = !handedBack // true = kept
	}
}

func (t handbackTally) say(c *Controller) {
	serials := make([]string, 0, len(t))
	for serial, kept := range t {
		if !kept {
			serials = append(serials, serial)
		}
	}
	sort.Strings(serials)
	for _, serial := range serials {
		c.say("phone %s was handed back: the proxy setting a capture left on it is cleared", serial)
	}
}

// release stops what a session holds on this machine: its proxy, its file's handle (the file is kept), and its lock.
func (c *Controller) release(s *session) {
	if s == nil {
		return
	}
	c.mu.Lock()
	delete(c.said, "peer "+s.lease)
	c.mu.Unlock()
	if s.proxy != nil {
		_ = s.proxy.Close()
		s.proxy = nil
	}
	if s.rec != nil {
		s.rec.Close()
	}
	if s.lock != nil {
		s.lock.unlock()
		s.lock = nil
	}
}

// HandBackOrphans hands back every record no capture of this process is running for — a capture from before a restart,
// a trust check whose process died, a phone that was out of reach. A phone another process holds is left to it. Each phone
// handed back is said once (handedBack).
func (c *Controller) HandBackOrphans(ctx context.Context) int {
	records, unreadable := c.cfg.Store.Records()
	for _, path := range unreadable {
		c.sayOnce("unreadable "+path, "the handback record %s cannot be read, or is not one this agent writes, so the phone it names cannot be handed back by this agent: check that phone's proxy setting by hand, then delete the file", path)
	}
	handedBack := 0
	phones := handbackTally{}
	defer phones.say(c)
	for _, r := range records {
		c.mu.Lock()
		live := c.sessions[r.Lease] != nil && c.sessions[r.Lease].stopped.IsZero()
		other := c.liveOnLocked(r.Serial) != nil
		c.mu.Unlock()
		if live || other {
			continue
		}
		gate := c.gate(r.Serial)
		gate.Lock()
		lock, err := c.cfg.Store.tryLockPhone(r.Serial)
		if err != nil {
			gate.Unlock()
			continue // another process is changing this phone right now; the next pass tries
		}
		if c.cfg.Store.holdsRecord(r.Lease) {
			ok, _ := c.handBackLocked(ctx, r, &session{lock: lock})
			phones.add(r.Serial, ok)
			if ok {
				handedBack++
			}
		} else {
			lock.unlock()
		}
		gate.Unlock()
	}
	return handedBack
}

// HandBackForFarm answers the farm's handback question (NodeCaptureHandbackRequest) the way the node does: the capture is
// stopped if it still runs, every record of the phone is handed back, and the phone itself is read. Clean only when no
// record of the phone remains and its proxy is not ours. The capture CA a person installed on the phone is theirs, and
// does not make it "not clean".
func (c *Controller) HandBackForFarm(ctx context.Context, udid, lease string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, HandbackBound)
	defer cancel()
	lease = strings.ToLower(strings.TrimSpace(lease))
	c.mu.Lock()
	s := c.sessions[lease]
	if s == nil {
		c.endedLocked(lease)
	}
	c.mu.Unlock()
	if s != nil && s.serial == udid {
		c.Stop(ctx, lease)
	}

	// A PHONE THIS MACHINE HAS NEVER HAD — no capture, no record, not set up here, not attached now — is answered
	// from adb as before, and nothing is kept for it: no gate, and no lock file in the customer's folder.
	if !c.knows(ctx, udid) {
		return c.answerFromThePhone(ctx, udid, "")
	}

	gate := c.gate(udid)
	gate.Lock()
	defer gate.Unlock()
	c.mu.Lock()
	running := c.liveOnLocked(udid)
	c.mu.Unlock()
	if running != nil {
		if running.lease == lease {
			return false, "the capture is still starting on this phone; it is handed back as soon as it has started"
		}
		return false, "another capture is running on this phone, so this machine leaves it as it is until that capture stops"
	}
	lock, err := c.cfg.Store.lockPhone(ctx, udid)
	if err != nil {
		return false, "another nth-agent process on this phone's machine is changing it right now (setting it up for capture, or turning capture off)"
	}
	defer lock.unlock()

	var keptBecause string
	// THE FARM'S QUESTION SAYS IT TOO (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 3). A phone kept because it was out of reach is asked
	// about by the farm every half minute and handed back by this machine's own pass every half minute; whichever reaches it first
	// once it is plugged back in clears its record. Only the pass said so — so on the acceptance run the farm's question won, the
	// record went, and the line never came.
	phones := handbackTally{}
	defer phones.say(c)
	records, _ := c.cfg.Store.Records()
	for _, r := range records {
		if r.Serial != udid {
			continue
		}
		ok, observation := c.handBackLocked(ctx, r, nil)
		phones.add(r.Serial, ok)
		if !ok && keptBecause == "" {
			keptBecause = observation
		}
	}
	return c.answerFromThePhone(ctx, udid, keptBecause)
}

// answerFromThePhone reads the phone's proxy and clears one of ours still on it — the end of every handback answer.
func (c *Controller) answerFromThePhone(ctx context.Context, udid, keptBecause string) (bool, string) {
	value, read, failure := readProxy(ctx, c.cfg.Adb, udid)
	if !read {
		return false, unreachableObservation(failure)
	}
	if isOurProxy(value, nil) {
		// THE HANDBACK THE CHECK ASKS FOR: what is still on the phone is taken off, and the phone is read once more.
		_, _, _, _ = c.cfg.Adb(ctx, proxyClearArgs(udid)...)
		if value, read, failure = readProxy(ctx, c.cfg.Adb, udid); !read {
			return false, unreachableObservation(failure)
		}
		if isOurProxy(value, nil) {
			return false, fmt.Sprintf("its global proxy still points at this machine's capture (%s) after it was cleared", value)
		}
	}
	if keptBecause != "" {
		return false, keptBecause
	}
	return true, ""
}

// knows says whether udid is a phone of this machine: one a capture here is on, that was handed back here, has a record,
// was set up here, or is attached to it now.
func (c *Controller) knows(ctx context.Context, udid string) bool {
	c.mu.Lock()
	_, gated := c.gates[udid]
	live := false
	for _, s := range c.sessions {
		live = live || s.serial == udid
	}
	c.mu.Unlock()
	if gated || live {
		return true
	}
	if _, setUp := c.cfg.Store.Phone(udid); setUp {
		return true
	}
	records, _ := c.cfg.Store.Records()
	for _, r := range records {
		if r.Serial == udid {
			return true
		}
	}
	stdout, _, exit, _ := c.cfg.Adb(ctx, devicesArgs()...)
	if exit != 0 {
		return false
	}
	for _, serial := range attachedPhones(stdout) {
		if serial == udid {
			return true
		}
	}
	return false
}

// endedLocked records that lease's capture is over before any start reserved it (see Controller.ended). Caller holds mu.
func (c *Controller) endedLocked(lease string) {
	if !netcap.IsLease(lease) {
		return
	}
	if len(c.ended) >= sayOnceBound {
		c.ended = map[string]time.Time{}
	}
	c.ended[lease] = c.cfg.Now()
}

// ── the pull ──────────────────────────────────────────────────────────────────────────────────────────────────

// Pull is lease's masked capture as it stands, or why there is none — the sentence the run reads. A capture file this
// process did not start (it was running before a restart) is served too, as the node serves one: it is masked, and the
// farm names the lease it pulls for.
func (c *Controller) Pull(lease string) ([]byte, string) {
	lease = strings.ToLower(lease)
	if !netcap.IsLease(lease) {
		return nil, pullNothingKnown
	}
	c.mu.Lock()
	s := c.sessions[lease]
	o, hasOutcome := c.outcomes[lease]
	c.mu.Unlock()
	if s != nil && s.rec != nil && !s.pulled {
		data, err := s.rec.Snapshot()
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, pullDeleted
		case err != nil:
			return nil, pullUnreadableState
		}
		return data, ""
	}
	if hasOutcome {
		return nil, o.sentence
	}
	if data, err := readOwn(filepath.Join(c.cfg.Store.FilesDir(), lease+".har")); err == nil {
		return data, ""
	}
	return nil, pullNothingKnown
}

// Pulled deletes lease's capture file — the farm has it — and records nothing more for that lease.
func (c *Controller) Pulled(lease string) {
	lease = strings.ToLower(lease)
	if !netcap.IsLease(lease) {
		return
	}
	c.mu.Lock()
	s := c.sessions[lease]
	if s != nil {
		s.pulled = true
	}
	c.outcomes[lease] = outcome{sentence: pullAlreadyTaken, at: c.cfg.Now()}
	c.mu.Unlock()
	if s != nil && s.rec != nil {
		_ = s.rec.Remove()
		return
	}
	path := filepath.Join(c.cfg.Store.FilesDir(), lease+".har")
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(path)
	}
}

// ── the timers ───────────────────────────────────────────────────────────────────────────────────────────────

// Run hands back what a previous run left and sweeps old capture files, at once and then on a timer, and watches the
// mark: capture turned off stops every capture. It returns when ctx ends, after stopping every capture still running.
func (c *Controller) Run(ctx context.Context) {
	_, c.wasOn = c.cfg.Store.Enabled()
	// OUT OF BACKUPS AGAIN AT EVERY START (backup.go): a folder enabled by an older agent, or copied here, is marked now.
	if c.wasOn {
		if err := keepOutOfBackups(c.cfg.Store.Root); err != nil {
			c.say("network capture: %s (%v) — a backup of this machine may copy the capture CA's key and capture files", ErrNotExcluded, err)
		}
	}
	c.pass(ctx)
	ticker := time.NewTicker(c.cfg.MarkEvery)
	defer ticker.Stop()
	lastPass := c.cfg.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		c.watchMark(ctx)
		c.stopOverdue(ctx)
		if c.cfg.Now().Sub(lastPass) >= c.cfg.PassEvery {
			lastPass = c.cfg.Now()
			c.pass(ctx)
		}
	}
}

// pass is one timer pass: records handed back, old capture files swept, stale outcomes forgotten.
func (c *Controller) pass(ctx context.Context) {
	c.HandBackOrphans(ctx)
	now := c.cfg.Now()
	netcap.Sweep(c.cfg.Store.FilesDir(), now, c.cfg.Retention, func(lease string) (bool, time.Time, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		s := c.sessions[lease]
		if s == nil {
			return false, time.Time{}, false
		}
		return s.stopped.IsZero(), s.stopped, true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	for lease, s := range c.sessions {
		if !s.stopped.IsZero() && now.Sub(s.stopped) >= c.cfg.Retention {
			if !s.pulled {
				c.outcomes[lease] = outcome{sentence: pullExpired, at: now}
			}
			delete(c.sessions, lease)
		}
	}
	for lease, o := range c.outcomes {
		if now.Sub(o.at) >= c.cfg.Retention {
			delete(c.outcomes, lease)
			delete(c.said, "refused "+lease)
		}
	}
	for lease, at := range c.ended {
		if now.Sub(at) >= c.cfg.Retention {
			delete(c.ended, lease)
		}
	}
}

// watchMark stops every capture the moment capture is turned off on this machine (`capture disable`, from any terminal) —
// on EVERY tick that finds the mark gone, not only the one that saw it go: a capture that started between two
// ticks, or after the tick that saw the change, is stopped by the next.
func (c *Controller) watchMark(ctx context.Context) {
	_, on := c.cfg.Store.Enabled()
	c.mu.Lock()
	turnedOff := c.wasOn && !on
	c.wasOn = on
	running := !on && c.liveCountLocked() > 0
	c.mu.Unlock()
	if turnedOff || running {
		c.say("network capture was turned off on this machine — every capture is stopped and its phone handed back")
	}
	if !on {
		c.StopAll(ctx)
	}
}

func (c *Controller) stopOverdue(ctx context.Context) {
	now := c.cfg.Now()
	var overdue []*session
	c.mu.Lock()
	for _, s := range c.sessions {
		if s.stopped.IsZero() && s.settled && now.Sub(s.started) >= c.cfg.MaxCapture {
			overdue = append(overdue, s)
		}
	}
	c.mu.Unlock()
	for _, s := range overdue {
		c.say("the capture on phone %s ran for %s without being stopped, so this machine stopped it and handed the phone back", s.serial, c.cfg.MaxCapture)
		c.Stop(ctx, s.lease)
	}
}

// StopAll stops every capture still running and hands its phone back.
func (c *Controller) StopAll(ctx context.Context) {
	var leases []string
	c.mu.Lock()
	for lease, s := range c.sessions {
		if s.stopped.IsZero() {
			leases = append(leases, lease)
		}
	}
	c.mu.Unlock()
	for _, lease := range leases {
		c.Stop(ctx, lease)
	}
}

// Running is how many captures are running now.
func (c *Controller) Running() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.liveCountLocked()
}

// ── small pieces ───────────────────────────────────────────────────────────────────────────────────────────────

func (c *Controller) gate(serial string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.gates[serial]
	if g == nil {
		g = &sync.Mutex{}
		c.gates[serial] = g
	}
	return g
}

// liveOnLocked is the capture running (or starting) on serial, if any.
func (c *Controller) liveOnLocked(serial string) *session {
	for _, s := range c.sessions {
		if s.serial == serial && s.stopped.IsZero() {
			return s
		}
	}
	return nil
}

func (c *Controller) liveCountLocked() int {
	n := 0
	for _, s := range c.sessions {
		if s.stopped.IsZero() {
			n++
		}
	}
	return n
}

// choosePortLocked is the first of serial's candidate ports that no capture here holds and nothing on this machine answers.
func (c *Controller) choosePortLocked(serial string) int {
	held := map[int]bool{}
	for _, s := range c.sessions {
		if s.stopped.IsZero() {
			held[s.port] = true
		}
	}
	for _, port := range netcap.PortCandidates(serial) {
		if !held[int(port)] && c.cfg.PortFree(port) {
			return int(port)
		}
	}
	return 0
}

func (c *Controller) keepOutcome(lease, sentence string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.outcomes) >= sayOnceBound {
		c.outcomes = map[string]outcome{} // the node's backstop: a bound that bites starts over rather than grows
	}
	c.outcomes[lease] = outcome{sentence: sentence, at: c.cfg.Now()}
}

// say writes one line to the console. Every narration is a LITERAL format at its call, so the transcript census
// (internal/connect/transcript_test.go) reads it.
func (c *Controller) say(format string, args ...any) {
	fmt.Fprintf(c.cfg.Out, format+"\n", args...)
}

// sayOnce says a line once for key, until the line for that key changes.
func (c *Controller) sayOnce(key, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	c.mu.Lock()
	if c.said[key] == line {
		c.mu.Unlock()
		return
	}
	if len(c.said) >= sayOnceBound {
		c.said = map[string]string{} // the backstop: a line may be said once more, rather than kept forever
	}
	c.said[key] = line
	c.mu.Unlock()
	fmt.Fprintln(c.cfg.Out, line)
}

// looksLikeAnIPhone: an iOS device's udid — 40 hex digits, or the 8-and-16 form newer iPhones use. Capture is Android's.
func looksLikeAnIPhone(serial string) bool {
	hexOnly := func(s string) bool {
		for _, r := range s {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
		return s != ""
	}
	if len(serial) == 40 && hexOnly(serial) {
		return true
	}
	before, after, ok := strings.Cut(serial, "-")
	return ok && len(before) == 8 && len(after) == 16 && hexOnly(before) && hexOnly(after)
}
