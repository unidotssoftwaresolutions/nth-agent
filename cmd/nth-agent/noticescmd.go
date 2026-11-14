package main

import (
	"fmt"
	"io"

	nthagent "github.com/unidotssoftwaresolutions/nth-agent"
)

// runNotices prints THIRD_PARTY_NOTICES.txt, byte for byte, from inside this binary — so a copy of the agent that travels
// alone, away from the package and the release it came in, still carries the notices its licences ask for. The file is
// built from what the release links (the module root's notices_test.go), and the publish refuses a package whose file and
// this command disagree.
func runNotices(stdout io.Writer) int {
	fmt.Fprint(stdout, nthagent.ThirdPartyNotices)
	return exitOK
}
