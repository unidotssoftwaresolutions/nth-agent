//go:build windows

package capture

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// exposeToOthers makes path readable by other users of this machine: an access-list entry that lets the Users group read
// it, beside this account's own (the mode, a Unix idea, is not used here).
func exposeToOthers(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	users, err := windows.StringToSid("S-1-5-32-545")
	must(t, err)
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(users)},
	}}, nil)
	must(t, err)
	must(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil))
}
