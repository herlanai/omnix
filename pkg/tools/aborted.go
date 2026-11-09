// Match the upstream session checkpoint policy for cancellation before dispatch. Return an
// isError result with a stable code before entering the tool body, rather than executing with
// partially canceled state.
package tools

import "github.com/herlanai/omnix/pkg/brand"

// AbortedBeforeDispatch is the stable code for a tool canceled before dispatch.
const AbortedBeforeDispatch = "TOOL_ABORTED_BEFORE_DISPATCH"

// AbortedBeforeDispatchMessage is the model-visible cancellation message.
const AbortedBeforeDispatchMessage = "tool call aborted before dispatch"

// AbortedBeforeDispatchResult constructs a pre-dispatch cancellation result.
func AbortedBeforeDispatchResult(callID brand.ToolCallID) *ToolCallResult {
	return &ToolCallResult{
		CallID:    callID,
		IsError:   true,
		Error:     AbortedBeforeDispatchMessage,
		ErrorCode: AbortedBeforeDispatch,
	}
}
