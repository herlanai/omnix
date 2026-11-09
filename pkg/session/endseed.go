// This file implements the session/end-seed boundary marker (M20), following
// upstream session semantics. Resume replays persisted seed events, then writes
// end-seed before live work. Fork does the same after replaying its parent seed.
// Compaction uses this boundary to distinguish seed history from live events.
package session

import (
	"github.com/herlanai/omnix/pkg/brand"
)

// MarkEndSeed records the boundary after Resume/Fork seed replay and before
// live work. id identifies the session or another unique seed; optional
// parentID records the fork source in EndSeedData.ParentSession.
func MarkEndSeed(sl *SessionLog, parentID brand.SessionID) (uint64, error) {
	return sl.Append(EndSeedData{ParentSession: parentID})
}

// SeedEndIndex returns the last end-seed event index, or -1 if absent.
// SeedEndSeq returns its sequence number, or zero if absent.
func SeedEndSeq(events []SessionEvent) uint64 {
	var last uint64
	for _, ev := range events {
		if ev.Type == EventEndSeed {
			last = ev.Seq
		}
	}
	return last
}

// IsAfterEndSeed reports whether a sequence belongs to the live region after the last marker.
func IsAfterEndSeed(events []SessionEvent, seq uint64) bool {
	seed := SeedEndSeq(events)
	if seed == 0 {
		// Without an end-seed marker, all events are live.
		return true
	}
	return seq > seed
}

// EndSeedMarker returns the replayed seed length recorded in SessionHeader
// by the caller after Resume/Fork replay completes.
func EndSeedMarker(header *SessionHeader) uint64 {
	if header == nil {
		return 0
	}
	return header.SeedLength
}
