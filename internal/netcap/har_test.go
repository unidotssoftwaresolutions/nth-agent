package netcap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// THE FIELDS THE RUNNER READS, WITH mitmproxy's MEANINGS — and nothing raw beside them.
func TestTheEntryCarriesWhatTheRunnerReadsAndNoSecondCopyOfAnything(t *testing.T) {
	flow := Flow{
		Started: time.Date(2026, 10, 4, 12, 0, 0, 123456000, time.UTC), Method: "POST",
		URL:      "https://api.example/v1/login?access_token=QSHIDDEN&page=2",
		ReqProto: "HTTP/1.1",
		ReqHeaders: []Header{{"Host", "api.example"}, {"Content-Type", "application/x-www-form-urlencoded"},
			{"Cookie", "sid=COOKIEHIDDEN"}, {"Authorization", "Bearer AUTHHIDDEN"}},
		ReqBody: []byte("user=keepUSER&password=PWHIDDEN"), ReqBodySize: 31,
		Status: 302, Reason: "Found", RespProto: "HTTP/2.0",
		RespHeaders: []Header{{"Location", "https://app.example/cb?code=keepCODE&id_token=IDHIDDEN"},
			{"Set-Cookie", "session=SETHIDDEN; Path=/"}, {"Content-Type", "application/json"}},
		RespBody: []byte(`{"ok":true,"refreshToken":"RTHIDDEN"}`), RespBodySize: 37,
		SendMs: 1.5, WaitMs: 20, ReceiveMs: 2.25,
	}
	entry := entryFor(flow)
	encoded, err := encodeEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"QSHIDDEN", "COOKIEHIDDEN", "AUTHHIDDEN", "PWHIDDEN", "IDHIDDEN", "SETHIDDEN", "RTHIDDEN"} {
		if strings.Contains(string(encoded), gone) {
			t.Fatalf("%s reached the entry: %s", gone, encoded)
		}
	}
	for _, kept := range []string{"keepUSER", "keepCODE", "page=2", "api.example/v1/login"} {
		if !strings.Contains(string(encoded), kept) {
			t.Fatalf("%s must stay: %s", kept, encoded)
		}
	}
	var read map[string]any
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read["startedDateTime"] != "2026-10-04T12:00:00.123456+00:00" || read["time"].(float64) != 23.75 {
		t.Fatalf("startedDateTime is Python's isoformat in UTC and time the sum of the timings: %v %v", read["startedDateTime"], read["time"])
	}
	request := read["request"].(map[string]any)
	response := read["response"].(map[string]any)
	if len(request["cookies"].([]any)) != 0 || len(request["queryString"].([]any)) != 0 || len(response["cookies"].([]any)) != 0 ||
		len(request["postData"].(map[string]any)["params"].([]any)) != 0 {
		t.Fatal("cookies, queryString and params would be a second copy of what is already masked once; they stay empty")
	}
	if response["status"].(float64) != 302 || response["httpVersion"] != "HTTP/2.0" ||
		!strings.Contains(response["redirectURL"].(string), "keepCODE") || strings.Contains(response["redirectURL"].(string), "IDHIDDEN") {
		t.Fatalf("status, version and the masked redirect: %v", response)
	}
	content := response["content"].(map[string]any)
	if content["size"].(float64) != 37 || content["mimeType"] != "application/json" || content["encoding"] != nil {
		t.Fatalf("content.size is the size on the wire and a text body is never base64: %v", content)
	}
}

func TestOnlyPostPutAndPatchCarryPostData(t *testing.T) {
	for method, carries := range map[string]bool{"POST": true, "PUT": true, "PATCH": true, "GET": false, "DELETE": false} {
		entry := entryFor(Flow{Method: method, URL: "https://h/", ReqBody: []byte("a=1"), ReqBodySize: 3, Status: 200})
		if (entry.Request.PostData != nil) != carries {
			t.Fatalf("%s: postData=%v, mitmproxy writes it for POST, PUT and PATCH only", method, entry.Request.PostData != nil)
		}
	}
}

func TestARefusingHostIsRecordedAsAConnectThatNeverAnswered(t *testing.T) {
	entry := refusalEntry(time.Now(), "pinned.example:443")
	if entry.Request.Method != "CONNECT" || entry.Request.URL != "https://pinned.example:443/" ||
		entry.Response.Status != 0 || entry.Response.Error != NotReadableRefused {
		t.Fatalf("mitmproxy's CONNECT shape, no response, and why: %+v", entry)
	}
	// AN_AGENT_CAPTURE_READY_TO_SHIP, rule 5: a host is not the app — the words name the host, and why that usually happens.
	if NotReadableRefused != "not readable: this host did not accept the capture certificate (the app pins it, or its library trusts only its own certificates)" {
		t.Fatal("the refusal's words are the slice's, verbatim")
	}
}

func TestAnExchangeWithNoResponseSaysWhyInTheAgentsWords(t *testing.T) {
	entry := entryFor(Flow{Method: "GET", URL: "https://h/?token=T", Err: "the host did not answer in time, so the capture proxy gave up on it"})
	if entry.Response.Status != 0 || entry.Response.BodySize != -1 || !strings.Contains(entry.Response.Error, "did not answer in time") {
		t.Fatalf("savehar's no-response shape with this agent's sentence: %+v", entry.Response)
	}
}

func TestAWebSocketUpgradeIsRecordedAndItsMessagesAreNot(t *testing.T) {
	entry := entryFor(Flow{Method: "GET", URL: "https://h/socket", Status: 101, WebSocket: true,
		RespHeaders: []Header{{"Upgrade", "websocket"}}})
	encoded, _ := encodeEntry(entry)
	if entry.ResourceType != "websocket" || !strings.Contains(string(encoded), `"_webSocketMessages":[]`) {
		t.Fatalf("the upgrade is recorded as a websocket with no messages: %s", encoded)
	}
}
