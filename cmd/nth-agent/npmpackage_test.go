package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farmtest"
)

// THE COMMAND A CUSTOMER CAN ACTUALLY TYPE.
//
// The release page is where the npm package is built from, not a download, and the docs page once told a customer to
// "download the release for your machine" — with no link, no URL and no repository they could reach then. `npx` is the distribution decision; these seals hold the parts of it that a person
// cannot check by looking.

func npmFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "publish", "npm", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v — the npm package IS the documented way to obtain this agent", name, err)
	}
	return string(data)
}

func npmPackageJSON(t *testing.T) map[string]any {
	t.Helper()
	var pkg map[string]any
	if err := json.Unmarshal([]byte(npmFile(t, "package.json")), &pkg); err != nil {
		t.Fatalf("package.json does not parse: %v — npm would refuse it too", err)
	}
	return pkg
}

func workflowText(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

func TestThePackageNameLivesInExactlyOnePlace(t *testing.T) {
	// ⚠️ THE NAME IS DECIDED, AND THIS PARAGRAPH USED TO SAY IT WAS NOT. It said: "THE NAME IS NOT DECIDED.
	// The platform is being renamed, `nth-agent` is here because it is what the binary is already called, and the
	// operator has not chosen the npm name." Commit 8fe743d chose it on 2026-09-18 — the package is
	// **nthcase-agent** — and rewrote the surrounding comments mechanically while this sentence and three in
	// publish-npm.yml were left claiming the opposite. A reader who believed them typed the old name into the
	// workflow's confirm_name gate and was refused by a gate doing its job for a document that lied.
	//
	// What the seal itself is for has NOT changed, and is now the second half of a pair: a published npm name
	// cannot be withdrawn, only deprecated, so CHANGING it must stay ONE edit — the `name` field — and the count
	// of places it appears is derived here rather than trusted.
	//
	// ⚠️ THE POPULATION IS THE PACKAGE'S OWN FILES, AND THE REASON IS A MEASUREMENT. The first version of this
	// seal counted the workflows too and found SEVEN occurrences — and every extra one was correct:
	// `dist/nth-agent` is the built FILE, `./nth-agent` is what a person runs, `tar … nth-agent` is
	// the archive member. Those are all the BINARY, which since 8fe743d is a different string from the package —
	// so a count over the workflows would measure the binary and call it the package. (While the two were equal
	// no regex could have told them apart at all; that they now differ is exactly what
	// TestTheCommandKeyEqualsTheDirectoryUnderCmd exists to police.)
	//
	// So the count is over publish/npm/** — where the name may appear once, as the declaration — and the
	// workflow is held to DERIVING it instead, which is the property that actually makes a rename one edit.
	name, _ := npmPackageJSON(t)["name"].(string)
	if name == "" {
		t.Fatal("package.json declares no name — there is nothing to publish and nothing to seal")
	}

	// The name as a standalone token: not part of a longer one. `nth-agent-darwin-arm64` (a binary filename
	// chosen by release.yml) and `nth-agent_v0.1.0_...` (an archive name) are different strings that happen
	// to start the same way, and renaming the package must not rename either of them.
	token := regexp.MustCompile(`(^|[^-\w])` + regexp.QuoteMeta(name) + `($|[^-\w.])`)

	type hit struct {
		file string
		line int
		text string
	}
	var hits []hit

	scan := func(path, rel string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if token.MatchString(line) {
				hits = append(hits, hit{rel, i + 1, strings.TrimSpace(line)})
			}
		}
	}

	npmDir := filepath.Join(repoRoot(t), "publish", "npm")
	entries, err := os.ReadDir(npmDir)
	if err != nil {
		t.Fatalf("reading publish/npm: %v", err)
	}
	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue // bin/ is filled at publish time with binaries, not text
		}
		scanned++
		scan(filepath.Join(npmDir, entry.Name()), filepath.Join("publish", "npm", entry.Name()))
	}
	if scanned < 2 {
		t.Fatalf("instrument guard: only %d files read from publish/npm — the package is index.js AND package.json", scanned)
	}

	// …and the publish machinery must READ the name rather than spell it, which is the half a count cannot see.
	publish := workflowText(t, "publish-npm.yml")
	if !strings.Contains(publish, "require('./publish/npm/package.json').name") {
		t.Fatal("the publish workflow does not read the package name out of package.json — if it spells the name " +
			"itself, renaming the package means editing the workflow too, and the two can disagree in between")
	}

	if len(hits) != 1 {
		var where []string
		for _, h := range hits {
			where = append(where, h.file+":"+itoa(h.line)+"  "+h.text)
		}
		t.Fatalf("the package name %q appears in %d places inside publish/npm and must appear in exactly one "+
			"(package.json's `name`), so choosing the real name later is a single edit:\n  %s",
			name, len(hits), strings.Join(where, "\n  "))
	}
	if !strings.HasSuffix(hits[0].file, "package.json") {
		t.Fatalf("the one occurrence is in %s — it must be package.json's `name` field, which is the declaration; "+
			"everything else derives it", hits[0].file)
	}
}

