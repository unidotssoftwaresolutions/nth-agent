# nth-agent

The NthCase device agent. It reports what devices are attached to this host — and when it cannot find
out, it says so instead of reporting an empty list.

> **An empty list is a true answer. Not having asked is not.**

## The commands

```
npx {{PACKAGE}}@latest devices      # what is attached here, what is not, and what to do about it
npx {{PACKAGE}}@latest doctor       # what this machine NEEDS to run tests, what it has, and what to do
npx {{PACKAGE}}@latest setup-ios    # the one-time WebDriverAgent build a real iPhone needs, on this machine
npx {{PACKAGE}}@latest upload       # put a build on your BrowserStack account and print the id connect needs
npx {{PACKAGE}}@latest connect      # register this machine with your workspace and keep it updated
npx {{PACKAGE}}@latest capture      # network capture on this machine — OFF until you turn it on (see below)
npx {{PACKAGE}}@latest notices      # the third-party licences this program carries (see "Third-party notices")
```

`devices` and `doctor` both take `--json` for the same facts, machine-readable, and `--skip android|ios`
to leave a platform alone (it then reports *not asked*, which is a decision rather than a failure).

The platform is a **field on a device**, never a verb: there is no `ios-devices`, no required
`--platform`. Android comes from `adb`; iOS comes from `go-ios` talking to usbmuxd.

### `devices` — what is attached

The inventory, and the three outcomes below are its whole vocabulary. Exit 0 when at least one probe
established the truth; **zero devices attached is a success**.

### `doctor` — what this machine is missing

