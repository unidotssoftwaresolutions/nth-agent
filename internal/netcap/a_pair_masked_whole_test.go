package netcap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ── A CREDENTIAL IN A QUERY OR A FORM BODY IS MASKED WHOLE ──────────────────────────────────────────────
//
// ONE TABLE, EVERY PLACE A PAIR CAN STAND. The agent masks name=value pairs in four readings: an address (request.url and
// the headers that hold one), a form body, a JSON string that holds an address or a form, and free text. Each vector below
// is put in every place it can stand and must lose every character of its credential there, and keep every pair around it.
// A place a vector does not list is one where that character means something else by the place's own syntax — said
// beside each, never silently.

// pairPlace is one place a pair can stand in a recorded exchange.
type pairPlace string

const (
	inAddress    pairPlace = "address"     // request.url
	inLocation   pairPlace = "location"    // a Location header, and the redirectURL copied from it
	inReferer    pairPlace = "referer"     // a Referer header
	inForm       pairPlace = "form"        // an x-www-form-urlencoded request body
	inJSONURL    pairPlace = "json-url"    // a JSON string holding an address
	inJSONForm   pairPlace = "json-form"   // a JSON string holding a form
	inFreeText   pairPlace = "free-text"   // a text/plain body with an address in a sentence
	inTextHeader pairPlace = "text-header" // a header that holds no address
)

var everyPlace = []pairPlace{inAddress, inLocation, inReferer, inForm, inJSONURL, inJSONForm, inFreeText, inTextHeader}

// The places that read a value as URL and form encoding define it — to the next & (or # in an address).
var encodedPlaces = []pairPlace{inAddress, inLocation, inReferer, inForm, inJSONURL, inJSONForm}

type pairVector struct {
	name    string
	pairs   string // what follows `?` in an address, or the whole form body
	gone    []string
	kept    []string
	places  []pairPlace
	whyNotS string // why the places left out are left out
}

