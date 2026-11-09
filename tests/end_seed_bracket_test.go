// M20 session/end-seed boundary marker tests.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// TestEndSeedBracket verifies the seed/live boundary after resume or fork.
func TestEndSeedBracket(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("s1"))

	// Append seed events to simulate replayed history.
	_, _ = sl.Append(session.UserMessageData{Content: "seed1"})
	_, _ = sl.Append(session.UserMessageData{Content: "seed2"})

	// Append end-seed after replay, without a parent session.
	if _, err := session.MarkEndSeed(sl, brand.SessionID{}); err != nil {
		t.Fatalf("MarkEndSeed 失败: %v", err)
	}

	// Live work follows the marker.
	_, _ = sl.Append(session.UserMessageData{Content: "live1"})

	seedSeq := session.SeedEndSeq(sl.Events())
	if seedSeq == 0 {
		t.Fatal("应存在 end-seed")
	}

	// Seed events precede end-seed; live events follow it.
	if !session.IsAfterEndSeed(sl.Events(), seedSeq+1) {
		t.Fatal("live1 应在 end-seed 之后")
	}
	if session.IsAfterEndSeed(sl.Events(), 1) {
		t.Fatal("seed1 不应在 end-seed 之后")
	}
}

// TestEndSeedForkParent verifies forked end-seed events carry the parent session.
func TestEndSeedForkParent(t *testing.T) {
	parentID := brand.NewSessionID("parent_s")
	sl := session.NewSessionLog(brand.NewSessionID("child_s"))

	_, err := session.MarkEndSeed(sl, parentID)
	if err != nil {
		t.Fatalf("MarkEndSeed 失败: %v", err)
	}

	events := sl.Events()
	last := events[len(events)-1]
	if last.Type != session.EventEndSeed {
		t.Fatalf("最后一条应为 end-seed: %+v", last)
	}
	d, ok := last.Data.(session.EndSeedData)
	if !ok || d.ParentSession.Raw() != "parent_s" {
		t.Fatalf("end-seed 应携带父会话: %+v", last.Data)
	}
}

// TestEndSeedHeaderLength verifies seed length in the session header.
func TestEndSeedHeaderLength(t *testing.T) {
	h := session.NewSessionHeader(brand.NewSessionID("s1"), "/ws")
	h.SeedLength = 42
	if session.EndSeedMarker(h) != 42 {
		t.Fatalf("SeedLength 分界 = %d, want 42", session.EndSeedMarker(h))
	}
}
