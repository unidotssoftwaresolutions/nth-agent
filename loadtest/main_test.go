package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

func TestTheDriverRefusesWithoutTheProductionFlag(t *testing.T) {
	stdout, stderr := &lockedBuffer{}, &lockedBuffer{}
	env := map[string]string{"NTH_AGENT_LOADTEST_API": "https://api.example.com",
		"NTH_AGENT_LOADTEST_API_KEYS": "00000000-0000-0000-0000-000000000001=gp_never_printed_key"}
	code := run([]string{"drive", "--scenario", "00000000-0000-4000-8000-000000000042", "--no-pause"}, stdout, stderr,
		func(name string) string { return env[name] })
	if code != 2 || !strings.Contains(stderr.String(), "--i-know-this-is-production") {
		t.Fatalf("drive without the flag exited %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "gp_never_printed_key") {
		t.Fatal("the driver printed the key")
	}
}
