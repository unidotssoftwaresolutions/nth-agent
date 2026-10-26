package main

import (
	"fmt"
	"io"
	"strings"
)

// version is stamped at build time (-ldflags "-X main.version=…"); "dev" for a local build.
var version = "dev"

// A QUESTION IS NOT A MISTAKE. `--help`, `-h` and `--version` are the standard questions a CLI is asked; they
// are answered on stdout with exit 0. A mistake — an unknown command, an unknown option, a flag that needs a
// value and got none — is answered on stderr with the complaint, the usage, and exit 2, so a script reading
// the code can tell the two apart. The table below IS the CLI's surface: the parser reads it, and the sweep
// that audits every question-vs-mistake case discovers its cases from it rather than from a hand-written list.

type flagSpec struct {
	Name       string // "--farm"
	TakesValue bool   // "--farm URL" vs "--json"
}

type verbSpec struct {
	Name  string
	Flags []flagSpec
	// Subcommands, when a verb has them, are its second word — `capture enable` — and exactly one is required.
	Subcommands []subcommandSpec
}

// subcommandSpec is one second word. Operand names the one optional value that may follow it (`capture setup SERIAL`);
// empty means none may.
type subcommandSpec struct {
	Name    string
	Operand string
}

var questions = []string{"--help", "-h", "--version"}

