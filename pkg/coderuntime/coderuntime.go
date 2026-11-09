// Package coderuntime defines the code-execution protocol from packages/code-runtime.
// In PTC mode a model writes a TS/Python program that calls host capabilities
// through injected namespaces such as tools. Runtime executes that program.
//
// The upstream worker-thread backend executes TypeScript in Node. This package
// provides only requests, results, failure types, and interfaces, not a language
// engine. Implementations may use subprocesses, embedded interpreters, or remote
// services while preserving run_code and child-dispatch semantics.
package coderuntime

import "context"

// BindingFunction is an asynchronous host function exposed to a program.
// Arguments and resolved results must be lossless JSON across serialization
// boundaries. Failure appears as rejection of the corresponding program call.
type BindingFunction func(ctx context.Context, args any) (any, error)

// BindingNamespace is a named global object exposed to the program, such as tools.
type BindingNamespace struct {
	// Global is a portable program identifier matching [A-Za-z_][A-Za-z0-9_]*.
	Global string
	// Functions maps exact program-visible member names to functions.
	Functions map[string]BindingFunction
	// ErrorClass optionally names the typed error class used for member rejections.
	ErrorClass string
}

// FailureKind classifies independent execution failures.
type FailureKind string

// Failure kinds match upstream CodeRunFailure.kind.
const (
	// FailException indicates a thrown exception or parsing/transformation failure.
	FailException FailureKind = "exception"
	// FailTimeout indicates expiration of an implementation-owned budget.
	FailTimeout FailureKind = "timeout"
	// FailAbort indicates cancellation of the request.
	FailAbort FailureKind = "abort"
	// FailWorkerExit indicates premature executor termination, such as OOM.
	FailWorkerExit FailureKind = "worker-exit"
	// FailInvalidOutput indicates a completion value that is not lossless JSON.
	FailInvalidOutput FailureKind = "invalid-output"
	// FailOutputLimit indicates serialized logs, values, or diagnostics exceed limits.
	FailOutputLimit FailureKind = "output-limit"
)

// RunFailure describes an execution failure.
type RunFailure struct {
	// Kind identifies the failure category.
	Kind FailureKind
	// Message provides readable details suitable for model self-correction.
	Message string
}

// RunRequest contains source code and its execution environment.
type RunRequest struct {
	// Program is source executed as an async function body, allowing top-level await/return.
	Program string
	// Bindings exposes host namespaces to the program.
	Bindings []BindingNamespace
	// Ctx cancels execution, including loops, with a hard stop and FailAbort.
	Ctx context.Context
}

// RunResult reports program failure as data rather than a Go error from Run.
// Reporting failed programs is part of normal caller handling.
type RunResult struct {
	// Value is the top-level return value, or nil on failure or absent output.
	Value any
	// Logs contains program output in order.
	Logs []string
	// Failure is nonnil when execution failed.
	Failure *RunFailure
}

// Failed reports whether execution failed.
func (r *RunResult) Failed() bool { return r.Failure != nil }

// Runtime is the interface implemented by code-execution backends.
type Runtime interface {
	// Language is an informational lowercase source-language name, not an admission gate.
	Language() string
	// Isolation describes the executor, such as worker-thread, process, or container.
	Isolation() string
	// Run executes a program and captures output; Go errors indicate contract misuse only.
	Run(req *RunRequest) (*RunResult, error)
}
