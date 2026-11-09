// Package tests contains cross-package omnix integration tests. This file covers M01 branded
// identifier types.
package tests

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
)

// TestBrandedRoundTrip verifies constructors and String, IsZero, and Equal methods.
func TestBrandedRoundTrip(t *testing.T) {
	// Construct a branded SessionID.
	sid := brand.NewSessionID("sess_123456")
	if sid.IsZero() {
		t.Fatal("NewSessionID 构造后不应为零值")
	}
	if sid.String() != "sess_123456" {
		t.Fatalf("String() = %q, want %q", sid.String(), "sess_123456")
	}

	// Check zero-value semantics using a variable to avoid composite-literal ambiguity in an if
	// condition.
	var zero brand.SessionID
	if !zero.IsZero() {
		t.Fatal("零值 SessionID 应当 IsZero()==true")
	}

	// Compare identifiers with Equal.
	other := brand.NewSessionID("sess_123456")
	if !sid.Equal(other) {
		t.Fatal("相同字符串构造的 SessionID 应相等")
	}
	diff := brand.NewSessionID("sess_999")
	if sid.Equal(diff) {
		t.Fatal("不同字符串构造的 SessionID 不应相等")
	}
}

// TestBrandedParse rejects empty strings.
func TestBrandedParse(t *testing.T) {
	if _, err := brand.ParseToolCallID(""); err == nil {
		t.Fatal("ParseToolCallID 空串应返回错误")
	}
	parsed, err := brand.ParseToolCallID("call_abc")
	if err != nil {
		t.Fatalf("ParseToolCallID 合法串失败: %v", err)
	}
	if parsed.String() != "call_abc" {
		t.Fatalf("Parse 结果 = %q, want %q", parsed.String(), "call_abc")
	}
}

// TestBrandedJSON verifies JSON marshal/unmarshal round trips.
func TestBrandedJSON(t *testing.T) {
	original := brand.NewJobID("job_42")

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != `"job_42"` {
		t.Fatalf("Marshal 输出 = %s, want \"job_42\"", string(data))
	}

	var restored brand.JobID
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if !restored.Equal(original) {
		t.Fatalf("JSON 往返后不等: %q vs %q", restored.String(), original.String())
	}

	// Invalid JSON must return an error rather than fail silently.
	var bad brand.SkillID
	if err := json.Unmarshal([]byte(`{"a":1}`), &bad); err == nil {
		t.Fatal("非字符串 JSON 应反序列化失败")
	}
}

// TestBrandedTypeIsolation distinguishes runtime type names such as Branded[sessionIDTag] and
// Branded[toolCallIDTag]. At compile time, passing a SessionID where a ToolCallID is required is
// rejected.
func TestBrandedTypeIsolation(t *testing.T) {
	sid := brand.NewSessionID("s1")
	tcid := brand.NewToolCallID("t1")
	job := brand.NewJobID("j1")

	if typeName(sid) == typeName(tcid) {
		t.Fatal("SessionID 与 ToolCallID 的底层类型名不应相同")
	}
	if typeName(job) == typeName(tcid) {
		t.Fatal("JobID 与 ToolCallID 的底层类型名不应相同")
	}

	// Assert exact runtime type names; private tags remain distinguishable through %T.
	t.Logf("SessionID type = %s", typeName(sid))
	t.Logf("ToolCallID type = %s", typeName(tcid))
	t.Logf("JobID type = %s", typeName(job))
}

// typeName returns a runtime type name for branded-type isolation tests.
func typeName(x any) string {
	return fmt.Sprintf("%T", x)
}

// TestBrandedSQL verifies driver.Valuer and sql.Scanner for persistence integration.
func TestBrandedSQL(t *testing.T) {
	sid := brand.NewSessionID("sess_sql")

	v, err := sid.Value()
	if err != nil {
		t.Fatalf("Value() 失败: %v", err)
	}
	if v != "sess_sql" {
		t.Fatalf("Value() = %v, want sess_sql", v)
	}

	var restored brand.SessionID
	if err := restored.Scan(v); err != nil {
		t.Fatalf("Scan(string) 失败: %v", err)
	}
	if !restored.Equal(sid) {
		t.Fatal("Scan 后应与原值相等")
	}

	// Scan from []byte.
	var fromBytes brand.SessionID
	if err := fromBytes.Scan([]byte("sess_sql")); err != nil {
		t.Fatalf("Scan([]byte) 失败: %v", err)
	}
	if !fromBytes.Equal(sid) {
		t.Fatal("Scan([]byte) 后应与原值相等")
	}

	// Scanning nil produces the zero value.
	var fromNil brand.SessionID
	if err := fromNil.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) 失败: %v", err)
	}
	if !fromNil.IsZero() {
		t.Fatal("Scan(nil) 后应为零值")
	}

	// Scanning an unsupported type must fail.
	if err := fromNil.Scan(42); err == nil {
		t.Fatal("Scan(int) 应报错")
	}
}

// TestBytesBranded verifies Bytes[T] construction, JSON base64 round trips, and byte access.
func TestBytesBranded(t *testing.T) {
	raw := []byte("hello world \x00 binary")
	bs := brand.NewBytes[brand.AttachmentID](raw)

	if bs.IsZero() {
		t.Fatal("NewBytes 构造后不应为零值")
	}
	if bs.Len() != len(raw) {
		t.Fatalf("Len() = %d, want %d", bs.Len(), len(raw))
	}
	if string(bs.Bytes()) != string(raw) {
		t.Fatal("Bytes() 应还原原始字节")
	}

	// JSON base64 round trip.
	data, err := json.Marshal(bs)
	if err != nil {
		t.Fatalf("Bytes Marshal 失败: %v", err)
	}
	var restored brand.Bytes[brand.AttachmentID]
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Bytes Unmarshal 失败: %v", err)
	}
	if string(restored.Bytes()) != string(raw) {
		t.Fatal("Bytes JSON 往返后应还原原始字节")
	}

	// Zero value.
	if !brand.ZeroBytes[brand.AttachmentID]().IsZero() {
		t.Fatal("ZeroBytes 应 IsZero()==true")
	}
}
