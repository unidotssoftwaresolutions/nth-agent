package capture

import (
	"fmt"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// ── WHAT THE CUSTOMER IS TOLD ───────────────────────────────────────────────────────────────────────────────────
//
// EVERY TEXT SAYS THE SAME SIX THINGS, each in its own length (texts_test.go reads each one for all six):
//
//   1. during a capture run the WHOLE PHONE's traffic goes through this machine, not only the app's — use a test phone;
//   2. only debug builds that trust user certificates are readable;
//   3. what is masked on this machine, and that it is masked again on arrival;
//   4. what reaches us, kept one hour, and nothing raw kept on this machine;
//   5. the agent never sends the CA's key anywhere and keeps it out of backups — a backup made before that, or by a tool
//      that ignores it, may still hold a copy — and the CA lasts one year (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, "never
//      leaves this machine" was not true of a Time Machine copy);
//   6. how to turn it all off and remove the certificate (on a Samsung, both places).
//
// The long forms are the `enable` explanation and the README's section; every shorter text carries SixThings(), the same
// six in one paragraph. Every text is here and nowhere else, and texts.golden.txt pins each one as written.

// SixThings is the six things in one paragraph — what every short text ends with.
func SixThings() string {
	return "What network capture means here: during a capture run the whole phone's traffic, not only the app's, " +
		"goes through this machine — use a test phone. Only debug builds that trust user certificates can be read. Everything " +
		"is masked on this machine before it is sent, and masked again when it reaches us. We keep what arrives with the run " +
		"for one hour, and nothing raw is ever kept on this machine. The agent never sends the capture CA's key anywhere and " +
		"keeps it out of backups, though a backup made before that, or by a tool that ignores it, may still hold a copy. The CA " +
		"lasts one year. " + command.Line("capture disable") + " turns it all off — then remove the certificate on the phone (on a Samsung, " +
		"in both places it is listed)."
}

// ThePromise is what this agent promises about signing, now that it makes a CA of its own.
func ThePromise() string {
	return "nth-agent never signs or re-signs your apps, and never touches your signing certificates or keychain. " +
		"When you turn on network capture, it makes one certificate authority of its own, on this machine, used only to stand " +
		"in for the servers the apps on your test phone talk to, so that their HTTPS can be read — and only in apps built to " +
		"trust it. The agent never sends its private key anywhere and keeps it out of backups (a backup made before that, or by a " +
		"tool that ignores it, may still hold a copy); it lasts one year, and you can remove it any time (" +
		command.Line("capture disable") + ", then delete the certificate on the phone)."
}

// SharedMachine is THE RESIDUAL ON A MACHINE OTHER PEOPLE LOG INTO, said plainly: the capture proxy listens on this
// machine's loopback, which every account here can reach, and it lets in only this account's connections and the
// administrator's (netcap/peer.go). `capture enable` says it before it asks, and the README says it word for word.
const SharedMachine = "On a machine other people log into, the machine's administrator (root) could send traffic " +
	"through the capture proxy while a capture runs; a connection from any other account is refused unread."

// EnableExplanation is what `capture enable` prints before it asks.
func EnableExplanation() string {
	return `Network capture records your test app's network traffic during a run, so each request and response
appears on the run's Network tab. It is off until you turn it on here, set up each phone, and a scenario asks for it.

  - During a capture run the WHOLE PHONE's traffic — every app's, not only the one under test — goes through a proxy on
    this machine and out through this machine's network. Use a test phone, not your daily one.
  - Only apps built to trust user-installed certificates can be read — usually debug builds. A release build, or a host
    an app pins, is not read: it is recorded once as "not readable" and then passed through untouched, so the phone's
    own services keep working.
  - Before anything leaves this machine it is masked here: the Authorization, Proxy-Authorization, Cookie and Set-Cookie
    headers; any header, field or query parameter whose name says it holds a credential; and credential-shaped values
    (JWTs, bearer and basic credentials, published API-key formats) — in compressed bodies too, which are decoded first.
    A body this agent cannot read is left out entirely. We mask again when it arrives.
  - What reaches us is the masked record, kept with the run for one hour. Nothing raw is ever kept on this machine: the
    masked copy waits here only until your workspace pulls it, and is deleted then, or an hour after the capture stops.
  - To read HTTPS, this agent makes one certificate authority of its own, here, valid for one year. The agent never sends
    its private key anywhere, and keeps the capture folder out of backups: out of Time Machine on a Mac, and marked for
    backup tools that honour CACHEDIR.TAG on Linux. A backup made before that, or by a tool that ignores it, may still
    hold a copy of the key and of capture files; removing the certificate from every phone is what ends a copied key's
    use. You install its certificate on each test phone yourself (` + command.Line("capture setup") + `).
  - To turn it all off: ` + command.Line("capture disable") + `. It hands every phone back, deletes the key and every capture file
    on this machine, and says how to remove the certificate from each phone (on a Samsung, in both places it is listed).

` + SharedMachine + `

` + ThePromise() + `
`
}

// ConfirmPrompt is the question `capture enable` asks; only ENABLE, typed, is a yes.
const ConfirmPrompt = "Type ENABLE to turn on network capture: "

// NotEnabled is what `capture enable` says when the answer was not ENABLE.
const NotEnabled = "Nothing was changed — network capture stays off."

// Enabled is what `capture enable` says once it is on.
func Enabled(caName string, validUntil time.Time) string {
	return fmt.Sprintf(`Network capture is on for this machine. Its certificate authority is "%s", valid until %s.
Next, set up each test phone: %s
If npx nthcase-agent connect is running it tells your workspace within about ten seconds; otherwise it does when it starts.

%s
`, caName, validUntil.UTC().Format(time.DateOnly), command.Line("capture setup [serial]"), SixThings())
}

// AlreadyEnabled is what `capture enable` says when capture is already on.
func AlreadyEnabled(caName string, validUntil time.Time) string {
	return fmt.Sprintf(`Network capture is already on for this machine. Its certificate authority is "%s", valid until %s.
Set up a phone with: %s

%s
`, caName, validUntil.UTC().Format(time.DateOnly), command.Line("capture setup [serial]"), SixThings())
}

// UnsupportedSystem is what `capture enable` says on a system this agent cannot keep a key to one user on.
func UnsupportedSystem(goos string) string {
	return fmt.Sprintf("Network capture runs on macOS and Linux only, and this machine is %s: this agent cannot keep the "+
		"capture key to you alone here, so it does not make one. Nothing was changed.\n\n%s\n", goos, SixThings())
}

// ── capture setup ──────────────────────────────────────────────────────────────────────────────────────────────

// Vendor is how a phone is set up: Samsung's settings differ from stock Android's.
type Vendor int

const (
	VendorStock Vendor = iota
	VendorSamsung
)

// VendorOf reads ro.product.manufacturer.
func VendorOf(manufacturer string) Vendor {
	if strings.EqualFold(strings.TrimSpace(manufacturer), "samsung") {
		return VendorSamsung
	}
	return VendorStock
}

// SetupSteps is what `capture setup` prints once the certificate is on the phone.
func SetupSteps(serial, model string, vendor Vendor) string {
	var steps string
	if vendor == VendorSamsung {
		steps = `  2. On the phone, open Settings → Security and privacy → More security settings → Install from device storage.
  3. Choose "CA certificate", tap "Install anyway", then pick nthcase-agent-ca.crt.`
	} else {
		steps = `  2. On the phone, open Settings → Security & privacy → More security & privacy → Encryption & credentials →
     Install a certificate → CA certificate, and tap "Install anyway".
  3. Pick nthcase-agent-ca.crt.`
	}
	steps += `
     (The names differ a little between Android versions: searching Settings for "CA certificate" finds the screen.)`
	name := serial
	if model != "" {
		name = serial + " (" + model + ")"
	}
	return fmt.Sprintf(`Setting up phone %s for network capture.

  1. The capture certificate is on the phone now, in Downloads, as nthcase-agent-ca.crt.
%s
  4. If the phone has no screen lock yet, Android asks for one first — a PIN, a pattern or a password. Android will not
     trust a certificate on a phone without one. Set it, then finish the install.

Android does not let a computer install a certificate: this step is yours, on the phone.

%s

Press Enter here when the certificate is installed, and this agent checks that the phone trusts it.
(Ctrl-C stops here. Only the certificate file was copied to the phone's Downloads; nothing else on it was changed.)
`, name, steps, SixThings())
}

// The trust check's three outcomes.

// putBackLine says whether the check's change to the phone was undone — and never that it was when it was not.
func putBackLine(putBack bool) string {
	if putBack {
		return "The phone's proxy setting was put back as it was."
	}
	return "The phone's proxy setting could NOT be put back yet — what it is left with, and how to clear it, is above."
}

// What the phone's browser may still show once the check is over — the check's own page, said as such (AN_AGENT_CAPTURE_READY_TO_SHIP,
// rule 4). `capture setup` asks the browser to open a page that says the check is over, in the check's tab; these say what may be
// there whether or not it did, and that it is the check, so nothing left on the phone reads as something to fear.
const (
	browserAfterTrusted  = "On the phone, the browser may still show a page from this check (trust-check.nthcase.invalid). It is the check's own page, and you can close that tab."
	browserAfterRefused  = "On the phone, the browser may still show a \"Your connection is not private\" warning for trust-check.nthcase.invalid. That warning is this check itself — the phone's browser refusing the capture certificate, which is what the check found — not an attack, and you can close that tab."
	browserAfterNoAnswer = "On the phone, the browser may show a page for trust-check.nthcase.invalid that could not be opened. It is this check's own page, and you can close that tab."
)

// TrustTrusted is the check passing.
func TrustTrusted(serial string, putBack bool) string {
	return fmt.Sprintf(`Phone %s is set up for network capture: its browser trusted the capture certificate.
That proves the certificate is installed and trusted on the phone. It does not prove the app you test trusts it: only apps
built to trust user certificates (usually debug builds) can be read, and a host an app pins is passed through unread.
%s
%s

%s
`, serial, putBackLine(putBack), browserAfterTrusted, SixThings())
}

// workPhone is the MDM sentence every failed check carries.
const workPhone = "If this is a work phone, your organisation may block installing certificates. Network capture needs a " +
	"phone you can install a certificate on — use a dedicated test phone."

// TrustRefused is the phone's browser refusing the certificate.
func TrustRefused(serial string, putBack bool) string {
	return fmt.Sprintf(`Phone %s is NOT set up: its browser refused the capture certificate, so the certificate is not installed as
a trusted CA on it. Go through the steps above, then run %s once more.
%s
%s
%s

%s
`, serial, command.Line("capture setup "+serial), workPhone, putBackLine(putBack), browserAfterRefused, SixThings())
}

// TrustNoAnswer is the check never arriving.
func TrustNoAnswer(serial string, putBack bool) string {
	return fmt.Sprintf(`Phone %s is NOT set up: the check never reached this machine. The phone's browser did not open the check
page, the phone was locked, or its proxy setting did not take. Unlock the phone, keep it plugged in, and run
%s once more.
%s
%s
%s

%s
`, serial, command.Line("capture setup "+serial), workPhone, putBackLine(putBack), browserAfterNoAnswer, SixThings())
}

// ── the phone that could not be handed back ─────────────────────────────────────────────────────────────────────

// Unreachable is the spike's message, corrected: what the phone is left with, what that costs, and how to fix it.
func Unreachable(serial, proxy string) string {
	return unreachableBody(serial, proxy) + "\n\n" + SixThings() + "\n"
}

func unreachableBody(serial, proxy string) string {
	return fmt.Sprintf(`Phone %s still has its network proxy pointed at this machine's capture (%s), and it could not be cleared:
the phone was not reachable over adb (unplugged, switched off, or no longer allowed USB debugging). Until it is cleared
the phone has no working internet, because its proxy points at a capture that has stopped.
Plug it back in, unlock it and allow USB debugging if it asks: while npx nthcase-agent connect runs it clears this within half a
minute, and otherwise when it starts. Or clear it yourself now:  adb -s %s shell settings put global http_proxy :0`, serial, proxy, serial)
}

// stillOwed is a phone a handback reached and could not finish on, for a reason other than reaching it.
func stillOwed(serial, proxy, observation string) string {
	return fmt.Sprintf("Phone %s could not be handed back yet (the capture set its proxy to %s): %s. While npx nthcase-agent connect "+
		"runs it keeps trying; or clear the phone's proxy yourself now:  adb -s %s shell settings put global http_proxy :0",
		serial, proxy, observation, serial)
}

// ── capture disable ────────────────────────────────────────────────────────────────────────────────────────────

// DisableSummary is what `capture disable` says: what it did — and, plainly, anything it could not do — each phone it
// could not hand back and what that phone is left with, and where the certificate is to be removed on each phone. It
// never says a thing was deleted that was not.
func DisableSummary(r DisableResult, caDir string) string {
	var b strings.Builder
	if r.MarkFailure != nil {
		fmt.Fprintf(&b, "Network capture could NOT be turned off: its mark %s could not be deleted (%v). Delete that file by hand — while it is there, capture is on.\n",
			r.MarkPath, r.MarkFailure)
	} else {
		b.WriteString("Network capture is off for this machine.\n")
	}
	if len(r.HandedBack) > 0 {
		fmt.Fprintf(&b, "  - Handed back, with the proxy setting cleared: %s.\n", strings.Join(r.HandedBack, ", "))
	}
	if r.CAFailure != nil {
		fmt.Fprintf(&b, "  - The capture CA's key could NOT be deleted (%v) — delete the folder %s by hand.\n", r.CAFailure, caDir)
	} else {
		b.WriteString("  - The capture CA's key and certificate are deleted from this machine.\n")
	}
	switch {
	case len(r.FilesFailed) > 0:
		fmt.Fprintf(&b, "  - %d capture file(s) are deleted, and these could NOT be: %s — delete them by hand.\n", r.FilesDeleted, strings.Join(r.FilesFailed, ", "))
	case r.FilesDeleted == 0:
		b.WriteString("  - No capture file was left on this machine to delete.\n")
	default:
		fmt.Fprintf(&b, "  - Every capture file on this machine is deleted (%d).\n", r.FilesDeleted)
	}
	b.WriteString("  - Your workspace hears it within about ten seconds if npx nthcase-agent connect is running; otherwise when it starts.\n")
	for _, o := range r.StillOwed {
		text := unreachableBody(o.Record.Serial, o.Record.Proxy)
		if !strings.HasPrefix(o.Observation, "the phone was not reachable") {
			text = stillOwed(o.Record.Serial, o.Record.Proxy, o.Observation)
		}
		b.WriteString("\nNOT HANDED BACK YET — " + text + "\n")
	}
	caName := r.CAName
	if caName == "" {
		caName = "NthCase agent CA"
	}
	fmt.Fprintf(&b, `
The capture certificate stays on each phone until you remove it there:
  Samsung — it is listed in two places; remove it in both:
    Settings → Security and privacy → More security settings → View security certificates → User: delete "%s".
    Settings → Security and privacy → More security settings → User certificates: delete it there too.
  Pixel and other Android phones:
    Settings → Security & privacy → More security & privacy → Encryption & credentials → User credentials: remove "%s".
  (The names differ a little between Android versions: searching Settings for "certificate" finds both screens.)

%s
`, caName, caName, SixThings())
	return b.String()
}

// ── what a refused start and a pull without a capture say (the run reads these) ───────────────────────────────

// Each sentence below reaches the run that asked for capture, through the farm (the pull's answer): it names what
// happened on the machine and what the person does there, and never a lease or a path.
const (
	refusedAt4          = "no capture: the machine this phone is plugged into was already capturing four phones"
	refusedNoPort       = "no capture: the machine this phone is plugged into had no free port for the capture"
	refusedNotRecorded  = "no capture: the machine could not write down what the capture would change on the phone, so it did not touch it"
	refusedFile         = "no capture: the machine could not create the capture's file, so it did not touch the phone"
	refusedReverse      = "no capture: the phone did not accept the capture's adb reverse, so the capture was stopped and the phone handed back"
	refusedProxy        = "no capture: the phone did not accept the capture's proxy setting, so the capture was stopped and the phone handed back"
	refusedNotAndroid   = "no capture: network capture runs on Android phones only"
	refusedPeerCheck    = "no capture: the machine this phone is plugged into could not check which of its accounts connects to the capture, so it did not touch the phone"
	refusedEnded        = "no capture: the run had already ended, and its phone had been handed back, when the machine it is plugged into came to start the capture"
	pullAlreadyTaken    = "no capture: it was pulled once and deleted from the machine it was captured on"
	pullExpired         = "no capture: it was deleted from the machine it was captured on an hour after the capture stopped"
	pullDeleted         = "no capture: it was deleted from the machine it was captured on before it was pulled (network capture was turned off there, or the file was removed by hand)"
	pullNothingKnown    = "no capture for this run on the machine its phone is plugged into, and that machine has no record of one — it may have restarted, or the run never reached it"
	pullUnreadableState = "no capture: the capture file on that machine could not be read"
)

// The four below name a command to type on that machine; a run reads them away from it, so each is spelled with
// command.OnThatMachine, which never carries a path of this machine.

func refusedOff() string {
	return "no capture: network capture is switched off on the machine this phone is plugged into (on that machine: " + command.OnThatMachine("capture enable") + ")"
}

func refusedCA() string {
	return "no capture: the capture certificate on the machine this phone is plugged into cannot be used — on that machine, " + command.OnThatMachine("doctor") + " says why"
}

func refusedNotSetUp(serial string) string {
	return "no capture: phone " + serial + " has not been set up for capture on the machine it is plugged into (on that machine: " + command.OnThatMachine("capture setup "+serial) + ")"
}

func refusedOldCA(serial string) string {
	return "no capture: phone " + serial + " was set up for an earlier capture certificate (on that machine: " + command.OnThatMachine("capture setup "+serial) + ")"
}

func refusedBusy(serial string) string {
	return "no capture: phone " + serial + " was being set up or handed back on its machine when the run asked"
}

func refusedLive(serial string) string {
	return "no capture: another capture was already running on phone " + serial
}

// LocalNetworkOpen is said at connect's start and at every capture's start while netcap.AllowLocalFlag is on
// (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 2).
const LocalNetworkOpen = "network capture can reach this machine and its local network (" + netcap.AllowLocalFlag + " is on): " +
	"an app, a web page or the person driving a phone under capture can reach your local services through it, and their answers are recorded"

// recordedAndMasked is what a stopped capture says it kept: how many requests — and, when it kept only the last of them,
// how many the app made.
func recordedAndMasked(kept, seen int) string {
	if seen > kept {
		return fmt.Sprintf("the app made %d requests, and the last %d were recorded and masked", seen, kept)
	}
	return fmt.Sprintf("%d request(s) recorded and masked", kept)
}
