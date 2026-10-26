//go:build !darwin && !linux

package netcap

import "net"

// peerUID has no kernel to ask here, and capture does not run here (capture.Supported): no connection is anyone's.
func peerUID(_, _ *net.TCPAddr) (int, bool) { return 0, false }

// listenerUID has no kernel to ask here either.
func listenerUID(_ *net.TCPAddr) (int, bool) { return 0, false }
