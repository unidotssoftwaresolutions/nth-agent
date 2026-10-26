package farmtest

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// WHAT A PUBLISHED TREE MUST NOT HOLD (AN_AGENT_READY_TO_BE_READ), kept here so every seal that needs it reads one list:
// the tree's own census (the module root's a_tree_anyone_can_read_test.go), the npm README's, and the BrowserStack
// capability seal's. The values are held as SHA-256 sums, so this file does not hold them either.

// forbiddenValue is one value refused anywhere in the tree, held as the SHA-256 of its lower-case spelling. A value that
// can sit inside a longer word (a name inside a bundle id, a repository name inside an identifier) is matched as part of
// any run of letters and digits; the rest are matched as a whole token.
type forbiddenValue struct {
	sum    string
	length int
	inside bool
	what   string
}

var forbiddenValues = []forbiddenValue{
	{"c3820664036d333475b647369a04695850012f2a88ac7925a757914c92d074a8", 26, false, "a person's e-mail address (an Apple ID)"},
	{"7cc262efe86d12b06486f3227c71eedd6994abbc35bd92614b8955ae63d38cb0", 13, true, "a person's name, inside a bundle id"},
	{"4764f92da79a79b737659ff107c3adaf43153b561da3c14589b9224a55888c82", 6, true, "a person's name"},
	{"fe604600879c2512397964183c848337e9ba366ead980e4542dc7d0adf2b9ccd", 5, true, "a person's name"},
	{"b56ae9b640d3472cd60036119ba70bc22aa36314cc8b2f41283c25ee58f5d940", 9, true, "a person's account name (a home path)"},
	{"112778e6bbf770e24fc06a93988be106d5eaf4a8592ba9e6981ade88744c5ac5", 10, true, "a real Apple Developer team id"},
	{"98a43cd035a284df91f6a0ff24a0373b81e647701c54e866fa4c5d8fb6bfe100", 10, true, "a real signing certificate's code"},
	{"47629e5b94183014e216560bdcc16a813c9f8c906f8f895f3026bc071d8dea04", 11, true, "a real phone's serial"},
	{"174e41635ab87bdb0807ad8a978e38bf84a03a9a7f7961777db0954aba0d1788", 5, true, "a customer's name"},
	{"1300f75473af8cb9327a39f55b9506b8fbb36d3e412fce605ea2f01b0a7801dc", 30, false, "a real machine's temporary directory"},
	{"817c7702fd5f4aaf6b5b5f30252fe173eadcda7262b4191aa987b1d922c8c94a", 25, false, "a real iPhone's UDID"},
	{"e52bee8ec3713f45099ef5c084bd96670403683a066ecc6502775babad7e4abc", 25, false, "a real iPhone's UDID"},
	{"69744abf29558454481cfb0c5aa10e74e9793b691f58590c5fecb4eb90687b51", 25, false, "a real iPhone's UDID"},
	{"9443be7102b8dd3b80e2b72a19d5d3e68aed10f4bb8a20477ee36fb658aa1108", 15, false, "the start of a real iPhone's UDID"},
	{"c8a2d89db9794a767dbca51ace43b4b9f4ab84a9a181097e9eb1c24784f175fa", 36, false, "a node id that may be a real one"},
	{"f0d8c998e87d707ca08d1233862b92239029ba1d7e1d90148d255c3d37ef93cb", 36, false, "a simulator UDID that may be a real one"},
	{"6c3a4d760128c5307f2b2f7e7cedcbb53ef194112b2d486abe61f6dcbe74be33", 40, false, "a session id from a real BrowserStack account"},
	{"2def80f045205077eccef1feaec676505a817488c450afc387114cd36968b4c2", 40, false, "a build id from a real BrowserStack account"},
	{"c570d16890a2dc7810bca164daac3731f7e773d03b0616e5d0173aaeca42932d", 40, false, "a build id from a real BrowserStack account"},
	{"00f7f8f3ebfcc05c7d44acf056410b6a0a72303b730888c12e26a476955552a4", 8, true, "the name of a private repository"},
	{"84b2cd4d4aaf90e335a6d3177232b9ced45b2c9da30ab053948f7b4736b2732c", 6, true, "the name of a private repository"},
	{"b336221e127bf95e04fa7e1203dd7e0dee921545f80cc9140835d991c579cbb6", 9, true, "the name of a private repository"},
	{"c5511a006b00ac7f507a1f16b4b9fda8e977ba75864aeed3ee0727af82c7804f", 9, true, "the name of a private repository"},
	{"93a071d8610637f7c629904449319cb2ab4bf165c7df65575cad1ed8199ee552", 9, true, "the name of a private repository"},
	{"90af5f2689e4971dfa34d2c31898771647342d06a2a0087c3455fcf9ccfe8286", 9, true, "the name of a private repository"},
	{"92f543815252aa50295d4a49b57e38823d4475881abb45fab2ed26811c27d6a7", 13, false, "a script in a private repository"},
}

