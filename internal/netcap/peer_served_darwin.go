//go:build darwin

package netcap

// rootMakesOnlyItsOwn is false on a Mac: launchd (root) makes the sockets other accounts' LaunchAgents listen on.
const rootMakesOnlyItsOwn = false
