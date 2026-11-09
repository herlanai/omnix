// Package storage provides Storage Domain KV abstractions, matching core/storage-domain.
// Backend supplies versioned Get/Put/Delete/List with file, SQLite, and memory
// implementations. Domain[T] serializes typed values and exposes consistent CAS
// semantics: writes supply an expected version and conflicts return ErrCASMismatch.
// Feedback, goal views, session sidecars, skill catalogs, and workspace registries
// share this typed persistence layer.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CAS version conflicts.

// ErrCASMismatch indicates that an expected write version differs from the stored version.
type ErrCASMismatch struct {
	Key             string
	ExpectedVersion uint64
	ActualVersion   uint64
}

// Error implements error.
func (e *ErrCASMismatch) Error() string {
	return fmt.Sprintf("storage: CAS version mismatch for key %q: expected %d, actual %d",
		e.Key, e.ExpectedVersion, e.ActualVersion)
}

// Backend interface.

// Backend provides versioned key-value storage. Creation expects version zero
// and produces version one. Other Put calls must match the current version or
// return *ErrCASMismatch.
type Backend interface {
	// Get returns data and version, or ErrKeyNotFound.
	Get(ctx context.Context, key string) (data []byte, version uint64, err error)
	// Put writes with CAS; expectedVersion=0 requests creation.
	Put(ctx context.Context, key string, data []byte, expectedVersion uint64) (version uint64, err error)
	// Delete removes a key; missing keys are not errors.
	Delete(ctx context.Context, key string) error
	// List returns keys beneath a prefix with that prefix removed.
	List(ctx context.Context, prefix string) (keys []string, err error)
}

// ErrKeyNotFound indicates a missing key.
type ErrKeyNotFound struct {
	Key string
}

// Error implements error.
func (e *ErrKeyNotFound) Error() string {
	return fmt.Sprintf("storage: key not found: %q", e.Key)
}

// IsKeyNotFound identifies missing-key errors.
func IsKeyNotFound(err error) bool {
	_, ok := err.(*ErrKeyNotFound)
	return ok
}

// IsCASMismatch identifies CAS version conflicts.
func IsCASMismatch(err error) bool {
	_, ok := err.(*ErrCASMismatch)
	return ok
}

// Typed Domain[T] persistence.

// Domain provides typed storage with a common prefix to prevent cross-domain collisions.
type Domain[T any] struct {
	backend Backend
	prefix  string
}

// NewDomain uses a prefix such as feedback/ to isolate a domain.
func NewDomain[T any](backend Backend, prefix string) *Domain[T] {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &Domain[T]{backend: backend, prefix: prefix}
}

// fullKey joins the domain prefix and domain key.
func (d *Domain[T]) fullKey(key string) string {
	return d.prefix + key
}

// Get reads and decodes a value, returning its current version.
func (d *Domain[T]) Get(ctx context.Context, key string) (T, uint64, error) {
	var zero T
	raw, version, err := d.backend.Get(ctx, d.fullKey(key))
	if err != nil {
		return zero, 0, err
	}
	if err := json.Unmarshal(raw, &zero); err != nil {
		return zero, version, fmt.Errorf("storage: unmarshal domain %q: %w", key, err)
	}
	return zero, version, nil
}

// Put serializes and CAS-writes a value using the version from Get; zero creates it.
func (d *Domain[T]) Put(ctx context.Context, key string, value T, expectedVersion uint64) (uint64, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("storage: marshal domain %q: %w", key, err)
	}
	return d.backend.Put(ctx, d.fullKey(key), raw, expectedVersion)
}

// Delete removes a domain value.
func (d *Domain[T]) Delete(ctx context.Context, key string) error {
	return d.backend.Delete(ctx, d.fullKey(key))
}

// List returns domain-relative keys.
func (d *Domain[T]) List(ctx context.Context) ([]string, error) {
	keys, err := d.backend.List(ctx, d.prefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, strings.TrimPrefix(k, d.prefix))
	}
	return out, nil
}

// Cross-backend migration.

// Migrate copies source keys to a destination backend. Conflicting destination
// keys with newer versions are skipped and recorded.
func Migrate(ctx context.Context, src, dst Backend) (migrated int, skipped []string, err error) {
	keys, err := src.List(ctx, "")
	if err != nil {
		return 0, nil, fmt.Errorf("storage: list src: %w", err)
	}
	for _, key := range keys {
		data, version, err := src.Get(ctx, key)
		if err != nil {
			if IsKeyNotFound(err) {
				continue
			}
			return migrated, skipped, fmt.Errorf("storage: get %q from src: %w", key, err)
		}
		// Skip equal or newer destination versions for idempotent migration.
		_, dstVersion, dstErr := dst.Get(ctx, key)
		if dstErr == nil && dstVersion >= version {
			skipped = append(skipped, key)
			continue
		}
		// Overwrite an older destination value through its next CAS version.
		if _, err := dst.Put(ctx, key, data, dstVersion); err != nil {
			return migrated, skipped, fmt.Errorf("storage: put %q to dst: %w", key, err)
		}
		migrated++
	}
	return migrated, skipped, nil
}
