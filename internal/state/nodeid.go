// Package state persists the ONE fact the agent keeps between runs: its node id.
package state

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreateNodeID returns the stable node identity, minting one on first run.
//
// WHY IT MUST BE STABLE: the farm's ownership is write-once and a node id it has never seen is a NEW
// node row — an agent that invents a fresh id per start leaves one unreclaimable row per restart,
// removable only by an operator purge.
//
// WHAT IS PERSISTED: the id, and NOTHING else — no token, no inventory, no cache. Losing this file
// costs exactly one orphan row on the next start (the old row ages out via the offline reaper and an
// operator purge); it loses no data, because the truth lives in the cloud, not on this machine. The
// agent stays disposable.
func LoadOrCreateNodeID(configDir string) (string, error) {
	dir := filepath.Join(configDir, "nth-agent")
	path := filepath.Join(dir, "node-id")

	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if looksLikeUUID(id) {
			return id, nil
		}
		// A corrupt file is treated like a missing one: one orphan row, never a crash loop.
	}

	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if beforeMinting != nil {
		beforeMinting()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the config directory: %w", err)
	}
	return mintOnce(dir, path, id)
}

// mintOnce writes id as the node identity ONLY IF THERE IS NONE, and returns whichever identity the folder holds then
// (A_MACHINE_THAT_STAYS_THE_CUSTOMERS). The id is written whole to a file of its own and then LINKED to node-id: a
// link never replaces a file, so two first starts racing on one folder cannot both win, the loser adopts the winner's id,
// and nobody ever reads a half-written one. A file there that is not an id (corrupt) is the one case replaced — renamed
// over, whole — as before: one orphan row, never a crash loop.
func mintOnce(dir, path, id string) (string, error) {
	temp, err := os.CreateTemp(dir, ".node-id-*")
	if err != nil {
		return "", fmt.Errorf("persisting the node id: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	_, writeErr := temp.WriteString(id + "\n")
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil {
		return "", fmt.Errorf("persisting the node id: %w", errors.Join(writeErr, closeErr))
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return "", fmt.Errorf("persisting the node id: %w", err)
	}
	linkErr := os.Link(tempPath, path)
	if linkErr == nil {
		return id, nil
	}
	if !errors.Is(linkErr, fs.ErrExist) {
		return "", fmt.Errorf("persisting the node id: %w", linkErr)
	}
	if data, err := os.ReadFile(path); err == nil {
		if existing := strings.TrimSpace(string(data)); looksLikeUUID(existing) {
			return existing, nil // another start minted first: its identity is this folder's
		}
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", fmt.Errorf("persisting the node id: %w", err)
	}
	return id, nil
}

// beforeMinting runs once a process has decided to mint and before it writes — A TEST SEAM, unexported: the race seal's
// barrier holds every racer here.
var beforeMinting func()

// newUUID is a v4 UUID from the CSPRNG — the farm's node ids are Guids.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}
