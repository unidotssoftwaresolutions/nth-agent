package farmtest

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// WHAT A PUBLISHED TREE MUST NOT HOLD is a list kept OUTSIDE this tree, and only read here. Every seal that needs it reads it
// through TheForbiddenList: the tree's own census (the module root's a_tree_anyone_can_read_test.go), the npm package's
// pages, the BrowserStack capability seal and the commit-message census.
//
// It used to live in this file as the SHA-256 of each value, beside facts about the value. A sum is not a hiding place for
// a short value: it is found again from its sum by trying every candidate, and facts beside the sum say where to look. So
// the list is now nowhere in the tree — no value, no sum of one, no length, no kind. It is
// given to a run in one of two ways, and the first one present wins:
//
//   - the environment variable NTH_AGENT_FORBIDDEN_VALUES, holding the whole list; CI fills it from a repository secret
//     of the same name, and refuses to run without it;
//   - a file beside this repository, ForbiddenValuesFile, for a checkout in the workspace the list's owner keeps.
//
// With neither, every seal that reads the list skips and says, in its skip line, that this run did not check for the values.
//
// THE FORMAT, one value per line:
//
//	# a comment; blank lines are skipped too
//	whole    <value>    <what it is, in words>
//	inside   <value>    <what it is, in words>
//	protocol <literal>  <why it is let through>
//
//   - whole: refused as a whole run of letters and digits, or as a whole part of a token joined by "-", "@" or ".": an id,
//     an address, a file name. It may hold only letters, digits and those three joiners.
//   - inside: refused anywhere inside a run of letters and digits — a name inside a bundle id. Letters and digits only.
//   - protocol: a name of the wire protocol this agent speaks that holds one of the values; it is blanked before the census
//     reads, so the protocol is not refused. Any text without spaces.
//
// Case does not matter: the list and the text are both read in lower case. What it is, in words, is what a hit says; a
// value is never printed, by a hit or by an error about the list (which names the line, never its text).

// ForbiddenValuesFile is where a checkout whose owner keeps the list beside this repository finds it, relative to the
// directory that holds this repository.
const ForbiddenValuesFile = "docs/nth-agent-private/forbidden-values.txt"

// ForbiddenList is the list as read: each value in lower case with what it is, and the protocol names blanked before reading.
type ForbiddenList struct {
	whole    map[string]string
	inside   map[string]string
	protocol []string
	// From says where it was read: the variable's name or the file's path.
	From string
}

// Values is how many values the list holds (protocol names are not values).
func (l *ForbiddenList) Values() int { return len(l.whole) + len(l.inside) }

var (
	alnumRun    = regexp.MustCompile(`[a-z0-9]+`)
	joinedToken = regexp.MustCompile(`[a-z0-9]+(?:[-@.][a-z0-9]+)+`)
	wholeShape  = regexp.MustCompile(`^[a-z0-9]+(?:[-@.][a-z0-9]+)*$`)
	insideShape = regexp.MustCompile(`^[a-z0-9]+$`)
)

// ParseForbiddenList reads the list's text. An error names the line and what is wrong with it, never what the line holds.
func ParseForbiddenList(text string) (*ForbiddenList, error) {
	l := &ForbiddenList{whole: map[string]string{}, inside: map[string]string{}}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		at := "line " + strconv.Itoa(i+1)
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, errors.New(at + " is not `how value what-it-is`: it has " + strconv.Itoa(len(fields)) + " field(s), and three are needed")
		}
		how, value, what := fields[0], strings.ToLower(fields[1]), strings.Join(fields[2:], " ")
		switch how {
		case "whole":
			if !wholeShape.MatchString(value) {
				return nil, errors.New(at + ": a whole value is letters and digits, joined only by - @ or . — this one could never be found")
			}
			l.whole[value] = what
		case "inside":
			if !insideShape.MatchString(value) {
				return nil, errors.New(at + ": an inside value is letters and digits only — this one could never be found")
			}
			l.inside[value] = what
		case "protocol":
			l.protocol = append(l.protocol, value)
		default:
			return nil, errors.New(at + " begins with " + strconv.Quote(how) + "; a line begins with whole, inside or protocol")
		}
	}
	if l.Values() == 0 {
		return nil, errors.New("the list holds no value")
	}
	// Longest first, so a protocol name inside another is never blanked in part first.
	sort.Slice(l.protocol, func(a, b int) bool { return len(l.protocol[a]) > len(l.protocol[b]) })
	return l, nil
}