Every requirement a run on this machine depends on, one line each, in this order: **unusable devices** (the
phones it saw and cannot use, and why), **devices left out** (the ones it saw and does not offer because they are
not on this machine's USB cable — see `connect`, below), **adb** and the **adb server** (and whose it is), **Appium**, the **Appium endpoint** you
give it, **Appium driver uiautomator2** and **Appium driver xcuitest**, the **Xcode command-line tools**, **iOS
Developer Mode**, the **iOS signing identity**, the **iOS provisioning profile**, the **iOS WebDriverAgent
project** and the **iOS WebDriverAgent port** — and last, one **live view** line per phone (*Watching a run on
your phone*, below). Each is reported in one of six states, and each says **what it did not establish** as well
as what it found. After them it says whether network capture is on, which phones are set up for it, and when its
certificate authority ends. It **installs nothing, changes nothing and starts nothing**: it reads facts that are
already true and reports the ones it could not.

```
npx {{PACKAGE}}@latest doctor --appium http://127.0.0.1:4723 \
  --ios-wda-bundle-id com.example.WebDriverAgentRunner --ios-team-id ABCDE12345
```

`--appium` is the endpoint it probes — it never guesses one. The two iOS flags are what a **real device**
needs: the bundle id it looks for a provisioning profile for, and the team that profile must belong to.
**Your team id is not the code in your certificate's name**: a certificate reads
`Apple Development: you@example.com (XXXXXXXXXX)` and those ten characters are the *certificate's*. Run
`doctor` without `--ios-team-id` and it reads the real one off your own provisioning profiles and tells
you what it is.

`--why` prints every caveat, including the ones a passing check states — by default those move behind the
flag and the report ends with a count naming it. `--no-color` switches colour off; so does setting
`NO_COLOR` (its presence, not its value, per no-color.org), and the flag wins over the variable.

### `setup-ios` — the one-time build

Driving a real iPhone means Appium building and **signing** WebDriverAgent on this machine first, and the
identity that does it belongs to *this* machine. This does that build, with the `xcodebuild` flags Appium
does not pass.

```
npx {{PACKAGE}}@latest setup-ios --ios-team-id ABCDE12345 --ios-wda-bundle-id com.example.WebDriverAgentRunner
```

It **states what it will do** — the project, the target, the phone, the team, and that **Apple will
register that phone** against your developer account — **prints the exact command** so you can run it
yourself instead, and **waits for you to type yes**. On failure it shows `xcodebuild`'s own error, never
an exit code. `--udid` picks the phone when more than one is attached; `--yes` answers in advance.

It builds Appium's own WebDriverAgent project, found in Appium's usual home (honouring `APPIUM_HOME` if
you have moved it) — `--wda-project` points it at yours instead, and if the search finds nothing it lists
where it looked. The signing identity is `--ios-signing-id`; unset, it is **`Apple Development`** — the
name a free Personal Team's certificate carries, and Appium's own default. The build lands under your
user config directory unless `--derived-data-path` says otherwise.

### `upload` — put a build where BrowserStack can run it

```sh
npx {{PACKAGE}}@latest upload --file ./app/build/outputs/apk/release/app-release.apk
```

It needs your BrowserStack credentials and **nothing else** — no farm, no workspace token — because you must be
able to get a build id *before* `connect` will start, and `connect` refuses to start without one. Set them the
way *Your own BrowserStack devices* below sets them: the username is not a secret, the access key is, and the key
goes in at a prompt rather than into your shell's history.

**`--file` and `--url` are two different operations, and the difference is which machine fetches.**

| flag | who reads the build | use it when |
|---|---|---|
| `--file PATH` | **this agent**, from your disk | the build is on this machine — which is every pre-release build behind a NAT or a VPN |
| `--url URL` | **BrowserStack**, from their network | the build is genuinely published on the public internet |

`--url` is the older of the two and the assumption inside it is worth saying out loud: the address is resolved
*from BrowserStack's data centre*, so a build served by a local HTTP server on your own laptop works perfectly in
your browser and fails here. `--file` exists because the agent is already on your side of that line — which is
the whole reason it runs on your machine.

Exactly one of the two. Passing neither is not "upload nothing": the agent says which one is missing.

**Four things are checked before the transfer starts**, so a mistake costs a second instead of the upload: the
path exists, it is a file rather than a directory, it is a shape BrowserStack accepts (`.apk`, `.aab`, `.xapk`,
`.ipa`), and it is inside their 1 GB limit. The last two are *their* rules copied here; if a refusal looks wrong,
the message says so rather than blaming your file.

`--custom-id NAME` is optional and gives the build your own name — usable in `--browserstack-app` instead of the
hash, and repointed at the new build each time you upload with the same one.

The output is the id **and the command to use it with**, because an id nobody knows what to do with is half a
fix. Uploaded builds are deleted by BrowserStack after 30 days.

### `connect` — join a workspace

The standing process: register, then heartbeat. With `--appium` your usable devices are offered for real
runs and driven through an outbound tunnel this agent dials itself — nothing dials in to you.

Set the token at a prompt, so it is never an argument and never a line in your history file:

```sh
printf 'workspace agent token: ' && read -rs NTH_AGENT_TOKEN && echo && export NTH_AGENT_TOKEN  # paste this line ALONE
```

```
npx {{PACKAGE}}@latest connect --farm https://<your-farm-url> --appium http://127.0.0.1:4723 \
  --ios-team-id ABCDE12345 --ios-wda-bundle-id com.example.WebDriverAgentRunner
```

**Paste that first line on its own.** `read` takes whatever follows it as the value, so pasting it
together with the next line makes that line your token — measured on a person, 2026-09-03. The prompt
comes from `printf` and not from `read` because no single `read` serves both shells: zsh wants
`read -rs "VAR?..."`, which bash rejects as *"not a valid identifier"*, and bash wants
`read -rsp "..." VAR`, which zsh rejects with *"-p: no coprocess"*.

**TWO EXPOSURES, and they are different.** `--token` exists for the odd machine whose scripting cannot
carry an environment, but it puts the value in `argv`, visible to every process on this machine (`ps`)
for as long as the agent runs; and typing `NTH_AGENT_TOKEN=…` inline writes it verbatim into your shell's
HISTORY FILE, which outlives the process, the terminal and the reboot. The second is longer-lived, so
*"prefer the variable"* on its own would be poor advice — hence the form above, which is neither. The
token is never written to disk or into any output either way.

`--farm` must be `https://` unless it is exactly `localhost` (in lower case) or a loopback address such as
`127.0.0.1`, and a plain-`http` farm is never reached through a proxy. Without the two iOS flags your
iPhones are reported as **attached and not offered**, with the reason — a phone we cannot sign for is a
phone we will not pretend we can drive.

**Only phones on this machine's USB cable are offered.** An emulator, or a phone that adb or the Apple device
daemon reaches over Wi-Fi or the network (`adb connect`, wireless debugging, an iPhone shown on Wi-Fi), is left
out: `connect` names each one once, with how it is reached, `devices` and `doctor` list them, and the farm is not
told about them at all. To offer them too, say so:

```
npx {{PACKAGE}}@latest connect … --offer-uncabled-devices
```

`devices` and `doctor` take the same flag, to show what it would offer.

**One `connect` at a time on this machine, for your account.** A second one refuses to start and says to stop the
first: two would offer the same phones under two identities, and one run's capture could record another's. Joining
a second workspace means stopping the first `connect` before you start the next.

**Only programs your account runs.** `--appium` is an address on this machine (`127.0.0.1` or `localhost`). Before
the agent sends a run there, it asks the system which account runs the program listening on that port — the same
question the capture proxy asks (*On a machine other people log into*, below) — and it asks the same of adb's
server and of each phone's screen server. A program another account runs, or one whose account the system will not
name, is not used, and `connect` and `doctor` say so; root's is used, since root can already read everything of
yours. The temporary forward to a phone's WebDriverAgent listens on this machine's loopback only, and lets in only
this agent.

## What the farm can ask of this machine, and no more

`connect` keeps one connection to the farm open, and the farm sends requests down it. This is all they can do.

**Inside a session, the farm drives the phone.** A run starts an Appium session on a phone you offer and then sends
that session's commands: taps, swipes and typing, the screen's page source, screenshots, the phone's own log, sensor
and location changes, and installing and removing the build under test. They act on the phone and the app on it. A
request that names no session reaches your Appium only as `GET /status`; a request for a session this agent did not
start is refused, and so is one whose path could point anywhere but your `--appium` address.

**A session starts only from a list.** A session-create reaches your Appium only when:

- every capability in it is on the agent's allow-list (`internal/allowed/allowed.go`), which says why each one is
  there. Anything else — a capability the farm starts sending later, another driver, a value of the wrong kind — is
  refused until it is added to that list on purpose;
- the phone it names (`appium:udid`), in every set of capabilities Appium could start it from, is one this agent offers
  right now. A phone you left out with `--skip`, a phone on Wi-Fi or an emulator it does not offer, and a create that
  names no phone (Appium would pick one itself) are refused;
- the build it installs, if it names one (`app`), is an `https` address on the farm's own host — the one you gave
  `--farm`. A path on this machine, another host or plain `http` is refused, and so is such a build installed later
  in the session.

**What the farm can never ask.** Whatever a later farm sends, the agent refuses:

- a capability that names a program, a path or an address on this computer: `appium:chromedriverExecutable`,
  `appium:chromedriverExecutableDir`, `appium:webDriverAgentUrl`, `appium:derivedDataPath`, `appium:prebuiltWDAPath`,
  `appium:keychainPath` and `appium:keychainPassword`;
- inside a session, whatever runs a program, or reads or writes files, on this computer rather than on the phone:
  `execute_driver`, `mobile: shell`, an emulator's console (`mobile: execEmuConsoleCommand`), screen and audio
  recordings and screen streams that Appium makes or uploads from this computer, `xctrace`, XCTest bundles run from
  here, `mobile: simctl`, a certificate served from here, and Appium's own server log;
- registering a phone with your Apple developer team (`appium:allowProvisioningDeviceRegistration`). That is the yes
  `npx {{PACKAGE}}@latest setup-ios` asks you for, at this machine;
- a live view of anything but a phone's own screen server, at the address this machine reserved for it.

Each refusal is a sentence in your terminal and an error the farm gets back, and nothing is sent to Appium. A redirect
from BrowserStack's hub is not followed. An answer too large for one message to your workspace is answered with an
error rather than relayed, and nothing the farm sends is printed with terminal control characters in it.

**Do not start Appium with `--relaxed-security` or `--allow-insecure` for this agent.** They switch on Appium features
that run commands on this computer — `mobile: shell`, `execute_driver`, an emulator's console, fetching a chromedriver.
The agent refuses the ones it knows by name; started without those flags, Appium refuses all of them itself, including
any a newer driver or plugin adds, so the second lock does not depend on this agent knowing their names. A web-view run
then uses a chromedriver your Appium already has.

**What the agent tells the farm about this machine**, with every heartbeat:

- the machine's host name — on a Mac, often a person's name;
- each phone it finds: its name, model, maker, OS version and serial or UDID, whether it is offered and, if not, why;
- the Apple team id, WebDriverAgent bundle id and signing identity you declared, the WebDriverAgent port it chose for
  each iPhone and the live-view port for each phone;
- your Appium's local address (`--appium`);
- what `doctor` found about each phone, as check names and states, never its text;
- whether network capture is on here, and which phones are set up for it;
- this agent's version.

Beyond that, what leaves this machine for the farm is only what it asks for: your Appium's answers to a session's
commands, a phone's live view, and a capture run's masked recording when the farm asks for that run's file. Your
BrowserStack key is never sent to us.

**Network capture records a phone's traffic, and only you can turn it on.** It needs three yeses, each from a person
— this machine, each phone, and the scenario (*Network capture*, below). The farm can start and stop a capture only on a
phone a person set up, on a machine where a person turned capture on. While a capture run lasts, every app's traffic on
that phone goes through a proxy on this machine; what is recorded is masked here, and stays here until the farm asks
for that run's file.

## Your own BrowserStack devices

**A walking skeleton, and it is honest about being one.** With both halves supplied, `connect` replays the
drive tunnel against BrowserStack's hub instead of your local Appium:

```sh
printf 'BrowserStack access key: ' && read -rs NTH_AGENT_BROWSERSTACK_KEY && echo && export NTH_AGENT_BROWSERSTACK_KEY  # paste this line ALONE
```

```sh
export NTH_AGENT_BROWSERSTACK_USER=<your BrowserStack username>
npx {{PACKAGE}}@latest connect --farm https://farm.example
```

**Paste that first line on its own**, for the same reason as the token above — `read` takes whatever
follows it as the value. The username is not a secret and is set the ordinary way; the key is not.

**Your key never leaves this machine, and that is architectural rather than careful.** The platform drives
this agent by sending a method, a path, a body and a content type — it cannot set a header and it has no field
that could carry a credential. The key is read here, merged into the one session-creation request this agent
sends to BrowserStack, and never written to disk, never printed, and never sent to us. BrowserStack's answers
reach your run through us, so the agent does not count on them leaving the key out: before it relays any answer
from BrowserStack, it replaces the key wherever it appears — as written, escaped, percent-encoded, in hex or in
base64 — with `«redacted»`. Prefer the environment variable over `--browserstack-key` for the same reason as the
token: arguments are visible to every process on this machine (`ps`), an environment variable is not — and prefer
the prompt above to either, because an inline `export` writes the key verbatim into your shell's HISTORY FILE,
which outlives the process, the terminal and the reboot. *"Prefer the variable"* on its own would be poor advice.

**A build is required, and this is not our rule.** BrowserStack runs *its* devices: there is no adb, nothing for
us to install, and a session cannot start without an `app` capability naming a build already in their storage.
Ask the agent to put one there — `upload` above — then give `connect` the id it returns:

```sh
npx {{PACKAGE}}@latest upload --file ./path/to/app.apk
export NTH_AGENT_BROWSERSTACK_APP=bs://<the id it printed>
```

If you set the credentials and no app, `connect` stops before any session is attempted. That is deliberate: the
hub's own refusal for a missing build says *"Platform and Browser not valid."*, which sends you to check two
things that are fine and never mentions the build.

**With neither credential set, nothing changes** — the agent drives your local Appium exactly as before, and the
app is not required or read. With only one credential set, `connect` stops and says so, because guessing either
way would hide the same typo.

**Name the devices you want offered.** One `--browserstack-device` per device, as `"Name|OsVersion"`:

```sh
npx {{PACKAGE}}@latest connect --farm https://farm.example \
  --browserstack-device "Google Pixel 9|15.0" \
  --browserstack-device "Samsung Galaxy S23|13.0"
```

Each becomes a leasable row on the platform, exactly like a phone plugged into your machine. They are checked
against your account's own device list when the agent starts — free, no session, no device-minute — and a device
your account does not offer stops the agent with a list of the ones it does.

**You name them; we do not publish your catalogue.** An account offers around a hundred devices and you use a
handful. A Devices screen with a hundred rows on it is a worse product than one with your three, so the choice is
yours rather than automatic. Nothing technical prevents publishing all of them.

**No local Appium is needed for this.** If every device you want is on BrowserStack, `connect` needs no
`--appium` and no phone plugged in: the agent registers, opens its tunnel, and publishes your cloud rows. The
local-Appium prerequisite still applies, unchanged, to devices attached to *your* machine — it exists because we
will not install software on hardware we do not own, and on the BrowserStack path nothing is installed anywhere.

**A machine can do both.** A phone on your desk and two cloud devices from one agent: three rows, and neither
path weakens the other. Every session is routed to where its device is — a phone's to the local Appium, a cloud
device's to BrowserStack's hub — decided from the device the lease named, never from which flags the agent was
started with, and a session for a phone on your desk is never sent to anybody's hub. (This sentence was not true
until 2026-09-02: with credentials configured, every session went to the hub and a local one was refused there.)

