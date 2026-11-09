// Subprocess-backed jobs run long tasks such as Bash in the background.
package jobs

import (
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/subprocess"
)

// SubprocessSpec configures a subprocess-backed job.
type SubprocessSpec struct {
	// Kind defaults to bash.
	Kind JobKind
	// Label is a single-line model-facing command label.
	Label string
	// OwnerSession binds the job to its owner (M46).
	OwnerSession brand.SessionID
	// Argv contains the executable and arguments without shell interpretation.
	Argv []string
	// Cwd is the working directory.
	Cwd string
	// StdoutMaxBytes limits in-memory output before truncation.
	StdoutMaxBytes int
	// SpillRoot stores complete streams when the limit is exceeded.
	SpillRoot string
	// GraceMs is the termination grace period.
	GraceMs int
}

// StartSubprocess starts a subprocess-backed job on the runtime.
func (r *Runtime) StartSubprocess(spec SubprocessSpec) (*Job, error) {
	kind := spec.Kind
	if kind == "" {
		kind = KindBash
	}
	rt := subprocess.NewLocal()
	handle := rt.Spawn(subprocess.SubprocessSpawnSpec{
		Argv:           spec.Argv,
		Cwd:            spec.Cwd,
		StdoutMaxBytes: spec.StdoutMaxBytes,
		SpillRoot:      spec.SpillRoot,
		GraceMs:        spec.GraceMs,
	})
	return r.Start(JobStart{
		Kind:         kind,
		Label:        spec.Label,
		OwnerSession: spec.OwnerSession,
		Run: func() *JobHooks {
			return &JobHooks{
				// Cancel the process tree and clean up orphans (M46).
				Cancel: func(reason string) { handle.Terminate() },
				Done:   translateDone(handle),
				ReadOutput: func() string {
					return handle.Collected().Stdout().ReadOutput(0).Text
				},
			}
		},
	})
}

// translateDone maps subprocess exit facts to JobOutcome.
func translateDone(handle *subprocess.SubprocessHandle) chan JobOutcome {
	ch := make(chan JobOutcome, 1)
	go func() {
		res := <-handle.Done()
		out := JobOutcome{}
		switch {
		case res.Signal != "":
			out.Status = StatusKilled
			out.Detail = "signal: " + res.Signal
		case res.ExitCode == 0:
			out.Status = StatusCompleted
		default:
			out.Status = StatusFailed
			out.Detail = "exit code: " + itoa(res.ExitCode)
		}
		ch <- out
	}()
	return ch
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
