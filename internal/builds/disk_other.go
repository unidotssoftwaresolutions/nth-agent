//go:build !darwin && !linux

package builds

import "errors"

// freeBytesAt cannot read the free disk on a system this agent is not released for, so every fetch there is refused.
func freeBytesAt(string) (uint64, error) {
	return 0, errors.New("free disk is not read on this system")
}
