# Changelog

What changed in each release of nth-agent, newest first. Each heading is a version as npm names it, and a release's
notes are its section here. A command is named here by what you type after the package's name, `capture enable` for
example: run it through `npx` with that name in front, the way the README shows.

## 0.5.7

- **A request is read the way Appium reads it.** Inside a session, the agent read a request's body by Go's rules, which take
  a member's name in any letters and keep the last one, while Appium takes the exact name; and it trimmed a `mobile:`
  script by Go's idea of white space, which leaves out a character JavaScript trims. Either way a request could show the
  agent one command and Appium another. A body that names a member twice, or in two spellings that differ only in their
  letters, is now refused, a script is trimmed as the drivers trim it, and a command whose name holds anything but
  letters and digits is refused.
- **Two more things that work on this computer are refused.** The upload of an iPhone screen recording that XCTest made
  (`mobile: stopXCTestScreenRecording`), which Appium sends from this computer to any address, and an app's strings
  (`getStrings`, `mobile: getAppStrings`), which Appium reads from a file here. The farm's runner sends neither.
- **An emulator's console gets one line.** Appium writes what `mobile: sendSms`, `gsmCall`, `sensorSet` and their like say
  to the emulator's console on this computer as one line, so a line break in it would have started a second console
  command. One with a line break or another control character in it is refused. The runner's shake still passes.
- **A session's create is held to its shapes.** `language`, `locale`, `appPackage` and `appActivity` must have the shape
  their kind of value has (`sv_SE`, `com.example.app`, `.MainActivity`): a driver may put them in a shell command on the
  phone. A capability named both in a set and inside its `appium:options` is refused, since Appium reads one of the two
  and the agent judged only one; both values are judged now.
- **BrowserStack gets only the options the agent sends.** A cloud session's `bstack:options` is built from what the agent
  writes and a few labels (`projectName`, `buildName`, `buildTag`, `sessionName`, `appiumVersion`,
  `deviceOrientation`). Anything else a farm's create asked for with your key — BrowserStack Local, other apps, uploaded
  media, logging options, the older `browserstack.*` names — is left out and named in your terminal, and the session
  starts without it.
- **Capture only on a phone the agent offers.** The farm could start a capture, or ask whether a phone was handed back, on
  any phone set up for capture, even one you left out with `--skip`. Both now need a phone `connect` offers right now;
  for any other, nothing runs and nothing is said about it.
- **A live view ends with its session.** A phone's live view used to go on until the farm cancelled it, so a viewer from
  one lease could keep watching the phone into the next. It now ends when the session on that phone ends, and the next
  session's view is opened afresh.
- **A capture does not reach this computer by its public address.** The capture proxy refused loopback and the local
  ranges, but not a public IPv4 address or a global IPv6 address on this computer's own interfaces, and the system
  delivers a connection to one of those here. It now refuses every address this computer's interfaces carry, read when it
  connects.
- **What a phone or BrowserStack says is printed safely.** `devices`, `doctor`, `capture setup`, `capture status` and
  `upload` printed a phone's name, adb's words and BrowserStack's answer as they came, so an escape code in one could
  drive your terminal. They now go through the same filter as `connect`'s own lines. The `export` line `upload` prints
  quotes the id, so a pasted line runs nothing after it.
- **A build is checked for what it really holds.** The agent counted a build by the sizes its list declares; a file could
  declare a kilobyte and unpack to a megabyte, and a size near 2^63 could wrap the total. Each file is now unpacked on the
  way into nothing, never written, and must be exactly its declared size, with its checksum; the total is checked before
  each file is added.
- **A farm on this computer is sent nothing unless you run it.** A plain-`http` farm is allowed only on this computer's
  loopback, where the token never crosses a network; but every account here can listen there, and one holding the farm's
  port while your local farm was down was sent your workspace token. The farm's calls and the tunnel are now dialled the way
  Appium is, and the connection is closed before a byte is written when another account runs what took it.
- **On a Mac, a program behind a socket root made is not taken for yours.** The agent let root's programs serve as your
  Appium, adb server or live view, but the system names the account that made a socket, not the one serving it, and on a
  Mac root's launchd makes the listening socket any account's own LaunchAgent asks for. On Linux and Windows, where only an
  administrator can hand on such a socket and adb started with sudo runs as root, root is still used.
