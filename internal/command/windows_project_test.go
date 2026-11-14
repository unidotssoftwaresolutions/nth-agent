package command

import (
	"runtime"
	"testing"
)

// A PROJECT INSTALL ON WINDOWS IS TOLD ITS OWN COMMAND (AN_AGENT_THAT_PASSES_ON_WINDOWS). The first windows-latest run
// printed `npx nthcase-agent@latest doctor` to an agent installed in a project: the one rule refused every `\`, so no Windows
// path was ever spelled. The Windows rule is read on every system here (projectLinkOn), so this is red on a Mac at f77a50a.
func TestAProjectOnWindowsIsSpelledAsItsOwnLink(t *testing.T) {
	for project, want := range map[string]string{
		`C:\work\app`: `C:\work\app\node_modules\.bin\nth-agent.cmd`,
		`C:\Users\runneradmin\qa_bench-2.0\app+web@x`: `C:\Users\runneradmin\qa_bench-2.0\app+web@x\node_modules\.bin\nth-agent.cmd`,
		`d:\src\bench\`:      `d:\src\bench\node_modules\.bin\nth-agent.cmd`,
		`C:\`:                `C:\node_modules\.bin\nth-agent.cmd`,
		`\\server\share\app`: `\\server\share\app\node_modules\.bin\nth-agent.cmd`,
	} {
		if got, ok := projectLinkOn("windows", project); !ok || got != want {
			t.Errorf("%s: spelled %q (%v), want %q", project, got, ok, want)
		}
	}
	// Not spelled, so the npx form is used: not absolute on Windows, a `/`, or a mark a shell would need quoted — a space,
	// cmd.exe's own separators `,` `=` `;`, its `%` `^` `&` `|` `<` `>`, PowerShell's `$` `` ` `` `'` — or a letter a
	// terminal might not show.
	for _, project := range []string{`C:work\app`, `/work/app`, `C:/work/app`, `work\app`, `\\`, `\\\x`, `\\?\C:\work`, "",
		`C:\Users\Ada Lovelace\app`, `C:\a,b`, `C:\a=b`, `C:\a;b`, `C:\100%`, `C:\a^b`, `C:\a&b`, `C:\a|b`, `C:\a<b`, `C:\$x`,
		"C:\\a`b", `C:\it's`, "C:\\caf\u00e9", "C:\\\xff", "C:\\a\x1b[2Jb"} {
		if got, ok := projectLinkOn("windows", project); ok {
			t.Errorf("%q: must not be spelled on Windows, got %q", project, got)
		}
	}
}

// macOS and Linux keep their rule, read the same on every system.
func TestAProjectOnUnixIsSpelledAsBefore(t *testing.T) {
	for _, system := range []string{"darwin", "linux"} {
		if got, ok := projectLinkOn(system, "/home/ada/bench"); !ok || got != "/home/ada/bench/node_modules/.bin/nth-agent" {
			t.Errorf("%s: spelled %q (%v)", system, got, ok)
		}
		for _, project := range []string{`C:\work\app`, "relative/bench", "/Users/ada/my bench", `/tmp/a\b`} {
			if got, ok := projectLinkOn(system, project); ok {
				t.Errorf("%s, %q: must not be spelled, got %q", system, project, got)
			}
		}
	}
}

// …and on Windows itself, the project is read from where npm put the program, so every line above is reached. Runs in CI's
// windows-latest job: here filepath splits at / alone, and a C:\ path is one name.
func TestOnWindowsAProjectInstallIsReadFromWhereItsProgramIs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a Windows path is split as Windows splits it only on Windows; projectLinkOn's rule is tested above on every system")
	}
	files := map[string]bool{`C:\work\app\package.json`: true}
	isFile := func(p string) bool { return files[p] }
	none := func(string) string { return "" }
	project := HowStarted(`C:\work\app\node_modules\nthcase-agent\bin\nth-agent-windows-amd64.exe`, none, isFile)
	if project != (Install{Started: StartedFromProject, Project: `C:\work\app`}) {
		t.Fatalf("a project install reads as %+v", project)
	}
	if got := project.Line("doctor"); got != `C:\work\app\node_modules\.bin\nth-agent.cmd doctor` {
		t.Fatalf("a project install on Windows is told %q", got)
	}
	npx := HowStarted(`C:\Users\ada\AppData\Local\npm-cache\_npx\0a1b2c3d\node_modules\nthcase-agent\bin\nth-agent-windows-amd64.exe`, none, isFile)
	if npx.Started != StartedByNpx {
		t.Fatalf("npx's cache reads as %+v", npx)
	}
}
