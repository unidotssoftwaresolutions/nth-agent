package builds

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A BUILD IT HAS COUNTED: THE LIST RULES. A build is walked as a zip archive with the farm's own list rules for an unpack,
// and nothing in it is unpacked: the end record (one with another end record in its comment is refused), the list walked to
// the size the record gives and one entry more looked for, at most 50,000 files, every file's own header exactly where the
// list says and saying what the list says, no byte before the list that the list does not name, every name inside the app
// and none twice, only stored or deflated files, and at most 1 GiB declared. One crafted archive per rule, each refused for
// that rule; and real shapes pass.

func countBytes(t *testing.T, data []byte, kind string) Fault {
	t.Helper()
	path := filepath.Join(t.TempDir(), "build"+kind)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return Count(path, kind)
}

func TestEveryListRuleRefusesItsCraftedArchive(t *testing.T) {
	app := anApp()
	with := func(change func([]rawEntry) []rawEntry) []rawEntry {
		entries := append([]rawEntry(nil), anApp()...)
		return change(entries)
	}
	hidden := rawZip{entries: []rawEntry{{name: "Payload/Lantern.app/Hidden", data: []byte("not on the list")}}}.bytes()
	hiddenLocal := hidden[:bytes.Index(hidden, []byte{0x50, 0x4b, 0x01, 0x02})]
	for name, c := range map[string]struct {
		archive []byte
		kind    string
		want    Fault
	}{
		"no bytes at all":    {nil, ".ipa", NotAZip},
		"not a zip at all":   {bytes.Repeat([]byte("not a zip "), 40), ".ipa", NotAZip},
		"only an end record": {rawZip{}.bytes()[:20], ".ipa", NotAZip},
		// The end record a reader takes is the last one whose comment runs exactly to the archive's end; a second one inside
		// that comment is what a reader scanning from the front would take instead.
		"an end record with another one in its comment": {rawZip{entries: app,
			comment: []byte{0x50, 0x4b, 0x05, 0x06, 1, 2, 3, 4}}.bytes(), ".ipa", RunsPastItsRecord},
		"the end record counts fewer files than its list holds": {rawZip{entries: app, count: 3, countSet: true}.bytes(), ".ipa", RunsPastItsRecord},
		"a list entry past the list the end record describes":   {rawZip{entries: app, extraListEntry: true}.bytes(), ".ipa", RunsPastItsRecord},
		"a list that runs past its stated size":                 {rawZip{entries: app, listSizeLie: -10}.bytes(), ".ipa", NotAZip},
		"more files than a build may list":                      {rawZip{entries: app, count: MaxEntries + 1, countSet: true}.bytes(), ".ipa", TooManyEntries},
		"a file hidden between two listed ones": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[2].before = hiddenLocal
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"a file hidden before the first listed one": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[0].before = hiddenLocal
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"bytes between the last file and the list": {rawZip{entries: app, beforeList: hiddenLocal}.bytes(), ".ipa", UnlistedEntry},
		"a file's own header names another file": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[1].localName = "Payload/Lantern.app/Other"
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"a file's own header declares another size": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[3].localDeclared = 99
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"a file's own header carries another checksum": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[3].localCRC = 0x1234
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"a descriptor that declares another size": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[2].descDeclared = 7
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"the list points a file at another place": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[1].localAtShift = 2
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"the list points a file past the archive": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[3].localAtShift = 1 << 30
			return e
		})}.bytes(), ".ipa", UnlistedEntry},
		"an absolute name":            {rawZip{entries: []rawEntry{{name: "/etc/hosts", data: []byte("x")}}}.bytes(), ".ipa", LeavesTheApp},
		"a name with a drive":         {rawZip{entries: []rawEntry{{name: "C:Payload/x", data: []byte("x")}}}.bytes(), ".ipa", LeavesTheApp},
		"a name with a backslash":     {rawZip{entries: []rawEntry{{name: `Payload\..\x`, data: []byte("x")}}}.bytes(), ".ipa", LeavesTheApp},
		"a name that climbs out":      {rawZip{entries: []rawEntry{{name: "Payload/../../x", data: []byte("x")}}}.bytes(), ".ipa", LeavesTheApp},
		"a name that is only a climb": {rawZip{entries: []rawEntry{{name: "..", data: []byte("x")}}}.bytes(), ".apk", LeavesTheApp},
		"a name holding a NUL":        {rawZip{entries: []rawEntry{{name: "Payload/a\x00b", data: []byte("x")}}}.bytes(), ".ipa", LeavesTheApp},
		"an empty name":               {rawZip{entries: []rawEntry{{name: "", data: []byte("x")}}}.bytes(), ".ipa", LeavesTheApp},
		"one name twice, in other letters": {rawZip{entries: []rawEntry{{name: "Payload/A", data: []byte("x")},
			{name: "payload/a", data: []byte("y")}}}.bytes(), ".ipa", NamedTwice},
		"a folder and a file of one name": {rawZip{entries: []rawEntry{{name: "Payload/"}, {name: "Payload", data: []byte("y")}}}.bytes(), ".ipa", NamedTwice},
		"an encrypted file": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[3].flags = 1
			return e
		})}.bytes(), ".ipa", UnknownPacking},
		"a file packed in a way no build is": {rawZip{entries: with(func(e []rawEntry) []rawEntry {
			e[3].method = 12
			return e
		})}.bytes(), ".apk", UnknownPacking},
		"more than 1 GiB declared": {rawZip{entries: []rawEntry{
			{name: "classes.dex", data: []byte("x"), declared: 600 << 20},
			{name: "resources.arsc", data: []byte("y"), declared: 600 << 20}}}.bytes(), ".apk", TooLarge},
		"a ZIP64 locator that points past the archive": {rawZip{entries: app, zip64: true, zip64LocatorPast: true}.bytes(), ".ipa", NotAZip},
		"an APK signing block in an .ipa":              {rawZip{entries: app, apkSigningBlock: bytes.Repeat([]byte{7}, 64)}.bytes(), ".ipa", UnlistedEntry},
		"an APK signing block whose size does not add up": {rawZip{entries: app, apkSigningBlock: bytes.Repeat([]byte{7}, 64),
			apkBlockSizeLie: 8}.bytes(), ".apk", UnlistedEntry},
		"a file hidden where an APK signing block would be": {rawZip{entries: app, beforeList: append(append([]byte(nil), hiddenLocal...),
			[]byte("APK Sig Block 42")...)}.bytes(), ".apk", UnlistedEntry},
	} {
		t.Run(name, func(t *testing.T) {
			if got := countBytes(t, c.archive, c.kind); got != c.want {
				t.Fatalf("got %v (%q), want %v (%q)", got, got.Sentence(), c.want, c.want.Sentence())
			}
		})
	}
}

