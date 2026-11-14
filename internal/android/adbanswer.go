package android

import (
	"bytes"
	"fmt"
	"sync"
)

// ── adb's ANSWER IS READ UP TO A BOUND (ANOTHER_ACCOUNT_AND_LIMITS) ──────────────────────────────────────────────────
//
// adb's output was read whole, bounded only by each command's time. Every command the agent runs is answered in a few
// kilobytes: `adb devices -l` with sixty-four phones is under 10 KB, a property is one line, a capture's proxy setting
// one line. An endpoint adb took for a phone could answer with as much as it can send in the probe's twenty seconds,
// and the agent held all of it, every tick. So an answer is read up to MaxAnswerBytes; past it adb is stopped, and what
// it said is not used.

// MaxAnswerBytes bounds what one adb command may print: a hundred times the largest answer a real run reads.
const MaxAnswerBytes = 1 << 20

// TooLong is said of an answer past MaxAnswerBytes.
func TooLong() error {
	return fmt.Errorf("adb printed more than %d MiB for one command, which no command this agent runs is answered with, so adb was stopped and its answer was not used", MaxAnswerBytes>>20)
}

// BoundedOutput is where adb's output goes: kept up to max bytes in all, and past that dropped and stop called once.
type BoundedOutput struct {
	mu    sync.Mutex
	kept  bytes.Buffer
	max   int
	total int
	over  bool
	stop  func()
}

// NewBoundedOutput keeps up to max bytes, and calls stop when more arrive.
func NewBoundedOutput(max int, stop func()) *BoundedOutput {
	return &BoundedOutput{max: max, stop: stop}
}

// Write keeps p while the bound holds. It never fails, so adb is stopped by stop rather than by a broken pipe.
func (b *BoundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	if b.total+len(p) > b.max {
		b.over = true
		b.kept.Reset()
		if b.stop != nil {
			b.stop()
		}
		return len(p), nil
	}
	b.total += len(p)
	b.kept.Write(p)
	return len(p), nil
}

// Over says whether the bound was passed.
func (b *BoundedOutput) Over() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

func (b *BoundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.kept.String()
}
