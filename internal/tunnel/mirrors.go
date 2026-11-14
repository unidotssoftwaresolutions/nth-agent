package tunnel

import (
	"context"
	"errors"
	"sync"
)

// A PHONE'S MIRROR ENDS WITH ITS SESSION (WHAT_A_LEASE_CAN_ASK). The farm checks a live view's lease once, when the view
// opens, and a mirror pump here ended only on the farm's cancel, the phone going away, silence or the tunnel's end — so
// between two leases of the same phone, a viewer from the first could keep watching it into the second. Every mirror of a
// phone now ends when a session this agent started on that phone ends: by the client's own DELETE, or by the agent's
// release of a silent one. The next session's live view is opened for it afresh.

// errSessionEnded is why a pump's context ended when its phone's session ended.
var errSessionEnded = errors.New("the session on the phone ended")

// mirrorSet is which phones each session started on, and which mirror pumps run for each phone.
type mirrorSet struct {
	mu       sync.Mutex
	phonesOf map[string][]string
	pumps    map[string]map[*int]context.CancelCauseFunc
}

// sessionStarted records the phones a session that really started on this machine named.
func (m *mirrorSet) sessionStarted(id string, phones []string) {
	if len(phones) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phonesOf == nil {
		m.phonesOf = map[string][]string{}
	}
	m.phonesOf[id] = phones
}

// add counts one mirror pump for phone, until the function it gives back is called.
func (m *mirrorSet) add(phone string, cancel context.CancelCauseFunc) (done func()) {
	key := new(int)
	m.mu.Lock()
	if m.pumps == nil {
		m.pumps = map[string]map[*int]context.CancelCauseFunc{}
	}
	if m.pumps[phone] == nil {
		m.pumps[phone] = map[*int]context.CancelCauseFunc{}
	}
	m.pumps[phone][key] = cancel
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.pumps[phone], key)
		if len(m.pumps[phone]) == 0 {
			delete(m.pumps, phone)
		}
	}
}

// sessionEnded ends every mirror of the phones session id named, and forgets the session.
func (m *mirrorSet) sessionEnded(id string) {
	m.mu.Lock()
	var ends []context.CancelCauseFunc
	for _, phone := range m.phonesOf[id] {
		for _, cancel := range m.pumps[phone] {
			ends = append(ends, cancel)
		}
	}
	delete(m.phonesOf, id)
	m.mu.Unlock()
	for _, cancel := range ends {
		cancel(errSessionEnded)
	}
}

// sessionEnded is what a session's end takes with it, whoever ended it: the builds held for it (builds.go) and every
// mirror of its phone.
func (t *Tunnel) sessionEnded(id string) {
	t.dropBuilds(id)
	t.mirrors.sessionEnded(id)
}
