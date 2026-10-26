package capture_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/capture"
)

// A_MACHINE_THAT_STAYS_THE_CUSTOMERS — capture's adb commands set a phone's proxy and read it back, so they go only
// to an adb server this account runs. A stub adb on PATH and a listener this test holds stand in for both: no real adb
// runs and no real adb server is contacted.
func TestCaptureNeverSendsAPhoneCommandThroughAnotherAccountsAdbServer(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte("#!/bin/sh\ntouch '"+mark+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ADB_SERVER_SOCKET", "")
	t.Setenv("ANDROID_ADB_SERVER_ADDRESS", "")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))

	anotherAccount := func(net.Conn) (int, bool) { return 1002, true }
	_, _, exit, err := capture.NewRealAdb(anotherAccount)(context.Background(), "-s", "R5CT64M346B", "shell", "settings", "get", "global", "http_proxy")
	if err == nil || exit != -1 {
		t.Fatalf("a phone command through another account's adb server must not run: exit %d err %v", exit, err)
	}
	if _, statErr := os.Stat(mark); statErr == nil {
		t.Fatal("adb ran against a server another account runs")
	}
	if !strings.Contains(err.Error(), "runs as another account on this machine (uid 1002), so the agent will not use it as adb's server") {
		t.Fatalf("the refusal says why: %v", err)
	}

	if _, _, exit, err := capture.NewRealAdb(nil)(context.Background(), "devices"); err != nil || exit != 0 {
		t.Fatalf("this account's own server, asked of the real kernel, is used: exit %d err %v", exit, err)
	}
	if _, statErr := os.Stat(mark); statErr != nil {
		t.Fatal("adb did not run against this account's own server")
	}
}
