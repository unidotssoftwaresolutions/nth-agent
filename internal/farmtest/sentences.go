// Package farmtest is what the farm says to an agent, read from the farm's own source for the seals
// (AN_AGENT_THAT_SAYS_WHY_THE_FARM_SAID_NO). It is imported by tests only, never by a program: its own test holds that.
//
// HOW IT FINDS THE FILES: through ServerSource, which reads where they are from a file kept BESIDE this repository, never in
// it. When the server's source is not there (nth-agent's own CI, and a published copy, check out nth-agent alone), a seal
// that needs its sentences SKIPS and says so; in a checkout that has both, it always runs. It never passes having read
// nothing: a file that is named and does not hold a sentence fails.
package farmtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Sentences are the farm's refusals to a node, each built the way the farm builds it, with the farm's default limits.
type Sentences struct {
	// TooManyMachines is register's 429: a new machine past the workspace's machines.
	TooManyMachines string
	// TooManyDevices is register's and heartbeat's 429: a report past one machine's devices.
	TooManyDevices string
	// TooManyTunnels is the tunnel upgrade's 429: a machine past the workspace's open tunnels.
	TooManyTunnels string
	// HeartbeatInFlight and RegistryBusy are heartbeat's 503s: the previous heartbeat still being applied, and contention.
	HeartbeatInFlight string
	RegistryBusy      string
	// LeftHeartbeat is heartbeat's 404 for a node that said it was leaving (the goodbye on a stop).
	LeftHeartbeat string

	tooSoon     string // HeartbeatTooSoonMessage, its {since…} hole still open
	unknownNode string // RequireNodeOwnershipAttribute.RefusalMessage, its {nodeId} hole still open
}

// HeartbeatTooSoon is heartbeat's 503 for one that came seconds after the last.
func (s Sentences) HeartbeatTooSoon(seconds float64) string {
	return strings.Replace(s.tooSoon, "\x00since\x00", strconv.FormatFloat(seconds, 'f', 1, 64), 1)
}

// UnknownNode is the 404 every node door gives for a node this caller does not own, or that does not exist.
func (s Sentences) UnknownNode(nodeID string) string {
	return strings.Replace(s.unknownNode, "\x00nodeId\x00", nodeID, 1)
}

// Envelope is the farm's error envelope (ApiResponse.ErrorResponse) around message, as ASP.NET writes it.
func Envelope(message string) string {
	encoded, _ := json.Marshal(message)
	return `{"success":false,"data":null,"message":` + string(encoded) + `,"errors":null}`
}

// Read reads the farm's sentences, or skips the test when the farm's source is not beside this repository.
func Read(t testing.TB) Sentences {
	t.Helper()
	nodes := readSource(t, ServerSource(t, FarmNodeRefusals))
	registry := readSource(t, ServerSource(t, FarmRegistryOptions))
	tunnels := readSource(t, ServerSource(t, FarmTunnelLimits))
	ownership := readSource(t, ServerSource(t, FarmNodeOwnership))

	maxNodes := option(t, registry, "MaxNodesPerWorkspace")
	days := option(t, registry, "WorkspaceNodesCountedDays")
	maxDevices := option(t, registry, "MaxDevicesPerWorkspaceNode")
	minSeconds := option(t, registry, "MinHeartbeatIntervalSeconds")
	maxTunnels := option(t, tunnels, "MaxTunnelsPerWorkspace")

	sentence := func(source, name string, holes map[string]string) string {
		t.Helper()
		text, err := Member(source, name, holes)
		if err != nil {
			t.Fatalf("the farm's source is beside this repository and its %s could not be read: %v", name, err)
		}
		return text
	}
	return Sentences{
		TooManyMachines:   sentence(nodes, "TooManyMachinesMessage", map[string]string{"max": maxNodes, "days": days}),
		TooManyDevices:    sentence(nodes, "TooManyDevicesMessage", map[string]string{"max": maxDevices}),
		TooManyTunnels:    sentence(nodes, "TooManyTunnelsMessage", map[string]string{"max": maxTunnels}),
		HeartbeatInFlight: sentence(nodes, "HeartbeatInFlightMessage", nil),
		RegistryBusy:      sentence(nodes, "ContendedHeartbeatMessage", nil),
		LeftHeartbeat:     sentence(nodes, "LeftHeartbeatMessage", nil),
		tooSoon: sentence(nodes, "HeartbeatTooSoonMessage",
			map[string]string{"minSeconds": minSeconds, "since.TotalSeconds:0.0": "\x00since\x00"}),
		unknownNode: sentence(ownership, "RefusalMessage", map[string]string{"nodeId": "\x00nodeId\x00"}),
	}
}

