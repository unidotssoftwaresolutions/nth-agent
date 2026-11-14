package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/command"
)

// ── `upload` — THE VERB THAT MAKES `--browserstack-app` POSSIBLE TO SATISFY ─────────────────────────────────
//
// connect refuses to start a cloud session without a build id, and until this verb existed there was no way to
// obtain one through this product: the uploader was a library function called from one build-tagged test. The
// README already told people to "ask the agent to put one there" and the agent had no verb that could.
//
// IT NEEDS NO FARM AND NO TOKEN. Credentials and a build, nothing else — because a person must be able to get an
// id BEFORE they can connect, and requiring a workspace to obtain one would make the two steps circular.

// uploaderFor is a TEST SEAM and deliberately not a customer surface: no flag and no environment variable
// reaches it. The tests point it at a stub server so they drive the SAME verb the binary does — a path proved
// against a different path is a path nobody proved.
var uploaderFor = func() browserstack.Uploader {
	return browserstack.Uploader{Client: browserstack.FileUploadClient()}
}

// runUpload puts a build on the customer's BrowserStack account and tells them what to do with the id.
func runUpload(inv *invocation, stdout, stderr io.Writer) int {
	// THE KEY IS READ HERE AND GOES ONE PLACE: the Authorization header browserstack.Uploader builds. It is not
	// printed by any line below — the success line prints the bs:// id, which is not a secret — and the key-read
	// census names this file for exactly this read.
	creds := browserstack.Credentials{
		User: firstNonEmpty(inv.first("--browserstack-user"), os.Getenv("NTH_AGENT_BROWSERSTACK_USER")),
		Key:  firstNonEmpty(inv.first("--browserstack-key"), os.Getenv("NTH_AGENT_BROWSERSTACK_KEY")),
	}
	switch {
	case creds.Partial():
		fmt.Fprintln(stderr, "BrowserStack needs BOTH halves: --browserstack-user and --browserstack-key (or "+
			"NTH_AGENT_BROWSERSTACK_USER and NTH_AGENT_BROWSERSTACK_KEY). Only one was supplied.")
		return exitMistake
	case !creds.Configured():
		fmt.Fprintln(stderr, "an upload needs your BrowserStack credentials: --browserstack-user and "+
			"--browserstack-key (or NTH_AGENT_BROWSERSTACK_USER and NTH_AGENT_BROWSERSTACK_KEY). No farm and no "+
			"workspace token are needed for this — the upload goes from this machine to your own account.")
		return exitMistake
	}

	// ⚠️ TWO FLAGS BECAUSE TWO DIFFERENT MACHINES DO THE FETCHING, and one label over two facts is the defect
	// this programme keeps deleting. Exactly one of them, and "neither" is not "upload nothing" — it is a person
	// who has not been told which of the two they meant.
	path, buildURL := strings.TrimSpace(inv.first("--file")), strings.TrimSpace(inv.first("--url"))
	switch {
	case path != "" && buildURL != "":
		fmt.Fprintln(stderr, "--file and --url are different operations, so name one: --file sends the bytes from "+
			"this machine, --url asks BrowserStack to fetch the address themselves. Preferring one silently would "+
			"pick a machine on your behalf.")
		return exitMistake
	case path == "" && buildURL == "":
		fmt.Fprintln(stderr, "an upload needs a build: --file PATH sends a build on this machine, or --url URL "+
			"asks BrowserStack to fetch one. Use --file for anything that is not published on the public "+
			"internet — a build behind your NAT or VPN is reachable from here and not from them.")
		return exitMistake
	}

	customID := strings.TrimSpace(inv.first("--custom-id"))
	uploader := uploaderFor()

	// THE OUTER BOUND IS A CONTEXT DEADLINE, not a client timeout — see browserstack.UploadCeiling for why an
	// overall client deadline would punish the slow link this verb exists to serve.
	ctx, cancel := context.WithTimeout(context.Background(), browserstack.UploadCeiling)
	defer cancel()

	var app browserstack.App
	var err error
	if path != "" {
		// REFUSED BEFORE THE WAIT, NOT AFTER IT. InspectBuild answers the four local questions and says which one
		// failed; spending twenty minutes of somebody's uplink to be told "wrong extension" at the far end is the
		// unkindness this check exists to prevent.
		size, inspectErr := browserstack.InspectBuild(path)
		if inspectErr != nil {
			fmt.Fprintf(stderr, "%v\n", inspectErr)
			return exitMistake
		}
		// ⚠️ SAY SOMETHING BEFORE A LONG WAIT. A 200 MB upload with no output reads as a hang, and a person who
		// thinks a command has hung kills it — which on this path means an upload started and abandoned. One line
		// before and one after; not a progress bar, which would be a terminal-detection problem for no more
		// information than this.
		fmt.Fprintf(stdout, "uploading %s (%s) to your BrowserStack account — this can take a while on a slow link\n",
			path, browserstack.HumanBytes(size))
		app, err = uploader.UploadFile(ctx, creds, path, customID)
	} else {
		fmt.Fprintf(stdout, "asking BrowserStack to fetch %s — the address has to be reachable from THEIR network, "+
			"not from this machine\n", buildURL)
		app, err = uploader.UploadByURL(ctx, creds, buildURL, customID)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	printUploaded(stdout, app)
	return exitOK
}

// printUploaded is HALF THE FIX, and it is a named function so that half can be sealed.
//
// The finding this verb answers was that a person cannot GET a build id. A verb that returns a hash and stops
// has fixed one half of that and left the other: the id means nothing until you know it goes in
// --browserstack-app. So the next command is printed, every time, with the id already in it.
//
// THE ID IS NOT A SECRET. A bs:// id names a build inside an account that already belongs to the customer and
// authenticates nothing on its own. The access key is a different thing entirely and appears nowhere here.
func printUploaded(stdout io.Writer, app browserstack.App) {
	fmt.Fprintf(stdout, "uploaded: %s\n", app.URL)
	if app.CustomID != "" {
		fmt.Fprintf(stdout, "custom id: %s — uploading again with the same one repoints it at the new build\n", app.CustomID)
	}
	fmt.Fprintf(stdout, "\nUse it with connect:\n  %s\n", command.Line("connect --farm https://... --browserstack-app "+app.URL))
	fmt.Fprintf(stdout, "or export it once:\n  export NTH_AGENT_BROWSERSTACK_APP=%s\n", app.URL)
	fmt.Fprintln(stdout, "\nBrowserStack deletes uploaded builds after 30 days, so this id stops working then.")
}
