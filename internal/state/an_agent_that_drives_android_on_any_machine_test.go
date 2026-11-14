package state

import "testing"

// ON WINDOWS THE AGENT KEEPS ITS THINGS IN LOCAL APPDATA (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): Windows' config
// folder roams with a profile, and the capture key must never leave the machine. Everywhere else, the config folder.
func TestOnWindowsTheAgentsFolderIsLocalAppData(t *testing.T) {
	config := func() (string, error) { return "roaming", nil }
	local := func() (string, error) { return "local", nil }
	for system, want := range map[string]string{"windows": "local", "darwin": "roaming", "linux": "roaming"} {
		if got, _ := configDirFor(system, config, local); got != want {
			t.Errorf("%s: the agent's folder is under %q, want %q", system, got, want)
		}
	}
}
