// Anonymous identity matches identity/anonymous-user-id: persist one random UUID
// per Harness home in .anonymous-user-id. Missing or corrupt files are recreated;
// persistence failures still return a usable ID. Results are memoized by path.
package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/herlanai/omnix/pkg/homepath"
	"github.com/herlanai/omnix/pkg/uuid"
)

// AnonymousIDFileName stores the anonymous ID.
const AnonymousIDFileName = ".anonymous-user-id"

var (
	anonMu      sync.Mutex
	anonMemo    = map[string]string{}
	uuidPattern = func(s string) bool {
		parts := strings.Split(s, "-")
		if len(parts) != 5 {
			return false
		}
		lens := []int{8, 4, 4, 4, 12}
		for i, p := range parts {
			if len(p) != lens[i] {
				return false
			}
			for _, c := range p {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
					return false
				}
			}
		}
		return true
	}
)

// GetOrCreateAnonymousID reads or creates and persists a Harness home's anonymous ID.
func GetOrCreateAnonymousID(env map[string]string) (string, error) {
	home, err := homepath.Resolve("", env)
	if err != nil {
		return "", err
	}
	file := filepath.Join(home, AnonymousIDFileName)

	anonMu.Lock()
	defer anonMu.Unlock()
	if cached, ok := anonMemo[file]; ok {
		return cached, nil
	}

	if raw, err := os.ReadFile(file); err == nil {
		if v := strings.TrimSpace(string(raw)); uuidPattern(v) {
			anonMemo[file] = v
			return v, nil
		}
	}

	id, err := uuid.NewV4()
	if err != nil {
		return "", err
	}
	// Persistence is best effort; directory or write failures do not prevent use.
	if err := os.MkdirAll(home, 0o700); err == nil {
		_ = os.WriteFile(file, []byte(id+"\n"), 0o600)
	}
	anonMemo[file] = id
	return id, nil
}
