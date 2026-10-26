package farm_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// ── item 1: the scheme seal + the loopback exception ─────────────────────────────────────────────────────────

func TestTheSchemeRuleAndItsNarrowException(t *testing.T) {
	refused := []string{
		"http://farm.example",  // the café network reads the token
		"http://10.0.0.5:8080", // "it's our LAN" — the packets still leave the machine
		"http://192.168.1.20",  //
		"http://farm.internal", // "it's behind the VPN" — same argument, same answer
		"ftp://farm.example",   // not a transport this client speaks
		"farm.example",         // no scheme at all
	}
	for _, raw := range refused {
		if err := farm.ValidateBaseURL(raw); err == nil {
			t.Fatalf("%q must be refused before any request carries the token", raw)
		}
	}

	// THE EXCEPTION, EXACTLY AS NARROW AS THE ARGUMENT: loopback packets never leave the machine.
	allowed := []string{
		"https://farm.example",
		"https://farm.example:8443/base",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://127.0.0.2:8080", // all of 127/8 IS loopback
		"http://[::1]:8080",
	}
	for _, raw := range allowed {
		if err := farm.ValidateBaseURL(raw); err != nil {
			t.Fatalf("%q must be allowed: %v", raw, err)
		}
	}

	// The refusal names what is wrong and what to do, in one sentence.
	err := farm.ValidateBaseURL("http://farm.example")
	for _, must := range []string{"cleartext", "https://", "loopback"} {
		if !strings.Contains(err.Error(), must) {
			t.Fatalf("the refusal must name %q, got %q", must, err)
		}
	}
}

// ── item 2: the redirect seal — asserted on what the SECOND server received ─────────────────────────────────

// A real redirect: server A answers 302 to server B. The proof is what B RECEIVED — an assertion on
// the client's return value alone cannot tell "we refused" from "we followed and the token arrived".
func TestARedirectIsRefusedAndTheTokenNeverReachesTheSecondHost(t *testing.T) {
	var secondHostRequests atomic.Int32
	var secondHostToken atomic.Value
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHostRequests.Add(1)
		secondHostToken.Store(r.Header.Get("X-Agent-Token"))
		w.Write([]byte(`{"success":true,"data":{"nodeId":"11111111-2222-3333-4444-555555555555"}}`))
	}))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+r.URL.Path, http.StatusFound)
	}))
	defer first.Close()

	client := &farm.Client{BaseURL: first.URL, Token: "sk-agent-REDIRECTSECRET", HTTP: first.Client()}
	_, status, err := client.Register(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "host", farm.Report{})

	if got := secondHostRequests.Load(); got != 0 {
		t.Fatalf("the second host received %d request(s) — the token was carried to a hop nobody checked (it got %q)",
			got, secondHostToken.Load())
	}
	if status != farm.CallTransport {
		t.Fatalf("a refused redirect surfaces as a transport failure, got %v", status)
	}
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("the failure names what happened, got %v", err)
	}
}

// ── item 0.1: the authority is at the point of use ──────────────────────────────────────────────────────────

// THE TRANSPORT-AUTHORITY SEAL: a Client constructed DIRECTLY — no loop, no startup check — still
// refuses to attach the token to a plaintext transport. The distinguishing mark is the validation
// sentence itself: a dial or DNS error could also fail this call, but only the pre-flight refusal
// says "cleartext", and only the pre-flight refusal makes no network attempt at all.
func TestTheSchemeIsEnforcedOnTheCallThatCarriesTheToken(t *testing.T) {
	client := &farm.Client{BaseURL: "http://farm.example", Token: "sk-agent-DIRECTSECRET", HTTP: &http.Client{}}
	_, status, err := client.Register(context.Background(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "host", farm.Report{})

	if err == nil || !strings.Contains(err.Error(), "cleartext") {
		t.Fatalf("the point of use must refuse the plaintext transport itself (not rely on a caller's courtesy check), got %v / %v", status, err)
	}
	// THE TERMINAL CLASS (AN_ASSERTION_THAT_BUYS_A_READ item 0): an unfixable transport is NOT
	// CallTransport — retrying never turns http into https, and a loop that backs off against it
	// forever treats a config error as weather. It gets its own class, like the revoked token.
	if status != farm.CallUnsafeTransport {
		t.Fatalf("plaintext must classify as CallUnsafeTransport (terminal), got %v", status)
	}
}

// ── item 4's instrument: every read of the token is classified ──────────────────────────────────────────────

// THE SWEEP'S MECHANICAL HALF: every place production code touches the credential is a potential way
// out of the process. The population is DISCOVERED — every `Token` selector/identifier in non-test
// sources — and each site's file must be classified below. A new read fails until its author says
// which exit it feeds and what guards it.
func TestEveryReadOfTheTokenIsClassified(t *testing.T) {
	classified := map[string]string{
		"client.go": "SETS the X-Agent-Token header — the one intended exit; transport enforced by ValidateBaseURL (https or loopback) and the forced redirect policy (httpClient); never a URL component (sealed by the URL recording in the loop tests). Its second read hands the token to readRefusal ONLY so it can be redacted out of a 401's text — see refusal.go",
		// AN_AGENT_THAT_GUESSES_WHY_IT_WAS_REFUSED: a 401's body is now read and printed, and a server that echoes the
		// token back into its refusal would have made printing it the way the token reached a terminal.
		"refusal.go": "reads the token ONLY TO REDACT IT: the farm's refusal text is searched for the agent's own token and every occurrence becomes «token» before anything is printed — compared against, never written anywhere; this is what keeps 'never written into any output' true against a server that echoes the token back (sealed in refusal_test.go)",
		"main.go":   "reads NTH_AGENT_TOKEN / --token into memory and hands it to farm.Client; never echoed (usage text warns that argv is visible to `ps` — the reason the env var is preferred)",
		"tunnel.go": "SETS X-Agent-Token on the tunnel upgrade — the second intended exit; its transport is validated at ITS OWN point of use (Run calls ValidateBaseURL before dialing, because the upgrade does not ride Client.post()); the census caught this dial before that check existed",
	}

	root := filepath.Join("..", "..")
	found := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, source, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				if node.Sel.Name == "Token" {
					found[filepath.Base(path)] = append(found[filepath.Base(path)], "selector")
				}
			case *ast.Ident:
				if node.Name == "token" && node.Obj != nil {
					found[filepath.Base(path)] = append(found[filepath.Base(path)], "ident")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}
	// INSTRUMENT GUARD: the one intended exit must be in the population — a sweep that lost the
	// header-set site is reading the wrong tree.
	if len(found["client.go"]) == 0 {
		t.Fatal("instrument guard: the sweep did not find client.go's header-set site")
	}
	for file := range found {
		if _, ok := classified[file]; !ok {
			t.Fatalf("%s reads the token and carries no classification — say which exit it feeds and what guards it", file)
		}
	}
	for file := range classified {
		if _, ok := found[file]; !ok {
			t.Fatalf("classification for %s is stale — nothing there reads the token any more", file)
		}
	}
}
