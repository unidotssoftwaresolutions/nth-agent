//go:build !darwin && !linux && !windows

package netcap

import "net"

// peerUID has no kernel to ask here, and capture does not run here (capture.Supported): no connection is anyone's.
func peerUID(_, _ *net.TCPAddr) (int, bool) { return 0, false }

// listenerUID has no kernel to ask here either.
func listenerUID(_ *net.TCPAddr) (int, bool) { return 0, false }

// listenAddresses has no kernel table to read here.
func listenAddresses(_ int) ([]net.IP, bool) { return nil, false }
