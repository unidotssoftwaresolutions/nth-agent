package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE PLACEHOLDER TEACHES THIS PACKAGE'S SPELLING (NTH_AGENT_0_4_4 item 3). publish/npm-placeholder holds the npm name
// nth-agent, so that `npx nth-agent` reaches us, and says where the agent is. It said `npx nthcase-agent` with no version,
// which can run a copy already on the machine, however old (0.4.3). It is JavaScript and cannot ask this package, so this
// seal holds its words to npxLatest: every npx command in it is the one place's spelling, and nothing else.
func TestThePlaceholderTeachesTheCommandThisPackageSpells(t *testing.T) {
	dir := filepath.Join("..", "..", "publish", "npm-placeholder")
	npx := regexp.MustCompile("npx\\s+[^\\s`'\")]+")
	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	for _, file := range []string{"index.js", "package.json", "README.md"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if file == "package.json" {
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			text = manifest.Description
		}
		commands := npx.FindAllString(text, -1)
		if len(commands) == 0 {
			t.Errorf("%s names no command to run — it is there to say where the agent is", file)
		}
		for _, c := range commands {
			if c != npxLatest && c != "npx "+Program {
				t.Errorf("%s teaches %q; the agent is run as %q (internal/command)", file, c, npxLatest)
			}
		}
		if !strings.Contains(text, npxLatest) {
			t.Errorf("%s does not say %q", file, npxLatest)
		}
	}
	if manifest.Name != Program || manifest.Version != "0.0.2" {
		t.Fatalf("the placeholder is %s@%s; its words changed, so it is %s@0.0.2", manifest.Name, manifest.Version, Program)
	}
}
