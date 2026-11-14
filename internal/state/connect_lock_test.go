package state

import (
	"errors"
	"strings"
	"testing"
)

func TestASecondConnectOnTheSameFolderIsRefused(t *testing.T) {
	dir := t.TempDir()
	release, err := HoldConnect(dir)
	if err != nil {
		t.Fatalf("the first connect holds the folder: %v", err)
	}
	if _, err := HoldConnect(dir); !errors.Is(err, ErrAnotherConnect) {
		t.Fatalf("a second connect on the same folder must be refused, got %v", err)
	}
	release()
	again, err := HoldConnect(dir)
	if err != nil {
		t.Fatalf("once the first has stopped, the folder is free: %v", err)
	}
	again()
	if !strings.Contains(ErrAnotherConnect.Error(), "stop it first") {
		t.Fatalf("the refusal says what to do: %q", ErrAnotherConnect)
	}
}
