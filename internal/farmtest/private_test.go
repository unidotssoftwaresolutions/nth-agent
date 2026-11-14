package farmtest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The list's reader, on a list of INVENTED values written here: the real list is never in this tree, so its reading is
// proved on one whose answers are known.
const plantedList = `# an invented list, in the format the real one keeps
whole    zq-0042-fixture        an invented id
whole    ada@example.com        an invented address
inside   qzvxk                  an invented name
protocol x-qzvxk-route          an invented wire name that holds it

`

func planted(t *testing.T) *ForbiddenList {
	t.Helper()
	l, err := ParseForbiddenList(plantedList)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestTheListFindsEachKindOfValueAndNeverPrintsOne(t *testing.T) {
	l := planted(t)
	if l.Values() != 3 {
		t.Fatalf("the planted list holds 3 values, read %d", l.Values())
	}
	text := "line one\nthe id is ZQ-0042-FIXTURE here\nmail ada@example.com, or host.zq-0042-fixture.log\n" +
		"com.example.qzvxkapp\nthe header x-qzvxk-route is protocol\nqzvx is not it, and zq-0042 is not either\n"
	got := strings.Join(l.In("planted", text), "\n")
	for _, want := range []string{"planted:2 — an invented id", "planted:3 — an invented address", "planted:3 — an invented id",
		"planted:4 — an invented name"} {
		if !strings.Contains(got, want) {
			t.Errorf("the list did not find %q in:\n%s", want, got)
		}
	}
	for _, line := range []string{"planted:5", "planted:6", "planted:1"} {
		if strings.Contains(got, line+" ") {
			t.Errorf("the list refused %s, which holds no value (a protocol name, or a part of one): %s", line, got)
		}
	}
	for _, value := range []string{"zq-0042-fixture", "ada@example.com", "qzvxk"} {
		if strings.Contains(strings.ToLower(got), value) {
			t.Errorf("a hit printed the value it found: %s", got)
		}
	}
}

func TestTheListFindsASumOfAValue(t *testing.T) {
	sum := sha256.Sum256([]byte("qzvxk"))
	text := "var x = \"" + strings.ToUpper(hex.EncodeToString(sum[:])) + "\"\n"
	if got := planted(t).SumsIn("planted", text); len(got) != 1 || got[0] != "planted:1 — a sum of an invented name" {
		t.Fatalf("the sum of a value was not refused: %v", got)
	}
}

func TestAListThatCannotBeReadSaysWhichLineAndNotWhatItHolds(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"whole only-two-fields\n", "line 1 is not `how value what-it-is`"},
		{"# c\nbeside qzvxk a name\n", "line 2 begins with \"beside\""},
		{"whole /var/qzvxk a path\n", "line 1: a whole value is letters and digits"},
		{"inside qz-vxk a name\n", "line 1: an inside value is letters and digits only"},
		{"# nothing\nprotocol x-route why\n", "the list holds no value"},
	} {
		_, err := ParseForbiddenList(c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.text, err, c.want)
			continue
		}
		if strings.Contains(err.Error(), "qzvxk") || strings.Contains(err.Error(), "vxk") {
			t.Errorf("the error printed what the line holds: %v", err)
		}
	}
}

func TestTheVariableWinsAndTheFileIsReadBesideTheRepository(t *testing.T) {
	beside := t.TempDir()
	if l, absent, err := loadTheForbiddenList("", false, beside); l != nil || err != nil ||
		!strings.Contains(absent, "THIS RUN DID NOT CHECK FOR THE VALUES") {
		t.Fatalf("with neither, the seal must skip and say why: %v %q %v", l, absent, err)
	}
	file := filepath.Join(beside, filepath.FromSlash(ForbiddenValuesFile))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("whole fromthefile a value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, _, err := loadTheForbiddenList("", false, beside); err != nil || l == nil || l.From != file || len(l.In("x", "fromthefile")) != 1 {
		t.Fatalf("the file beside the repository was not read: %v %v", l, err)
	}
	if l, _, err := loadTheForbiddenList(plantedList, true, beside); err != nil || l.From != "NTH_AGENT_FORBIDDEN_VALUES" || l.Values() != 3 {
		t.Fatalf("the variable did not win over the file: %v %v", l, err)
	}
	if _, _, err := loadTheForbiddenList("whole\n", true, beside); err == nil || !strings.Contains(err.Error(), "NTH_AGENT_FORBIDDEN_VALUES is set and cannot be read") {
		t.Fatalf("a list that is given and cannot be read must fail, got %v", err)
	}
}
