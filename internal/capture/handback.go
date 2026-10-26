package capture

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/netcap"
)

// handBackSteps is THE ONE SEQUENCE every handback takes — a capture that stops, a start the phone refused, the timer,
// the farm's question, `capture disable`, the trust check putting the phone back — the node's HandBackLockedAsync minus
// what this agent never does (no CA to remove, no adb root to undo):
//
//  1. clear the phone's global proxy FIRST, so it stops sending to a capture about to go;
//  2. remove the `adb reverse` by its port;
//  3. between() — a capture's own proxy stops here;
//  4. read the phone back.
//
// A phone adb cannot reach is asked nothing more. The record is removed only once the phone reads back with no proxy of
// ours; otherwise it is kept, with what was seen written onto it — what `doctor` repeats — and the next handback tries.
func handBackSteps(ctx context.Context, st Store, adb Adb, now func() time.Time, r Record, between func()) (bool, string) {
	keep := func(observation string) (bool, string) {
		at := now()
		r.LastTried, r.LastObservation = &at, observation
		_ = st.writeRecord(r)
		return false, observation
	}
	_, stderr, exit, _ := adb(ctx, proxyClearArgs(r.Serial)...)
	if exit != 0 && adbCouldNotReach(exit, stderr) {
		if between != nil {
			between()
		}
		return keep(unreachableObservation(adbFailure(exit, stderr)))
	}
	if r.Reverse {
		_, _, _, _ = adb(ctx, reverseRemoveArgs(r.Serial, r.Port)...)
	}
	if between != nil {
		between()
	}
	value, read, failure := readProxy(ctx, adb, r.Serial)
	switch {
	case !read:
		return keep(unreachableObservation(failure))
	case isOurProxy(value, &r):
		return keep(fmt.Sprintf("its global proxy still points at this machine's capture (%s) after it was cleared", value))
	}
	if err := st.removeRecord(r.Lease); err != nil {
		return false, "this machine could not delete its record of the capture on it"
	}
	return true, ""
}

func unreachableObservation(failure string) string {
	return "the phone was not reachable over adb (" + failure + "); it may be unplugged, switched off, or no longer allowed USB debugging"
}

func readProxy(ctx context.Context, adb Adb, serial string) (value string, read bool, failure string) {
	stdout, stderr, exit, _ := adb(ctx, proxyReadArgs(serial)...)
	if exit != 0 {
		return "", false, adbFailure(exit, stderr)
	}
	return strings.TrimSpace(stdout), true, ""
}

// isOurProxy: the phone's global proxy is this agent's — the record's own value, or the phone's loopback on a port in the
// capture range. ":0", "null", nothing, and anybody else's proxy are not.
func isOurProxy(value string, r *Record) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if r != nil && strings.EqualFold(value, r.Proxy) {
		return true
	}
	host, portText, ok := strings.Cut(value, ":")
	if !ok || (host != "127.0.0.1" && !strings.EqualFold(host, "localhost")) {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && netcap.IsCapturePort(port)
}
