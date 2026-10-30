package farmtest

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// SourcesFile is where a checkout that has the server's source beside this repository says where each file a seal reads
// is: a JSON object of names to paths, relative to the file's own directory, kept beside this repository and never in it.
// A published tree names no file of a repository its reader cannot open.
const SourcesFile = "nth-agent.server-sources.json"

// The names a seal asks for.
const (
	FarmNodeRefusals    = "farm.nodeRefusals"    // the farm's refusals to a node, with their sentences
	FarmRegistryOptions = "farm.registryOptions" // the farm's per-workspace limits
	FarmTunnelLimits    = "farm.tunnelLimits"    // the farm's tunnel limits
	FarmNodeOwnership   = "farm.nodeOwnership"   // the farm's 404 for a node the caller does not own
	FarmLeaseQueries    = "farm.leaseQueries"    // the files that hold the farm's lease SQL (a list)
	RunnerNameRule      = "runner.nameRule"      // the runner's sensitive-name rule, which the agent's masker ports
	RunnerMaskVectors   = "runner.maskVectors"   // the masking vectors both maskers are held to
	FarmLiveHarAddon    = "farm.liveHarAddon"    // the farm node's capture add-on, which writes the left-out sentence
	RunnerHarNodeCut    = "runner.harNodeCut"    // the runner's reading of that sentence
	RunnerReadingBounds = "runner.readingBounds" // the runner's bounds on what a run receives
	RunnerBoundedText   = "runner.boundedText"   // the runner's cut marker
)

// ServerSource is the path of the server's file called name, or the test is skipped, loudly, when this checkout has no
// SourcesFile beside it (nth-agent's own CI, and a published copy, check out nth-agent alone). Once the file is there,
// a name it does not give fails: a seal never passes having read nothing.
func ServerSource(t testing.TB, name string) string {
	t.Helper()
	paths := ServerSources(t, name)
	if len(paths) != 1 {
		t.Fatalf("%s names %d files for %s, and this seal reads one", SourcesFile, len(paths), name)
	}
	return paths[0]
}

// ServerSources is ServerSource for a name that may give a list of files.
func ServerSources(t testing.TB, name string) []string {
	t.Helper()
	manifest := filepath.Join(moduleRoot(t), "..", SourcesFile)
	data, err := os.ReadFile(manifest)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("THE SERVER'S SOURCE IS NOT BESIDE THIS REPOSITORY (no ../" + SourcesFile + " says where it is), so " + name +
			" cannot be read: THIS RUN DOES NOT HOLD THE AGENT TO THE SERVER'S SIDE OF IT. Run it in a checkout that has both.")
	}
	if err != nil {
		t.Fatalf("%s is there and cannot be read: %v", SourcesFile, err)
	}
	var named map[string]json.RawMessage
	if err := json.Unmarshal(data, &named); err != nil {
		t.Fatalf("%s is not a JSON object of names to paths: %v", SourcesFile, err)
	}
	raw, ok := named[name]
	if !ok {
		t.Fatalf("%s is there and does not say where %s is", SourcesFile, name)
	}
	var paths []string
	var one string
	if json.Unmarshal(raw, &one) == nil {
		paths = []string{one}
	} else if err := json.Unmarshal(raw, &paths); err != nil || len(paths) == 0 {
		t.Fatalf("%s gives %s as neither a path nor a list of paths", SourcesFile, name)
	}
	for i, p := range paths {
		paths[i] = filepath.Join(filepath.Dir(manifest), filepath.FromSlash(p))
	}
	return paths
}

// moduleRoot is the directory holding go.mod, found from the test's own directory.
func moduleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory — this is not the nth-agent tree")
		}
		dir = parent
	}
}
