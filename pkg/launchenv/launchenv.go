// Package launchenv freezes environment layers at launch, matching util/launch-environment.
// Trust order is process environment, project .env, then user .env. Windows variable
// names are case-folded.
package launchenv

import "runtime"

// Source identifies a layer in descending trust order.
type Source string

const (
	SourceProcess    Source = "process"
	SourceProjectEnv Source = "project-env"
	SourceUserEnv    Source = "user-env"
)

// sourceOrder is the fixed trust order.
var sourceOrder = []Source{SourceProcess, SourceProjectEnv, SourceUserEnv}

// Entry is a resolved environment entry.
type Entry struct {
	Value  string
	Source Source
	Path   string // Source file path; empty for process environment.
}

type layer struct {
	path   string
	values map[string]string
}

// Snapshot is an immutable launch environment.
type Snapshot struct {
	bySource map[Source]*layer
}

// LayerInput contains raw input for one layer.
type LayerInput struct {
	Source Source
	Path   string
	Values map[string]string
}

// New accepts layers in any order and resolves them in fixed trust order.
func New(layers []LayerInput) *Snapshot {
	s := &Snapshot{bySource: map[Source]*layer{}}
	for _, l := range layers {
		cp := &layer{path: l.Path, values: map[string]string{}}
		for k, v := range l.Values {
			cp.values[lookupKey(k)] = v
		}
		s.bySource[l.Source] = cp
	}
	return s
}

// Get resolves a variable across all layers in trust order.
func (s *Snapshot) Get(name string) (Entry, bool) {
	return s.GetFrom(name, sourceOrder)
}

// GetFrom resolves only the specified layers in fixed trust order.
func (s *Snapshot) GetFrom(name string, sources []Source) (Entry, bool) {
	key := lookupKey(name)
	for _, src := range sourceOrder {
		if !contains(sources, src) {
			continue
		}
		if l, ok := s.bySource[src]; ok {
			if v, ok := l.values[key]; ok {
				return Entry{Value: v, Source: src, Path: l.path}, true
			}
		}
	}
	return Entry{}, false
}

func lookupKey(name string) string {
	if runtime.GOOS == "windows" {
		return toUpper(name)
	}
	return name
}

func toUpper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

func contains(list []Source, v Source) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