// TheForbiddenList is the list this run was given, or the test is skipped, saying so, when it was given none. A list that is
// there and cannot be read fails: a seal never passes having read nothing.
func TheForbiddenList(t testing.TB) *ForbiddenList {
	t.Helper()
	given, set := os.LookupEnv("NTH_AGENT_FORBIDDEN_VALUES")
	l, absent, err := loadTheForbiddenList(given, set, filepath.Join(moduleRoot(t), ".."))
	if err != nil {
		t.Fatal(err)
	}
	if l == nil {
		t.Skip(absent)
	}
	return l
}

// loadTheForbiddenList reads the variable's text (given, when set), then the file beside the repository at beside. absent is
// the skip line when neither is there.
func loadTheForbiddenList(given string, set bool, beside string) (l *ForbiddenList, absent string, err error) {
	if text := given; set && strings.TrimSpace(text) != "" {
		l, err := ParseForbiddenList(text)
		if err != nil {
			return nil, "", fmt.Errorf("NTH_AGENT_FORBIDDEN_VALUES is set and cannot be read: %v", err)
		}
		l.From = "NTH_AGENT_FORBIDDEN_VALUES"
		return l, "", nil
	}
	path := filepath.Join(beside, filepath.FromSlash(ForbiddenValuesFile))
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "THE FORBIDDEN LIST WAS NOT GIVEN (NTH_AGENT_FORBIDDEN_VALUES is not set, and there is no ../" +
			ForbiddenValuesFile + " beside this repository), so THIS RUN DID NOT CHECK FOR THE VALUES A PUBLISHED TREE MUST " +
			"NOT HOLD. CI gives the list from a repository secret; a checkout run where its owner keeps the file reads it there.", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("../%s is there and cannot be read: %v", ForbiddenValuesFile, err)
	}
	l, err = ParseForbiddenList(string(data))
	if err != nil {
		return nil, "", fmt.Errorf("../%s cannot be read: %v", ForbiddenValuesFile, err)
	}
	l.From = path
	return l, "", nil
}

func lineOf(text string, offset int) int { return strings.Count(text[:offset], "\n") + 1 }

// readable is text in lower case with each protocol name blanked, the same length in lines as text.
func (l *ForbiddenList) readable(text string) string {
	lower := strings.ToLower(text)
	for _, name := range l.protocol {
		lower = strings.ReplaceAll(lower, name, strings.Repeat(" ", len(name)))
	}
	return lower
}

// In returns "path:line — what it is" for every value of the list in text, never the value itself.
func (l *ForbiddenList) In(path, text string) []string {
	lower := l.readable(text)
	var hits []string
	seen := map[string]bool{}
	hit := func(offset int, what string) {
		line := strconv.Itoa(lineOf(lower, offset))
		if !seen[line+what] {
			seen[line+what] = true
			hits = append(hits, path+":"+line+" — "+what)
		}
	}
	for _, at := range alnumRun.FindAllStringIndex(lower, -1) {
		run := lower[at[0]:at[1]]
		if what, ok := l.whole[run]; ok {
			hit(at[0], what)
		}
		for value, what := range l.inside {
			if strings.Contains(run, value) {
				hit(at[0], what)
			}
		}
	}
	// A joined token (an id with hyphens, an address with dots and an @) is matched whole, and so is every run of its parts:
	// "host.<udid>.log" holds the udid, and an e-mail address sits inside a longer string.
	for _, at := range joinedToken.FindAllStringIndex(lower, -1) {
		joined := lower[at[0]:at[1]]
		bounds := []int{0}
		for i := 0; i < len(joined); i++ {
			if joined[i] == '-' || joined[i] == '@' || joined[i] == '.' {
				bounds = append(bounds, i)
			}
		}
		bounds = append(bounds, len(joined))
		for a := 0; a < len(bounds)-1; a++ {
			start := bounds[a]
			if a > 0 {
				start++
			}
			for b := a + 1; b < len(bounds); b++ {
				if what, ok := l.whole[joined[start:bounds[b]]]; ok {
					hit(at[0], what)
				}
			}
		}
	}
	return hits
}

// SumsIn returns "path:line — a sum of what it is" for every SHA-256, SHA-1 or MD5 of a value of the list in text, in hex of
// either case: a sum of a short value gives the value away as surely as the value does.
func (l *ForbiddenList) SumsIn(path, text string) []string {
	lower := strings.ToLower(text)
	var hits []string
	for _, values := range []map[string]string{l.whole, l.inside} {
		for value, what := range values {
			s256, s1, m5 := sha256.Sum256([]byte(value)), sha1.Sum([]byte(value)), md5.Sum([]byte(value))
			for _, sum := range []string{hex.EncodeToString(s256[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(m5[:])} {
				if at := strings.Index(lower, sum); at >= 0 {
					hits = append(hits, path+":"+strconv.Itoa(lineOf(lower, at))+" — a sum of "+what)
				}
			}
		}
	}
	sort.Strings(hits)
	return hits
}
