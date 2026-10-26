package liveview

import "testing"

// A PORT SOMETHING ANSWERS ON IS NOT FREE, whatever a bind says. The composition is sealed with fakes because the case
// it exists for — a listener on 0.0.0.0:P or [::]:P that a 127.0.0.1 bind does not see (measured on macOS; see
// LoopbackFree) — would need this test binary to listen on every interface, which a Mac with its firewall on answers
// with a prompt. The real-socket half is TestTheRealCheckSeesAPortSomethingHolds.
func TestAPortSomethingAnswersOnIsNotFreeEvenWhenItBinds(t *testing.T) {
	var asked []string
	answers := func(address string) bool { asked = append(asked, "connect "+address); return true }
	binds := func(address string) bool { asked = append(asked, "bind "+address); return true }
	if loopbackFree(30123, answers, binds) {
		t.Fatalf("127.0.0.1:30123 answers a connection and was called free (asked: %v)", asked)
	}
	if len(asked) != 1 || asked[0] != "connect 127.0.0.1:30123" {
		t.Fatalf("the connect comes first, on loopback, and a port that answers is not bound at all: %v", asked)
	}
	nobody := func(string) bool { return false }
	if !loopbackFree(30123, nobody, func(string) bool { return true }) || loopbackFree(30123, nobody, nobody) {
		t.Fatal("free is: nothing answers AND it binds")
	}
}
