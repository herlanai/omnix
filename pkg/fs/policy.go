// Filesystem observation policy: read-before-write/edit freshness guards.
package fs

import (
	"sync"
)

// FsObservation records an authoritative version when present, or confirmed absence.
type FsObservation struct {
	Kind    string // "present" | "absent"
	Version FsVersion
}

// ObservationPolicy tracks observations per owner and target without file I/O.
// Unseen or absent targets use createIfAbsent for writes; edits fail with
// FS_NOT_OBSERVED or FS_NOT_FOUND respectively. Present observations use
// replaceIfVersion for writes and the observed version for edit guards.
type ObservationPolicy struct {
	mu    sync.Mutex
	state map[string]map[FsTargetKey]FsObservation // owner -> targetKey -> observation
}

// NewObservationPolicy creates an observation policy.
func NewObservationPolicy() *ObservationPolicy {
	return &ObservationPolicy{state: map[string]map[FsTargetKey]FsObservation{}}
}

// Record records an observation.
func (p *ObservationPolicy) Record(owner string, key FsTargetKey, obs FsObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.state[owner]
	if !ok {
		m = map[FsTargetKey]FsObservation{}
		p.state[owner] = m
	}
	m[key] = obs
}

// Decision describes a write or edit decision.
type Decision struct {
	// WriteIntent is the write guard used by WriteDecision.
	WriteIntent *FsWriteIntent
	// EditVersion is the version guard used by EditDecision.
	EditVersion *FsVersion
	// Deny rejects edits of unobserved or absent targets.
	Deny bool
	// DenyCode is the stable rejection code.
	DenyCode FsErrorCode
}

// DecideWrite uses createIfAbsent for unseen/absent targets and replaceIfVersion for present ones.
func (p *ObservationPolicy) DecideWrite(owner string, key FsTargetKey) Decision {
	p.mu.Lock()
	obs, ok := p.state[owner][key]
	p.mu.Unlock()
	if !ok {
		return Decision{WriteIntent: &FsWriteIntent{Kind: "createIfAbsent"}}
	}
	if obs.Kind == "absent" {
		return Decision{WriteIntent: &FsWriteIntent{Kind: "createIfAbsent"}}
	}
	return Decision{WriteIntent: &FsWriteIntent{Kind: "replaceIfVersion", Version: obs.Version}}
}

// DecideEdit guards present targets by version; unseen and absent targets return
// FS_NOT_OBSERVED and FS_NOT_FOUND respectively.
func (p *ObservationPolicy) DecideEdit(owner string, key FsTargetKey) Decision {
	p.mu.Lock()
	obs, ok := p.state[owner][key]
	p.mu.Unlock()
	if !ok {
		return Decision{Deny: true, DenyCode: FSErrNotObserved}
	}
	if obs.Kind == "absent" {
		return Decision{Deny: true, DenyCode: FSErrNotFound}
	}
	v := obs.Version
	return Decision{EditVersion: &v}
}

// DropOwner removes all observations for an owner during disposal or hot reload.
func (p *ObservationPolicy) DropOwner(owner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.state, owner)
}

// Apply observation policies to LocalFS.

// PolicyFS makes read-before-write/edit guards the default over LocalFS.
type PolicyFS struct {
	Local *LocalFS
	Obs   *ObservationPolicy
}

// NewPolicyFS creates a filesystem facade with observation guards.
func NewPolicyFS(local *LocalFS, obs *ObservationPolicy) *PolicyFS {
	return &PolicyFS{Local: local, Obs: obs}
}

// ReflectObservation records presence after a caller performs read or stat.
func (p *PolicyFS) ReflectObservation(owner string, t FsTarget) {
	if v, ok := p.Local.ObservedVersion(t); ok {
		p.Obs.Record(owner, t.TargetKey, FsObservation{Kind: "present", Version: v})
	}
}

// Write writes a file using observation guards.
func (p *PolicyFS) Write(owner string, t FsTarget, content string) (FsWriteOutcome, error) {
	dec := p.Obs.DecideWrite(owner, t.TargetKey)
	return p.Local.WriteText(t, content, dec.WriteIntent)
}

// Edit edits a file using observation guards.
func (p *PolicyFS) Edit(owner string, t FsTarget, edit FsEditRequest) (FsEditOutcome, error) {
	dec := p.Obs.DecideEdit(owner, t.TargetKey)
	if dec.Deny {
		return FsEditOutcome{}, fsErr(dec.DenyCode, "edit %q blocked by observation policy", t.DisplayPath)
	}
	return p.Local.EditText(t, edit, dec.EditVersion)
}