- **On Windows, a socket's owner is the process that made it.** The agent read a socket's account from whichever process
  holds its recorded process id now. When the process that made it has ended, Windows can give that id to another one; a
  process that started after the socket was made is no longer taken for its owner.
- **On Linux, a connection that has already closed is not let in as root.** The kernel prints a closed connection's row with
  no owner, which read as root's, so another account could reach a running capture proxy by closing its end before it was
  asked about. Such a row is no answer now, and the connection is refused.
- **adb's server is asked about on every address adb reaches.** The agent asked who ran adb's server on 127.0.0.1 only, and
  read a connection that timed out as no server at all. adb's client reaches localhost on `::1` too, and the address the
  environment names, so every listener there is asked about, from the system's own table; one that will not take the
  agent's connection is refused, and an answer given when no server of yours was listening afterwards is not used.
- **An emulator is offered only when you run it.** An emulator was offered because adb named it `emulator-5554`, and any
  account here can make this computer's adb server list a program of its own under that name. Its two ports must now be
  yours; one another account holds is shown as not usable, in words, and no flag offers it. Its console is never asked for
  the emulator's name either, since adb sends your console's token to whatever listens there.
- **An iPhone's WebDriverAgent port is yours, and its alone.** The port declared for each iPhone is now one no other
  account holds, checked again as each session starts, and never the same as another phone's: two phones could be declared
  one port, and the farm pins each session to it. A session that names another phone's port is refused.
- **One phone, one `connect`, on the whole computer.** One `connect` per account held its own state folder, which follows
  `HOME`, so a second `connect` started with another `HOME`, or one of another account sharing adb or the Apple device
  daemon, could offer the same phone under a second identity. Each phone is now claimed on the computer itself before it is
  offered, and one another `connect` holds is named in your terminal and not offered.
- **Capture is not turned on where its key would leave this computer.** On a network home, the capture's key would be made
  on a file server and copied with its backups. `capture enable` now refuses a state folder on network storage, and says
  so.
- **A body is masked whole before it is cut.** A request or answer body was cut to its preview first, so a credential the
  cut fell inside was read as an unfinished value and kept in part. It is masked whole now, then cut.
- **A capture keeps what a real app would make it keep.** A host's answer with more headers than a browser accepts
  (256 KiB) is not read, and the phone is told why; a header value longer than any real one (64 KiB) is left out whole rather
  than cut and then masked; at most 4096 hosts that refuse the capture's certificate are remembered, and the record says when
  that is reached; and masking reads each value once, so a text built of nested pairs no longer costs the square of its
  length.
- **adb's answers are read up to a bound.** adb's output was held whole, however long, every tick. Past 1 MiB, more than a
  hundred times any answer the agent reads, adb is stopped and its answer is not used.
- **Handback questions are answered a few at a time.** Each of the farm's capture handback questions started its own work on
  this computer, adb included, with nothing counting how many ran at once. Eight are answered at once now; one past that is
  answered with a 503 in words, and the farm asks it again.
- **A refused request holds nothing but its id.** When the farm sent more requests than the agent keeps waiting, each refusal
  waited for room to be sent while holding the whole request, body and all. A refusal now holds the request's id and its
  sentence, and at most 512 wait at once; past that a request goes unanswered and the farm's own deadline fails it.
- **What waits to be sent to the farm has a ceiling.** The agent queued whatever it was asked to send, so a farm that read
  next to nothing while it kept asking could make it hold any amount. Past 512 MiB the agent ends the tunnel, says why, and
  dials again. A frame whose id is longer than the farm's own ids is skipped rather than echoed.
- **Answers on their way to the farm share one limit.** Each session held its whole answer while it waited for the farm to
  read, and nothing counted them together. Answers held at once are now limited to 256 MiB across the agent; one past that is
  answered with a 502 in words, and the tunnel stays up.
- **A hub answer is measured after the key is masked.** Replacing a short BrowserStack user name with the mask can make an
  answer longer, and the size was checked before that. It is checked again after, and no frame larger than one tunnel message
  is ever written: the farm would have ended the tunnel for every lease on the machine.
- **A create the farm has given up on is not started.** Session creates take turns on a machine, and one that waited behind a
  slow create past the farm's 190 seconds was still sent to Appium, starting a session no lease held. It is now left, and
  your terminal says so.