// pairVectors is THE shared table.
var pairVectors = []pairVector{
	{name: "an apostrophe in the value", pairs: "user=keepA&password=it'sDUMMY1", gone: []string{"sDUMMY1", "it'"}, kept: []string{"user=keepA"}, places: everyPlace},
	{name: "a value starting with an apostrophe", pairs: "password='DUMMY2&keep=K2", gone: []string{"DUMMY2"}, kept: []string{"keep=K2"}, places: everyPlace},
	{name: "a value starting with a double quote", pairs: "password=\"DUMMY2B", gone: []string{"DUMMY2B"}, places: everyPlace},
	{name: "a backslash in the value", pairs: `password=pa\ss\DUMMY3&keep=K3`, gone: []string{"DUMMY3", `pa\`}, kept: []string{"keep=K3"}, places: everyPlace},
	{name: "angle brackets in the value", pairs: "password=a<b>DUMMY4&keep=K4", gone: []string{"DUMMY4"}, kept: []string{"keep=K4"}, places: encodedPlaces,
		whyNotS: "in free text an angle bracket begins markup, and ends the value there"},
	{name: "a raw space in the value", pairs: "password=it is DUMMY5&keep=K5", gone: []string{"DUMMY5", "it is"}, kept: []string{"keep=K5"}, places: encodedPlaces,
		whyNotS: "in free text a space ends a word, and the value with it (the runner's own reading, sealed by TestWhiteSpaceIsDotNetsWhiteSpace)"},
	{name: "a raw no-break space in the value", pairs: "token=a DUMMY5B&keep=K5B", gone: []string{"DUMMY5B"}, kept: []string{"keep=K5B"}, places: encodedPlaces,
		whyNotS: "as for a space"},
	{name: "an address nested in a value", pairs: "next=/reset?token=DUMMY6", gone: []string{"DUMMY6"}, kept: []string{"next=/reset"}, places: everyPlace},
	{name: "an address nested behind a later pair", pairs: "a=1&next=/r?token=DUMMY7&keep=K7", gone: []string{"DUMMY7"}, kept: []string{"a=1", "keep=K7"}, places: everyPlace},
	{name: "a percent-encoded value", pairs: "password=it%27s%20DUMMY9&keep=K9", gone: []string{"DUMMY9", "it%27s"}, kept: []string{"keep=K9"}, places: everyPlace},
	{name: "a plus in the value", pairs: "password=a+DUMMY10&keep=K10", gone: []string{"DUMMY10"}, kept: []string{"keep=K10"}, places: everyPlace},
	{name: "a repeated key", pairs: "password=DUMMY11A&password=DUMMY11B&keep=K11", gone: []string{"DUMMY11A", "DUMMY11B"}, kept: []string{"keep=K11"}, places: everyPlace},
	{name: "an empty value, then a credential", pairs: "password=&token=DUMMY12&keep=K12", gone: []string{"DUMMY12"}, kept: []string{"password=&", "keep=K12"}, places: everyPlace},
	{name: "a percent-encoded name", pairs: "pass%77ord=DUMMY13&user%5Bpassword%5D=DUMMY13B&keep=K13", gone: []string{"DUMMY13"}, kept: []string{"keep=K13"}, places: everyPlace},
	{name: "a bracketed name", pairs: "user[password]=DUMMY13C&keep=K13C", gone: []string{"DUMMY13C"}, kept: []string{"keep=K13C"}, places: everyPlace},
	{name: "a ? and = inside the value", pairs: "token=ab?c=DUMMY14&keep=K14", gone: []string{"DUMMY14"}, kept: []string{"keep=K14"}, places: everyPlace},
	{name: "a ; inside the value", pairs: "password=a;DUMMY15&keep=K15", gone: []string{"DUMMY15"}, kept: []string{"keep=K15"}, places: encodedPlaces,
		whyNotS: "in free text and in a header that holds no address, ; separates (a=1; token=X)"},
	{name: "a # inside a form value", pairs: "password=a#DUMMY16&keep=K16", gone: []string{"DUMMY16"}, kept: []string{"keep=K16"}, places: []pairPlace{inForm, inJSONForm},
		whyNotS: "in an address # begins the fragment — the value ends there by URL syntax — and in free text it ends the value"},
	{name: "a quote after the value", pairs: "password=DUMMY17\"tail&keep=K17", gone: []string{"DUMMY17", "tail"}, kept: []string{"keep=K17"}, places: encodedPlaces,
		whyNotS: "in free text a double quote closes the string the address was written in"},
}

// placePairs puts one vector in one place and returns the masked text the record holds there.
func placePairs(t *testing.T, place pairPlace, pairs string) string {
	t.Helper()
	address := "https://h.example/p?" + pairs
	f := Flow{Started: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Method: "GET", URL: "https://h.example/", ReqProto: "HTTP/1.1"}
	jsonBody := func(value string) {
		quotedValue, _ := json.Marshal(value)
		f.Status, f.RespProto = 200, "HTTP/1.1"
		f.RespHeaders = []Header{{"Content-Type", "application/json"}}
		f.RespBody = []byte(`{"keepK":"v","target":` + string(quotedValue) + `}`)
		f.RespBodySize = int64(len(f.RespBody))
	}
	switch place {
	case inAddress:
		f.URL = address
		return entryFor(f).Request.URL
	case inLocation:
		f.Status, f.RespProto = 302, "HTTP/1.1"
		f.RespHeaders = []Header{{"Location", address}}
		entry := entryFor(f)
		if entry.Response.RedirectURL != pairValue(t, entry.Response.Headers, "Location") {
			t.Fatalf("redirectURL is the masked Location")
		}
		return entry.Response.RedirectURL
	case inReferer:
		f.ReqHeaders = []Header{{"Referer", address}}
		return pairValue(t, entryFor(f).Request.Headers, "Referer")
	case inTextHeader:
		f.ReqHeaders = []Header{{"X-Note", "see " + address}}
		return pairValue(t, entryFor(f).Request.Headers, "X-Note")
	case inForm:
		f.Method = "POST"
		f.ReqHeaders = []Header{{"Content-Type", "application/x-www-form-urlencoded"}}
		f.ReqBody, f.ReqBodySize = []byte(pairs), int64(len(pairs))
		return fieldOf(t, entryFor(f), "reqBody", "")
	case inJSONURL:
		jsonBody(address)
	case inJSONForm:
		jsonBody(pairs)
	case inFreeText:
		body := "go to " + address + "\nthanks"
		f.Status, f.RespProto = 200, "HTTP/1.1"
		f.RespHeaders = []Header{{"Content-Type", "text/plain"}}
		f.RespBody, f.RespBodySize = []byte(body), int64(len(body))
	default:
		t.Fatalf("no place %q", place)
	}
	return fieldOf(t, entryFor(f), "respBody", "")
}

func TestEveryPairVectorIsMaskedWholeInEveryPlaceItCanStand(t *testing.T) {
	for _, v := range pairVectors {
		if len(v.places) < len(everyPlace) && v.whyNotS == "" {
			t.Fatalf("%s: a vector that leaves a place out says why", v.name)
		}
		for _, place := range v.places {
			t.Run(v.name+"/"+string(place), func(t *testing.T) {
				got := placePairs(t, place, v.pairs)
				for _, gone := range v.gone {
					if strings.Contains(got, gone) {
						t.Errorf("%s must be masked whole and survived in %s", quoted(gone), quoted(got))
					}
				}
				for _, kept := range v.kept {
					if !strings.Contains(got, kept) {
						t.Errorf("%s must be kept and was lost from %s", quoted(kept), quoted(got))
					}
				}
				if !strings.Contains(got, Mask) && !strings.HasPrefix(v.name, "an empty") {
					t.Errorf("nothing was masked in %s", quoted(got))
				}
			})
		}
	}
}

// A PATH PARAMETER IS NOT A HIDING PLACE for the query behind it, and its own value ends at the next path character.
func TestAPathParameterNeitherHidesTheQueryNorRunsIntoIt(t *testing.T) {
	got := maskAddress("https://h.example/api;v=1?access_token=DUMMYP1&keep=KP1")
	if strings.Contains(got, "DUMMYP1") || !strings.Contains(got, "keep=KP1") || !strings.Contains(got, ";v=1?") {
		t.Fatalf("the query behind a path parameter is masked: %s", got)
	}
	got = maskAddress("https://h.example/a;jsessionid=DUMMYP2/b?keep=KP2")
	if strings.Contains(got, "DUMMYP2") || !strings.Contains(got, "/b?keep=KP2") {
		t.Fatalf("a held path parameter is masked to the next path character: %s", got)
	}
}

// A FRAGMENT'S PAIRS ARE PAIRS — an OAuth implicit grant hands its token back there.
func TestAFragmentsPairsAreMasked(t *testing.T) {
	got := maskAddress("https://app.example/cb#access_token=DUMMYF1&state=keepST&id_token=it'sDUMMYF2")
	if strings.Contains(got, "DUMMYF1") || strings.Contains(got, "DUMMYF2") || !strings.Contains(got, "state=keepST") {
		t.Fatalf("a fragment's credentials are masked: %s", got)
	}
}

// MASKING TWICE IS MASKING ONCE: the runner masks the agent's record again, and a masked value must read as masked.
func TestAMaskedPairMasksToItself(t *testing.T) {
	for _, v := range pairVectors {
		for _, place := range []pairPlace{inAddress, inForm, inFreeText} {
			once := placePairs(t, place, v.pairs)
			var twice string
			switch place {
			case inAddress:
				twice = maskAddress(once)
			case inForm:
				twice = maskBodyText(once, "application/x-www-form-urlencoded")
			default:
				twice = maskBodyText(once, "text/plain")
			}
			if twice != once {
				t.Fatalf("%s/%s: masked twice %q, once %q", v.name, place, twice, once)
			}
		}
	}
}
