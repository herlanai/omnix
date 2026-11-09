// M45 storage-domain KV tests cover CAS consistency and cross-backend migration.
package tests

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/herlanai/omnix/pkg/storage"
)

// makeBackends constructs file, SQLite, and memory backends and returns SQLite cleanup.
func makeBackends(t *testing.T) (fileKV, sqliteKV, memKV storage.Backend, cleanup func()) {
	t.Helper()
	dir := t.TempDir()

	fk, err := storage.NewFileKV(filepath.Join(dir, "filekv"))
	if err != nil {
		t.Fatalf("NewFileKV 失败: %v", err)
	}
	sk, err := storage.NewSQLiteKV(filepath.Join(dir, "sqlite.db"))
	if err != nil {
		t.Fatalf("NewSQLiteKV 失败: %v", err)
	}
	return fk, sk, storage.NewMemoryKV(), func() {
		_ = sk.Close()
	}
}

// TestStorageCASMismatchConsistent compares CAS conflict behavior across backends.
func TestStorageCASMismatchConsistent(t *testing.T) {
	fileKV, sqliteKV, memKV, cleanup := makeBackends(t)
	defer cleanup()
	ctx := context.Background()

	for name, backend := range map[string]storage.Backend{
		"filekv":   fileKV,
		"sqlitekv": sqliteKV,
		"memorykv": memKV,
	} {
		t.Run(name, func(t *testing.T) {
			// Create a record.
			v1, err := backend.Put(ctx, "k1", []byte("a"), 0)
			if err != nil {
				t.Fatalf("新建失败: %v", err)
			}
			if v1 != 1 {
				t.Fatalf("首次版本应为 1: %d", v1)
			}

			// Updating with the wrong version must conflict.
			_, err = backend.Put(ctx, "k1", []byte("b"), 99)
			if !storage.IsCASMismatch(err) {
				t.Fatalf("应返回 ErrCASMismatch, 实际 %v", err)
			}
			var mismatch *storage.ErrCASMismatch
			if !asCASMismatch(err, &mismatch) {
				t.Fatalf("错误类型应为 *ErrCASMismatch: %T", err)
			}
			if mismatch.ExpectedVersion != 99 || mismatch.ActualVersion != 1 {
				t.Fatalf("CAS 字段异常: %+v", mismatch)
			}

			// Update with the correct version.
			v2, err := backend.Put(ctx, "k1", []byte("b"), 1)
			if err != nil {
				t.Fatalf("正确版本更新失败: %v", err)
			}
			if v2 != 2 {
				t.Fatalf("第二次版本应为 2: %d", v2)
			}

			// expectedVersion=0 requests creation and must conflict with an existing key.
			if _, err := backend.Put(ctx, "k1", []byte("c"), 0); !storage.IsCASMismatch(err) {
				t.Fatalf("对已存在 key 用 0 新建应冲突: %v", err)
			}

			// Read the record.
			data, version, err := backend.Get(ctx, "k1")
			if err != nil {
				t.Fatalf("Get 失败: %v", err)
			}
			if string(data) != "b" || version != 2 {
				t.Fatalf("读取结果异常: %q v%d", data, version)
			}
		})
	}
}

// asCASMismatch performs a convenient type assertion.
func asCASMismatch(err error, target **storage.ErrCASMismatch) bool {
	e, ok := err.(*storage.ErrCASMismatch)
	if ok {
		*target = e
	}
	return ok
}

// TestStorageTypedDomain verifies typed Domain[T] reads, writes, and CAS.
func TestStorageTypedDomain(t *testing.T) {
	_, sqliteKV, _, cleanup := makeBackends(t)
	defer cleanup()
	ctx := context.Background()

	type feedback struct {
		Rating int    `json:"rating"`
		Note   string `json:"note"`
	}

	domain := storage.NewDomain[feedback](sqliteKV, "feedback")

	// Create a record.
	if _, err := domain.Put(ctx, "msg_1", feedback{Rating: 5, Note: "good"}, 0); err != nil {
		t.Fatalf("新建失败: %v", err)
	}
	// Read the record.
	fb, version, err := domain.Get(ctx, "msg_1")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if fb.Rating != 5 || fb.Note != "good" {
		t.Fatalf("读取类型化值异常: %+v", fb)
	}

	// Update through CAS.
	if _, err := domain.Put(ctx, "msg_1", feedback{Rating: 4}, version); err != nil {
		t.Fatalf("CAS 更新失败: %v", err)
	}
	// CAS conflict.
	if _, err := domain.Put(ctx, "msg_1", feedback{Rating: 1}, version); !storage.IsCASMismatch(err) {
		t.Fatalf("过期版本更新应冲突: %v", err)
	}

	// List isolates the domain prefix.
	if _, err := domain.Put(ctx, "msg_2", feedback{Rating: 3}, 0); err != nil {
		t.Fatalf("msg_2 新建失败: %v", err)
	}
	keys, err := domain.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("域内 key 数 = %d, want 2: %v", len(keys), keys)
	}
}

// TestStorageMigration verifies idempotent file-to-SQLite migration.
func TestStorageMigration(t *testing.T) {
	fileKV, sqliteKV, _, cleanup := makeBackends(t)
	defer cleanup()
	ctx := context.Background()

	// Write three source keys, including one with a nested prefix.
	_, _ = fileKV.Put(ctx, "a", []byte("1"), 0)
	_, _ = fileKV.Put(ctx, "b/x", []byte("2"), 0)
	_, _ = fileKV.Put(ctx, "c", []byte("3"), 0)

	migrated, skipped, err := storage.Migrate(ctx, fileKV, sqliteKV)
	if err != nil {
		t.Fatalf("Migrate 失败: %v", err)
	}
	if migrated != 3 {
		t.Fatalf("迁移数量 = %d, want 3", migrated)
	}
	if len(skipped) != 0 {
		t.Fatalf("首次迁移不应跳过: %v", skipped)
	}

	// Verify identical data in the destination.
	for _, key := range []string{"a", "b/x", "c"} {
		data, _, err := sqliteKV.Get(ctx, key)
		if err != nil {
			t.Fatalf("目标端读取 %q 失败: %v", key, err)
		}
		if key == "a" && string(data) != "1" {
			t.Fatalf("迁移后数据不一致: %q", data)
		}
	}

	// Repeating migration skips entries whose destination versions are already current.
	migrated2, skipped2, err := storage.Migrate(ctx, fileKV, sqliteKV)
	if err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
	if migrated2 != 0 || len(skipped2) != 3 {
		t.Fatalf("二次迁移应全部跳过: migrated=%d skipped=%v", migrated2, skipped2)
	}
}

// TestStorageKeyNotFound returns ErrKeyNotFound for absent keys.
func TestStorageKeyNotFound(t *testing.T) {
	_, _, memKV, cleanup := makeBackends(t)
	defer cleanup()
	ctx := context.Background()

	if _, _, err := memKV.Get(ctx, "missing"); !storage.IsKeyNotFound(err) {
		t.Fatalf("应返回 ErrKeyNotFound, 实际 %v", err)
	}
}
