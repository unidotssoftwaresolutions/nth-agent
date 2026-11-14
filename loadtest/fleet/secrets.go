package fleet

import (
	"errors"
	"fmt"
	"os"
)

// TokenVariable is the one place the fleet's workspace agent token comes from. There is no flag for it: a flag is visible
// to every account on the machine in the process list, and a load-test token is a real workspace's token.
const TokenVariable = "NTH_AGENT_LOADTEST_TOKEN"

// FarmVariable names the farm the fleet connects to.
const FarmVariable = "NTH_AGENT_LOADTEST_FARM"

// Credential holds the workspace agent token. It cannot be printed: every way fmt reads a value answers with the token's
// length, never its text, so a stray %v in a log line says how long it is and nothing more.
type Credential struct{ value string }

// CredentialFromEnvironment reads the token from TokenVariable, and nowhere else.
func CredentialFromEnvironment(getenv func(string) string) Credential {
	if getenv == nil {
		getenv = os.Getenv
	}
	token := getenv(TokenVariable)
	return Credential{value: token}
}

// secret is the token's text, for the two places it leaves the process: the X-Agent-Token header of the farm client and of
// the tunnel's upgrade (the agent's own code, which never writes it anywhere else).
func (c Credential) secret() string { return c.value }

// Length is how many characters the token has: the most any diagnostic says about it.
func (c Credential) Length() int { return len(c.value) }

// Check refuses a missing token, naming the variable and not the value.
func (c Credential) Check() error {
	if c.value == "" {
		return errors.New("the workspace agent token is read from " + TokenVariable + " only, and it is not set")
	}
	return nil
}

func (c Credential) String() string   { return fmt.Sprintf("«token: %d characters»", len(c.value)) }
func (c Credential) GoString() string { return c.String() }

// Format answers every verb with String, so %s, %v, %+v, %#v, %q and %x all say the length only.
func (c Credential) Format(f fmt.State, _ rune) { fmt.Fprint(f, c.String()) }
