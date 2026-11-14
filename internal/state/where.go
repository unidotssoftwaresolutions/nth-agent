package state

import "path/filepath"

// Folder is where this account's agent keeps its own files on this machine: <ConfigDir>/nth-agent — the node id, the
// connect lock, the capture folder and the builds it fetched (WHAT_THE_FIRST_TRY_SHOWED). Doctor names it.
func Folder(configDir string) string { return filepath.Join(configDir, "nth-agent") }

// NodeIDFile is the file this machine's node id is kept in, inside Folder. A refused identity names it.
func NodeIDFile(configDir string) string { return filepath.Join(Folder(configDir), "node-id") }
