# Changelog

What changed in each release of nth-agent, newest first. Each heading is a version as npm names it, and a release's
notes are its section here. A command is named here by what you type after the package's name, `capture enable` for
example: run it through `npx` with that name in front, the way the README shows.

## 0.5.1

- **A capture keeps its last 500 requests, and says how many it left out.** Until now a long run's capture kept its first
  500 requests and dropped the newest. Now it keeps the last 500, and the run's Network tab says how many earlier requests
  are not shown. A header longer than 16,384 characters and an address longer than 4,000 are cut, and say where.
- **A session cannot ask your Appium to read a screen deeper than a run reads.** A settings call that raises Appium's
  `snapshotMaxDepth` above 126, or names it in a form the agent cannot read, is refused before it reaches Appium. The depths
  a run uses pass as before.
- **A route is judged however it is spelled.** Appium matches routes without regard to upper and lower case; the agent now
  does too, so a refused command cannot pass in other letters.

## 0.5.0

- **A session the farm starts on your phone carries only what the agent's list allows.** Until now the agent refused one
  capability on a session for a phone on this machine — registering the phone with your Apple team — and passed the rest
  to your Appium as the farm wrote them. Now every capability must be on the agent's allow-list, with a value of the kind
  that list allows, or the session is refused before it reaches Appium. The README lists what the farm can ask.
- **A session starts only on a phone this agent offers right now.** A session for a phone you left out with `--skip`, a
  phone on Wi-Fi or an emulator the agent does not offer, a phone unplugged since the last heartbeat, or a session that
  names no phone, so that Appium would pick one itself, is refused.
- **A build comes only from the farm.** The build a session installs must be an `https` address on the farm's own host,
  the one you gave `--farm`. A path on this machine, another host or plain `http` is refused, at the start of a session
  and during one.
- **Never a program, a path or an address on this computer.** A capability naming a chromedriver to run, a folder to start
  one from, a WebDriverAgent address, build or folder, or a keychain and its password is refused, whatever sends it.
- **Inside a session, nothing runs on this computer.** `execute_driver`, `mobile: shell`, an emulator's console, screen and
  audio recordings and streams that Appium makes or uploads from this computer, `xctrace`, XCTest bundles run from here,
  `mobile: simctl`, a certificate served from here and Appium's own server log are refused. Taps, typing, the page
  source, screenshots, the phone's log, and installing and removing the build under test pass as before.
- **What you might notice.** A refused session fails at its start, and your terminal and the run both say what was
  refused and why; nothing reached your Appium. Every session the farm sends today passes as it did. If you started
  Appium with `--relaxed-security` or `--allow-insecure` for the agent, you no longer need to and should not: a web-view
  run then uses a chromedriver your Appium already has.
- **The agent's source is public, under the Apache License 2.0.** The package now carries `LICENSE` and `NOTICE`, and its
  licence field says `Apache-2.0`. The README says what the agent tells the farm about this machine. To report a security
  problem, follow `SECURITY.md`: privately, by GitHub's private vulnerability reporting or by e-mail, never in a public
  issue.
- **Every archive on the release page carries the licence too.** `LICENSE` and `NOTICE` are inside each one, beside the
  binary, as `THIRD_PARTY_NOTICES.txt` is.

To update, run `--version` through `npx` with `@latest` after the package's name; its first line answers
`nth-agent v0.5.0`. A machine pinned to a version stays on it until you change the pin.

## 0.4.4

- **A farm that is restarting reads as a farm that cannot be reached.** While the farm restarts, the address in front of
  it answers 502, 503 or 504 with a page of its own and none of the farm's words. `connect` took that for the farm's
  answer: it said the farm did not take its heartbeat and refused the drive tunnel, then that the farm took its
  heartbeat. Nothing was refused. Now it says once that it cannot reach the farm, with the status, and "connection to the
  farm restored" when the farm is back. The drive tunnel says it is not connected yet, or that it dropped, and the
  goodbye on a stop says it could not reach the farm. How soon the agent asks again does not change.
