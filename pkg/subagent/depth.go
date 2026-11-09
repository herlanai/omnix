// Delegation depth matches subagent/src/depth.ts: roots start at zero and each
// child adds one. Use the maximum persisted and runtime depth so resumed children
// cannot lower their depth and gain extra delegation budget.
package subagent

import "errors"

// ErrInvalidDepth indicates a depth or limit that is not a nonnegative integer.
var ErrInvalidDepth = errors.New("subagent: depth must be a non-negative integer")

// ChildDepth returns the parent depth plus one.
func ChildDepth(parentDepth int) (int, error) {
	if parentDepth < 0 {
		return 0, ErrInvalidDepth
	}
	return parentDepth + 1, nil
}

// ResolveDepth uses the larger persisted or runtime depth; runtime can only deepen it.
func ResolveDepth(headerDepth, runtimeDepth int) (int, error) {
	if headerDepth < 0 || runtimeDepth < 0 {
		return 0, ErrInvalidDepth
	}
	if runtimeDepth > headerDepth {
		return runtimeDepth, nil
	}
	return headerDepth, nil
}

// CanDelegate requires depth below maxDepth; negative maxDepth means unlimited.
func CanDelegate(depth, maxDepth int) bool {
	if depth < 0 {
		return false
	}
	if maxDepth < 0 {
		return true
	}
	return depth < maxDepth
}
