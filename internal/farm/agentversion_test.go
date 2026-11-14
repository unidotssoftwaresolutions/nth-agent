package farm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/farm"
)

// A_BINARY_NOBODY_CAN_DOWNLOAD, item 2 — the version the PLATFORM can read.
//
// `--version` answers the person standing at the machine, and nobody is ever standing at the machine. So the same
// string crosses to the farm, where the operator can read it — and when a device behaves oddly in the field,
// somebody can finally say which agent produced the row.
//
// ⚠️ THE WHOLE OF WHAT THESE SEALS HOLD IS THAT IT INVENTS NOTHING. `dev` is an answer, silence is silence, and
// neither may be turned into the other.

func TestAStampedVersionCrossesVerbatim(t *testing.T) {
	report := farm.DeclareAgentVersion(farm.Report{}, "v1.4.0")

	if report.AgentVersion == nil || *report.AgentVersion != "v1.4.0" {
		t.Fatalf("the stamped version must cross unchanged, got %v", report.AgentVersion)
	}
}

func TestDevIsAnAnswerAndCrossesAsItself(t *testing.T) {
	// ⚠️ A build somebody made themselves says `dev` because that is what it is. The agent does not substitute a
	// git SHA or a date it would be guessing at, and it does not suppress the value to look tidier: a row saying
	// `dev` is a TRUE statement, and the operator's screen exists to make it legible rather than pretty.
	report := farm.DeclareAgentVersion(farm.Report{}, "dev")

	if report.AgentVersion == nil || *report.AgentVersion != "dev" {
		t.Fatalf("`dev` must cross as `dev`, got %v", report.AgentVersion)
	}
}

func TestNothingStatedDeclaresNothingAndIsNeverSubstituted(t *testing.T) {
	// ⚠️ THE ONE THAT MATTERS. nil means "this agent did not say", which is every agent running today. A
	// substitute — "unknown", "unknown-build", "" — is a value, and a value is a claim: the farm cannot tell it
	// from something the agent actually reported, and this side is the only place that knows the difference.
	for _, blank := range []string{"", "   ", "\t\n"} {
		report := farm.DeclareAgentVersion(farm.Report{}, blank)
		if report.AgentVersion != nil {
			t.Fatalf("a blank version declared %q — an agent that said nothing must send nothing", *report.AgentVersion)
		}
	}
}

func TestTheFieldIsOmittedFromTheWireWhenNothingWasStated(t *testing.T) {
	// The nullable idiom the four standing facts beside it already use: an omitted field and a farm that has
	// never heard of the field produce IDENTICAL BYTES. A `"agentVersion":""` would be a new value on the wire.
	silent, err := json.Marshal(farm.RegisterRequest{HostName: "h", NodeID: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(silent), "agentVersion") {
		t.Fatalf("an agent that said nothing put the field on the wire: %s", silent)
	}

	version := "v1.4.0"
	spoken, err := json.Marshal(farm.RegisterRequest{HostName: "h", NodeID: "n", AgentVersion: &version})
	if err != nil {
		t.Fatal(err)
	}
	// …and the instrument guard: the field really is on the wire when there IS something to say, or the
	// assertion above passes on a struct that simply has no such field.
	if !strings.Contains(string(spoken), `"agentVersion":"v1.4.0"`) {
		t.Fatalf("a stated version did not reach the wire: %s", spoken)
	}
}
