package doctor

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// The coloured variant of the doctor's main screen: a 100-column terminal, colour on (golden_test.go).
func TestGoldenDoctorWithAMixOfFindingsColour(t *testing.T) {
	var out strings.Builder
	if err := Human(&out, tonight(), Options{Mode: ui.Mode{Color: true, Terminal: true, Columns: 100}}); err != nil {
		t.Fatal(err)
	}
	golden(t, "doctor-mixed.colour.golden", out.String())
	if ui.Plain(out.String()) != func() string {
		var plain strings.Builder
		_ = Human(&plain, tonight(), Options{Mode: ui.Mode{Terminal: true, Columns: 100}})
		return plain.String()
	}() {
		t.Fatal("colour may only repeat what the text says: stripped of its colour, the screen is the plain one")
	}
}