var (
	alnumRun    = regexp.MustCompile(`[a-z0-9]+`)
	joinedToken = regexp.MustCompile(`[a-z0-9]+(?:[-@.][a-z0-9]+)+`)
	// Two names in the wire protocol this agent answers carry the farm's old name: the route prefix of the farm's own
	// requests to a node, and the header that marks one. They are protocol, not pointers, so they are blanked before the
	// census reads.
	theFarmsRoute = regexp.MustCompile(`gurufarm-node/|x-gurufarm-asks`)
)

func withoutTheFarmsRoute(lower string) string {
	return theFarmsRoute.ReplaceAllStringFunc(lower, func(m string) string { return strings.Repeat(" ", len(m)) })
}

func lineOf(text string, offset int) int { return strings.Count(text[:offset], "\n") + 1 }

func sumOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ForbiddenValuesIn returns "path:line — what" for every forbidden value in text, never the value itself.
func ForbiddenValuesIn(path, text string) []string {
	lower := withoutTheFarmsRoute(strings.ToLower(text))
	whole := map[string]string{}
	inside := map[int]map[string]string{}
	for _, f := range forbiddenValues {
		if f.inside {
			if inside[f.length] == nil {
				inside[f.length] = map[string]string{}
			}
			inside[f.length][f.sum] = f.what
		} else {
			whole[f.sum] = f.what
		}
	}
	var hits []string
	seen := map[string]bool{}
	hit := func(offset int, what string) {
		key := strconv.Itoa(lineOf(text, offset)) + what
		if !seen[key] {
			seen[key] = true
			hits = append(hits, path+":"+strconv.Itoa(lineOf(text, offset))+" — "+what)
		}
	}
	for _, at := range alnumRun.FindAllStringIndex(lower, -1) {
		run := lower[at[0]:at[1]]
		if what, ok := whole[sumOf(run)]; ok {
			hit(at[0], what)
		}
		for length, sums := range inside {
			for i := 0; i+length <= len(run); i++ {
				if what, ok := sums[sumOf(run[i:i+length])]; ok {
					hit(at[0], what)
				}
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
				if what, ok := whole[sumOf(joined[start:bounds[b]])]; ok {
					hit(at[0], what)
				}
			}
		}
	}
	return hits
}

// PrivateRepositoryIn is the first name of a private repository, or of a script in one, in text: what it is and its line,
// or "" and 0. For a seal on text a customer reads, which names what it found without this file naming it.
func PrivateRepositoryIn(text string) (string, int) {
	for _, hit := range ForbiddenValuesIn("", text) {
		if strings.Contains(hit, "private repository") {
			at, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(hit, ":"), " ", 2)[0])
			return hit[strings.Index(hit, " — ")+len(" — "):], at
		}
	}
	return "", 0
}