## Watching a run on your phone — live view

A person watching a run on one of your phones sees its screen as the run happens. The picture is Appium's own screen
server for that phone — the UiAutomator2 driver's on Android, the XCUITest driver's on an iPhone — on a port this
machine keeps for that phone alone, on its loopback address, and the agent carries it through the connection it has
already opened to your workspace: nothing dials in to you.

`npx {{PACKAGE}}@latest devices` prints a `live view:` line under each usable phone, and `npx {{PACKAGE}}@latest doctor` a **live view** line
per phone: *yes*, with the port it is on, or *no*, with why and what to do. A phone can be shown when Appium's driver
for it is installed here — for an iPhone, XCUITest 8.1.0 or later, the first release that forwards a real iPhone's
screen server to this machine — and a port is free for it. **Live view never stops a run**: a phone that cannot be
shown is still driven.

## Network capture — off until you turn it on

A capture run records your test app's network traffic, so each request and response appears on the run's Network
tab. **It is off.** It needs three yeses, each from a person: this machine (`npx {{PACKAGE}}@latest capture enable`, which explains
all of this first and asks you to type `ENABLE`), each phone (`npx {{PACKAGE}}@latest capture setup`), and the scenario, which asks
for capture. Android phones only, on macOS and Linux.

```
npx {{PACKAGE}}@latest capture enable          # explains what capture does, asks you to type ENABLE, then makes this machine's CA
npx {{PACKAGE}}@latest capture setup [SERIAL]  # copies the CA's certificate to a test phone, says how to install it, checks it
npx {{PACKAGE}}@latest capture disable         # hands every phone back, deletes the CA's key and every capture file here
```