func readSource(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the farm's source is beside this repository and %s cannot be read: %v", filepath.Base(path), err)
	}
	return string(data)
}

// option is the default one of the farm's options classes gives a property: `public int Name { get; set; } = 10;`.
func option(t testing.TB, source, name string) string {
	t.Helper()
	m := regexp.MustCompile(`\bpublic int ` + name + ` \{ get; set; \} = (\d+);`).FindStringSubmatch(source)
	if m == nil {
		t.Fatalf("the farm's options no longer give %s a default this reader can see", name)
	}
	return m[1]
}

// Member is the text of the C# member `name` — a constant, or an expression-bodied method — built from the string literals
// it concatenates, with each interpolation hole filled from holes (keyed by the hole's text, its format included). A hole
// with no value, or a member with no literal, is an error: a sentence half-read is not a sentence.
func Member(source, name string, holes map[string]string) (string, error) {
	at := regexp.MustCompile(`\b(?:const|static) string ` + regexp.QuoteMeta(name) + `\b[^=;]*?(=>|=)`).FindStringIndex(source)
	if at == nil {
		return "", fmt.Errorf("no member named %s", name)
	}
	var out strings.Builder
	literals := 0
	for i := at[1]; i < len(source); {
		switch {
		case source[i] == ';':
			if literals == 0 {
				return "", fmt.Errorf("%s holds no string literal", name)
			}
			return out.String(), nil
		case strings.HasPrefix(source[i:], `$"`) || source[i] == '"':
			interpolated := source[i] == '$'
			if interpolated {
				i++
			}
			text, next, err := literal(source, i+1, interpolated, holes)
			if err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			out.WriteString(text)
			literals++
			i = next
		default:
			i++
		}
	}
	return "", fmt.Errorf("%s never ends", name)
}

// literal reads one C# string literal from its first character to its closing quote, and returns its text and where
// reading goes on.
func literal(source string, i int, interpolated bool, holes map[string]string) (string, int, error) {
	var out strings.Builder
	for i < len(source) {
		c := source[i]
		switch {
		case c == '"':
			return out.String(), i + 1, nil
		case c == '\\' && i+1 < len(source):
			switch source[i+1] {
			case '"', '\\', '\'':
				out.WriteByte(source[i+1])
			case 'n':
				out.WriteByte('\n')
			default:
				return "", 0, fmt.Errorf("an escape this reader does not know: %q", source[i:i+2])
			}
			i += 2
		case interpolated && strings.HasPrefix(source[i:], "{{"):
			out.WriteByte('{')
			i += 2
		case interpolated && strings.HasPrefix(source[i:], "}}"):
			out.WriteByte('}')
			i += 2
		case interpolated && c == '{':
			end := strings.IndexByte(source[i:], '}')
			if end < 0 {
				return "", 0, errors.New("an interpolation hole that never closes")
			}
			hole := source[i+1 : i+end]
			value, ok := holes[hole]
			if !ok {
				return "", 0, fmt.Errorf("the hole {%s} has no value here", hole)
			}
			out.WriteString(value)
			i += end + 1
		default:
			out.WriteByte(c)
			i++
		}
	}
	return "", 0, errors.New("a literal that never closes")
}
