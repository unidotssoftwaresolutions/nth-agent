//go:build !darwin

package netcap

// rootMakesOnlyItsOwn is true here: a socket root (or LocalSystem) made serves another account only by an administrator's
// own doing.
const rootMakesOnlyItsOwn = true