- **Limits hold across a reconnect.** A request still running when the tunnel dropped was counted by nothing, so creates were
  no longer one at a time and requests in flight had no limit. One create at a time and at most 512 requests in flight now
  hold for the whole agent, whichever tunnel they came on.
- **Three libraries are raised past flaws Go's vulnerability database names.** The brotli decoder that reads the farm's
  frames could be made to hold far more memory than a crafted stream sends; Go's HTTP/2 code (`golang.org/x/net`), which
  BrowserStack uploads go through, had five flaws, among them a crash and unbounded memory and CPU; and Go's text library
  (`golang.org/x/text`) could loop forever on invalid input. They are now brotli 1.2.7, `x/net` 0.60.0 and `x/text` 0.42.0,
  with the `x/sys` and `x/crypto` releases those need. CI now checks every change against Go's vulnerability database, and
  fails one whose code can reach a known flaw.

## 0.5.6

- **Appium stays on your computer.** Appium listens on every address your computer has unless it is started with
  `--address`, and it asks a client for nothing, so anything on your network that reached it could drive your phones
  without going through the agent and its limits. Every command the agent and the README give now starts it as
  `appium --address 127.0.0.1`. `doctor` reads, from the system's own table of listening sockets, where the program on
  your `--appium` port listens, and marks the Appium endpoint as needing a look when it listens anywhere but this
  computer's loopback; `connect` says so once, when it starts. Nothing is sent anywhere to find out, and the agent still
  drives that Appium.
- **Built with Go 1.27.4.** Every release so far was built with Go 1.27.0, the first release of that line, and carried none
  of the fixes to Go's standard library since. Releases are now built with the Go the repository names, and a release
  refuses a binary built with any other. CI also checks the code against Go's vulnerability database.
- **Only the release's reviewers can publish it.** The npm package is published from a protected environment, whose
  reviewers approve each publish, through npm's trusted publishing alone and with no stored credential. A release is
  built from the tag's own files, never from a build cache another CI run wrote.
- **The repository's history begins at this release.** Every earlier version is still on npm and installs as before. The
  release pages and source links of 0.5.0 to 0.5.5 are no longer on GitHub.

## 0.5.5

- **A value typed into your app stays out of Appium's log.** When a run types into a field on a phone on your computer — a
  saved login, text a person recorded, text the run writes itself — the request now reaches your Appium with
  `X-Appium-Is-Sensitive`, exactly as the farm sent it, and Appium writes that request's body as `**SECURE**` in its log.
  The agent passed on `Content-Type` alone before, so each such value was written in clear in the log of the Appium on
  your computer. Nothing else the farm sends is passed on, and a run on a BrowserStack phone carries the header there too.
- **`doctor` says what Appium's log level means.** Appium does not tell a client the level it logs at, so `doctor` says it
  could not read it, that Appium logs at debug unless it is started with `--log-level`, what debug writes that no request
  can mask — a field's text read back, an error that quotes a typed value — and how to start it at info:
  `appium --log-level info`. It stops no run, and the agent still never starts or changes your Appium.

## 0.5.4

- **The agent says what is wrong first, and what to do about it.** `doctor` and `connect` now open with their answer: in
  green, what is ready, such as how many phones are offered; or, in red, how many things stop runs on this computer. Every
  problem is said in three parts, in this order: what is wrong, why it matters, and what to do, on a line of its own after
  `→`, ready to copy.
- **One look everywhere.** `connect`, `doctor`, `devices` and the command line's own mistakes share one set of marks — `✓`
  ready, `!` needs a look, `✗` stops a run, `·` detail — and one palette: green, yellow, red, dim. On a terminal that cannot
  show the marks, they are `ok`, `!!`, `xx`, `--` and `->`.
- **Colour only on a terminal.** Never into a pipe or a file, and never with `--json`, `--no-color`, `NO_COLOR` or
  `TERM=dumb`. `--json` prints exactly what it printed before. `--no-color` now works on `devices` and `connect` too.
- **`devices` is a table:** each phone's name, platform, Android or iOS version, and whether it is ready to offer or left
  out, and why, in a few words.
- **`connect` starts with four lines:** its version and whether a newer one is out, the farm it is connected to, what this
  computer can serve, and how to stop it. On a terminal, connecting and each heartbeat are one line that updates in place;
  in a log, nothing about them changes.
