package doctor

import (
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/localconn"
)

// appiumAddress is the Appium endpoint check's second question, asked once Appium answered: WHERE it listens (see
// internal/localconn/address.go). On loopback alone, nothing changes. On any other address too, the check stays ready —
// connect still drives this Appium, so it stops no run — and becomes a look, naming the addresses and the command that
// keeps Appium on this computer. Read from the kernel's own table; nothing is sent to any address to find out.
func appiumAddress(env Env, c Check) Check {
	beyond, known := localconn.AlsoListensOn(env.AppiumURL, env.AppiumListeners)
	if !known {
		c.Limits = "where Appium listens was not read here (this system's table of listening sockets could not be read), so " +
			"whether another computer can reach it is not established"
		return c
	}
	if len(beyond) == 0 {
		return c
	}
	c.Advisory = true
	c.Detail += "; it also listens on " + strings.Join(beyond, ", ") + ", where another computer on your network can reach it"
	c.Remedy = localconn.AppiumBeyondLoopbackDo
	return c
}