func TestTheCommandKeyEqualsTheDirectoryUnderCmd(t *testing.T) {
	// A COMMAND THE CUSTOMER CANNOT TYPE — and the seal that replaces the one that could not see it.
	//
	// ⚠️ WHAT USED TO STAND HERE, AND WHY IT WAS RIGHT UNTIL IT WAS NOT. This test required `bin` to be a
	// plain STRING, arguing that npm then derives the command from `name` and that an object "spells the name a
	// second time". That argument was sound ONLY WHILE THE PACKAGE NAME AND THE BINARY NAME WERE THE SAME
	// STRING. Commit 8fe743d ("Take the name the customer will type", 2026-09-18) made the package
	// `nthcase-agent` and left the binary `nth-agent`, deliberately — and a string `bin` CANNOT EXPRESS THAT
	// SPLIT. npm installed a command called `nthcase-agent`, while /docs/your-devices, the Devices page and the
	// Settings token block all printed `nth-agent …`. A customer who followed our own install sentence and
	// typed the very next line got `command not found`.
	//
	// ⚠️ AND BOTH SEALS STAYED GREEN, because each was right about what it read: this one read `bin`, and
	// The web app's THE_AGENTS_THREE_NAMES_ARE_THE_ONES_IT_DECLARES read the cmd/ directory. Nothing compared the
	// two. The hole was BETWEEN them, which is where this kind of hole always is.
	//
	// THE RULE NOTHING HAS EVER HELD: the key of `bin` is the COMMAND, and the command already has exactly one
	// home — the single directory under cmd/, because in Go the directory IS the built binary's name. So the
	// key is read against that directory rather than against the package name. An object does not "spell the
	// name a second time": it spells a DIFFERENT FACT, once.
	//
	// TestThePackageNameLivesInExactlyOnePlace is unaffected and must stay at ONE: its token regex is built from
	// the PACKAGE name, and `nth-agent` is not `nthcase-agent`, so this key adds no occurrence to that count.
	cmdDir := filepath.Join(repoRoot(t), "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("reading cmd/: %v — the directory under cmd/ IS the command a customer types", err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("instrument guard: %d directories under cmd/ (%s) — this agent is one program, and with two "+
			"of them 'the command a customer types' has no single answer for this seal to check",
			len(dirs), strings.Join(dirs, ", "))
	}
	command := dirs[0]

	pkg := npmPackageJSON(t)
	bin, isObject := pkg["bin"].(map[string]any)
	if !isObject {
		t.Fatalf("package.json's `bin` is %T (%v), so npm names the installed command after the PACKAGE, %q — "+
			"but every surface that prints it says %q: /docs/your-devices' table, the Devices page's empty state "+
			"and the Settings token block. A customer who runs `npm i -g %s` and then types `%s` as those pages "+
			"instruct gets `command not found`. `bin` must be an object whose single key is %q.",
			pkg["bin"], pkg["bin"], pkg["name"], command, pkg["name"], command, command)
	}
	if len(bin) != 1 {
		var keys []string
		for key := range bin {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("`bin` declares %d commands (%s) and this agent is one program — every extra key is a command "+
			"put on a customer's PATH that no page tells them about and no seal in either repository names",
			len(bin), strings.Join(keys, ", "))
	}
	for key, target := range bin {
		if key != command {
			t.Fatalf("`bin` puts %q on the customer's PATH and the agent's binary is %q (the one directory under "+
				"cmd/). Those two strings must be equal: the pages that teach this command — "+
				"/docs/your-devices' table, the Devices page's empty state, the Settings token block — all print "+
				"%q, so after `npm i -g %s` the first command a reader types answers `command not found`.",
				key, command, command, pkg["name"])
		}
		if target != "index.js" {
			t.Fatalf("`bin[%q]` points at %q and the package ships index.js as its shim — npm would link a file "+
				"that is not there and the command would fail on its first run", key, target)
		}
	}
}

func TestNothingPublishesUnlessAPersonAsksItTo(t *testing.T) {
	// ⚠️ A PUBLISHED NPM NAME CANNOT BE WITHDRAWN, only deprecated — and the name is not decided. So the publish
	// path must not be armed by anything that fires on its own. `release.yml` runs on a tag, which is correct
	// for building artifacts nobody outside the company can see; publishing to a public registry is a different
	// act and needs a different gate.
	workflow := workflowText(t, "publish-npm.yml")

	on, _, found := strings.Cut(workflow, "\njobs:")
	if !found {
		t.Fatal("instrument guard: no `jobs:` in publish-npm.yml — the parse is reading the wrong thing")
	}
	if !strings.Contains(on, "workflow_dispatch:") {
		t.Fatal("publish-npm.yml has no workflow_dispatch trigger — nothing could ever publish it deliberately")
	}
	for _, trigger := range []string{"push:", "release:", "schedule:", "pull_request:", "repository_dispatch:"} {
		if strings.Contains(on, "\n  "+trigger) || strings.Contains(on, "\n    "+trigger) {
			t.Fatalf("publish-npm.yml is armed by %q — that trigger fires without anybody deciding to publish, "+
				"and a published npm name cannot be withdrawn. The name is not chosen yet; nothing may reach the "+
				"registry except by a person running this workflow and typing the name back", trigger)
		}
	}

	// …and the dispatch itself asks for the name, so running it by accident still stops before the registry.
	if !strings.Contains(workflow, "confirm_name") {
		t.Fatal("the dispatch does not ask the person to confirm the package name — `workflow_dispatch` alone is " +
			"one misclick, and this one cannot be taken back")
	}
}