- **The farm's own answers keep their words.** A 503 or a 429 with the farm's sentence reads as it did in 0.4.2, and so
  does every other status.
- **Stopping `connect` during a heartbeat no longer says the farm cannot be reached.** Ctrl-C ends the heartbeat or the
  registration in flight, and the agent said it could not reach the farm and would retry, while it was stopping. Now it
  says nothing about that call. The stop's own lines are as before.

To update, run `--version` through `npx` with `@latest` after the package's name; its first line answers
`nth-agent v0.4.4`. A machine pinned to a version stays on it until you change the pin.

## 0.4.3

- **Every command the agent tells you to type runs this release or a newer one.** Its lines used to name the package
  through `npx` with no version, and with no version `npx` can run a copy that is already on the machine, however old it
  is. A person who copied the agent's own advice could start an old copy and not know it. Now each command fits how the
  agent was started. Started through `npx`, it has `@latest` after the package's name. From a global install, it is the
  `nth-agent` command alone, which runs that install. From a project, it is that project's own copy. When the agent
  cannot tell, it uses `@latest`. This holds in `connect`, `doctor`, `capture`, `setup-ios`, `upload`, `--help` and
  `--version`.
- **The lines that tell you to start `connect` again keep their flags and their words.** Only the command changes: for an
  Appium on another machine, for phones not on this machine's USB cable, for a capture that may reach your local network,
  and for a build you have just uploaded.
- **A sentence read on another screen names no folder of this machine.** What your workspace, a run or a phone reads, such
  as why a capture was refused or why an iPhone is not offered, uses `@latest` when the agent was installed in a project,
  instead of that project's folder.
- **Lines about a `connect` that is already running read as before.** They name a program that is running, not a command
  to type.
- **An answer the agent cannot use is said as an answer.** When the farm takes a call and the agent cannot use its answer
  (it cannot be read, it is too large, or it has another shape), `connect` says the farm answered, with the status and
  what was wrong, once. It used to say it could not reach the farm. It retries as before.
- **The README says what `npx` does.** It said that `npx` fetches the latest version by default. It does not: with no
  version it can run a copy already on the machine. Every command in the README now has `@latest`, and the README says
  what `npx` does with no version, with `@latest`, and with a pinned version.

To update, run `--version` through `npx` with `@latest` after the package's name; its first line answers
`nth-agent v0.4.3`. A machine pinned to a version stays on it until you change the pin.

## 0.4.2

- **`connect` says why the farm said no.** When the farm turns a call away, the agent prints the farm's own sentence with
  the status. For example, it says that your workspace already has as many machines as it may have, and what to do about
  it. It no longer says it cannot reach the farm when the farm answered. When the farm gives no reason, the agent says so
  and names the status.
- **A limit is said once.** A machine at one of its workspace's limits sees the line once, and again only when the farm says
  something different. When the farm takes its calls again, one line says so. A heartbeat the farm did not apply used to
  print a line every ten seconds.
- **At a limit, the agent keeps running and asks less often.** It waits longer each time, up to a minute. After a heartbeat
  the farm turned away it waits at least its usual ten seconds.
- **The drive tunnel says why the farm refused it.** When your workspace already has as many machines with a tunnel open as
  it may have, `connect` prints the farm's sentence instead of a WebSocket error. It says it again only if the farm's reason
  changes.
- **At the tunnel limit, the drive tunnel tries less often.** Every heartbeat the farm took made it try again at once, so it
  tried about 24 times a minute for as long as the limit lasted. Now it waits a second, then twice as long each time, up
  to a minute, and heartbeats do not cut the wait short. When a machine with a tunnel open stops, this one connects on a
  later try, within about a minute. Any other failure, or a tunnel that opens, puts the old schedule back, so a farm that
  was away and comes back still gets its tunnel back at once.
- **A heartbeat the farm answers with 404 says what the farm said.** The agent prints the farm's sentence, for example that
  this node said it was leaving, and registers again with the same identity, as before. It used to guess that the node
  may have been purged. When the farm gives no sentence, the line is as before.
