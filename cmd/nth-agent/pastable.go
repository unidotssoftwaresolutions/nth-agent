package main

import (
	"regexp"
	"strings"
)

// WHAT `upload` PRINTS FOR A PERSON TO PASTE (WHAT_A_LEASE_CAN_ASK). The build's id is BrowserStack's text, and `upload`
// prints it inside two lines a person pastes into a shell: the connect line and the export line. Everything `upload`
// prints goes through the agent's terminal filter (main.go hands runUpload filtered writers); and so that a pasted line
// runs nothing after the id, the export line always quotes it, and the connect line quotes one that holds anything a
// real id does not.

// aPlainID is an id that reads the same to every shell: bs:// or a custom id's own letters, digits, : / . _ and -.
var aPlainID = regexp.MustCompile(`^[A-Za-z0-9:/._-]+$`)

// pastable is id as it stands in the connect line: as it is when it is plain, quoted when it is not.
func pastable(id string) string {
	if aPlainID.MatchString(id) {
		return id
	}
	return shellQuoted(id)
}

// shellQuoted is s in single quotes, as a POSIX shell reads it: every character as it is, a ' written as '\”.
func shellQuoted(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