**The whole phone's traffic goes through this machine — use a test phone.** For the length of a capture run the
phone's proxy setting points at this machine, over the USB cable (`adb reverse`), so every app on the phone that uses
that setting — not only the one under test — goes through a proxy on this machine and out through this machine's
network. Use a phone kept for testing, not your daily one.

**A capture keeps to the internet.** While capture is on, the proxy refuses to connect to this machine and its local
network: loopback, link-local addresses (the cloud metadata address `169.254.169.254` among them), private and
unique-local ranges, and carrier-grade NAT, over IPv4 and IPv6. It judges the address it actually dials, after the
name is resolved, so a name or a redirect that leads there is refused too. A refused request is in the capture with
the reason, and the phone gets the same answer: *"refused: the capture proxy does not connect to this machine or its
local network — to let a capture reach a local backend, restart with npx {{PACKAGE}}@latest connect …
--capture-allow-local-network"*. If your app under test talks to a backend on this machine or your office network,
start `connect` with that flag. While it is on, `connect` says so when it starts and when each capture starts.

**Only debug builds that trust user certificates can be read.** An Android app trusts a certificate a person
installed only when it is built to, which a debug build usually is. In a release build, every host it reaches over
HTTPS is recorded once as not readable and passed through untouched. In a debug build, a host the app pins, or one
whose library trusts only its own certificates, is recorded the same way — *"not readable: this host did not accept
the capture certificate (the app pins it, or its library trusts only its own certificates)"* — and the rest of the
app's traffic is read. Either way the phone's own services keep working.

