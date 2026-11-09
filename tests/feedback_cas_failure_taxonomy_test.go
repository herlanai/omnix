// S09 feedback tests cover rating/note/version/timestamps, VERSION_CONFLICT on CAS mismatch,
// NOT_FOUND for missing messages, and SESSION_NOT_FOUND for missing sessions.
package tests

import (
	"context"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/feedback"
	"github.com/herlanai/omnix/pkg/storage"
)

// TestFeedbackWriteRead verifies reading and writing with incrementing CAS versions.
func TestFeedbackWriteRead(t *testing.T) {
	ctx := context.Background()
	store := feedback.New(storage.NewMemoryKV())
	sid := brand.NewSessionID("s1")

	v1, err := store.Put(ctx, sid, "msg1", feedback.RatingThumbsUp, "great", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v1 != 1 {
		t.Fatalf("首版应为 1, 实际 %d", v1)
	}
	fb, err := store.Get(ctx, sid, "msg1")
	if err != nil {
		t.Fatal(err)
	}
	if fb.Rating != feedback.RatingThumbsUp || fb.Note != "great" {
		t.Fatalf("读取失准: %+v", fb)
	}
	if fb.Version != 1 {
		t.Fatalf("version 应 1, 实际 %d", fb.Version)
	}
	// An unconstrained update advances the version to two.
	v2, _ := store.Put(ctx, sid, "msg1", feedback.RatingThumbsDown, "revised", nil)
	if v2 != 2 {
		t.Fatalf("更新后版本应 2, 实际 %d", v2)
	}
}

// TestFeedbackCASConflict returns VERSION_CONFLICT on a version mismatch.
func TestFeedbackCASConflict(t *testing.T) {
	ctx := context.Background()
	store := feedback.New(storage.NewMemoryKV())
	sid := brand.NewSessionID("s1")
	if _, err := store.Put(ctx, sid, "m", feedback.RatingThumbsUp, "", nil); err != nil {
		t.Fatal(err)
	}
	// expectedVersion=0 means creation and cannot overwrite an existing record.
	exp := uint64(0)
	_, err := store.Put(ctx, sid, "m", feedback.RatingThumbsDown, "", &exp)
	if feedback.CodeOf(err) != feedback.CodeVersionConflict {
		t.Fatalf("CAS 冲突应 VERSION_CONFLICT, 实际 %v", err)
	}
}

// TestFeedbackNotFound returns NOT_FOUND for an absent message.
func TestFeedbackNotFound(t *testing.T) {
	ctx := context.Background()
	store := feedback.New(storage.NewMemoryKV())
	_, err := store.Get(ctx, brand.NewSessionID("s1"), "ghost")
	if feedback.CodeOf(err) != feedback.CodeNotFound {
		t.Fatalf("应 NOT_FOUND, 实际 %v", err)
	}
}

// TestFeedbackSessionNotFound returns SESSION_NOT_FOUND for an absent session.
func TestFeedbackSessionNotFound(t *testing.T) {
	ctx := context.Background()
	store := feedback.New(storage.NewMemoryKV())
	_, err := store.Put(ctx, brand.SessionID{}, "m", feedback.RatingThumbsUp, "", nil)
	if feedback.CodeOf(err) != feedback.CodeSessionNotFound {
		t.Fatalf("应 SESSION_NOT_FOUND, 实际 %v", err)
	}
}

// TestFeedbackListAndDelete verifies listing and session deletion.
func TestFeedbackListAndDelete(t *testing.T) {
	ctx := context.Background()
	sid := brand.NewSessionID("sX")
	other := brand.NewSessionID("sY")
	store := feedback.New(storage.NewMemoryKV())
	for _, m := range []string{"a", "b"} {
		if _, err := store.Put(ctx, sid, m, feedback.RatingThumbsUp, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Put(ctx, other, "c", feedback.RatingThumbsUp, "", nil); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, sid)
	if err != nil || len(list) != 2 {
		t.Fatalf("sX 应列出 2 条, 实际 %d (%v)", len(list), err)
	}
	if err := store.Delete(ctx, sid, "a"); err != nil {
		t.Fatal(err)
	}
	list, _ = store.List(ctx, sid)
	if len(list) != 1 {
		t.Fatalf("删除后应剩 1 条, 实际 %d", len(list))
	}
}