func TestThePackageAndTheBinaryCannotClaimDifferentVersions(t *testing.T) {
	// The customer's question is "which one am I running?" and there are two answers on the machine: what npm
	// installed, and what the binary prints. They are stamped from ONE source — the release tag — and this holds
	// the workflow to actually checking that, rather than stamping twice and hoping.
	workflow := workflowText(t, "publish-npm.yml")

	if !strings.Contains(workflow, "npm version") {
		t.Fatal("the publish workflow never stamps the package version — it would publish whatever literal is " +
			"checked in, which is the count-in-a-comment defect wearing a version field")
	}
	if !strings.Contains(workflow, "--version") {
		t.Fatal("the publish workflow never runs the binary it is about to publish; nothing establishes that the " +
			"bytes report the version the package claims")
	}
	// The comparison must be a REFUSAL. A workflow that prints both and publishes anyway has checked nothing.
	compare, _, found := strings.Cut(workflow, "\n      - name: Publish")
	if !found {
		t.Fatal("instrument guard: no publish step found — the parse is reading the wrong thing")
	}
	idx := strings.Index(compare, "--version")
	if idx < 0 || !strings.Contains(compare[idx:], "exit 1") {
		t.Fatal("the version comparison does not exit non-zero — a package that lies about its own version is " +
			"worse than no package, because the next bug report describes a build that does not exist")
	}

	// The checked-in version is a placeholder that is always overwritten. If it were ever a real-looking number,
	// a publish that skipped the stamp would ship it and nobody would notice.
	version, _ := npmPackageJSON(t)["version"].(string)
	if version != "0.0.0" {
		t.Fatalf("package.json's version is %q — it must stay 0.0.0 in the tree, so that a publish which failed "+
			"to stamp is obviously wrong rather than plausibly right", version)
	}
}

func TestTheShimShipsExactlyTheTargetsTheReleaseBuilds(t *testing.T) {
	// ⚠️ THIS IS THE SEAL THE PLATFORM-CLAIMS ONE CANNOT BE. That seal works at GOOS level — it asks whether a
	// user-visible file names a platform we do not build — so `linux-arm64` is invisible to it: "linux" IS
	// built, and no new word appears. The reference shim this one is copied from
	// (mobile-mcp/mobilecli/publish/npm/index.js) names exactly that target, so shipping its table verbatim
	// would have promised a binary that does not exist and failed at `join(__dirname, "bin", undefined)`.
	//
	// The matrix is the only statement of what we compile, so this reads it rather than a list kept beside it.
	shim := npmFile(t, "index.js")
	release := workflowText(t, "release.yml")

	// Node's spellings on the left of the table; the release matrix speaks Go's.
	nodeArch := map[string]string{"x64": "amd64", "arm64": "arm64", "ia32": "386"}

	keys := regexp.MustCompile(`"([a-z0-9]+)-([a-z0-9]+)":`).FindAllStringSubmatch(shim, -1)
	if len(keys) == 0 {
		t.Fatal("no platform keys parsed out of the shim's table — the parse is wrong, not the shim")
	}
	shipped := map[string]bool{}
	for _, key := range keys {
		arch, known := nodeArch[key[2]]
		if !known {
			t.Fatalf("the shim ships %q and this seal does not know what GOARCH %q is — a target nobody can "+
				"map to the matrix is a target nobody can prove we build", key[1]+"-"+key[2], key[2])
		}
		shipped[key[1]+"/"+arch] = true
	}

	built := map[string]bool{}
	var goos string
	for _, line := range strings.Split(release, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if after, ok := strings.CutPrefix(line, "goos:"); ok {
			goos = strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(line, "goarch:"); ok && goos != "" {
			built[goos+"/"+strings.TrimSpace(after)] = true
		}
	}
	if len(built) < 3 {
		t.Fatalf("instrument guard: only %d targets parsed from release.yml — the parse is wrong, not the matrix", len(built))
	}

	for target := range shipped {
		if !built[target] {
			t.Fatalf("the shim offers %q and the release does not build it — `npx` would resolve a binary that "+
				"was never compiled, and the customer's error would be about a missing file rather than an "+
				"unsupported machine", target)
		}
	}
	for target := range built {
		if !shipped[target] {
			t.Fatalf("the release builds %q and the shim never selects it — that binary ships inside the package "+
				"and no machine can ever run it", target)
		}
	}
}

// itoa keeps the failure message above free of a strconv import for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestTheSelfUpdateRefusalSaysWhatItActuallyRefuses(t *testing.T) {
	// ⚠️ THE COLLISION THIS SEAL EXISTS FOR. A slice wrote "there is no self-update, deliberately" into three
	// places a reader meets, with a security argument and a support argument. The distribution channel chosen a
	// day later — `npx` — fetches the latest published version BY DEFAULT. Both sentences are true and together
	// they read as a contradiction, which is worse than either being wrong: a reader trusts the one that suits
	// them.
	//
	// The distinction is real and must be visible wherever the refusal is made: a self-updating binary rewrites
	// itself UNASKED; `npx` fetches WHEN A PERSON TYPES THE COMMAND, and `@1.4.0` pins it. So a file that
	// refuses self-update while documenting `npx` has to show the pin, or it is claiming a property the command
	// printed beside it contradicts.
	//
	// This is prose, and prose is where this programme's claims keep rotting — which is exactly why it is
	// checked rather than trusted to a careful reader.
	// ⚠️ THE PIN IS DERIVED NOW, AND THIS SEAL HAD TO BE RE-ARGUED TO SAY SO. It used to require a literal
	// `@1.4` shape, and that literal was exactly the defect A_REPOSITORY_NAME_THE_BINARY_CARRIES_FOREVER found:
	// the README taught `npx nth-agent@1.4.0`, a package that does not exist pinned to a version nobody ever
	// published. A version typed into prose is a fact with no source. So the tree carries `@{{VERSION}}`, the
	// publish step substitutes it from the tag it is publishing, and REFUSES if any token survives — which means
	// the page a reader actually meets shows a real, shipped version. Both forms count as the pin here; the
	// argument the seal was written with is untouched.
	root := repoRoot(t)
	pinned := regexp.MustCompile(`@(?:\d+\.\d+|\{\{VERSION\}\})`)

	files := []string{filepath.Join(root, "README.md")}
	workflows, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatalf("reading the workflow directory: %v", err)
	}
	for _, entry := range workflows {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yml") {
			files = append(files, filepath.Join(root, ".github", "workflows", entry.Name()))
		}
	}

	refusals := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		text := string(data)
		if !strings.Contains(strings.ToLower(text), "self-update") {
			continue
		}
		refusals++
		if !strings.Contains(text, "npx") {
			t.Fatalf("%s refuses self-update and never mentions npx — the documented way to obtain this agent "+
				"fetches the latest version by default, and a refusal that does not say so is read as a promise "+
				"the command beside it breaks", path)
		}
		if !pinned.MatchString(text) {
			t.Fatalf("%s refuses self-update beside an npx command and never shows the pinned form (name@version) "+
				"— which is the whole distinction: npx fetches when a person types it, and a pin makes that "+
				"repeatable. Without it the refusal claims something the default behaviour contradicts", path)
		}
	}
	if refusals == 0 {
		t.Fatal("instrument guard: nothing in this repository refuses self-update any more. It was a decision " +
			"with a security argument and a support argument; if it has been abandoned that is a slice of its " +
			"own, and this seal must not pass by finding nothing to check")
	}
}

