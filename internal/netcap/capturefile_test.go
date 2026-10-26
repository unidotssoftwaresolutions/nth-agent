package netcap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const lease1 = "0f0e0d0c-0b0a-4908-8706-050403020100"
const lease2 = "1f1e1d1c-1b1a-4918-9716-151413121110"

func readHAR(t *testing.T, data []byte) (entries []map[string]any, comment string) {
	t.Helper()
	var doc struct {
		Log struct {
			Creator map[string]string `json:"creator"`
			Entries []map[string]any  `json:"entries"`
			Comment string            `json:"comment"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the capture file must be a whole HAR at every moment: %v\n%s", err, data)
	}
	if doc.Log.Creator["name"] != "nth-agent" {
		t.Fatalf("the HAR says who wrote it: %v", doc.Log.Creator)
	}
	return doc.Log.Entries, doc.Log.Comment
}

func TestTheCaptureFileIsOwnerOnlyNamedByItsLeaseAndWholeAfterEveryEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	rec, err := NewRecording(dir, lease1, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	if filepath.Base(rec.Path()) != lease1+".har" {
		t.Fatalf("the file is named by its lease, got %s", rec.Path())
	}
	info, _ := os.Stat(rec.Path())
	folder, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o600 || folder.Mode().Perm() != 0o700 {
		t.Fatalf("the file is 0600 in a 0700 folder, got %04o in %04o", info.Mode().Perm(), folder.Mode().Perm())
	}
	for i := 0; i < 3; i++ {
		data, err := rec.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if entries, _ := readHAR(t, data); len(entries) != i {
			t.Fatalf("after %d entries the document holds %d", i, len(entries))
		}
		rec.Record(Flow{Method: "GET", URL: "https://host.example/" + strings.Repeat("p", i), Status: 204})
	}
	if _, err := NewRecording(dir, lease1, "dev", nil); err == nil {
		t.Fatal("a lease captures once on a machine: a second file for it is refused, never overwritten")
	}
	if _, err := NewRecording(dir, "../escape", "dev", nil); err == nil {
		t.Fatal("a name that is not a lease names no file")
	}
}

// 500 ENTRIES — the runner's own per-run bound — and the document says where it stopped.
func TestACaptureRecordsAtMostFiveHundredExchangesAndSaysSo(t *testing.T) {
	rec, err := NewRecording(filepath.Join(t.TempDir(), "files"), lease1, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	for i := 0; i < MaxEntries+20; i++ {
		rec.Record(Flow{Method: "GET", URL: "https://host.example/item", Status: 200})
	}
	data, _ := rec.Snapshot()
	entries, comment := readHAR(t, data)
	if len(entries) != MaxEntries || rec.Recorded() != MaxEntries {
		t.Fatalf("a capture records %d exchanges, got %d", MaxEntries, len(entries))
	}
	if !strings.Contains(comment, "reached its bound of 500 requests") {
		t.Fatalf("the document says where it stopped, got %q", comment)
	}
}

// PAST THE BYTE BOUND an entry keeps its method, address, status and sizes, and its body is left out with the reason.
func TestPastItsByteBoundAnEntryKeepsItsHeadAndLeavesItsBodyOut(t *testing.T) {
	rec, err := NewRecording(filepath.Join(t.TempDir(), "files"), lease1, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	// The file filled to within a body of its bound by entries already written (padding: the bound is the writer's rule,
	// whatever the entries say).
	for _, size := range []int{1 << 20, 16 << 10} {
		pad := []byte(`{"pad":"` + strings.Repeat("p", size) + `"}`)
		for MaxFileBytes-rec.file.size > int64(len(pad))+100<<10 {
			if ok, err := rec.file.add(pad, func() []byte { return nil }, time.Now()); !ok || err != nil {
				t.Fatalf("padding: %v %v", ok, err)
			}
		}
	}
	body := strings.Repeat("y", 250*1024)
	rec.Record(Flow{Method: "GET", URL: "https://host.example/big", Status: 200,
		RespHeaders: []Header{{"Content-Type", "text/plain"}}, RespBody: []byte(body), RespBodySize: int64(len(body))})
	data, _ := rec.Snapshot()
	if len(data) > MaxFileBytes {
		t.Fatalf("the file holds at most %d bytes, holds %d", MaxFileBytes, len(data))
	}
	entries, _ := readHAR(t, data)
	last := entries[len(entries)-1]
	content := last["response"].(map[string]any)["content"].(map[string]any)
	if last["request"].(map[string]any)["url"] != "https://host.example/big" || content["text"] != "" ||
		content["size"].(float64) != float64(len(body)) || !strings.Contains(content["comment"].(string), "reached the 16 MB") {
		t.Fatalf("the exchange is recorded with its address and sizes, and its body left out with the reason: comment=%v text=%s",
			content["comment"], quoted(fmt.Sprint(content["text"])))
	}
}

// THE SWEEP DELETES ONLY WHAT IT WRITES, and only once its hour is up — counted from the capture's end when this process
// knows it, and from the file's own last write after a restart.
func TestTheSweepDeletesOnlyItsOwnFilesAndOnlyAfterTheirHour(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	write := func(name string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, now.Add(-age), now.Add(-age))
		return path
	}
	old := write(lease1+".har", 2*time.Hour)
	recent := write(lease2+".har", 10*time.Minute)
	stranger := write("notes.har", 5*time.Hour)
	unlisted := write(lease1+".har.bak", 5*time.Hour)
	if err := os.Mkdir(filepath.Join(dir, "3f3e3d3c-3b3a-4938-b736-353433323130.har"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := write("target.txt", 5*time.Hour)
	link := filepath.Join(dir, "4f4e4d4c-4b4a-4948-8746-454443424140.har")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(target, now.Add(-5*time.Hour), now.Add(-5*time.Hour))

	if deleted := Sweep(dir, now, time.Hour, nil); deleted != 1 {
		t.Fatalf("after a restart (nothing known) only the old file of its own shape goes, %d went", deleted)
	}
	for path, gone := range map[string]bool{old: true, recent: false, stranger: false, unlisted: false, link: false, target: false} {
		_, err := os.Lstat(path)
		if gone != os.IsNotExist(err) {
			t.Fatalf("%s: gone=%v expected", filepath.Base(path), gone)
		}
	}

	// A LIVE CAPTURE'S FILE IS NEVER SWEPT, and a stopped one is counted from its stop.
	running := write(lease1+".har", 9*time.Hour)
	known := func(lease string) (bool, time.Time, bool) {
		switch lease {
		case lease1:
			return true, time.Time{}, true
		case lease2:
			return false, now.Add(-61 * time.Minute), true
		}
		return false, time.Time{}, false
	}
	if deleted := Sweep(dir, now, time.Hour, known); deleted != 1 {
		t.Fatalf("the stopped capture's file goes an hour after its stop, the running one stays: %d went", deleted)
	}
	if _, err := os.Stat(running); err != nil {
		t.Fatal("a running capture's file is never swept")
	}
	if _, err := os.Stat(recent); !os.IsNotExist(err) {
		t.Fatal("a file whose capture stopped over an hour ago goes, whatever its own age")
	}
}

func TestRemoveAllDeletesEveryCaptureFileAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{lease1 + ".har", lease2 + ".har", "keep.txt"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	}
	if deleted, failed := RemoveAll(dir); deleted != 2 || len(failed) != 0 {
		t.Fatalf("both capture files go: %d deleted, %v failed", deleted, failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal("a file of another name is never removed")
	}
}

// A CAPTURE THAT STOPPED let its handle go and kept its file for the pull: the snapshot reads it back by its name — a
// regular file, never a link left at that name.
func TestAStoppedCaptureIsReadBackByItsNameAndNeverThroughALink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	rec, err := NewRecording(dir, lease1, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec.Record(Flow{Method: "GET", URL: "https://example.test/kept", Status: 200})
	running, err := rec.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	rec.Close()
	stopped, err := rec.Snapshot()
	if err != nil || string(stopped) != string(running) {
		t.Fatalf("a stopped capture must still be served whole: %v\n%s", err, stopped)
	}

	// Replaced by a link to something else: refused, not followed.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.har")
	if err := os.WriteFile(elsewhere, []byte(`{"log":{"entries":[{"secret":"not the agent's"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(rec.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, rec.Path()); err != nil {
		t.Fatal(err)
	}
	if data, err := rec.Snapshot(); err == nil {
		t.Fatalf("a link at the capture's name was followed: %s", data)
	}
}
