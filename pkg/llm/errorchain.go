// This file implements the R05 errorChain parity fix, following upstream
// packages/llm/llm/src/error.ts. RenderErrorChain accepts errors or values with
// a message field and renders the full cause chain. Identical wrapper/cause
// messages are not repeated. Single-cause Unwrap and aggregate Unwrap []error
// are supported; aggregate members render as [a; b]. An active-path set
// collapses true cycles to <circular cause> while fully expanding shared causes.
package llm

import (
	"fmt"
	"reflect"
	"strings"
)

// RenderErrorChain renders arbitrary values with their full cause chain.
// Errors use Error(), falling back to the type name if empty. Other values
// prefer a Message field, then ordinary formatting. Aggregate members append
// as [a; b]; a single cause appends with a colon unless identical to its wrapper.
// Use this only for diagnostics, never routing; route on stable Kind/code values.
func RenderErrorChain(v any) string {
	path := make(map[error]bool)
	return renderChain(v, path)
}

// renderChain recursively renders causes; path detects cycles only on the active path.
func renderChain(cur any, path map[error]bool) string {
	err, ok := cur.(error)
	if !ok {
		// For non-errors, prefer a Message field before ordinary formatting.
		if s, ok2 := messageOf(cur); ok2 {
			return s
		}
		return fmt.Sprint(cur)
	}
	if path[err] {
		return "<circular cause>"
	}
	path[err] = true
	defer delete(path, err)

	msg := err.Error()
	if msg == "" {
		msg = typeNameOf(err)
	}

	// Handle the AggregateError equivalent: errors.Join with Unwrap() []error.
	if mw, ok := err.(interface{ Unwrap() []error }); ok {
		members := mw.Unwrap()
		parts := make([]string, 0, len(members))
		for _, m := range members {
			if m != nil {
				parts = append(parts, renderChain(m, path))
			}
		}
		if len(parts) == 0 {
			return msg
		}
		return msg + " [" + strings.Join(parts, "; ") + "]"
	}

	// Handle a single cause.
	if uw, ok := err.(interface{ Unwrap() error }); ok {
		cause := uw.Unwrap()
		if cause != nil {
			causeText := renderChain(cause, path)
			if causeText != "" && causeText != msg {
				return msg + ": " + causeText
			}
		}
	}

	return msg
}

// messageOf extracts a semantic Message field from a non-error struct or pointer.
func messageOf(v any) (string, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "", false
	}
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return "", false
	}
	f := rv.FieldByName("Message")
	if f.IsValid() && f.Kind() == reflect.String {
		return f.String(), true
	}
	return "", false
}

// typeNameOf returns the error type name as a fallback.
func typeNameOf(err error) string {
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Name() == "" {
		return "error"
	}
	return t.Name()
}