- **A refused identity says what is true.** When the farm will not register this node, the line says that this id is
  already registered at the farm, but not to the workspace of your token, and what to do. It used to say the node may
  belong to another workspace.
- **The goodbye says what the farm said.** When the farm does not take the goodbye on a stop and gives a reason, the line
  quotes it. When the farm did not know this node, the line says just that, and that the phones go off offer within a
  minute, without the farm's advice to register again.

To update, run `--version` through `npx` with `@latest` after the package's name; its first line answers
`nth-agent v0.4.2`. A machine pinned to a version stays on it until you change the pin.

## 0.4.1

- **`connect` says when a newer agent is out.** When your workspace knows of a newer release than the one running, the
  agent says so once: its own version, the newer one, and how to update. It says it again only if your workspace names a
  different release later, and it keeps working as it is: nothing stops, and nothing updates by itself. How to update
  depends on how it was started, which the agent reads from where its own program is. Started through `npx`, run
  `connect` through `npx` with `@latest` after the package's name. Installed with `npm install -g`, run that install
  with `@latest` after the name. Installed in a project, update it in that project. When the agent cannot tell, it names
  the first two. A copy installed with `npm install -g` is what `npx` runs before it fetches a newer one, and when that
  is what happened the agent says so.
- **Stopping `connect` takes its phones off offer at once.** On Ctrl-C, SIGTERM or the terminal closing, once its phones
  are handed back, the agent tells your workspace this machine is leaving, so its phones stop being offered now instead
  of 30 to 40 seconds later. It waits three seconds at most for the answer, and a second Ctrl-C still ends it at once.
  When the workspace does not take the goodbye, the agent says so in one line and exits as before; the phones then go
  off offer the old way. It says no goodbye when it ends for any other reason, a refused token for example.
- **`connect` fits its lines to your terminal.** In a terminal, each line is folded at a space to the terminal's width,
  and a command, an address or a path is never split. Written to a file, a pipe or a CI log, every line stays whole, as
  before.
- **A start with `--appium` no longer says that devices are not offered.** The drive tunnel opens once this machine is
  registered, so every start used to say "devices are NOT being offered", seconds before the drive path was ready. It
  now says that the tunnel opens after registration. Where devices really are not offered (no `--appium`, Appium not
  answering, the tunnel down), the reason and what to do are still said, without capitals.
- **`doctor` explains network capture once.** It used to print what capture means and then the agent's promise about
  signing, and both said that the agent never sends the key anywhere, that it keeps it out of backups, that the
  certificate authority lasts a year and how to turn capture off. With capture on, its first line said the key sentence
  a third time. Each is said once now.

To update, run `--version` through `npx` with `@latest` after the package's name; its first line answers
`nth-agent v0.4.1`. A machine pinned to a version stays on it until you change the pin.

## 0.4.0

Read these first. Each is something 0.3.0 did by default that this release does only when you ask, or not at all.

- **Only phones on this machine's USB cable are offered.** Emulators, and phones reached over Wi-Fi or the network
  (`adb connect`, wireless debugging, an iPhone paired over the network), are no longer offered to your workspace or
  named to it, so their rows there go offline. `connect`, `devices` and `doctor` say which were left out and why. Plug
  them in by cable, or pass `--offer-uncabled-devices` to `connect` (and to `devices` and `doctor`) to offer them too.
- **A capture keeps to the internet.** During a network capture the agent no longer connects the phone to this machine
  or its local network: loopback, link-local, private and carrier-grade NAT addresses, IPv4 and IPv6, judged on the
  address it actually dials. The phone gets an error and the capture records why. If your test app talks to a backend
  on this machine or your office network, start `connect` with `--capture-allow-local-network`; `connect` says so while
  it is on.
- **One `connect` at a time.** A second `connect` for the same account on this machine is refused, with exit status 1,
  until the first is stopped. Two would offer the same phones twice.
- **The agent uses only the Appium, adb server and screen servers your own account runs on this machine.** One that
  runs as another account, or whose owner the system cannot name, is not used, and `connect` and `doctor` say whose it
  is. An `--appium` address that is not on this machine, or an Appium listening only on `::1`, is not used either; nor
  is an adb server that adb's own variables point away from `127.0.0.1`. Start Appium yourself, as this account, on
  `127.0.0.1`.
