package connect_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
)

// AN_AGENT_THAT_GUESSES_WHY_IT_WAS_REFUSED — what the person running the agent reads when the farm refuses its token.
//
// THE FARM KNOWS WHY; THE AGENT KNOWS WHAT TO DO NEXT. The agent used to throw the farm's sentence away and print a
// guess — "it may have been revoked" — and since agent tokens can end, the commonest refusal is a token that simply
// lived out its week. These cases drive the real loop against a farm that refuses at REGISTRATION and at HEARTBEAT,
// because the two used to say different things and now share one narration.

// The farm's own sentence for a token that reached its end (the farm's ApiKeyAuthMiddleware.ExpiredAgentTokenMessage).
const farmExpiredSentence = "This agent token has expired. Create a new one in Settings → Agent tokens and start the agent with it."

// causeWords are reasons an agent could GUESS for a refusal. None of them is the agent's to say.
var causeWords = regexp.MustCompile(`(?i)\b(revok\w*|expir\w*|invalid\w*|wrong|stolen|leak\w*|delet\w*|disabl\w*|compromis\w*|mistyp\w*|typo)\b`)

// refusingFarm answers a token refusal at one point of the conversation: "register" refuses the first call, and
// "heartbeat" accepts the registration and refuses the first heartbeat. It counts both.
func refusingFarm(t *testing.T, at, body string) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var registers, heartbeats atomic.Int32
	refuse := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, body)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			registers.Add(1)
			if at == "register" {
				refuse(w)
				return
			}
			_, _ = io.WriteString(w, `{"success":true,"data":{"nodeId":"11111111-2222-3333-4444-555555555555"}}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			heartbeats.Add(1)
			refuse(w)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, &registers, &heartbeats
}

// runRefused drives the real loop against a refusing farm. It runs under a DEADLINE: registration retries a failure it
// believes will heal without any limit of its own, so a refusal mistaken for one would otherwise hold a test until Go's
// ten-minute timeout — which is how the first plant of a retryable 401 reddened, in a panic rather than in a sentence.
func runRefused(t *testing.T, at, body string) (string, error, int32, int32) {
	t.Helper()
	server, registers, heartbeats := refusingFarm(t, at, body)
	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := connect.Run(ctx, config(server, &out, 5))
	return out.String(), err, registers.Load(), heartbeats.Load()
}

// farmSaying is the farm's refusal envelope around message, encoded as JSON encodes it — a control character travels
// as \u00XX. (strconv.Quote would write Go escapes like \x1b, which are not JSON: the first version of this helper
// did, and the hostile-sentence seal read "no reason" for a body that never parsed.)
func farmSaying(message string) string {
	encoded, _ := json.Marshal(message)
	return `{"success":false,"data":null,"message":` + string(encoded) + `,"errors":["x"]}`
}

// remedyProblem reads the remedy for what it must SAY — which variable holds the token, that a new one comes from the
// workspace, and that the agent must be started again — never for its exact words: rewording a remedy that still says
// all three is an improvement, and a seal that reddened on it would make nobody improve it. "" when the remedy is right.
func remedyProblem(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "NTH_AGENT_TOKEN") {
			continue
		}
		if !strings.Contains(strings.ToLower(line), "workspace") {
			return "the remedy names NTH_AGENT_TOKEN but not where a new token comes from (the workspace)"
		}
		if !regexp.MustCompile(`(?i)\b(re)?start`).MatchString(line) {
			return "the remedy never says the agent has to be started again"
		}
		return ""
	}
	return "no remedy at all — nothing names NTH_AGENT_TOKEN"
}

// SEAL 1 — AN EXPIRED TOKEN IS TOLD IT EXPIRED, at registration and at heartbeat.
func TestATokenThatReachedItsEndIsToldSo(t *testing.T) {
	for _, at := range []string{"register", "heartbeat"} {
		out, _, _, _ := runRefused(t, at, farmSaying(farmExpiredSentence))
		if !strings.Contains(out, "the farm refused this agent's token. It says: “"+farmExpiredSentence+"”") {
			t.Fatalf("at %s: the farm said the token had expired, and the person read:\n%s\n— they would go looking for whoever revoked it, and for the leak that explains why", at, out)
		}
		if problem := remedyProblem(out); problem != "" {
			t.Fatalf("at %s: the person was told why but not what to do next — %s:\n%s", at, problem, out)
		}
	}
}

// SEAL 2, behaviourally — whatever the farm says, the agent's OWN words name no cause.
func TestTheAgentNeverGuessesWhyItWasRefused(t *testing.T) {
	for _, at := range []string{"register", "heartbeat"} {
		for name, body := range map[string]string{
			"with the farm's reason": farmSaying(farmExpiredSentence),
			"with no reason":         "",
		} {
			out, _, _, _ := runRefused(t, at, body)
			ownWords := strings.ReplaceAll(out, farmExpiredSentence, "")
			if m := causeWords.FindString(ownWords); m != "" {
				t.Fatalf("at %s, %s: the agent's own words named a cause — %q — in:\n%s\nthe person would believe a reason the agent never had", at, name, m, out)
			}
		}
	}
}

// SEAL 3 — NO REASON, NO GUESS: a refusal the agent cannot read is said to be one, with the remedy, and nothing more.
func TestARefusalWithNoReadableReasonIsSaidPlainly(t *testing.T) {
	saysRefusedWithoutAReason := regexp.MustCompile(`(?i)refused[^\n]*\bno reason\b`)
	for _, at := range []string{"register", "heartbeat"} {
		for name, body := range map[string]string{
			"an empty body": "",
			"a non-JSON body": "<html><body><h1>401 Authorization Required</h1></body></html>",
			"no message":      `{"success":false,"data":null}`,
		} {
			out, _, _, _ := runRefused(t, at, body)
			if !saysRefusedWithoutAReason.MatchString(out) {
				t.Fatalf("at %s, with %s: the person should read that the token was refused and that no reason was given — got:\n%s", at, name, out)
			}
			if problem := remedyProblem(out); problem != "" {
				t.Fatalf("at %s, with %s: %s:\n%s", at, name, problem, out)
			}
			if m := causeWords.FindString(out); m != "" {
				t.Fatalf("at %s, with %s: the farm gave no reason and the agent supplied one — %q:\n%s", at, name, m, out)
			}
			if strings.Contains(out, "Authorization Required") || strings.Contains(out, "<html>") {
				t.Fatalf("at %s, with %s: a body that is not the farm's envelope was printed as if it were a reason:\n%s", at, name, out)
			}
		}
	}
}

// SEAL 4, through the loop — the farm's sentence reaches the terminal without its controls, and as ITSELF: a `%`
// verb inside it is printed, never interpreted as a format.
func TestTheFarmsSentenceReachesTheTerminalSafeAndAsItself(t *testing.T) {
	hostile := "\x1b[2J\x1b]0;owned\x07Refused %s %v %d%% \x1b[31mnow\x1b[0m\r\nregistered as node 1"
	for _, at := range []string{"register", "heartbeat"} {
		out, _, _, _ := runRefused(t, at, farmSaying(hostile))
		for _, r := range out {
			if (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Fatalf("at %s: a control character (U+%04X) from the farm's sentence reached the person's terminal:\n%q", at, r, out)
			}
		}
		if !strings.Contains(out, "It says: “Refused %s %v %d%% now registered as node 1”") {
			t.Fatalf("at %s: the farm's sentence was not printed as itself — a %% verb in it was interpreted, or its words were lost:\n%q", at, out)
		}
		if strings.Contains(out, "%!") {
			t.Fatalf("at %s: the farm's words were used as a FORMAT — a server controls how the agent's output is rendered:\n%q", at, out)
		}
	}
}

// SEAL 6, at the loop — a refused token stops the agent, at registration and at heartbeat: one call, no retry, no
// re-registration, a non-zero exit.
func TestARefusalIsTerminalAtBothSites(t *testing.T) {
	for _, site := range []struct {
		at                         string
		wantRegisters, wantBeats int32
	}{{"register", 1, 0}, {"heartbeat", 1, 1}} {
		_, err, registers, heartbeats := runRefused(t, site.at, farmSaying(farmExpiredSentence))
		if registers != site.wantRegisters || heartbeats != site.wantBeats {
			t.Fatalf("at %s: a refused token was tried again — %d registrations, %d heartbeats — and a person's laptop would keep presenting a credential that will never work, which is how it becomes the farm's incident", site.at, registers, heartbeats)
		}
		if err == nil {
			t.Fatalf("at %s: the agent was refused and did not exit with an error — whatever started it would read success", site.at)
		}
	}
}

// ── THE CENSUS: every place in nth-agent that turns a farm refusal into words a person reads ──────────────────
//
// Derived from the Go syntax tree, not from prose: comments explain the old guess in words this census forbids, and a
// census that read them would redden on its own history.
//
// THE POPULATION is every file that PRESENTS the token to the farm (a string literal "X-Agent-Token" in code) — any
// such place can be refused — plus every file that handles the refusal status. For each: a `case CallUnauthorized`
// narrates only through narrator.refused; refused's own words and RefusedError's name no cause; a file that presents
// the token either turns a 401 into a RefusedError or is argued below.

var refusalExempt = map[string]string{
	"internal/tunnel/tunnel.go": "The drive tunnel is dialled only after a registration succeeded with the same token, so a refusal there is the heartbeat's to report within one tick; the dial's own sentence quotes the farm's words for a refused upgrade (farm.RefusalReason, held by TestEveryAnswerTheFarmGaveIsReadThroughTheOneDoor) or the transport error, names no cause, and retries.",
}

type refusalSource struct {
	path   string
	file   *ast.File
	fset   *token.FileSet
	source []byte
}

func parseGo(t *testing.T, path string, source []byte) refusalSource {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return refusalSource{path: path, file: file, fset: fset, source: source}
}

// presentsTheToken: a string literal "X-Agent-Token" in code.
func presentsTheToken(s refusalSource) bool {
	found := false
	ast.Inspect(s.file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"X-Agent-Token"` {
			found = true
		}
		return true
	})
	return found
}