- **A system's own error is shown only when asked for.** `connect --verbose` prints it, dim, under the problem it belongs
  to; by default the line says what happened in a few plain words.
- **`doctor` says how to install adb for this computer:** `brew` on a Mac, and Google's own download on Linux.
- **The agent runs on Windows, and on Linux on arm64.** `npx` now picks a binary for `win32-x64` and `linux-arm64` too.
  On Windows it drives Android phones on a USB cable and Android emulators, through your own Appium, the same as on a
  Mac or Linux; it checks which account runs Appium, adb's server and each phone's live view the way it does there, and
  keeps the capture key and files to your account alone. iPhones need a Mac: on Windows `connect` says
  `iOS: needs a Mac` once, as a note, and `doctor` marks each iOS check n/a.
- **On Windows, a session that installs a build starts.** The agent hands Appium its own copy of the build, and it read
  that copy's Windows path (`C:\Users\…\builds\….apk`) as a web address, which names no kind of build, so every such
  session was refused there. A file the agent wrote is now read as a path of this computer. The same reading refused a
  build on a Mac or Linux whose agent folder had a `#` or a `?` in its path.
- **On Windows, the drive tunnel stays up while the farm answers.** Windows' clock moves in steps of up to 16 ms, and an
  answer to the agent's ping that came back within the same step was not counted, so with a farm a few milliseconds away
  and nothing else arriving, a healthy tunnel was dropped and dialled again within 45 seconds.
- **On Windows, an agent installed in a project is told the project's own command,**
  `<project>\node_modules\.bin\nth-agent.cmd`, in every line that names one, `doctor`'s hint included. It was told the
  `npx` form, which can fetch a different copy.
- **Android emulators are offered.** An emulator running on this computer is your own virtual device, so it is offered
  like a phone on the cable, and `devices` shows it as an `Android emulator` by its virtual device's name. Phones reached
  over Wi-Fi or the network are still left out unless you start with `--offer-uncabled-devices`.
- **adb is found where Android Studio puts it.** On `PATH` first, then in the SDK that `ANDROID_HOME` or
  `ANDROID_SDK_ROOT` names, then in Android Studio's own folder for this system.
- **`doctor` checks what Appium needs for Android:** Node.js, the Android SDK (`ANDROID_HOME`) and Java (`JAVA_HOME`),
  each with the command for this computer — `setx` on Windows, an `export` line on a Mac or Linux.
- **Every screen writes `Android` and `iOS`.** `connect`'s platform lines said `android:` and `ios:`.
- **A live view that cannot open is said like any other problem:** what is wrong, that the run is still driven, and how
  to free the port on this computer — on Windows, the program that holds it, by name.

## 0.5.3

- **Phones on one computer no longer wait for each other.** Until now the agent answered the farm one request at a time
  for the whole computer, so a slow step on one phone, such as installing the app, held every other phone's next command.
  Now each session's commands run in their own turn, in the order they came, beside the other phones' sessions.
- **Sessions still start one at a time on each computer.** Starting a session is when Appium's drivers choose the ports a
  session uses on your computer, and not every driver version guards that choice, so two sessions never start at once.
- **Stopping the agent as it starts now stops it.** When the agent runs through `npx` and the npm launcher got a SIGINT or
  SIGTERM in the moment the agent was starting, the launcher ended and the agent kept running. The launcher now listens
  for both before it starts the agent and passes them on. Ctrl-C in a terminal was not affected: it reaches the agent too.
- **`capture setup` leaves the phone's browser on the check's last page.** The agent could close its proxy while the page
  saying the check is over was still on its way, so a slow phone's browser could be left with an error page. It now sends
  the page out before it moves on.

## 0.5.2

- **The agent fetches a session's build itself, and Appium installs the agent's own copy.** Until now Appium downloaded the
  build from the farm with no size limit and kept it in its own cache. Now the agent fetches it from the farm's own host,
  follows no redirect, checks the file as an archive without unpacking it, and hands Appium a file in the agent's own
  folder, removed when the session ends, when the agent stops, and at the next start after a crash.
- **A build is refused past 400 MiB, or when fewer than 2 GiB of disk would be left.**
- **A `.zip` or `.apks` build is refused.** Appium would unpack one on this computer, and no phone this agent drives installs
  one: an iPhone takes an `.ipa`, an Android phone an `.apk`.

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
