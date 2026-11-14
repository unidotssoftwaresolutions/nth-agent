package capture

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/liveview"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── `nth-agent capture setup [serial]` ───────────────────────────────────────────────────────────────────────────
//
// THE PHONE'S HALF OF TURNING CAPTURE ON. It copies the capture certificate to the phone's Downloads, prints the steps for
// that phone's vendor, waits for the person to install it, and then checks: for a moment the phone's proxy points at this
// machine, the phone's browser opens one address that cannot exist (netcap.TrustCheckHost), and the check's own proxy
// answers it. The phone is recorded as set up only when its browser trusted the certificate.
//
// THE PHONE IS ALWAYS PUT BACK. What the check changes is written down before it changes it (a trust-check record), and it
// is put back by the one handback sequence — on every outcome, on Ctrl-C, and, if this process dies, by `connect`'s next
// pass. Nothing is decrypted or recorded during the check but the check's own address.

// SetupOptions wires `capture setup`; every seam is a parameter.
type SetupOptions struct {
	Store  Store
	Adb    Adb
	In     io.Reader
	Out    io.Writer
	Now    func() time.Time
	Serial string // "" = the one Android phone attached
	// CheckWait is how long the check waits for the phone's browser; zero = DefaultCheckWait.
	CheckWait time.Duration
	// LastPageWait is how long the check waits for the browser to load its last page; zero = lastPageBound. A TEST SEAM: no
	// flag or variable reaches it.
	LastPageWait time.Duration
	// PortFree checks a port; nil = liveview.LoopbackFree.
	PortFree func(uint16) bool
	// PeerCheck and PeerOwner are the controller's seams of the same names (Config): the check's proxy, too, lets in only
	// this account's connections. nil = netcap.PeerCheckWorks and netcap.OwnerOfPeer.
	PeerCheck func() error
	PeerOwner netcap.PeerOwner
}

// DefaultCheckWait is how long the trust check waits for the phone's browser to reach it.
const DefaultCheckWait = 45 * time.Second

// putBackBound is how long putting the phone back may take, whatever ended the check.
const putBackBound = time.Minute

// lastPageBound is how long the check waits for the phone's browser to load its last page (netcap.TrustCheckOverAddress) before
// it puts the proxy back regardless: a browser that is told to open a page loads it in a second or two, and one that is not
// going to (a locked phone) must not hold the phone's proxy at this machine.
const lastPageBound = 5 * time.Second

// Setup runs `capture setup`. It returns the exit code: 0 the phone is set up, 1 the check or the work failed, 2 the
// person stopped it or must say which phone.
func Setup(ctx context.Context, o SetupOptions) int {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.CheckWait <= 0 {
		o.CheckWait = DefaultCheckWait
	}
	if o.LastPageWait <= 0 {
		o.LastPageWait = lastPageBound
	}
	if o.PortFree == nil {
		o.PortFree = liveview.LoopbackFree
	}
	if o.PeerCheck == nil {
		o.PeerCheck = netcap.PeerCheckWorks
	}
	out := o.Out
	if _, on := o.Store.Enabled(); !on {
		fmt.Fprintf(out, "Network capture is off on this machine. Turn it on first: %s\n", command.Line("capture enable"))
		return 1
	}
	cert, err := netcap.ReadCertificate(o.Store.CADir())
	if err != nil || o.Now().After(cert.NotAfter) {
		fmt.Fprintf(out, "The capture certificate on this machine cannot be used (%s says why). Turn capture off and on: %s, then %s.\n",
			command.Line("doctor"), command.Line("capture disable"), command.Line("capture enable"))
		return 1
	}
	// THE CHECK'S PROXY, LIKE A CAPTURE'S, LETS IN ONLY THIS ACCOUNT — on a machine whose kernel cannot say who connects it
	// would refuse the phone's browser too, so that is found out before a phone is touched at all.
	if o.PeerCheck() != nil {
		fmt.Fprintf(out, "This machine could not check which of its accounts connects to the capture proxy, so network capture cannot run here. No phone was touched.\n")
		return 1
	}

	serial, code := choosePhone(ctx, o.Adb, o.Serial, out)
	if serial == "" {
		return code
	}
	manufacturer := property(ctx, o.Adb, serial, "ro.product.manufacturer")
	model := property(ctx, o.Adb, serial, "ro.product.model")

	if _, stderr, exit, _ := o.Adb(ctx, pushCertificateArgs(serial, netcap.CertificatePath(o.Store.CADir()))...); exit != 0 {
		fmt.Fprintf(out, "The capture certificate could not be copied to phone %s (%s). Unlock the phone, check it is plugged in, and run %s once more.\n",
			serial, adbFailure(exit, stderr), command.Line("capture setup "+serial))
		return 1
	}
	fmt.Fprint(out, SetupSteps(serial, model, VendorOf(manufacturer)))
	if !waitForEnter(ctx, o.In) {
		fmt.Fprintf(out, "\nStopped before the check. Only the certificate file was copied to the phone's Downloads.\n")
		return 2
	}

	outcome, putBack, ran := trustCheck(ctx, o, serial)
	if !putBack.handedBack {
		fmt.Fprint(out, Unreachable(serial, putBack.proxy))
	}
	if !ran {
		return 1 // the check could not be made: why was said, and no outcome is claimed
	}
	switch outcome {
	case netcap.TrustTrusted:
		if err := o.Store.setPhone(Phone{Serial: serial, Model: model, Manufacturer: manufacturer,
			CAFingerprint: netcap.Fingerprint(cert), SetUpAtUtc: o.Now().UTC()}); err != nil {
			fmt.Fprintf(out, "The phone trusted the certificate, and this machine could not write that down (%v). Run %s once more.\n", err, command.Line("capture setup "+serial))
			return 1
		}
		fmt.Fprint(out, TrustTrusted(serial, putBack.handedBack))
		return 0
	case netcap.TrustRefused:
		fmt.Fprint(out, TrustRefused(serial, putBack.handedBack))
	default:
		fmt.Fprint(out, TrustNoAnswer(serial, putBack.handedBack))
	}
	return 1
}