**Masked here, before anything leaves this machine — and masked again when it reaches us.** The `Authorization`,
`Proxy-Authorization`, `Cookie` and `Set-Cookie` headers; any header, JSON field, form field or query parameter whose
name says it holds a credential (`token`, `password`, `api_key`, `secret`, a signature such as `sig` or `af_sig`, …);
and values shaped like credentials — JWTs, bearer and basic credentials, private keys, published API-key formats —
become `«redacted»`. A form field's or query parameter's value is masked whole, whatever characters it holds, and a
parameter written inside another's value (`?next=/reset?token=…`) is found too. Compressed bodies (gzip, deflate, br,
zstd) are decoded first so that they are masked too. A body this agent cannot decode or read as text is left out
entirely — never sent as it is — and the record says why: that includes protobuf, gRPC, MessagePack, CBOR and other
binary formats, whatever their first bytes look like, and any body with a control character in it. We mask again on
arrival, and both maskers are held to one shared set of test cases.

**What reaches us, and for how long: the masked record, kept with the run for one hour.** A body is kept up to
256 KB, and a run records at most 500 requests. **Nothing raw is ever kept on this machine**: each request is masked
in memory before it is written, and the masked file waits only until your workspace pulls it, then is deleted — or an
hour after the capture stops, if nothing pulls it.

**The agent never sends the CA's key anywhere and keeps it out of backups, and the CA lasts one year.** To read
HTTPS, `capture enable` makes one certificate authority for this machine — RSA 2048, named *NthCase agent CA — (this
machine's name)* — in your user config folder, its key readable by you alone. It signs a short-lived certificate (two
days, held in memory) for each host a test phone talks to, and nothing else. Only the capture proxy reads the key,
and nothing sends it anywhere. `npx {{PACKAGE}}@latest doctor` says when the CA ends and, from 30 days before, how to renew
it.

**Backups.** The capture folder holds the key and, for an hour at most, capture files, so it is kept out of backups:
on a Mac it carries Time Machine's own exclusion (the one `tmutil addexclusion` sets, set on the folder itself, so
nothing about Time Machine changes), and on Linux it holds a `CACHEDIR.TAG`, which restic, borg, Déjà Dup and
GNU tar (asked to skip caches) leave out. A backup made before that, or by a tool that ignores both, may still hold a copy of
the key and of capture files; deleting the folder does not reach that copy. Removing the certificate from every
phone is what ends a copied key's use.

**Turning it all off.** `npx {{PACKAGE}}@latest capture disable` stops every capture, hands every phone back (its proxy setting
cleared, its `adb reverse` removed), deletes the CA's key and certificate and every capture file on this machine, and
prints how to remove the certificate from each phone. Deleting is a file removal: on a disk without encryption a
deleted file's blocks can outlive it, so keep this machine's own disk encryption on (FileVault on a Mac). **On a Samsung it is listed in two places** — *View security certificates →
User* and *User certificates* — remove it in both. A phone that cannot be reached keeps a record on this machine and
is handed back by the next `connect`; until then it has no working internet, and the command that clears it by hand is
printed.

**On a machine other people log into.** On a machine other people log into, the machine's administrator (root) could
send traffic through the capture proxy while a capture runs; a connection from any other account is refused unread.
The proxy listens on this machine's loopback address, which every account here can reach, so before it reads a byte of
a connection it asks the machine's own kernel which account opened it (`/proc/net/tcp` on Linux, the
`net.inet.tcp.pcblist64` list on macOS). A capture, and `capture setup`'s check, start only once that question has
been answered for a connection of this agent's own.

