package main

import (
	"strings"
	"testing"
)

// TEXT FROM OUTSIDE (WHAT_A_LEASE_CAN_ASK). `upload` printed BrowserStack's answer as it came — the build's id, its custom id
// and the account's own error words — and put the id unquoted into an `export` line a person pastes into a shell. What the
// hub says is made fit for a terminal, and the export line quotes the id.
func TestUploadPrintsNoEscapeTheHubSuppliedAndQuotesTheExportLine(t *testing.T) {
	stub := &uploadServer{reply: `{"app_url":"bs://9f8e\u001b]0;owned\u0007;touch /tmp/x","custom_id":"\u001b[2Jlantern"}`}
	stub.point(t)
	code, stdout, stderr := upload(t, "--file", apkAt(t, "app-release.apk", 64))
	if code != exitOK {
		t.Fatalf("want exit 0, got %d (stderr=%q)", code, stderr)
	}
	for _, bad := range []string{"\x1b", "\x07"} {
		if strings.Contains(stdout, bad) {
			t.Fatalf("upload printed %q from the hub's answer:\n%q", bad, stdout)
		}
	}
	if !strings.Contains(stdout, "export NTH_AGENT_BROWSERSTACK_APP='bs://9f8e") || !strings.Contains(stdout, ";touch /tmp/x'\n") {
		t.Fatalf("the export line must quote the id, so a pasted line runs nothing after it:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--browserstack-app 'bs://9f8e") {
		t.Fatalf("the connect line quotes an id that holds more than a real one does:\n%s", stdout)
	}

	stub = &uploadServer{reply: `{"error":"quota \u001b[2Jexceeded\u0007"}`}
	stub.point(t)
	code, _, stderr = upload(t, "--file", apkAt(t, "app-release.apk", 64))
	if code == exitOK {
		t.Fatal("an answer with no build id must fail")
	}
	if strings.ContainsAny(stderr, "\x1b\x07") || !strings.Contains(stderr, "quota") {
		t.Fatalf("the hub's own words are said, without its escape codes:\n%q", stderr)
	}
}

// A real id still reads as it did: quoted only where a shell needs it, and the connect line unchanged.
func TestARealIdStillReadsAsItDid(t *testing.T) {
	stub := &uploadServer{}
	stub.point(t)
	_, stdout, _ := upload(t, "--file", apkAt(t, "app-release.apk", 64))
	if !strings.Contains(stdout, "--browserstack-app bs://9f8e7d6c5b4a\n") || !strings.Contains(stdout, "export NTH_AGENT_BROWSERSTACK_APP='bs://9f8e7d6c5b4a'\n") {
		t.Fatalf("a real id: the connect line as before, the export line quoted:\n%s", stdout)
	}
}
