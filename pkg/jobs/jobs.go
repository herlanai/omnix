// Package jobs implements background jobs (S11) and owner binding (M46), matching
// jobs/jobs. Producers supply kind, label, owner, and cancel/done/readOutput hooks;
// the runtime owns identity, access, and lifecycle state. Owners are session IDs.
// Disposing an agent cancels every owned job, terminating subprocess trees and
// cleaning up orphans. JobSnapshot is read-only, with ownerSession for authorization
// and association. Each job has one consuming output cursor.
package jobs

import (
	"fmt"
	"sync"

	"github.com/herlanai/omnix/pkg/brand"
)

// Types.

// JobKind is a producer-defined type treated as an opaque registry namespace.
type JobKind string

// Built-in job kinds.
const (
	KindBash     JobKind = "bash"
	KindSubagent JobKind = "subagent"
)

// JobStatus is the lifecycle state of a job.
type JobStatus string

// Lifecycle states.
const (
	StatusRunning   JobStatus = "running"
	StatusStopping  JobStatus = "stopping"
	StatusCompleted JobStatus = "completed"
	StatusKilled    JobStatus = "killed"
	StatusFailed    JobStatus = "failed"
)

// JobOutcome is the terminal result reported through Done.
type JobOutcome struct {
	// Status is completed, killed, or failed at termination.
	Status JobStatus `json:"status"`
	// Detail contains kind-specific information, such as exit code: 3.
	Detail string `json:"detail,omitempty"`
	// Output is final output for jobs without ReadOutput; streaming jobs leave it empty.
	Output string `json:"output,omitempty"`
}

// JobSnapshot is a read-only projection of one job.
type JobSnapshot struct {
	// ID is assigned by the registry as kind-N.
	ID brand.JobID `json:"id"`
	// Kind identifies the producer type.
	Kind JobKind `json:"kind"`
	// Label is a single-line model-facing label.
	Label string `json:"label"`
	// OwnerSession identifies the owner for authorization and association; empty means unowned.
	OwnerSession brand.SessionID `json:"ownerSession,omitempty"`
	// Status is the lifecycle state.
	Status JobStatus `json:"status"`
	// Detail provides state details.
	Detail string `json:"detail,omitempty"`
	// Output is currently readable output consumed through the current cursor.
	Output string `json:"output,omitempty"`
}

// JobHooks lets the runtime control and observe producer work.
type JobHooks struct {
	// Cancel requests termination with a forwarded reason; it must be synchronous,
	// idempotent, and eventually settle Done.
	Cancel func(reason string)
	// Done settles after the producer releases resources, not merely when work ends.
	Done chan JobOutcome
	// ReadOutput consumes output since the previous call; the producer formats truncation/spill details.
	ReadOutput func() string
}

// JobStart declares producer work.
type JobStart struct {
	// Kind identifies the type and ID prefix.
	Kind JobKind
	// Label is a single-line model-facing label.
	Label string
	// OwnerSession authorizes and disposes jobs; zero means unowned.
	OwnerSession brand.SessionID
	// Run returns hooks synchronously after preflight and is called once; nil means
	// startup failed without leaving a registration.
	Run func() *JobHooks
}

// Jobs and runtime.

// Job represents a background task.
type Job struct {
	// ID uniquely identifies the job.
	ID brand.JobID
	// Kind identifies the job type.
	Kind JobKind
	// Label describes the job.
	Label string
	// OwnerSession identifies the owning session.
	OwnerSession brand.SessionID

	mu     sync.Mutex
	status JobStatus
	detail string
	cancel func(reason string)
	done   chan JobOutcome
	read   func() string

	// finished closes after settlement and registry removal so owner disposal can await cleanup.
	finished chan struct{}
}

// Snapshot returns a read-only projection.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	output := ""
	if j.read != nil {
		output = j.read()
	}
	return JobSnapshot{
		ID:           j.ID,
		Kind:         j.Kind,
		Label:        j.Label,
		OwnerSession: j.OwnerSession,
		Status:       j.status,
		Detail:       j.detail,
		Output:       output,
	}
}

// ReadOutput consumes incremental output.
func (j *Job) ReadOutput() string {
	if j.read == nil {
		return ""
	}
	return j.read()
}

// Cancel requests termination idempotently.
func (j *Job) Cancel(reason string) {
	j.mu.Lock()
	done := j.cancel
	j.mu.Unlock()
	if done != nil {
		done(reason)
	}
}

// Done returns completion notification.
func (j *Job) Done() <-chan JobOutcome { return j.done }

// resolveOutcome settles the final job state.
func (j *Job) resolveOutcome(o JobOutcome) {
	j.mu.Lock()
	j.status = o.Status
	j.detail = o.Detail
	if o.Output != "" {
		// Final output for a job without ReadOutput.
		j.read = func() string { return o.Output }
	}
	j.mu.Unlock()
	select {
	case j.done <- o:
	default:
	}
}

// Runtime implements background jobs (ctx.jobs).
type Runtime struct {
	mu     sync.Mutex
	seq    int
	jobs   map[int]*Job
	byKind map[string]int // kmd-N
}

// NewRuntime creates a job runtime.
func NewRuntime() *Runtime {
	return &Runtime{jobs: map[int]*Job{}, byKind: map[string]int{}}
}

// Start starts a job.
func (r *Runtime) Start(start JobStart) (*Job, error) {
	if start.Run == nil {
		return nil, fmt.Errorf("jobs: Run hook required")
	}
	hooks := start.Run()
	if hooks == nil {
		return nil, fmt.Errorf("jobs: producer refused to start")
	}
	r.mu.Lock()
	r.seq++
	r.byKind[string(start.Kind)]++
	n := r.byKind[string(start.Kind)]
	id := brand.NewJobID(fmt.Sprintf("%s-%d", start.Kind, n))
	job := &Job{
		ID:           id,
		Kind:         start.Kind,
		Label:        start.Label,
		OwnerSession: start.OwnerSession,
		status:       StatusRunning,
		cancel:       hooks.Cancel,
		done:         hooks.Done,
		read:         hooks.ReadOutput,
		finished:     make(chan struct{}),
	}
	r.jobs[r.seq] = job
	r.mu.Unlock()

	// Settle asynchronously: map Done to job state and remove its registration.
	go func(j *Job, h *JobHooks) {
		select {
		case o := <-h.Done:
			j.resolveOutcome(o)
		}
		r.unregister(j.ID)
		close(j.finished)
	}(job, hooks)
	return job, nil
}

func (r *Runtime) unregister(id brand.JobID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, j := range r.jobs {
		if j.ID == id {
			delete(r.jobs, k)
		}
	}
}

// JobByID looks up a job by ID.
func (r *Runtime) JobByID(id brand.JobID) *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// ListByOwner returns jobs belonging to an owner (M46/S11).
func (r *Runtime) ListByOwner(owner brand.SessionID) []*Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*Job
	for _, j := range r.jobs {
		if j.OwnerSession == owner {
			out = append(out, j)
		}
	}
	return out
}

// All returns all jobs.
func (r *Runtime) All() []*Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	return out
}

// DisposeOwner cancels an agent's jobs and waits for cleanup (M46). Cancel hooks
// terminate subprocess trees; waiting for finished ensures registry removal and
// resource release before disposal returns.
func (r *Runtime) DisposeOwner(owner brand.SessionID, reason string) {
	for _, j := range r.ListByOwner(owner) {
		j.Cancel(reason)
		<-j.finished // Wait for termination and cleanup.
	}
}

// Finished returns notification that cleanup is complete.
func (j *Job) Finished() <-chan struct{} { return j.finished }
