//go:build windows

package private_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/private"
)

// ON WINDOWS A PRIVATE FILE IS ONE ACCESS-LIST ENTRY, FOR THIS ACCOUNT (AN_AGENT_THAT_DRIVES_ANDROID_ON_ANY_MACHINE): no
// other account's SID is in it — not Users, not Everyone, not Authenticated Users, not the Administrators group — and a
// file made in a private folder inherits it. Runs in CI's windows-latest job.
func TestOnWindowsAPrivateFileListsThisAccountAlone(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	me := user.User.Sid.String()
	others := []string{"S-1-1-0", "S-1-5-11", "S-1-5-32-545", "S-1-5-32-544", "S-1-5-18"}

	dir := filepath.Join(t.TempDir(), "nth-agent", "capture")
	plain := filepath.Join(filepath.Dir(dir), "plain.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(plain)
	if private.Problem(plain, info) == "" {
		t.Fatal("instrument guard: a file made with the temp folder's inherited list is open to other accounts, and must be named")
	}

	if err := private.Folder(dir); err != nil {
		t.Fatal(err)
	}
	inherited := filepath.Join(dir, "made-inside.json")
	if err := os.WriteFile(inherited, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "ca-key.pem")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := private.File(key); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, inherited, key} {
		sids, err := private.Readers(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(sids) == 0 {
			t.Fatalf("%s: an empty or missing list is no proof of anything", path)
		}
		for _, sid := range sids {
			if sid != me {
				t.Fatalf("%s lets in %s; only this account (%s) may be in its list: %v", path, sid, me, sids)
			}
		}
		for _, other := range others {
			for _, sid := range sids {
				if sid == other {
					t.Fatalf("%s lets in %s", path, other)
				}
			}
		}
		info, _ := os.Lstat(path)
		if problem := private.Problem(path, info); problem != "" {
			t.Fatalf("%s is private and was called %q", path, problem)
		}
	}
}

// ONE PROCESS HOLDS A LOCK: a second LockFile on the same file is refused until the first lets go.
func TestOnWindowsALockIsHeldByOneHandleAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connect.lock")
	unlock, err := private.LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := private.LockFile(path); !errors.Is(err, private.ErrLocked) {
		t.Fatalf("a held lock is refused, got %v", err)
	}
	unlock()
	again, err := private.LockFile(path)
	if err != nil {
		t.Fatalf("a released lock can be taken: %v", err)
	}
	again()
}