// jobPermissions returns the publish job's own `permissions:` block — the lines the runner acts on, never the
// prose around them. A seal that reads a comment naming a permission and calls it a grant is the shape this
// programme keeps deleting, so the parse is indented-block-exact and guards itself.
func jobPermissions(t *testing.T, workflow string) string {
	t.Helper()
	_, afterJobs, found := strings.Cut(workflow, "\njobs:")
	if !found {
		t.Fatal("instrument guard: no `jobs:` in publish-npm.yml — the parse is reading the wrong thing")
	}
	_, block, found := strings.Cut(afterJobs, "\n    permissions:")
	if !found {
		t.Fatal("the publish job declares no `permissions:` of its own. Trusted publishing needs `id-token: write` " +
			"on the job that publishes; without it `npm publish` cannot mint the OIDC token, and the registry " +
			"answers 404 — an error about the NAME for a problem with the IDENTITY")
	}
	var lines []string
	for _, line := range strings.Split(block, "\n")[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "      ") {
			break // dedented back to a sibling key of the job
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		t.Fatal("instrument guard: the publish job's permissions block parsed as empty — the parse is reading the " +
			"wrong thing, not the workflow")
	}
	return strings.Join(lines, "\n")
}

func TestThePublishPathCanActuallyAuthenticate(t *testing.T) {
	// A COMMAND THE CUSTOMER CANNOT TYPE, part two. Publication day used to begin with two instructions that
	// could not be carried out: create an npm AUTOMATION token (all classic tokens were permanently revoked
	// 2025-12-09 and cannot be recreated), on a workflow pinned to Node 22, whose bundled npm 10.x cannot speak
	// the protocol that replaced them. This seal is the half a person cannot check by reading: that the file can
	// authenticate at all.

	workflow := workflowText(t, "publish-npm.yml")

	// ① THE GRANT, off the job's own block rather than the prose that explains it.
	if !strings.Contains(jobPermissions(t, workflow), "id-token: write") {
		t.Fatalf("the publish job does not grant `id-token: write`, so trusted publishing cannot mint its OIDC "+
			"token and `npm publish` is answered 404 by the registry. That error names the PACKAGE, so whoever "+
			"debugs it goes looking at the name — which is exactly the wrong place. The job grants:\n%s",
			jobPermissions(t, workflow))
	}

	// ② THE npm FLOOR, somewhere the runner reads — not only in a comment. Trusted publishing needs npm
	// >= 11.5.1 (Node >= 22.14). Either the Node pin is high enough that its bundled npm clears the floor, or
	// the workflow installs npm itself; a comment claiming either is not a check.
	// ⚠️ THE PIN THE RUNNER ACTS ON, NOT THE FIRST TEXT THAT LOOKS LIKE ONE. Written first as a plain search of
	// the whole file, it matched a COMMENT recording the old pin and reported Node 22 on a workflow pinned to 24
	// — a seal failing on prose about the very thing it guards. The parse starts at the setup-node step.
	_, afterSetup, foundSetup := strings.Cut(workflow, "uses: actions/setup-node")
	if !foundSetup {
		t.Fatal("instrument guard: no setup-node step in publish-npm.yml — the parse is reading the wrong thing")
	}
	pin := regexp.MustCompile(`node-version:\s*"?(\d+)`).FindStringSubmatch(afterSetup)
	installsNpm := regexp.MustCompile(`npm\s+(?:i|install)\s+-g\s+npm@`).MatchString(workflow)
	if pin == nil {
		t.Fatal("instrument guard: no node-version pin found in publish-npm.yml — the parse is reading the wrong thing")
	}
	major := 0
	for _, c := range pin[1] {
		major = major*10 + int(c-'0')
	}
	if major < 24 && !installsNpm {
		t.Fatalf("the workflow pins Node %s and installs no npm of its own. setup-node's Node %s bundles npm 10.x, "+
			"which has no trusted-publishing support at all — the publish fails as a 404 that reads like a missing "+
			"package and is a missing identity. Pin Node 24 (which bundles npm 11.x) or install npm >= 11.5.1 "+
			"explicitly before the publish step", pin[1], pin[1])
	}

	// ③ …and the floor is ASSERTED BY THE RUN, so a future bundle change is caught on the runner rather than
	// by whoever is reading this file afterwards. The version must appear in a step that refuses.
	if !strings.Contains(workflow, "11.5.1") {
		t.Fatal("the npm version trusted publishing requires (11.5.1) appears nowhere in the workflow. Which npm a " +
			"Node release bundles is setup-node's business, not ours, so a Node pin alone states the floor in a " +
			"place no run ever checks")
	}
	floor, _, found := strings.Cut(workflow, "\n      - name: Confirm the name")
	if !found {
		t.Fatal("instrument guard: no `Confirm the name` step — the parse is reading the wrong thing")
	}
	idx := strings.Index(floor, "11.5.1")
	if idx < 0 || !strings.Contains(floor[idx:], "exit 1") {
		t.Fatal("the npm floor is stated before the gate but never refuses: a step that prints the version and " +
			"carries on has checked nothing. It must exit non-zero, like the version comparison further down")
	}
}