// What every build in the field looks like passes: archive/zip's own output (deflated, with descriptors, and stored), a crafted
// app with a folder, Unix modes, a descriptor and a stored file, the same in ZIP64, and an .apk with its signing block.
func TestARealBuildIsCounted(t *testing.T) {
	written := func(stored bool) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, e := range anApp() {
			if strings.HasSuffix(e.name, "/") {
				w.Create(e.name)
				continue
			}
			method := zip.Deflate
			if stored {
				method = zip.Store
			}
			f, _ := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: method})
			f.Write(e.data)
		}
		w.Close()
		return b.Bytes()
	}
	for name, c := range map[string]struct {
		archive []byte
		kind    string
	}{
		"archive/zip, deflated":           {written(false), ".ipa"},
		"archive/zip, stored":             {written(true), ".apk"},
		"a crafted app":                   {rawZip{entries: anApp()}.bytes(), ".ipa"},
		"a crafted app in ZIP64":          {rawZip{entries: anApp(), zip64: true}.bytes(), ".ipa"},
		"an .apk with its signing block":  {rawZip{entries: anApp(), apkSigningBlock: bytes.Repeat([]byte{7}, 4096)}.bytes(), ".apk"},
		"an archive with a plain comment": {rawZip{entries: anApp(), comment: []byte("built by a build machine")}.bytes(), ".apk"},
	} {
		if got := countBytes(t, c.archive, c.kind); got != Counted {
			t.Errorf("%s: a real build must be counted, got %v (%q)", name, got, got.Sentence())
		}
	}
}

// Every refusal is a plain sentence that names no setting.
func TestEveryFaultHasAPlainSentence(t *testing.T) {
	seen := map[string]bool{}
	for f := NotAZip; f <= Unreadable; f++ {
		s := f.Sentence()
		if s == "" || seen[s] || strings.Contains(s, ":") || strings.ContainsAny(s, "{}") {
			t.Errorf("%v: %q is not a plain sentence of its own", f, s)
		}
		seen[s] = true
	}
	if !strings.Contains(TooManyEntries.Sentence(), "50,000") || !strings.Contains(TooLarge.Sentence(), "1 GiB") {
		t.Errorf("the bounds are said as numbers a person reads: %q, %q", TooManyEntries.Sentence(), TooLarge.Sentence())
	}
}