type putBack struct {
	handedBack bool
	proxy      string
}

// trustCheck runs the check and ALWAYS puts the phone back. It never touches the phone before its record is written. ran
// is false when the check could not be made at all: its reason is printed here, and no outcome is claimed for it.
func trustCheck(ctx context.Context, o SetupOptions, serial string) (netcap.TrustOutcome, putBack, bool) {
	notRun := func(format string, args ...any) (netcap.TrustOutcome, putBack, bool) {
		fmt.Fprintf(o.Out, format, args...)
		return netcap.TrustNoAnswer, putBack{handedBack: true}, false
	}
	lock, err := o.Store.tryLockPhone(serial)
	if err != nil {
		return notRun("Phone %s is in use by this machine's network capture right now (a capture is running on it, or another nth-agent is changing it). Run this once that has finished.\n", serial)
	}
	defer lock.unlock()

	ca, err := netcap.LoadCA(o.Store.CADir(), o.Now())
	if err != nil {
		return notRun("The capture CA on this machine cannot be used: %v\n", err)
	}
	port := 0
	for _, candidate := range netcap.PortCandidates(serial) {
		if o.PortFree(candidate) {
			port = int(candidate)
			break
		}
	}
	if port == 0 {
		return notRun("No free port for the check on this machine, so the phone was not touched.\n")
	}
	check := netcap.NewTrustCheck()
	var refusedOnce sync.Once
	proxy := netcap.NewProxy(netcap.Options{CA: ca, TrustCheck: check, Now: o.Now, PeerOwner: o.PeerOwner,
		Refused: func(uid int, found bool) {
			refusedOnce.Do(func() {
				if found {
					fmt.Fprintf(o.Out, "A connection to the check from another account on this machine (uid %d) was refused — only this account and the administrator may use it.\n", uid)
				} else {
					fmt.Fprintf(o.Out, "A connection to the check was refused, because this machine could not tell which account opened it.\n")
				}
			})
		}})
	if _, err := proxy.Listen(uint16(port)); err != nil {
		return notRun("The check could not listen on this machine (%v), so the phone was not touched.\n", err)
	}
	go proxy.Serve()
	defer proxy.Close()

	record := Record{Serial: serial, Lease: newID(), Kind: KindTrustCheck, Port: port, Proxy: ProxyValue(port), Reverse: true, Written: o.Now()}
	if err := o.Store.writeRecord(record); err != nil {
		return notRun("This machine could not write down what the check would change on the phone, so it did not touch it (%v).\n", err)
	}
	// FROM HERE THE PHONE IS CHANGED, AND IT IS PUT BACK WHATEVER HAPPENS — below, with a context of its own, so a
	// Ctrl-C that ended the check does not end the putting back.
	result := netcap.TrustNoAnswer
	_, stderr, exit, _ := o.Adb(ctx, reverseAddArgs(serial, port)...)
	if exit == 0 {
		_, stderr, exit, _ = o.Adb(ctx, proxySetArgs(serial, port)...)
	}
	ran := exit == 0
	if ran {
		fmt.Fprintf(o.Out, "\nChecking: the phone's browser opens %s through this machine for up to %s. Keep the phone unlocked.\n",
			"https://"+netcap.TrustCheckHost+"/", o.CheckWait)
		_, _, _, _ = o.Adb(ctx, openInBrowserArgs(serial, "https://"+netcap.TrustCheckHost+"/")...)
		wait, cancel := context.WithTimeout(ctx, o.CheckWait)
		result = check.Wait(wait)
		cancel()
	} else {
		fmt.Fprintf(o.Out, "The check could not point phone %s at this machine (%s), so it did not run. Unlock the phone, keep it plugged in, and run %s once more.\n",
			serial, adbFailure(exit, stderr), command.Line("capture setup "+serial))
	}
	back, cancel := context.WithTimeout(context.Background(), putBackBound)
	defer cancel()
	// THE BROWSER IS LEFT ON THE CHECK'S LAST PAGE, WHATEVER IT FOUND (AN_AGENT_CAPTURE_READY_TO_SHIP, rule 4). The check's page — or,
	// when the phone refused the certificate, the browser's red "Your connection is not private" warning for it — stayed on the
	// phone, and a person may read that warning as an attack. While the phone still points at this machine, its browser is asked to
	// open the check's last page in the same tab: plain HTTP, answered by the check's own proxy, saying the check is over. The same
	// command that opened the check — nothing stops, clears or closes the browser. The terminal says what may still be there.
	if ran {
		if _, _, exit, _ := o.Adb(back, openInBrowserArgs(serial, netcap.TrustCheckOverAddress)...); exit == 0 {
			shown, stop := context.WithTimeout(back, o.LastPageWait)
			check.WaitOver(shown)
			stop()
		}
	}
	ok, _ := handBackSteps(back, o.Store, o.Adb, o.Now, record, nil)
	return result, putBack{handedBack: ok, proxy: record.Proxy}, ran
}

