package capture

import "testing"

// CAPTURE RUNS ON WINDOWS TOO (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): the key and the files are kept to this account
// by an access list, a phone's lock is a locked byte, and the proxy asks Windows which account opened each connection.
func TestCaptureRunsOnTheThreeSystemsTheAgentIsBuiltFor(t *testing.T) {
	for goos, want := range map[string]bool{"darwin": true, "linux": true, "windows": true, "plan9": false, "freebsd": false} {
		if got := Supported(goos); got != want {
			t.Errorf("Supported(%q) = %v, want %v", goos, got, want)
		}
	}
	if got := UnsupportedSystem("plan9"); got[:len("Network capture runs on macOS, Linux and Windows only")] != "Network capture runs on macOS, Linux and Windows only" {
		t.Fatalf("the refusal names the three systems: %q", got)
	}
}