`capture enable` exits 0 once capture is on and 2 when `ENABLE` was not typed (nothing is changed); `capture setup`
exits 0 when the phone passed its check, 1 when the check failed or could not run, and 2 when you stopped it or must
name the phone; `capture disable` exits 1 when anything is left undone, and says what.

nth-agent never signs or re-signs your apps, and never touches your signing certificates or keychain. When you turn on
network capture, it makes one certificate authority of its own, on this machine, used only to stand in for the servers
the apps on your test phone talk to, so that their HTTPS can be read — and only in apps built to trust it. The agent
never sends its private key anywhere and keeps it out of backups (a backup made before that, or by a tool that ignores
it, may still hold a copy); it lasts one year, and you can remove it any time (npx {{PACKAGE}}@latest capture disable, then
delete the certificate on the phone).

**Read more** about what your workspace shows and keeps of a capture:
[Network capture on your own phone](https://nthcase.com/docs/your-devices#network-capture) and
[Privacy: network capture](https://nthcase.com/privacy#network-capture).

## What this leaves running on your phone, and when it stops

A run leaves **WebDriverAgent** running on the device. That is deliberate on Appium's side and it is why
the next run starts in about a fifth of the time (measured: 6702 ms cold, 1693 ms warm) — and it is why
your phone shows **Automation Running** after the test finishes.

By default `connect` **stops it once nothing has used it for five minutes**, and never while a session is
attached. It asks WebDriverAgent to end itself through its own `/wda/shutdown` route over a temporary USB
port forward; it installs nothing, starts no daemon, and kills no process. It says at startup which of
the two it will do, so nothing it does later is news.

- `--keep-wda-warm` — leave it running. The banner stays; the next run stays fast.
- `--wda-idle-after 30s` — change the window.

How it knows nothing is using it: WebDriverAgent's own `GET /status` carries
`sessionId = activeSession.identifier ?: null`, so a null is the runner saying, about itself, that nothing
is driving it. A port that does not answer means there is nothing to stop; **a question that could not be
answered stops nothing at all**.

## Three outcomes, carried in the type

Each platform probe returns exactly one of:

| Outcome | Means | Carries |
|---|---|---|
| `answered` | the probe ran and established the truth | the devices it can offer **and** the ones it saw and cannot offer — either list may be empty, and empty here is a real answer |
| `could_not_ask` | the probe ran and failed — tool missing, daemon unreachable, timeout, non-zero exit | a **reason** (what happened) and a **remedy** (what you do about it). This is not an empty inventory; it is the absence of an answer |
| `not_asked` | the probe is switched off on this host | nothing at all — a disabled probe is not a faulty one |

Two rules the types enforce rather than document:

- A device the agent **cannot offer can never become an offerable one**: they are different Go types,
  and the constructor for an answer takes only the offerable list, so the compiler refuses the swap.
- A device's state is the platform's **own word, verbatim** (`unauthorized`, `offline`,
  `no permissions`, …) — not an enum. A state a future `adb` or `go-ios` prints survives the trip and
  is shown to you, never flattened into "other".

## Exit codes

- `0` — at least one probe established the truth. **Zero devices attached is a success.**
- `1` — no probe could answer. That is not the same as "nothing is attached".
- `2` — the command line was not understood.

## Every input, and which wins

Wherever a flag and a variable name the same thing, **the flag wins and the variable is the fallback** —
every pairing below is read exactly that way, once, at startup. Nothing else is read from the
environment. (The binaries the doctor looks for — `adb`, `appium`, `xcodebuild` — are found on `PATH`,
which is your shell's mechanism rather than this agent's variable. adb's own `ADB_SERVER_SOCKET`,
`ANDROID_ADB_SERVER_ADDRESS` and `ANDROID_ADB_SERVER_PORT`, which adb reads anyway, are read only to find the server
they name and ask who runs it; one that points adb off this machine's `127.0.0.1` means adb is not used.)

| Flag | Environment fallback | Commands | What it is |
|---|---|---|---|
| `--json` | — | devices, doctor | the same facts, machine-readable |
| `--skip PLATFORM` | — | devices, doctor, connect | leave a platform alone (repeatable; `android`, `ios`) — it reports *not asked* |
| `--offer-uncabled-devices` | — | devices, doctor, connect | offer emulators and phones reached over Wi-Fi or the network too; without it only phones on this machine's USB cable are offered |
| `--appium URL` | `NTH_AGENT_APPIUM` | doctor, connect | the local Appium endpoint, on this machine — probed, or offered for runs; never guessed, and used only when your account runs it |
| `--why` | — | doctor | print every caveat, including the ones a passing check states |
| `--no-color` | `NO_COLOR` (presence, not value) | doctor | plain output; the flag wins over the variable |
| `--ios-team-id TEAM` | `NTH_AGENT_IOS_TEAM_ID` | doctor, setup-ios, connect | your Apple Developer team id — a free Personal Team works |
| `--ios-wda-bundle-id ID` | `NTH_AGENT_IOS_WDA_BUNDLE_ID` | doctor, setup-ios, connect | the WebDriverAgent bundle id registered to that team |
| `--ios-signing-id NAME` | `NTH_AGENT_IOS_SIGNING_ID` | setup-ios, connect | the signing identity; unset, **`Apple Development`** |
| `--udid UDID` | — | setup-ios | pick the phone when more than one is attached |
| `--wda-project PATH` | — (the search honours `APPIUM_HOME`) | setup-ios | the `WebDriverAgent.xcodeproj` to build, instead of searching Appium's home |
| `--derived-data-path PATH` | — | setup-ios | where the build lands; unset, a directory under your user config dir |
| `--yes` | — | setup-ios | answer the plan's question in advance |
| `--farm URL` | `NTH_AGENT_FARM` | connect | the farm's base URL — `https://` required unless exactly `localhost` or a loopback address |
| `--token TOKEN` | `NTH_AGENT_TOKEN` — **set it at a prompt** | connect | the workspace agent token; the flag puts it in `ps`, an inline `export` puts it in your history file — see `connect` above |
| `--keep-wda-warm` | — | connect | leave WebDriverAgent running after a run instead of stopping it when idle |
| `--wda-idle-after DUR` | — | connect | the idle window before it is stopped (default five minutes); ignored with `--keep-wda-warm` |
| `--capture-allow-local-network` | — | connect | let a capture reach this machine and its local network; without it the capture proxy refuses them, and with it `connect` says so on screen |
| `--browserstack-user NAME` | `NTH_AGENT_BROWSERSTACK_USER` | connect | your BrowserStack username — see *Your own BrowserStack devices* below |
| `--browserstack-key KEY` | `NTH_AGENT_BROWSERSTACK_KEY` — **set it at a prompt** | connect | your BrowserStack access key; the same two exposures as the token, and the same prompt form |
| `--browserstack-app ID` | `NTH_AGENT_BROWSERSTACK_APP` | connect | the build to run — a `bs://…` id or a `custom_id`; **required** whenever the two above are set |
| `--browserstack-device SPEC` | `NTH_AGENT_BROWSERSTACK_DEVICE` | connect | a device to offer, `"Name\|OsVersion"`; **repeatable**, checked against your account's list at startup |

## Getting it

```
npx {{PACKAGE}}@latest --version
```

That is the whole install. The package carries the binary for your machine and runs it; there is nothing to
download by hand, nothing to put on your `PATH`, and no repository to open.

**Every command on this page runs the same way**, as `npx {{PACKAGE}}@latest <command>`. The program inside the package is
called `nth-agent`, but `npx` does not leave that name on your `PATH`, and putting `npx` in front of it asks npm for a
different package.

**Say which release runs.** With no version after the name, `npx` can run a copy that is already on the machine —
one it fetched earlier, or one installed with `npm i -g` — however old it is. With `@latest` after the name, as every
command on this page has it, `npx` asks the registry for the newest release each time. **Pin it when you care which
build runs:** `npx {{PACKAGE}}@{{VERSION}} …` runs exactly that release, every time, on every machine, until you
change the pin.

**`--version` prints the release you are running**, and the npm package and the binary always agree: the publish
workflow stamps the package from the release tag, runs the binary it is about to publish, and refuses to publish
if the two disagree. Its second line says where the third-party notices are (below).

**Node 18 or newer**, which is what `npx` itself needs. The agent is a Go binary — Node runs the one-file shim
that picks it, and nothing else.

**What you get if your machine is not one of the three targets** — `darwin-arm64`, `darwin-x64`, `linux-x64` —
is a sentence saying so and naming what is built, not a broken install.

⚠️ **npm verifies what it downloads and there is no separate checksum step for you to run.** Every tarball the
registry serves carries an integrity hash that npm checks before it unpacks anything; a corrupted or substituted
download fails there. There is nothing further to verify by hand, and a checksum run against an npm install is
checking a box npm has already ticked.

⚠️ **There is no self-update — the agent never replaces itself — and that is still a decision.** It has no
update code path: nothing it
does on your machine changes which build runs the next time. That is not the same claim as "nothing ever
fetches a newer version" — `npx` does, when you type a command that asks for one, and that difference is the whole point:

- a **self-updating binary** rewrites itself *unasked*, on its own schedule, and has to be told where to fetch
  from — and whoever can answer that owns every device that machine drives;
- **`npx`** fetches *when you type the command*, from a registry you can inspect, list and audit, and `@{{VERSION}}`
  pins it. The fleet does not move underneath somebody debugging it unless somebody ran something.

## Third-party notices

nth-agent is built with code written by others — Go's standard library and runtime, the Go modules it uses, and one
function ported from mitmproxy — and their licences ask that their notices travel with every copy of it. They are in
`THIRD_PARTY_NOTICES.txt`, which ships in this package beside this README, and `npx {{PACKAGE}}@latest notices` prints the same text
on any machine the agent runs on. The list is made from what each published program links, not kept by hand.
nth-agent's own code is under the Apache License 2.0 (below), not under those licences.

## Licence

nth-agent is licensed under the Apache License 2.0 (see `LICENSE`, and `NOTICE` for what it includes from others). The
"NthCase" name and logo are trademarks of Unidots Software Solutions LLC and are not covered by that licence; you may not
use them for a fork or another product without our written permission. We do not accept pull requests at this time
(`CONTRIBUTING.md`); report a security problem as `SECURITY.md` says, privately, never in a public issue.

<!-- NOT-FOR-THE-REGISTRY -->

Everything above this line is the page npmjs.com renders for this package. Everything below it is ours: how this
repository is built and tested, and where its releases live. The publish workflow CUTS HERE — it does not copy
this file whole — so a section added below never reaches a customer by accident, and a file with no marker fails
the publish instead of shipping entire.

## The release page, and what it is for

`.github/workflows/release.yml` builds the three targets on a tag and publishes archives on this repository's release
page. **They are the source of the npm package, not a second way to install:** run the agent with `npx`, as everything
above this line says. The npm package above is built from exactly these bytes: the publish workflow downloads this release and
repackages it, so there is never a second build of one tag. It checks that first: every archive on the release must
be, byte for byte, the one the release workflow's run kept as an artifact for the tag's commit, or nothing is
published. Those artifacts are kept 90 days by default, so publish a release within that time, or cut a new tag.

Each archive holds the binary, this README, `THIRD_PARTY_NOTICES.txt`, and the agent's own `LICENSE` and `NOTICE`. The
notices are also attached on their own, and each build leg refuses a binary that links a module the notices do not name;
a tag whose tree has no `LICENSE` or no `NOTICE` is refused before anything is built. **The release notes are the tag's
own section of `CHANGELOG.md`**, and a tag with no section is refused before anything is published — so write the
section, commit it, and only then tag. The npm publish then refuses a release with no notices, or with notices that are
not the tag's.

If you take an archive directly anyway — to read the binary, or to check what npm shipped:

```
shasum -a 256 -c SHA256SUMS --ignore-missing
tar -xzf nth-agent_*.tar.gz
./nth-agent --version
```

⚠️ **macOS will refuse to open a hand-downloaded binary the first time, and nothing is wrong.** These binaries
are **not signed or notarized**, so Gatekeeper quarantines *anything a browser wrote* and reports *"cannot be
opened because the developer cannot be verified"*. Check the checksum above, then clear the attribute yourself:

```
xattr -d com.apple.quarantine ./nth-agent
```

**This does not apply to the npm path.** `com.apple.quarantine` is attached by the program that writes the file,
and npm does not attach it — measured, not assumed: nothing npm has installed on a development machine here
carries the attribute, while files in `~/Downloads` do. An `npx` user never sees the dialog and has no attribute
to clear. Signing and notarizing would remove the step for hand-downloads too, and need an Apple Developer ID
certificate plus a notarization submission — credentials this repository does not hold.

## Building

You can also build it yourself — Go is pinned by `go.mod` (one home; CI reads the same file):

```
go build ./...
go vet ./...
go test ./... -race
```
**A few seals compare this agent with the server's side of a contract** — the farm's sentences to a node, the
masking rules the server applies to a capture. They read the server's source only when a checkout has it beside this
repository, and a file there (`../nth-agent.server-sources.json`) says where each part is. Everywhere else, this
repository's CI included, they skip, and each says in its skip line that the agent was not held to that side in that
run.

CI builds and tests on Linux and cross-compiles **darwin/arm64, darwin/amd64 and linux/amd64** — the
platforms this repo claims are exactly the platforms it compiles.

**`THIRD_PARTY_NOTICES.txt` is generated, never edited by hand.** `go test ./...` fails while it is not what the
release's programs link — a module missing, a module no longer linked, a version or a licence text moved — and names
which. After a change to `go.mod`, a new import from a module, or a new Go version, rewrite it and read the diff:

```
go test . -run TestTheNoticesAreWhatTheReleaseLinks -update
```

## Tests need no phone

Every test runs without hardware, without a running adb server, and without a reachable usbmuxd: the
process seam (`android.Runner`) and the go-ios seam (`ios.Lister`) are injectable. Parser fixtures are
verbatim captures from a real machine wherever one could be taken — with the machine's and its phones' names, serials
and ids replaced by invented ones of the same shape — and every invented fixture is labelled as invented, with the case
it stands for, in the fixture directory's README.