func TestNoInstructionNamesACredentialNpmDestroyed(t *testing.T) {
	// ⚠️ THIS SEAL FAILS ON THE STRING, DELIBERATELY. npm permanently revoked every classic token — Automation
	// included — on 2025-12-09, and disabled creating them on 2025-11-05. They cannot be recreated. This file
	// used to open publication day by telling the operator to make one, so the instruction sent a person to an
	// npm UI that no longer offers the thing named. The replacement is a granular access token for the first
	// publish and trusted publishing for every one after it.
	//
	// A future edit that reintroduces the phrase is caught here rather than on publication day, which is the
	// only day anybody reads this file.
	workflow := workflowText(t, "publish-npm.yml")
	lower := strings.ToLower(workflow)

	for _, dead := range []string{"automation token", "automation type", "classic token", "classic automation"} {
		if strings.Contains(lower, dead) {
			line := 0
			for i, text := range strings.Split(lower, "\n") {
				if strings.Contains(text, dead) {
					line = i + 1
					break
				}
			}
			t.Fatalf("publish-npm.yml:%d names %q. Every npm classic token, Automation included, was permanently "+
				"revoked on 2025-12-09 and creation was disabled on 2025-11-05 — so this instruction sends the "+
				"operator to a screen in npm's UI that no longer offers it, on the one day anybody reads this "+
				"file. The first publish takes a granular access token; every publish after it takes trusted "+
				"publishing and no stored secret", line, dead)
		}
	}

	// …and the replacement must actually be named, or the paragraph says nothing a reader can act on.
	for _, needed := range []string{"granular access token", "trusted publishing"} {
		if !strings.Contains(lower, needed) {
			t.Fatalf("the workflow never names %q. A credential paragraph that removes the dead instruction and "+
				"puts nothing in its place leaves the operator to guess which of npm's several token kinds this "+
				"is, on the day it cannot be undone", needed)
		}
	}
}

