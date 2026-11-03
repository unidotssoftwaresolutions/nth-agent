package tunnel

import (
	"testing"
	"time"
)

// A PONG IN THE SAME CLOCK STEP AS ITS PING IS HEARD (AN_AGENT_THAT_PASSES_ON_WINDOWS). Windows' clock moves in steps of up
// to 15.6 ms; on the first windows-latest run a farm answering every ping was called dead within a second, because a pong
// that came back in the same step as its ping read as no later than it. This drives keepAlive's rule with such a clock, on
// every system.

// coarseClock reads like Windows' monotonic clock: real time rounded down to a 15.625 ms step.
type coarseClock struct{ real time.Duration }

func (c *coarseClock) read() time.Duration { return c.real - c.real%(15625*time.Microsecond) }

func TestAPongInTheSameClockStepAsItsPingIsHeard(t *testing.T) {
	const interval, within = 100 * time.Millisecond, 300 * time.Millisecond
	clock := &coarseClock{}
	v := &liveness{since: clock.read}
	v.hear() // the upgrade itself
	pings := 0
	for tick := 1; tick <= 50; tick++ { // five seconds of a healthy farm on loopback
		clock.real += interval
		ping, dead := v.tick(within)
		if dead {
			t.Fatalf("tick %d: a farm that answered every ping within 0.2 ms was called dead", tick)
		}
		if !ping {
			t.Fatalf("tick %d: the last ping was answered, so the next one goes out", tick)
		}
		pings++
		clock.real += 200 * time.Microsecond // the pong, in the same 15.6 ms step as its ping
		if clock.read() != v.sentAt {
			t.Fatalf("instrument guard: the pong must read the same instant as its ping (%v, %v)", clock.read(), v.sentAt)
		}
		v.hear()
	}
	if pings != 50 {
		t.Fatalf("one ping a tick: %d", pings)
	}
}

// …and the rule is otherwise the one it was: a ping is out until anything is heard, and a silence of within from the ping
// it met is a dead tunnel — on a coarse clock and a fine one.
func TestASilentFarmIsStillFoundDeadOnAnyClock(t *testing.T) {
	const interval, within = 100 * time.Millisecond, 300 * time.Millisecond
	for name, read := range map[string]func(*coarseClock) time.Duration{
		"coarse": (*coarseClock).read,
		"fine":   func(c *coarseClock) time.Duration { return c.real },
	} {
		clock := &coarseClock{}
		v := &liveness{since: func() time.Duration { return read(clock) }}
		v.hear()
		clock.real += interval
		if ping, _ := v.tick(within); !ping {
			t.Fatalf("%s: the first tick pings", name)
		}
		for tick := 2; ; tick++ {
			clock.real += interval
			ping, dead := v.tick(within)
			if ping {
				t.Fatalf("%s: tick %d pinged again with nothing heard", name, tick)
			}
			if dead {
				if silent := clock.real - interval; silent < within-16*time.Millisecond || silent > within+interval {
					t.Fatalf("%s: called dead after %v of silence; the bound is %v", name, silent, within)
				}
				break
			}
			if tick > 10 {
				t.Fatalf("%s: a silent farm was never found dead", name)
			}
		}
	}
	// Anything heard while a ping is out — a frame, not only its pong — counts, and the next tick pings again.
	clock := &coarseClock{}
	v := &liveness{since: clock.read}
	clock.real += interval
	v.tick(within)
	clock.real += 2 * interval
	v.hear()
	clock.real += 2 * interval
	if ping, dead := v.tick(within); !ping || dead {
		t.Fatalf("a farm heard from after the ping is alive: ping %v, dead %v", ping, dead)
	}
}