var verbs = []verbSpec{
	// --offer-uncabled-devices (inventory.OfferUncabledFlag) is on all three commands that look at this machine's phones,
	// so devices and doctor can show what connect would offer with it.
	{Name: "devices", Flags: []flagSpec{{Name: "--json"}, {Name: "--skip", TakesValue: true}, {Name: "--offer-uncabled-devices"}}},
	// doctor takes --appium so it can probe the endpoint a person WOULD pass to connect; it never guesses one.
	// It takes --ios-wda-bundle-id for the same reason: the provisioning-profile check asks about the bundle id a
	// person WOULD sign with, and a diagnostic that guesses one would answer about a phone nobody is running.
	{Name: "doctor", Flags: []flagSpec{
		{Name: "--json"}, {Name: "--skip", TakesValue: true}, {Name: "--appium", TakesValue: true},
		{Name: "--ios-wda-bundle-id", TakesValue: true}, {Name: "--offer-uncabled-devices"},
		// …and the TEAM, because "is there a profile for this bundle id" is half a question: the profile must also
		// belong to the team the build runs as, and a check that answered the half claimed the whole.
		{Name: "--ios-team-id", TakesValue: true},
		// --why prints EVERY caveat, including the ones a passing check states. By default those move behind this
		// flag and the report ends with a count naming it — the paragraphs are reference on a green check and
		// diagnosis on a failing one, and a failing one still prints its own by default.
		{Name: "--why"},
		// --no-color wins over the terminal check and over NO_COLOR's absence, because a person typing it is
		// answering the question directly.
		{Name: "--no-color"},
	}},
	// setup-ios is THE ONE-TIME BUILD, and it is a verb rather than a flag on connect because it is a thing a
	// person DOES once, with their attention on it, and answers a question during. connect is a process that runs
	// unattended; a prompt inside it would be a prompt nobody is there to answer.
	{Name: "setup-ios", Flags: []flagSpec{
		{Name: "--udid", TakesValue: true},
		{Name: "--ios-team-id", TakesValue: true},
		{Name: "--ios-wda-bundle-id", TakesValue: true},
		{Name: "--ios-signing-id", TakesValue: true},
		{Name: "--wda-project", TakesValue: true},
		{Name: "--derived-data-path", TakesValue: true},
		// --yes answers the question in advance, for a person scripting their own machine. It is NOT a default and
		// there is no way to make it one: without it the plan is printed and stdin is read.
		{Name: "--yes"},
	}},
	// upload puts a build on the customer's own BrowserStack account and prints the id connect needs. It is a
	// verb rather than a flag on connect because it is a thing a person DOES once per build, with their attention
	// on it, and because connect REFUSES TO START without the id this produces — a step that must happen first
	// cannot live inside the step that depends on it.
	{Name: "upload", Flags: []flagSpec{
		// TWO FLAGS, TWO MACHINES. --file sends the bytes from here; --url asks BrowserStack to fetch an address
		// reachable from THEIR network. One flag over both would be one label over two facts.
		{Name: "--file", TakesValue: true},
		{Name: "--url", TakesValue: true},
		{Name: "--custom-id", TakesValue: true},
		// The account. No --farm and no --token: an id must be obtainable before a workspace exists.
		{Name: "--browserstack-user", TakesValue: true},
		{Name: "--browserstack-key", TakesValue: true},
	}},
	{Name: "connect", Flags: []flagSpec{
		{Name: "--farm", TakesValue: true}, {Name: "--appium", TakesValue: true},
		{Name: "--token", TakesValue: true}, {Name: "--skip", TakesValue: true},
		// ONLY PHONES ON THIS MACHINE'S USB CABLE ARE OFFERED unless this is given: emulators, and phones reached over Wi-Fi
		// or the network, are named as left out (A_MACHINE_THAT_STAYS_THE_CUSTOMERS, decision 1).
		{Name: "--offer-uncabled-devices"},
		// A CAPTURE KEEPS TO THE INTERNET unless this is given: the capture proxy refuses this machine and its local network
		// (internal/netcap/destination.go), and with it connect says on screen that they are open.
		{Name: "--capture-allow-local-network"},
		// THIS MACHINE'S OWN WebDriverAgent signing identity. Without it an attached iPhone stays present and is
		// not offered — see farm.CanOffer.
		{Name: "--ios-team-id", TakesValue: true},
		{Name: "--ios-wda-bundle-id", TakesValue: true},
		{Name: "--ios-signing-id", TakesValue: true},
		// WE DO NOT SILENTLY LEAVE SOMETHING RUNNING ON A PHONE WE DO NOT OWN — nor silently stop one. This flag
		// is the other side of that: someone running back-to-back suites who would rather keep the banner than pay
		// the cold start. Either way the agent SAYS which it is doing, at startup.
		{Name: "--keep-wda-warm"},
		{Name: "--wda-idle-after", TakesValue: true},
		// THE CUSTOMER'S OWN BROWSERSTACK ACCOUNT, resolved on the customer's own machine. With both, the drive
		// tunnel replays against their hub instead of the local Appium; with neither, nothing changes. The key is
		// a SECRET and the same argv warning applies to it as to --token: prefer the environment twin.
		{Name: "--browserstack-user", TakesValue: true},
		{Name: "--browserstack-key", TakesValue: true},
		// THE BUILD. BrowserStack runs its own devices, so a session needs a build already in THEIR storage —
		// there is no adb here and nothing for the worker to install. The id is the customer's to supply because
		// only they know which of their apps this is.
		{Name: "--browserstack-app", TakesValue: true},
		// THE DEVICES TO OFFER, repeatable like --skip: "Google Pixel 9|15.0". The customer names them because
		// their account offers 110 and they use a handful — a Devices screen of 110 rows is a worse product.
		{Name: "--browserstack-device", TakesValue: true},
	}},
	// capture is NETWORK CAPTURE ON THIS MACHINE, and it is a verb with three words of its own because each is a thing a
	// person does on purpose, once: turn it on (and type ENABLE), set up a phone (and install a certificate on it by hand),
	// turn it all off. None of them runs inside `connect`, which runs unattended; `connect` only does what they decided.
	{Name: "capture", Flags: []flagSpec{}, Subcommands: []subcommandSpec{
		{Name: "enable"},
		{Name: "setup", Operand: "SERIAL"},
		{Name: "disable"},
	}},
	// notices prints THIRD_PARTY_NOTICES.txt, which this binary carries (the module root embeds it). The licences of the code
	// inside the agent ask that their notices travel with every copy, and a copy can be one binary on its own.
	{Name: "notices", Flags: []flagSpec{}},
}

// invocation is a parsed command line: the verb, its boolean flags, and the values of its valued flags
// (a repeatable flag such as --skip keeps every value, in order).
type invocation struct {
	Verb   string
	Bools  map[string]bool
	Values map[string][]string
	// Subcommand and Operand are a verb's second word and the value after it, when its spec has them.
	Subcommand string
	Operand    string
}

func (inv *invocation) first(flag string) string {
	if v := inv.Values[flag]; len(v) > 0 {
		return v[0]
	}
	return ""
}

