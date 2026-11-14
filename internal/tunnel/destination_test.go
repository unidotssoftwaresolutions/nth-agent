package tunnel_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/browserstack"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/tunnel"
)

// SIX_READERS_OF_ONE_WORD item 3.2 — the "established" sentence said where frames go, and it was wrong for every
// cloud-only node.
//
// "requests for this node's devices now reach the local Appium" was printed unconditionally, and a cloud-only
// node passes no --appium at all: a customer reading it concluded they had to install Appium for a device they
// will never touch. The sentence now comes from the SAME rule forward applies — with credentials configured every
// frame goes to BrowserStack's hub — so it cannot describe a route the frames do not take.

func establishedLine(t *testing.T, appiumURL string, creds browserstack.Credentials) string {
	t.Helper()
	farm, server := newGatedFarm(t)
	farm.accepting.Store(true)
	var out syncWriter
	drive := tunnel.New(tunnel.Config{
		Phones:  allowed.Offering(fixturePhones...),
		FarmURL: server.URL, NodeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Token: "sk-agent-T", AppiumURL: appiumURL,
		BrowserStack: creds, BrowserStackApp: "bs://cafebabe",
		ReconnectFloor: time.Millisecond, ReconnectCap: 2 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		},
		Out: &out,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go drive.Run(ctx)
	waitFor(t, "drive tunnel established", func() bool { return strings.Contains(out.String(), "drive tunnel established") })
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "drive tunnel established") {
			return line
		}
	}
	t.Fatal("no established line in the transcript")
	return ""
}

func TestACloudOnlyNodeIsNotToldItsRequestsReachALocalAppiumItDoesNotHave(t *testing.T) {
	// A cloud-only node: credentials configured, no --appium. The README says in bold that it needs no local
	// Appium; the tunnel's own sentence must not contradict the README two seconds after startup.
	line := establishedLine(t, "", browserstack.Credentials{User: "customer", Key: "k"})

	if strings.Contains(line, "local Appium") {
		t.Fatalf("a node with no --appium was told its requests reach the local Appium:\n%s", line)
	}
	if !strings.Contains(line, "BrowserStack's hub") {
		t.Fatalf("the sentence must say where forward actually sends the frames — the hub — got:\n%s", line)
	}
}

func TestALocalNodesEstablishedSentenceIsByteForByteWhatItWas(t *testing.T) {
	// The path with customers on it. A phone on a desk behind a local Appium reads exactly the sentence it read
	// before this slice; the fix for the cloud-only node must not make the local one vaguer.
	line := establishedLine(t, "http://127.0.0.1:1", browserstack.Credentials{})

	const before = "drive tunnel established — requests for this node's devices now reach the local Appium"
	if line != before {
		t.Fatalf("the local sentence moved:\n  want %q\n  got  %q", before, line)
	}
}