func TestThePackageNamesThePublicRepositoryItsProvenanceIsFor(t *testing.T) {
	// SIX_READERS_OF_ONE_WORD item 3.3, RE-ARGUED TWICE — and each re-argument is the point.
	//
	// ⚠️ IT USED TO SEAL THE ABSENCE OF PROVENANCE. `--provenance` needs TWO things: the publish job's
	// `id-token: write`, and a public source repository whose address the package names. This repository was
	// private, and npm generates no provenance for a private one, so the seal refused the flag and required the
	// workflow to say why. Before that it required the permission to be absent, which forbade trusted publishing,
	// the mechanism that needs the same grant to authenticate.
	//
	// ⚠️ NOW THE REPOSITORY IS PUBLIC, and npm attaches provenance by itself when a package published from a public
	// repository through trusted publishing names that repository. So what this seals is the half that makes the
	// statement true: package.json's `repository` names THIS repository. It is derived from go.mod's module path,
	// the measurement of where the repository is (TestTheModulePathIsTheRepositorysOwnName), not typed twice. A
	// package naming another repository, or none, would carry no provenance, or a statement about the wrong source.
	// The grant stays required, and the workflow must say this, not the old explanation.
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	module := ""
	for _, line := range strings.Split(string(data), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.TrimSpace(after)
			break
		}
	}
	if !strings.HasPrefix(module, "github.com/") || strings.Count(module, "/") != 2 {
		t.Fatalf("instrument guard: go.mod declares %q, not a github.com/<owner>/<repository> path — the address the package "+
			"must name cannot be read from it", module)
	}

	repository, ok := npmPackageJSON(t)["repository"].(map[string]any)
	if !ok {
		t.Fatal("package.json names no repository (`repository` is not an object) — npm attaches provenance only to a package " +
			"that names the public repository it was built from, so this package would be published with none")
	}
	if want := "git+https://" + module + ".git"; repository["type"] != "git" || repository["url"] != want {
		t.Fatalf("package.json's repository is %v — it must be {type: git, url: %s}, this repository, which go.mod's module "+
			"path names. Provenance states where the package was built; a package naming another repository states the wrong one",
			repository, want)
	}

	workflow := workflowText(t, "publish-npm.yml")
	job, _, found := strings.Cut(workflow[strings.Index(workflow, "\n  publish:\n")+1:], "\n    steps:\n")
	if !found || !strings.HasPrefix(job, "  publish:\n") {
		t.Fatal("instrument guard: no publish job with steps found — the parse is reading the wrong thing")
	}
	if !strings.Contains(job, "\n      id-token: write") {
		t.Fatal("the publish job does not grant id-token: write — trusted publishing cannot authenticate without it, and npm " +
			"cannot attach provenance either")
	}
	if strings.Contains(workflow, "private repositories") || strings.Contains(workflow, "--provenance` is still absent") {
		t.Fatal("the workflow still explains why provenance is absent. The repository is public now and the package names " +
			"it, so npm attaches provenance by itself; an explanation of its absence is a sentence that is no longer true")
	}
	// the comments as prose: a sentence may wrap onto the next comment line
	prose := strings.Join(strings.Fields(regexp.MustCompile(`\n\s*#`).ReplaceAllString(workflow, " ")), " ")
	for _, said := range []string{"the repository is public", "names it in package.json's `repository`", "npm attaches provenance by itself"} {
		if !strings.Contains(prose, said) {
			t.Fatalf("the workflow does not say %q — what makes the provenance true must be written where the grant is", said)
		}
	}
}

func TestTheModulePathIsTheRepositorysOwnName(t *testing.T) {
	// A REPOSITORY NAME THE BINARY CARRIES FOREVER.
	//
	// ⚠️ THE MODULE PATH IS IN THE BYTES, NOT ONLY IN THE SOURCE. Go writes it into the symbol of every
	// function the module compiles. Measured 2026-09-21 against the binary sitting in this tree: 509
	// occurrences of the old path, in symbols like `…/internal/android.AdbRunner`, and `go version -m` prints
	// it outright. `-trimpath`, which release.yml passes, strips FILESYSTEM prefixes and leaves the module
	// path untouched. So the first time the agent panicked on a customer's machine, the dead company name was
	// printed down every frame of the stack — and the bytes of a published release would have carried it for
	// as long as anybody kept the tarball.
	//
	// ⚠️ AND IT IS DERIVED, NOT SPELLED TWICE. A seal comparing the module path to a literal typed here would
	// be a second place the name lives, which is the defect its neighbours in this file exist to prevent. The
	// repository's own directory is the one fact both sides already agree to: Go resolves a by-path fetch
	// against the remote whose last segment is that name, so a module declaring anything else answers every
	// `go get` with "module declares its path as … but was required as …".
	root := repoRoot(t)
	repo := filepath.Base(root)

	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v — it is the file that decides what every symbol in every shipped binary says", err)
	}
	declared := ""
	for _, line := range strings.Split(string(data), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			declared = strings.TrimSpace(after)
			break
		}
	}
	if declared == "" {
		t.Fatal("go.mod declares no module path at all — nothing here can be built or fetched")
	}

	last := declared[strings.LastIndex(declared, "/")+1:]
	if last != repo {
		t.Fatalf("go.mod declares the module as %q and this repository's directory is %q. Go module paths are "+
			"case-sensitive and resolved by path, so every `go get` answers \"module declares its path as %s but "+
			"was required as …/%s\" — and worse, %q is compiled into the symbol of every function this module "+
			"ships, so it prints down the whole stack the first time the agent panics on a customer's machine.",
			declared, repo, declared, repo, last)
	}

	// …and the organisation is not this slice's to move: a rename that quietly changed it would point every
	// fetch at an account nobody here controls.
	if !strings.HasPrefix(declared, "github.com/unidotssoftwaresolutions/") {
		t.Fatalf("the module path is %q — the GitHub organisation is unidotssoftwaresolutions and renaming the "+
			"repository does not move it. A path under a different account is one nobody here can publish to.", declared)
	}
}

// readmeBoundary is the marker that separates the page npmjs.com renders from the part that is ours. It is a
// constant here and a literal in the workflow on purpose: both sides must agree, and this seal is what holds
// them to it — a marker only one side knows is a cut that silently takes everything.
const readmeBoundary = "<!-- NOT-FOR-THE-REGISTRY -->"

