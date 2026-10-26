package liveview

import "sync"

// Offered is WHICH MIRRORS THIS AGENT WILL STREAM RIGHT NOW: each mirror port it declared, mapped to the phone it was
// reserved for, for every phone it currently offers. The connect loop rewrites it every tick from the rows it
// publishes; the drive tunnel reads it before it opens a single connection.
//
// ⚠️ IT IS THE AGENT'S STREAMING RULE, AND IT IS STRICTER THAN THE NODE'S. The farm's rack node streams any absolute
// upstream that is loopback (RelayStreamUpstream) — on the platform's rack, loopback holds the platform's own
// services. On a customer's machine loopback holds THEIR services: dev servers, databases, admin pages that answer
// anyone on 127.0.0.1. The stream channel is opened by whoever holds a live lease id, so "any loopback port" would
// make a lease a read on all of them. This agent streams a phone's mirror and nothing else: the port must be one it
// declared for a phone it offers, which also tells the pump WHICH phone it is watching, so the pump can end when that
// phone goes away.
//
// ⚠️ WHAT IT CANNOT CHECK: which phone the LEASE is for. The stream frame carries no device, so a lease on one phone
// can open another offered phone's mirror on the same machine — one customer's phones, never anybody else's (the
// review's finding, 2026-10-03). Binding a stream to its lease's device is the farm's to do: it holds the lease's
// descriptor, MirrorPort included, and passes the consumer's path through unread (RelayStreamEndpoint).
type Offered struct {
	mu     sync.Mutex
	byPort map[uint16]*offer
	byUdid map[string]*offer
}

type offer struct {
	udid string
	port uint16
	gone chan struct{}
}

// Offer replaces what is on offer with ports (udid → its mirror port). A phone no longer in it, or now on a
// different port, is GONE: its channel closes, and every stream of its mirror ends.
func (o *Offered) Offer(ports map[string]uint16) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byPort == nil {
		o.byPort = map[uint16]*offer{}
		o.byUdid = map[string]*offer{}
	}
	for udid, current := range o.byUdid {
		if port, still := ports[udid]; still && port == current.port {
			continue
		}
		close(current.gone)
		delete(o.byUdid, udid)
		delete(o.byPort, current.port)
	}
	for udid, port := range ports {
		if _, kept := o.byUdid[udid]; kept {
			continue
		}
		if _, taken := o.byPort[port]; taken {
			// Two phones cannot share one mirror port — the chooser skips a port it already gave another phone
			// (Chooser.givenElsewhere), so this is unreachable from the one producer; refusing it keeps a second
			// phone's screen out of the first's stream if it ever is not.
			continue
		}
		fresh := &offer{udid: udid, port: port, gone: make(chan struct{})}
		o.byUdid[udid] = fresh
		o.byPort[port] = fresh
	}
}

// Mirror answers whose mirror a port is: the phone, and a channel closed when that phone stops being offered.
func (o *Offered) Mirror(port uint16) (udid string, gone <-chan struct{}, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	found, ok := o.byPort[port]
	if !ok {
		return "", nil, false
	}
	return found.udid, found.gone, true
}

// Count is how many mirrors are on offer.
func (o *Offered) Count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.byPort)
}
