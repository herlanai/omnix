// Filesystem-backed key-value storage.
package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// FileKV implements Backend with files at <root>/<key> containing
// {"version":N,"data":"<base64>"}.
type FileKV struct {
	root string
}

// NewFileKV creates a file backend and its root directory if needed.
func NewFileKV(root string) (*FileKV, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FileKV{root: root}, nil
}

// fileRecord represents a stored record.
type fileRecord struct {
	Version uint64 `json:"version"`
	Data    string `json:"data"` // base64
}

// keyPath maps a key to a safe path without directory traversal.
func (f *FileKV) keyPath(key string) string {
	// Replace separators to prevent ../ traversal.
	safe := strings.ReplaceAll(key, "..", "_")
	return filepath.Join(f.root, filepath.FromSlash(safe))
}

// Get implements Backend.
func (f *FileKV) Get(ctx context.Context, key string) ([]byte, uint64, error) {
	path := f.keyPath(key)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, &ErrKeyNotFound{Key: key}
		}
		return nil, 0, err
	}
	var rec fileRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, 0, err
	}
	data, err := base64.StdEncoding.DecodeString(rec.Data)
	if err != nil {
		return nil, 0, err
	}
	return data, rec.Version, nil
}

// Put implements Backend with CAS.
func (f *FileKV) Put(ctx context.Context, key string, data []byte, expectedVersion uint64) (uint64, error) {
	path := f.keyPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}

	current, currentVersion, err := f.Get(ctx, key)
	if err != nil {
		if !IsKeyNotFound(err) {
			return 0, err
		}
		// A missing key requires expectedVersion=0 for creation.
		if expectedVersion != 0 {
			return 0, &ErrCASMismatch{Key: key, ExpectedVersion: expectedVersion, ActualVersion: 0}
		}
	} else {
		_ = current
		if currentVersion != expectedVersion {
			return 0, &ErrCASMismatch{Key: key, ExpectedVersion: expectedVersion, ActualVersion: currentVersion}
		}
	}

	rec := fileRecord{Version: expectedVersion + 1, Data: base64.StdEncoding.EncodeToString(data)}
	raw, err := json.Marshal(rec)
	if err != nil {
		return 0, err
	}
	// Write a temporary file and rename it to avoid partial records.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return expectedVersion + 1, nil
}

// Delete implements Backend.
func (f *FileKV) Delete(ctx context.Context, key string) error {
	path := f.keyPath(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns keys under the root as relative paths, preserving key separators.
func (f *FileKV) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	err := filepath.Walk(f.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(f.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	return keys, err
}