// choosePhone is the phone to set up: the one named, if it is attached and ready; else the one Android phone attached.
// More than one, or none, is said — never guessed.
func choosePhone(ctx context.Context, adb Adb, requested string, out io.Writer) (string, int) {
	stdout, stderr, exit, _ := adb(ctx, devicesArgs()...)
	if exit != 0 {
		fmt.Fprintf(out, "adb could not list the attached phones (%s). Run %s to see what this machine is missing.\n", adbFailure(exit, stderr), command.Line("doctor"))
		return "", 1
	}
	attached := attachedPhones(stdout)
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, serial := range attached {
			if serial == requested {
				return serial, 0
			}
		}
		fmt.Fprintf(out, "No phone %s is attached and ready. Attached: %s\n", requested, listOrNone(attached))
		return "", 1
	}
	switch len(attached) {
	case 0:
		fmt.Fprintf(out, "No Android phone is attached and ready. Plug in the test phone, unlock it, accept the USB debugging prompt, and run this once more.\n")
		return "", 1
	case 1:
		return attached[0], 0
	default:
		fmt.Fprintf(out, "%d phones are attached: %s\nSay which one: %s\n", len(attached), strings.Join(attached, ", "), command.Line("capture setup SERIAL"))
		return "", 2
	}
}

// attachedPhones reads `adb devices`: the serials whose state is "device" (attached, authorised, ready).
func attachedPhones(stdout string) []string {
	var serials []string
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "device" && IsAdbSerial(fields[0]) && !looksLikeAnIPhone(fields[0]) {
			serials = append(serials, fields[0])
		}
	}
	return serials
}

func listOrNone(serials []string) string {
	if len(serials) == 0 {
		return "none"
	}
	return strings.Join(serials, ", ")
}

func property(ctx context.Context, adb Adb, serial, name string) string {
	stdout, _, exit, _ := adb(ctx, propertyArgs(serial, name)...)
	if exit != 0 {
		return ""
	}
	value := strings.TrimSpace(stdout)
	if len(value) > 64 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

// waitForEnter reads one line, or gives up when ctx ends (Ctrl-C) or the input closes.
func waitForEnter(ctx context.Context, in io.Reader) bool {
	line := make(chan bool, 1)
	go func() {
		_, err := bufio.NewReader(in).ReadString('\n')
		line <- err == nil
	}()
	select {
	case ok := <-line:
		return ok
	case <-ctx.Done():
		return false
	}
}

// newID is a fresh v4 UUID — a trust check's record is named like a capture's.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
