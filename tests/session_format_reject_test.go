// M44 session-format rejection and version tests.
package tests

import (
	"encoding/json"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// TestSessionFormatRejectUnknownEvent fails closed on unknown event types.
func TestSessionFormatRejectUnknownEvent(t *testing.T) {
	header := session.NewSessionHeader(brand.NewSessionID("s1"), "/ws")
	headerBytes, err := header.Marshal()
	if err != nil {
		t.Fatalf("header marshal 失败: %v", err)
	}

	// Accept known events.
	known, _ := json.Marshal(session.SessionEvent{
		Seq: 1, Type: session.EventUserMessage, Data: session.UserMessageData{Content: "hi"},
	})
	_, events, err := session.LoadSession(headerBytes, known)
	if err != nil {
		t.Fatalf("已知事件应通过: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("应加载 1 条事件: %d", len(events))
	}

	// An invalid type in raw JSON must produce CorruptEventError.
	unknown := []byte(`{"seq":2,"time":"2026-08-31T00:00:00Z","type":"bogus/event","data":{}}`)
	_, _, err = session.LoadSession(headerBytes, unknown)
	if err == nil {
		t.Fatal("未知事件类型应被拒绝")
	}
	if !session.IsCorruptEventError(err) {
		t.Fatalf("应为 CorruptEventError, 实际 %T", err)
	}
}

// TestSessionFormatCrossVersionReject rejects incompatible session versions.
func TestSessionFormatCrossVersionReject(t *testing.T) {
	// Construct a future-format header with version 999.
	headerBytes := []byte(`{"version":999,"id":"s_x","createdAt":"2026-08-31T00:00:00Z"}`)

	known, _ := json.Marshal(session.SessionEvent{
		Seq: 1, Type: session.EventUserMessage, Data: session.UserMessageData{Content: "hi"},
	})
	_, _, err := session.LoadSession(headerBytes, known)
	if err == nil {
		t.Fatal("不同版本的会话文件应互相拒绝")
	}
	var unsupported *session.SessionFormatUnsupportedError
	if !asUnsupported(err, &unsupported) {
		t.Fatalf("应为 SessionFormatUnsupportedError, 实际 %T", err)
	}
}

// TestSessionKnownEventTypesWhiteList verifies at least 45 known event types and valid round
// trips.
func TestSessionKnownEventTypesWhiteList(t *testing.T) {
	if len(session.KNOWN_SESSION_EVENT_TYPES) < 45 {
		t.Fatalf("白名单事件类型数 = %d, want >= 45", len(session.KNOWN_SESSION_EVENT_TYPES))
	}
	// Deserialize every known type using sampleDataFor from the M04 tests.
	header := session.NewSessionHeader(brand.NewSessionID("s2"), "/ws")
	headerBytes, _ := header.Marshal()

	for _, et := range session.AllEventTypes {
		data := sampleDataFor(et)
		if data == nil {
			t.Fatalf("缺少 %q 的样本数据", et)
		}
		line, err := json.Marshal(session.SessionEvent{Seq: 1, Time: fixedTestTime(), Type: et, Data: data})
		if err != nil {
			t.Fatalf("%q marshal 失败: %v", et, err)
		}
		if _, _, err := session.LoadSession(headerBytes, line); err != nil {
			t.Fatalf("%q 应通过白名单校验: %v", et, err)
		}
	}
}