- **A run cannot register your phone with your Apple developer team.** A session that asks Appium for that
  (`allowProvisioningDeviceRegistration`) is refused, and nothing is sent to Appium. `setup-ios`, at this machine, is
  where you say yes to it.

And the rest:

- **Every command the agent prints can be typed as it stands.** Help, remedies, errors, the second line of `--version`
  and the sentences your workspace shows from the agent now write each command through `npx` with the package's name
  in front. They used to give the program's own name alone, which `npx` would fetch as a different package.
- **The capture certificate authority's key is kept out of backups.** On macOS the capture folder is excluded from Time
  Machine, and on Linux it holds a `CACHEDIR.TAG`. `capture enable` sets this before it makes the key, and refuses if it
  cannot; `connect` sets it again each time it starts. This corrects 0.3.0, which said the key never leaves this
  machine: the agent never sends it anywhere, but a backup made before this release, or by a tool that ignores the
  exclusion, may still hold a copy. Removing the certificate from every phone ends a copied key's use.
- **A body the masker cannot read is left out of a capture, however its first bytes look.** gRPC, protobuf,
  MessagePack, CBOR, Thrift, Avro, BSON, FlatBuffers, `application/octet-stream`, images other than SVG, audio, video
  and fonts are left out with the reason, and so is a body with a control character anywhere in it, or one that is not
  the UTF-8 it is read as. Only the first hundred bytes used to be looked at.
- **A credential in an address or a form is masked whole.** The value of a query parameter, form field or fragment
  under a credential's name is masked whatever characters it holds (an apostrophe, a quote, a backslash, a space), and
  so is one written inside another parameter's value or after a path parameter.
- **Your BrowserStack access key is taken out of BrowserStack's answers.** Before the agent passes on an answer from
  BrowserStack's hub, it replaces the key with `«redacted»` wherever it appears: as written, escaped, percent-encoded,
  in hex or in base64. A redirect from the hub is not followed; the request gets an error instead.
- **The forward to an iPhone's WebDriverAgent listens on this machine's loopback address only**, and passes on
  connections from your account alone.
- **Your workspace can ask this machine for less.** A request that names no Appium session reaches your Appium only as
  `GET /status` or `GET /wd/hub/status`. A request whose path would take it anywhere but your Appium is refused. A live
  view opens only the port the agent kept for that phone, always at `127.0.0.1`. Each refusal says why.
- **One large answer no longer drops every phone's connection.** An answer from Appium or the hub too large for one
  message to your workspace, about 48 MiB, gets an error for that request alone. What the agent reads from your
  workspace is bounded too.
- **A plain `http` workspace address must be exactly `localhost` or a loopback address**, in lower case, and it never
  goes through a proxy. `LOCALHOST`, or any other spelling of it, is refused at start with the one to use. `https`
  addresses still follow this machine's proxy settings.
- **A capture start that comes too late touches nothing.** One that arrives after its run handed the phone back, or
  while `capture disable` runs, leaves the phone alone, and once capture is off every capture still running is
  stopped at the next check.
- **Closing the terminal hands the phones back.** `connect` now treats SIGHUP like Ctrl-C and SIGTERM: it gives its
  phones back and exits with status 129. It also gives them back however else it ends, a refused token included.
- **Nothing your workspace sends reaches your terminal with control characters in it**, and the agent always names
  itself by the node id it sent.
- **The node identity is made once.** Two first starts at the same moment agree on one id. When your workspace no
  longer knows this machine's id, the advice now says to stop every running `connect` before you remove the `node-id`
  file.

To install it, run `--version` through `npx` with the package's name in front, as the README's "Getting it" shows; its
first line answers `nth-agent v0.4.0`. To keep a machine on this release, pin the version after the package's name, the
way the README shows.

## 0.3.0