func TestTheReadmeThatShipsIsCutAtTheBoundary(t *testing.T) {
	// A REPOSITORY NAME THE BINARY CARRIES FOREVER, part two.
	//
	// ⚠️ `publish/npm/package.json` ships README.md, so THIS FILE IS THE npmjs.com PAGE for the package. Its
	// last sixty lines were ours, not theirs: the internal release page, the cross-repository seal architecture,
	// which script runs those seals, and a recounting of one that was red for three slices. A customer clicking
	// through to install an agent was handed the company's engineering narrative.
	//
	// ONE README, NOT TWO. A second file is a second home for every fact in it, which is the defect this whole
	// repository argues against. So the file carries a boundary and the publish CUTS at it.
	readme := readmeText(t)
	if !strings.Contains(readme, readmeBoundary) {
		t.Fatalf("README.md carries no %s boundary. Everything below it is internal and must not reach "+
			"npmjs.com; without the marker the publish cannot tell the customer's half from ours, and the file "+
			"that ships is the whole thing — which is what a customer was already getting.", readmeBoundary)
	}

	workflow := workflowText(t, "publish-npm.yml")
	if !strings.Contains(workflow, readmeBoundary) {
		t.Fatalf("the publish workflow never mentions %s, so whatever it copies into the package is not cut at "+
			"the boundary this file declares. A marker only one side knows about is not a boundary.", readmeBoundary)
	}
	// …and the ABSENCE of the marker must STOP the publish. Falling back to copying the whole file is the exact
	// behaviour being removed, so a cut that degrades to it has removed nothing.
	cut, _, found := strings.Cut(workflow, "\n      - name: The binary must report the version")
	if !found {
		t.Fatal("instrument guard: no version-check step after the README step — the parse is reading the wrong thing")
	}
	idx := strings.Index(cut, readmeBoundary)
	if idx < 0 || !strings.Contains(cut[idx:], "exit 1") {
		t.Fatal("the publish step reads the boundary but does not refuse when it is missing. A README with no " +
			"marker must fail the publish, not ship entire — the fallback IS the defect")
	}
	if strings.Contains(workflow, "cp README.md publish/npm/README.md") {
		t.Fatal("the publish workflow still copies README.md whole into the package. The customer's page is the " +
			"half above the boundary; copying the file hands them the internal half as well")
	}
}

func TestTheCustomerHalfOfTheReadmeIsTheirsAlone(t *testing.T) {
	// THE SAME RULE THE DOCS PAGE ALREADY LIVES BY, APPLIED TO THE FILE THAT SHIPS. The web app's C# seal refuses
	// `SHA256SUMS`, `com.apple.quarantine` and "download the release" on a page a customer reads, because each
	// is an instruction pointing at a release page that is not the way to install. This file IS such a page, and it
	// was never held to it.
	//
	// The half is CUT the way the workflow cuts it, so this measures what would actually be published rather
	// than what the file looks like.
	readme := readmeText(t)
	customer, _, found := strings.Cut(readme, readmeBoundary)
	if !found {
		t.Fatalf("README.md carries no %s boundary, so there is no customer half to check", readmeBoundary)
	}
	// ⚠️ AND CHANGELOG.md, WHOLE (AN_AGENT_THAT_SHIPS_ITS_NOTICES): it ships in the package beside the README, and the release
	// notes are its sections, so every line of it is read by somebody outside the company.
	changelog, err := os.ReadFile(filepath.Join(repoRoot(t), "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("reading CHANGELOG.md: %v — the npm package ships it", err)
	}
	for _, page := range []struct{ name, text string }{{"README.md", customer}, {"CHANGELOG.md", string(changelog)}} {
		checkTheirsAlone(t, page.name, page.text)
	}
}

// checkTheirsAlone refuses, in text a customer reads, anything that points at what only we can open.
func checkTheirsAlone(t *testing.T, name, customer string) {
	t.Helper()
	// The sibling repositories a customer cannot open, and the script that runs their seals, are named by sum
	// (farmtest.PrivateRepositoryIn), so this published file does not spell them either.
	if what, line := farmtest.PrivateRepositoryIn(customer); what != "" {
		t.Fatalf("%s:%d — text the npm package ships names %s. A person who came to install an agent is reading it on "+
			"the registry page, with no way to reach what it points at.", name, line, what)
	}
	for _, forbidden := range []struct{ text, why string }{
		{"SHA256SUMS", "a checksum file that only exists on the release page, which is not the way to install"},
		{"com.apple.quarantine", "an instruction for a file fetched by hand from the release page"},
		{"releases page", "the release page, which is the package's source and not the way to install"},
	} {
		if !strings.Contains(strings.ToLower(customer), strings.ToLower(forbidden.text)) {
			continue
		}
		line := 0
		for i, text := range strings.Split(customer, "\n") {
			if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden.text)) {
				line = i + 1
				break
			}
		}
		t.Fatalf("%s:%d — text the npm package ships names %q, which is %s. A person who came to "+
			"install an agent is reading it on the registry page, with no way to reach what it points at.",
			name, line, forbidden.text, forbidden.why)
	}
}

