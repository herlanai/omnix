// Package invariant registers invariant checks, matching runtime-diagnostics/invariants.
// Development checks enforce event ordering, turn/step pairing, tool pairing, and
// goal CAS revisions before invalid state reaches persistence. Violations identify
// the owning package. Production can disable checks with SetEnabled(false).
//
// Initialize a Registry and register checks per package; Run collects all failures.
// Errors use the prefix INVARIANT [pkgName] for diagnosis.
package invariant

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Checks default to enabled for development. Production may disable them with
// SetEnabled(false). atomic.Bool provides concurrency safety without locking.
var enabled atomic.Bool

func init() {
	enabled.Store(true)
}

// SetEnabled globally enables or disables checks; production can disable them at startup.
func SetEnabled(on bool) {
	enabled.Store(on)
}

// IsEnabled returns the global invariant-checking state.
func IsEnabled() bool {
	return enabled.Load()
}

// InvariantError describes a violation and its owning package.
type InvariantError struct {
	// Pkg is the registered owner, such as pkg/session.
	Pkg string
	// Msg describes the violation.
	Msg string
}

// Error includes the owning package for diagnosis.
func (e *InvariantError) Error() string {
	return fmt.Sprintf("INVARIANT [%s]: %s", e.Pkg, e.Msg)
}

// Checker returns nil on success or an error for a violation.
type Checker func() error

// Registry groups invariant checkers by package.
type Registry struct {
	mu       sync.Mutex
	checkers map[string][]Checker // Package names mapped to registered checkers.
	ledger   []string             // Violation ledger for auditing and test assertions.
}

// NewRegistry creates an empty invariant registry.
func NewRegistry() *Registry {
	return &Registry{
		checkers: make(map[string][]Checker),
		ledger:   make([]string, 0),
	}
}

// Register adds a checker to a package. Empty package names are rejected and nil
// checkers are ignored. Packages register during initialization; the harness runs
// checks before critical writes such as Session.Append.
func (r *Registry) Register(pkgName string, checker Checker) error {
	if pkgName == "" {
		return fmt.Errorf("invariant: pkgName must not be empty")
	}
	if checker == nil {
		return fmt.Errorf("invariant: checker must not be nil for pkg %q", pkgName)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[pkgName] = append(r.checkers[pkgName], checker)
	return nil
}

// Run returns all violations in deterministic package order. Disabled checks
// return nil immediately. Checker panics become errors so other checks still run.
func (r *Registry) Run() []error {
	if !IsEnabled() {
		return nil
	}
	r.mu.Lock()
	pkgs := make([]string, 0, len(r.checkers))
	for pkg := range r.checkers {
		pkgs = append(pkgs, pkg)
	}
	// Copy checker lists so concurrent registration cannot alter this run.
	snapshot := make(map[string][]Checker, len(r.checkers))
	for pkg, list := range r.checkers {
		cp := make([]Checker, len(list))
		copy(cp, list)
		snapshot[pkg] = cp
	}
	r.mu.Unlock()

	sort.Strings(pkgs)
	var failures []error
	for _, pkg := range pkgs {
		for _, checker := range snapshot[pkg] {
			if err := runSafe(checker); err != nil {
				// Wrap ordinary errors with package ownership.
				var invErr *InvariantError
				if as, ok := err.(*InvariantError); ok {
					invErr = as
				} else {
					invErr = &InvariantError{Pkg: pkg, Msg: err.Error()}
				}
				failures = append(failures, invErr)
				r.record(pkg, invErr.Error())
			}
		}
	}
	return failures
}

// runSafe converts checker panics to errors.
func runSafe(checker Checker) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("invariant checker panicked: %v", p)
		}
	}()
	return checker()
}

// record adds the package and violation details to the ledger.
func (r *Registry) record(pkg, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ledger = append(r.ledger, detail)
}

// Ledger returns violations in registration order for auditing and assertions.
func (r *Registry) Ledger() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.ledger))
	copy(out, r.ledger)
	return out
}

// MustSatisfy panics on any violation as a development assertion. It becomes a
// no-op when checks are disabled.
func (r *Registry) MustSatisfy() {
	if !IsEnabled() {
		return
	}
	failures := r.Run()
	if len(failures) > 0 {
		msgs := make([]string, 0, len(failures))
		for _, f := range failures {
			msgs = append(msgs, f.Error())
		}
		panic("invariant violated: " + strings.Join(msgs, "; "))
	}
}
