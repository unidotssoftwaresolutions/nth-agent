package tunnel

import (
	"fmt"
	"strings"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// namedForTerminal is names a body chose, each fit for a terminal, the first ten of them, in one phrase.
func namedForTerminal(names []string) string {
	const shown = 10
	var parts []string
	for i, name := range names {
		if i == shown {
			parts = append(parts, fmt.Sprintf("and %d more", len(names)-shown))
			break
		}
		parts = append(parts, farm.SafeForTerminal(name, ""))
	}
	return strings.Join(parts, ", ")
}
