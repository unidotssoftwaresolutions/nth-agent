package doctor

import (
	"strings"
	"testing"
)

// WHAT_THE_FIRST_TRY_SHOWED: doctor says where this account's agent keeps its node identity on this machine, so a person
// told the farm refused that identity can find it; with no folder known it says nothing about one.
func TestDoctorSaysWhereTheAgentKeepsItsState(t *testing.T) {
	const folder = "/Users/ada/Library/Application Support/nth-agent"
	var out strings.Builder
	if err := Human(&out, tonight(), Options{StateFolder: folder}); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(text, "agent state folder: "+folder) || !strings.Contains(text, "node identity (node-id)") {
		t.Fatalf("doctor must name the agent's state folder and what it holds:\n%s", out.String())
	}
	var none strings.Builder
	if err := Human(&none, tonight(), Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.String(), "state folder") {
		t.Fatalf("with no folder known, doctor says nothing about one:\n%s", none.String())
	}
}
