package render_test

import (
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/render"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/ui"
)

// The coloured variant of `devices`: a 100-column terminal, colour on (golden_test.go). Stripped of its colour it is the
// plain screen at the same width, and the state column is what carries the colour.
func TestGoldenDevicesOfferedAndLeftOutColour(t *testing.T) {
	terminal := ui.Mode{Color: true, Terminal: true, Columns: 100}
	var out strings.Builder
	if err := render.Styled(&out, terminal, goldenDevices(), goldenLiveView); err != nil {
		t.Fatal(err)
	}
	golden(t, "devices-mixed.colour.golden", out.String())

	var plain strings.Builder
	_ = render.Styled(&plain, ui.Mode{Terminal: true, Columns: 100}, goldenDevices(), goldenLiveView)
	if ui.Plain(out.String()) != plain.String() {
		t.Fatal("colour may only repeat what the text says")
	}
	for _, state := range []string{terminal.Good("ready to offer"), terminal.Stop("left out: unauthorized"), terminal.Look("left out: not on a USB cable")} {
		if !strings.Contains(out.String(), state) {
			t.Errorf("the state is coloured by what it means: %q missing", state)
		}
	}
}