- **Network capture on your own Android phone, off until you turn it on.** A run can now record your test app's network
  traffic on a phone plugged into this machine, and show each request and response on the run's Network tab. It takes
  three yeses, each from a person: `capture enable` on this machine (it explains all of this first and asks you
  to type `ENABLE`), `capture setup` for each phone (you install the agent's certificate on the phone yourself),
  and a scenario that asks for capture. Android phones, with the agent on macOS or Linux. In short:
  - during a capture run the whole phone's traffic, not only your app's, goes through this machine — use a test phone;
  - only debug builds that trust user certificates can be read, and everything else passes through untouched;
  - everything is masked on this machine before it is sent, and masked again when it reaches us;
  - we keep what arrives with the run for one hour, and nothing raw is ever kept on this machine;
  - the certificate authority the agent makes for this lasts one year, and its key never leaves this machine;
  - `capture disable` turns it all off; then remove the certificate from the phone (on a Samsung, in both
    places it is listed).
- **Each phone says whether it is ready for capture.** The agent tells your workspace, on every heartbeat, whether capture
  is on and whether each phone is set up for it, so a run that asks for capture on a phone that is not ready is told why
  before it starts, rather than when the capture fails to start. `doctor` says whether capture is on, which
  phones are set up, and when the certificate authority ends.
- **A check for a machine other people log into.** The capture proxy listens on this machine's loopback address, which
  every account here can reach, so before it reads a byte of a connection it asks the system which account opened it,
  and refuses every account but yours. The machine's administrator (root) could still send traffic through it while a
  capture runs, and `capture enable` says so before you turn capture on. On a system where that question cannot be
  answered, no phone is touched.
- **Masked on your machine.** Before anything leaves this machine, the `Authorization`, `Proxy-Authorization`, `Cookie`
  and `Set-Cookie` headers become `«redacted»`, and so does any header, JSON field, form field or query parameter whose
  name says it holds a credential — including a name whose last word is `sig`, `signature` or `auth` — and any value
  shaped like one. Compressed bodies (gzip, deflate, br, zstd) are decoded first so that they are masked too, and a body
  the agent cannot read is left out, never sent as it is.
- **Stopping it through npx.** A SIGINT or SIGTERM sent to the `npx` process now reaches the agent, which stops the way it
  always has, handing its phones back first; `npx` waits for it, and ends with the agent's own exit status or signal. A
  second Ctrl-C more than two seconds after the first still ends it at once.
- **Third-party notices.** The licences of the code the agent is built from — Go's own, the Go modules it uses, and one
  function ported from mitmproxy — ask that their notices travel with it, and now they do: `THIRD_PARTY_NOTICES.txt` is
  in the npm package and in every release, the `notices` command prints it, and `--version` says where it is on a
  second line.

## 0.2.0

- **Live view.** A person watching a run on your phone sees its screen as the run happens. The agent keeps a port on this
  machine for each phone and carries that phone's screen through its own connection, and `devices` and
  `doctor` say, phone by phone, whether it will be shown and why not. A phone that cannot be shown is still
  driven.
- **A connection that went quiet is noticed.** The agent and your workspace now check on each other every fifteen
  seconds; when nothing arrives for thirty, the agent drops the connection and opens a new one at once, instead of
  waiting on one that is already gone.
- **No redirect for the agent's token.** When the agent opens its connection it no longer follows a redirect, so your
  agent token is never sent on to another address.

## 0.1.1

- **Why the token was refused.** When your workspace refuses the agent's token, `connect` prints the reason the
  workspace gave — that the token has expired, for example — instead of guessing that it was revoked, and then what to
  do: which variable holds the token, that a workspace owner makes a new one, and that the agent must be restarted.

## 0.1.0

The first release published to npm. `devices` reports what is attached here and what is not; `doctor`
says what this machine needs to run tests and what to do about each gap; `setup-ios` makes the one-time
WebDriverAgent build a real iPhone needs; `upload` puts a build on your own BrowserStack account; and
`connect` joins your workspace and offers your phones — and devices from your own BrowserStack account — for
runs, through a connection the agent opens itself. After a run, WebDriverAgent is stopped once nothing has used it for
five minutes, unless you pass `--keep-wda-warm`.