// refusalCases returns every `case … CallUnauthorized:` clause.
func refusalCases(s refusalSource) []*ast.CaseClause {
	var cases []*ast.CaseClause
	ast.Inspect(s.file, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range clause.List {
			if name := exprName(e); name == "CallUnauthorized" {
				cases = append(cases, clause)
			}
		}
		return true
	})
	return cases
}

func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// callsNamed reports whether a node calls a function or method with this name.
func callsNamed(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && exprName(call.Fun) == name {
			found = true
		}
		return true
	})
	return found
}

// narratesOwnWords: a say / Fprint* / Fprintf call with a string literal of its own inside the node.
func narratesOwnWords(node ast.Node) []string {
	var said []string
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch exprName(call.Fun) {
		case "say", "Fprintf", "Fprintln", "Fprint", "Printf", "Println":
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					said = append(said, lit.Value)
				}
			}
		}
		return true
	})
	return said
}

// literalsOfFunc returns every string literal inside a function or method of this name.
func literalsOfFunc(s refusalSource, name string) ([]string, bool) {
	var lits []string
	found := false
	for _, decl := range s.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					lits = append(lits, v)
				}
			}
			return true
		})
	}
	return lits, found
}

func TestEveryPlaceARefusalBecomesWordsNamesNoCause(t *testing.T) {
	// INSTRUMENT: the census's own eyes, on a file built here — it must see a refusal case that narrates for itself,
	// a cause word in a refusal's words, and a refusal case that goes through narrator.refused as clean.
	fixture := parseGo(t, "fixture.go", []byte(`package x
func f(narrator *n, status int) {
	switch status {
	case farm.CallUnauthorized:
		narrator.say("the farm no longer accepts this token — it may have been revoked")
	case farm.CallBusy:
		narrator.say("the farm is busy")
	}
	switch status {
	case CallUnauthorized:
		narrator.refused(err)
	}
}
func (n *narrator) refused(err error) { n.say("your token expired") }
`))
	cases := refusalCases(fixture)
	if len(cases) != 2 || len(narratesOwnWords(cases[0])) != 1 || !callsNamed(cases[1], "refused") || len(narratesOwnWords(cases[1])) != 0 {
		t.Fatalf("instrument: the census cannot tell a refusal case that narrates for itself from one that goes through narrator.refused")
	}
	if lits, ok := literalsOfFunc(fixture, "refused"); !ok || causeWords.FindString(strings.Join(lits, " ")) == "" {
		t.Fatalf("instrument: the census does not see a cause word in a refusal's own words")
	}

	// THE WALK.
	root := filepath.Join("..", "..")
	var sources []refusalSource
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" || entry.Name() == "node_modules" {
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
		rel, _ := filepath.Rel(root, path)
		sources = append(sources, parseGo(t, filepath.ToSlash(rel), source))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	var presenters, refusalCaseSites []string
	var problems []string
	sawRefused, sawRefusedError := false, false
	for _, s := range sources {
		if presentsTheToken(s) {
			presenters = append(presenters, s.path)
		}
		for _, clause := range refusalCases(s) {
			site := s.fset.Position(clause.Pos()).String()
			refusalCaseSites = append(refusalCaseSites, site)
			if s.path == "internal/farm/client.go" {
				continue // the producer of the status, not a narrator of it
			}
			if !callsNamed(clause, "refused") {
				problems = append(problems, site+" handles a refused token without narrator.refused")
			}
			if own := narratesOwnWords(clause); len(own) > 0 {
				problems = append(problems, site+" narrates a refusal in words of its own: "+strings.Join(own, " | "))
			}
		}
		if lits, ok := literalsOfFunc(s, "refused"); ok {
			sawRefused = true
			if m := causeWords.FindString(strings.Join(lits, " ")); m != "" {
				problems = append(problems, s.path+": narrator.refused names a cause — "+m)
			}
		}
		if lits, ok := literalsOfFunc(s, "Error"); ok && strings.Contains(string(s.source), "type RefusedError struct") {
			sawRefusedError = true
			if m := causeWords.FindString(strings.Join(lits, " ")); m != "" {
				problems = append(problems, s.path+": RefusedError's words name a cause — "+m)
			}
		}
	}

	// INSTRUMENT GUARD on the real tree: the census must see both points where the farm can refuse the token, both
	// refusal sites in the loop, the one narration, and the carrier — or its green is a green over nothing.
	for _, want := range []string{"internal/farm/client.go", "internal/tunnel/tunnel.go"} {
		if !containsString(presenters, want) {
			t.Fatalf("instrument: the walk did not find %s presenting the token — it is reading the wrong tree", want)
		}
	}
	loopSites := 0
	for _, site := range refusalCaseSites {
		if strings.Contains(site, "loop.go") {
			loopSites++
		}
	}
	if loopSites < 2 || !sawRefused || !sawRefusedError {
		t.Fatalf("instrument: the census found %d refusal site(s) in loop.go (heartbeat and registration make two), narrator.refused=%v, RefusedError=%v", loopSites, sawRefused, sawRefusedError)
	}

	// Every place that presents the token either turns a 401 into the farm's words or is argued in the table.
	for _, path := range presenters {
		if path == "internal/farm/client.go" {
			if !strings.Contains(string(mustRead(t, filepath.Join(root, path))), "&RefusedError{") {
				problems = append(problems, path+" presents the token and no longer turns a 401 into a RefusedError — the farm's reason would be thrown away again")
			}
			continue
		}
		reason, ok := refusalExempt[path]
		if !ok {
			problems = append(problems, path+" presents the token to the farm and turns neither a refusal into the farm's words nor an argument here")
			continue
		}
		if len(strings.Fields(reason)) < 12 {
			problems = append(problems, path+" is exempted in fewer than twelve words — an exemption with no argument declares nothing")
		}
	}
	for path := range refusalExempt {
		if !containsString(presenters, path) {
			problems = append(problems, path+" is exempted but no longer presents the token — the row is stale")
		}
	}

	if len(problems) > 0 {
		t.Fatalf("a refusal would reach the person in words that guess, or without the farm's own:\n  %s\nThe farm knows why it refused; the agent knows what to do next — route the words through narrator.refused, which names no cause.", strings.Join(problems, "\n  "))
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ── AND EVERY OTHER ANSWER (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO) ─────────────────────────────────────────────────
//
// The same population — every file that presents the token — now reads the farm's sentence on EVERY answer it did not
// take a call with, not only a 401: post() through readRefusal into an *AnswerError, the tunnel's upgrade through
// farm.RefusalReason. And a 429 is the farm answering: no `case … CallTooMany` may say "cannot reach the farm" or tell the
// tunnel's state the farm did not answer, and the one sentence that says the farm cannot be reached is reached only for a
// call that got no answer.

// tooManyCases returns every `case … CallTooMany:` clause.
func tooManyCases(s refusalSource) []*ast.CaseClause {
	var cases []*ast.CaseClause
	ast.Inspect(s.file, func(n ast.Node) bool {
		if clause, ok := n.(*ast.CaseClause); ok {
			for _, e := range clause.List {
				if exprName(e) == "CallTooMany" {
					cases = append(cases, clause)
				}
			}
		}
		return true
	})
	return cases
}

// callersOf names every function or method in s that calls name.
func callersOf(s refusalSource, name string) []string {
	var callers []string
	for _, decl := range s.file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && callsNamed(fn.Body, name) {
			callers = append(callers, fn.Name.Name)
		}
	}
	return callers
}

// tooManyProblems is what the census says of one file's 429 cases.
func tooManyProblems(s refusalSource) []string {
	var problems []string
	for _, clause := range tooManyCases(s) {
		site := s.fset.Position(clause.Pos()).String()
		for _, wrong := range []string{"transportDown", "FarmDidNotAnswer"} {
			if callsNamed(clause, wrong) {
				problems = append(problems, site+" answers a 429 with "+wrong+" — the farm answered")
			}
		}
		if !callsNamed(clause, "notTaken") && !callsNamed(clause, "FarmTurnedAway") {
			problems = append(problems, site+" handles a 429 without the farm's words (notTaken) or the tunnel's answer (FarmTurnedAway)")
		}
	}
	return problems
}

func TestEveryAnswerTheFarmGaveIsReadThroughTheOneDoor(t *testing.T) {
	// INSTRUMENT: a 429 said as a lost farm is seen; one said in the farm's words is clean.
	fixture := parseGo(t, "fixture.go", []byte(`package x
func f(narrator *n, status int) {
	switch status {
	case farm.CallTooMany:
		narrator.transportDown(err)
	}
	switch status {
	case CallTooMany:
		narrator.notTaken("heartbeat", err)
	}
}
`))
	got := tooManyProblems(fixture)
	seen := len(got) > 0 && strings.Contains(strings.Join(got, " "), "answers a 429 with transportDown")
	for _, problem := range got {
		seen = seen && strings.HasPrefix(problem, "fixture.go:4:")
	}
	if !seen {
		t.Fatalf("instrument: the census cannot tell a 429 said as a lost farm from one in the farm's words: %v", got)
	}

	root := filepath.Join("..", "..")
	sources := map[string]refusalSource{}
	for _, rel := range []string{"internal/farm/client.go", "internal/tunnel/tunnel.go", "internal/connect/loop.go"} {
		sources[rel] = parseGo(t, rel, mustRead(t, filepath.Join(root, rel)))
	}
	var problems []string
	client := sources["internal/farm/client.go"]
	if !callsNamed(client.file, "readRefusal") || !strings.Contains(string(client.source), "&AnswerError{") ||
		strings.Count(string(client.source), "readRefusal(") < 2 {
		problems = append(problems, "internal/farm/client.go presents the token and no longer reads the farm's sentence on every answer it did not take — a 401 and every other status, through readRefusal")
	}
	if tunnelSource := sources["internal/tunnel/tunnel.go"]; !presentsTheToken(tunnelSource) || !callsNamed(tunnelSource.file, "RefusalReason") {
		problems = append(problems, "internal/tunnel/tunnel.go presents the token and does not read a refused upgrade's words through farm.RefusalReason")
	}
	// AND AN ANSWER FROM THE FARM'S ADDRESS IS NOT THE FARM'S (NTH_AGENT_0_4_4): both doors ask farm.GatewayOr whether a 502,
	// 503 or 504 is in the farm's envelope before it is read as the farm's answer.
	for _, rel := range []string{"internal/farm/client.go", "internal/tunnel/tunnel.go"} {
		if !callsNamed(sources[rel].file, "GatewayOr") {
			problems = append(problems, rel+" reads a 502, 503 or 504 as the farm's answer without asking farm.GatewayOr whether the farm's address answered in its place")
		}
	}
	loop := sources["internal/connect/loop.go"]
	if n := len(tooManyCases(loop)); n < 3 {
		problems = append(problems, "instrument: loop.go has "+strconv.Itoa(n)+" CallTooMany case(s) — heartbeat, registration and the tunnel's state make three")
	}
	problems = append(problems, tooManyProblems(loop)...)
	if callers := callersOf(loop, "transportDown"); len(callers) != 1 || callers[0] != "transportOrAnswer" {
		problems = append(problems, "loop.go: \"cannot reach the farm\" (transportDown) is said from "+strings.Join(callers, ", ")+
			" — only transportOrAnswer may say it, after it has found that no answer came back")
	}
	if len(problems) > 0 {
		t.Fatalf("an answer the farm gave would reach the person without its words, or as a farm that cannot be reached:\n  %s",
			strings.Join(problems, "\n  "))
	}
}
