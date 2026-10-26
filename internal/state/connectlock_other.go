//go:build !darwin && !linux

package state

import "errors"

// holdFile cannot hold a lock on a system this agent does not ship for, and connect does not start without one: two
// connects on one folder are what the lock is for.
func holdFile(string) (func(), error) {
	return nil, errors.New("this system cannot hold the state folder's lock, so connect does not start here")
}