const (
	exitOK      = 0
	exitMistake = 2
)

// parse reads args against the table. It returns (inv, -1) when the program should proceed, or (nil, code)
// after having already written the answer — a question's answer on stdout, a mistake's on stderr.
func parse(args []string, table []verbSpec, stdout, stderr io.Writer) (*invocation, int) {
	if len(args) == 0 {
		// A bare invocation with nothing to do: usage, exit 2. DECISION, not an accident — it is the one case
		// where "what is this?" and "you forgot to say what to do" are the same line, and the convention
		// (git, dotnet) is the non-zero exit; the usage is the answer either way.
		fmt.Fprint(stderr, usage())
		return nil, exitMistake
	}
	if code, answered := answerQuestion(args[0], stdout); answered {
		return nil, code
	}
	var verb *verbSpec
	for i := range table {
		if table[i].Name == args[0] {
			verb = &table[i]
		}
	}
	if verb == nil {
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage())
		return nil, exitMistake
	}

	inv := &invocation{Verb: verb.Name, Bools: map[string]bool{}, Values: map[string][]string{}}
	rest := args[1:]
	var sub *subcommandSpec
	for i := 0; i < len(rest); i++ {
		if code, answered := answerQuestion(rest[i], stdout); answered {
			return nil, code
		}
		// A VERB'S OWN WORDS: the first word that is not an option is its subcommand, and the one after it, when the
		// subcommand takes one, its operand. Anything more is a mistake, named.
		if len(verb.Subcommands) > 0 && !strings.HasPrefix(rest[i], "-") {
			switch {
			case sub == nil:
				sub = verb.subcommand(rest[i])
				if sub == nil {
					fmt.Fprintf(stderr, "unknown %s command %q — it is one of: %s\n\n%s", verb.Name, rest[i], verb.subcommandNames(), usage())
					return nil, exitMistake
				}
				inv.Subcommand = sub.Name
			case sub.Operand != "" && inv.Operand == "":
				inv.Operand = rest[i]
			default:
				fmt.Fprintf(stderr, "%s %s takes nothing more, and was given %q\n\n%s", verb.Name, sub.Name, rest[i], usage())
				return nil, exitMistake
			}
			continue
		}
		spec := verb.flag(rest[i])
		if spec == nil {
			fmt.Fprintf(stderr, "unknown option %q\n\n%s", rest[i], usage())
			return nil, exitMistake
		}
		if !spec.TakesValue {
			inv.Bools[spec.Name] = true
			continue
		}
		// A flag that takes a value and has none — at the end of the line, or followed by an empty string — was
		// silently DROPPED before this slice (the first real run would have been told "connect needs the farm URL"
		// after typing --farm). Name the flag; it is a mistake, not a question.
		if i+1 >= len(rest) || strings.TrimSpace(rest[i+1]) == "" {
			fmt.Fprintf(stderr, "%s needs a value\n\n%s", spec.Name, usage())
			return nil, exitMistake
		}
		i++
		inv.Values[spec.Name] = append(inv.Values[spec.Name], rest[i])
	}
	if len(verb.Subcommands) > 0 && sub == nil {
		fmt.Fprintf(stderr, "%s needs one of: %s\n\n%s", verb.Name, verb.subcommandNames(), usage())
		return nil, exitMistake
	}
	return inv, -1
}

func (v *verbSpec) subcommand(name string) *subcommandSpec {
	for i := range v.Subcommands {
		if v.Subcommands[i].Name == name {
			return &v.Subcommands[i]
		}
	}
	return nil
}

func (v *verbSpec) subcommandNames() string {
	names := make([]string, len(v.Subcommands))
	for i, s := range v.Subcommands {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

func (v *verbSpec) flag(name string) *flagSpec {
	for i := range v.Flags {
		if v.Flags[i].Name == name {
			return &v.Flags[i]
		}
	}
	return nil
}

// answerQuestion answers a standard question on stdout and reports whether it was one.
func answerQuestion(arg string, stdout io.Writer) (int, bool) {
	switch arg {
	case "--help", "-h":
		fmt.Fprint(stdout, usage())
		return exitOK, true
	case "--version":
		fmt.Fprint(stdout, versionAnswer(version))
		return exitOK, true
	}
	return 0, false
}