func TestNoUnderivedNameOrVersionSurvivesInTheReadme(t *testing.T) {
	// ⚠️ `1.4.0` MUST BE UNABLE TO COME BACK. The README taught `npx nth-agent@1.4.0` — a package that does not
	// exist, pinned to a version nobody ever published. 8fe743d rewrote the old package name mechanically and
	// the example came loose from both halves of what it was teaching; `agentDistribution.ts`'s docblock warns
	// about that exact invented number BY NAME, one repository away.
	//
	// A version or a package name typed into prose is a fact with no source. Both are tokens now, substituted at
	// publish time from package.json and from the tag being published, so neither can drift from what ships.
	readme := readmeText(t)
	customer, _, _ := strings.Cut(readme, readmeBoundary)

	if m := regexp.MustCompile(`@\d+\.\d+(?:\.\d+)?`).FindString(customer); m != "" {
		t.Fatalf("the customer's half of README.md pins %q. That is a version literal with no source: nothing "+
			"checks it was ever published, and the last one taught a build that never existed. Write "+
			"`@{{VERSION}}`, which the publish step fills from the tag it is publishing.", m)
	}

	name, _ := npmPackageJSON(t)["name"].(string)
	if strings.Contains(customer, name) {
		t.Fatalf("the customer's half of README.md types the package name %q. It is declared in package.json and "+
			"a seal holds that to ONE occurrence; typing it here is a second home for it, and the rename that "+
			"prompted this slice is what a second home costs. Write `{{PACKAGE}}`.", name)
	}
	if !strings.Contains(customer, "{{PACKAGE}}") || !strings.Contains(customer, "{{VERSION}}") {
		t.Fatal("the customer's half no longer uses the {{PACKAGE}} / {{VERSION}} tokens — the install line and " +
			"the pin example are what a reader copies, and both must come from the package being published")
	}
}

func readmeText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v — it is the page npmjs.com renders for this package", err)
	}
	return string(data)
}

func TestEveryFileThatShipsIsOneSomethingHolds(t *testing.T) {
	// THE CENSUS OF WHAT REACHES A CUSTOMER'S DISK. `files` in package.json is the whole list, and it is the
	// reason the README defect survived: three entries, and nothing anywhere enumerated them or asked what each
	// one was. README.md shipped WHOLE — sixty lines of internal engineering narrative on the registry page —
	// because "the package ships a README" was true and nobody had written down what that README was.
	//
	// Derived from the manifest rather than listed here, so an entry added later is a decision somebody argues
	// rather than a file that quietly starts being published.
	files, ok := npmPackageJSON(t)["files"].([]any)
	if !ok {
		t.Fatalf("package.json declares no `files` array — npm then publishes everything not ignored, which on "+
			"this repository is the whole source tree. Found %T", npmPackageJSON(t)["files"])
	}

	// What each entry is, and what holds it. A new entry lands here with its own sentence or this seal fails.
	held := map[string]string{
		"index.js":  "the platform shim, held against the release matrix by TestTheShimShipsExactlyTheTargetsTheReleaseBuilds",
		"bin/":      "the binaries the publish workflow downloads from the release it is republishing, never a second build",
		"README.md": "the registry page, cut at the boundary and checked for leaks by the two README seals above",
		// AN_AGENT_THAT_SHIPS_ITS_NOTICES: an npm install is a copy, and the licences of the code in the binaries ask for
		// their notices with every copy.
		"THIRD_PARTY_NOTICES.txt": "the licences of the third-party code inside the binaries, built from what the release links " +
			"(the module root's notices_test.go), copied in from the tag's own release and held to `nth-agent notices` " +
			"by TestThePublishRefusesAPackageWhoseNoticesAreNotTheBinarysOwn",
		"CHANGELOG.md": "what each release changed, the tag's own copy — release.yml refuses a tag with no section of its own, " +
			"and TestTheCustomerHalfOfTheReadmeIsTheirsAlone and the changelog seals hold it to the customer's register",
		// AN_AGENT_READY_TO_BE_READ: the agent's own code is under the Apache License 2.0, which asks for these with every copy.
		"LICENSE": "the Apache License 2.0 text, unmodified, from the tag's tree — held to the published text by its SHA-256 " +
			"(the module root's TestTheLicenceFilesAreThereAndTheNpmPackageCarriesThem) and put in the package by the step " +
			"TestThePublishPutsTheTagsNoticesAndChangelogInThePackage runs",
		"NOTICE": "the attribution notice the licence asks for, from the tag's tree, held by the same two seals",
	}

	if len(files) < 3 {
		t.Fatalf("instrument guard: the manifest lists %d shipped entries and this package has always had three "+
			"— the parse is reading the wrong thing, not a package that stopped shipping its binaries", len(files))
	}

	var unheld []string
	for _, entry := range files {
		name, _ := entry.(string)
		if _, known := held[name]; !known {
			unheld = append(unheld, name)
		}
	}
	if len(unheld) > 0 {
		sort.Strings(unheld)
		t.Fatalf("%d entr(ies) reach a customer's disk that nothing in this repository accounts for: %s. Every "+
			"file in `files` is published to npmjs.com and unpacked on machines we will never see; the README "+
			"shipped its internal half for two weeks because nobody had written down what it was. Say here what "+
			"the new entry is and which seal holds it.", len(unheld), strings.Join(unheld, ", "))
	}
}
