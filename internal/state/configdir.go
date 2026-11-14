package state

import (
	"os"
	"runtime"
)

// ConfigDir is the folder this agent keeps its own things under, for this account: the node id, the connect lock, the
// capture folder, the builds it fetched (each in <ConfigDir>/nth-agent).
//
// It is os.UserConfigDir — ~/Library/Application Support on macOS, $XDG_CONFIG_HOME or ~/.config on Linux — except on
// Windows, where it is os.UserCacheDir, %LocalAppData%. Windows' config folder, %AppData%, ROAMS: a profile on a domain is
// copied to a server when its person signs out, and the capture CA's key and the capture files must never leave this
// machine. Local AppData stays on it.
func ConfigDir() (string, error) {
	return configDirFor(runtime.GOOS, os.UserConfigDir, os.UserCacheDir)
}

func configDirFor(system string, config, local func() (string, error)) (string, error) {
	if system == "windows" {
		return local()
	}
	return config()
}
