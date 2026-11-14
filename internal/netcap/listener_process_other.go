//go:build !windows

package netcap

// ListenerProcess is not asked here: on macOS and Linux `lsof` names what holds a port, and the sentence says so.
func ListenerProcess(int) (pid int, program string, found bool) { return 0, "", false }
