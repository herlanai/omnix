// This file implements request-header snapshots and request/context routing
// (M19), following upstream session/request-header. EpochHeader records config,
// system, and tool generations; RequestContext records routing and rebuild
// reason. RebuildFromHeader restores prompt/schema inputs after compaction
// without relying on original events; fields should match the sent payload.
package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Reason identifies why a request is rebuilt.
type Reason string

// Request rebuild reasons.
const (
	ReasonInitial Reason = "initial"
	ReasonResume  Reason = "resume"
	ReasonChange  Reason = "change"
	ReasonSeries  Reason = "series"
)

// EpochHeader is the request/header snapshot.
type EpochHeader struct {
	// ConfigEpoch increases when settings change.
	ConfigEpoch uint64 `json:"configEpoch"`
	// SystemHash is the stable system-prompt hash.
	SystemHash string `json:"systemHash"`
	// ToolCount records tool count as a tool-generation signal.
	ToolCount int `json:"toolCount"`
}

// RequestContext records request routing information.
type RequestContext struct {
	// Provider identifies the model provider.
	Provider string `json:"provider"`
	// Model names the model.
	Model string `json:"model"`
	// ContextWindow specifies context capacity.
	ContextWindow int `json:"contextWindow,omitempty"`
	// Reason identifies the rebuild trigger.
	Reason Reason `json:"reason,omitempty"`
}

// RequestHeaderSnapshot contains the complete request-rebuild inputs.
type RequestHeaderSnapshot struct {
	// Header contains the epoch snapshot.
	Header EpochHeader `json:"header"`
	// Context contains routing information.
	Context RequestContext `json:"context"`
	// SystemPrompt contains text used for rebuilding.
	SystemPrompt string `json:"systemPrompt,omitempty"`
	// ToolSchemas contains tool-schema JSON used for rebuilding.
	ToolSchemas json.RawMessage `json:"toolSchemas,omitempty"`
}

// FoldRequestContext returns the latest request/context state.
func FoldRequestContext(events []SessionEvent) (RequestContext, bool) {
	var ctx RequestContext
	ok := false
	for _, ev := range events {
		if ev.Type != EventRequestContext {
			continue
		}
		if d, isData := ev.Data.(RequestContextData); isData {
			ctx = RequestContext{
				Provider:      d.Provider,
				Model:         d.Model,
				ContextWindow: d.Window,
				Reason:        Reason(d.Reason),
			}
			ok = true
		}
	}
	return ctx, ok
}

// FoldEpochHeader returns the latest request/header snapshot.
func FoldEpochHeader(events []SessionEvent) (EpochHeader, bool) {
	var h EpochHeader
	ok := false
	for _, ev := range events {
		if ev.Type != EventRequestHeader {
			continue
		}
		if d, isData := ev.Data.(RequestHeaderData); isData {
			h = EpochHeader{ConfigEpoch: d.ConfigEpoch, SystemHash: d.SystemHash, ToolCount: d.ToolCount}
			ok = true
		}
	}
	return h, ok
}

// RebuildFromHeader restores prompt and schema text from the snapshot.
// Both fields should match the corresponding sent payload fields.
func RebuildFromHeader(snap *RequestHeaderSnapshot) (systemPrompt string, toolSchemas json.RawMessage, err error) {
	if snap == nil {
		return "", nil, nil
	}
	return snap.SystemPrompt, snap.ToolSchemas, nil
}

// SnapshotHash detects snapshot drift by concatenating key fields in this simplified implementation.
func (s *RequestHeaderSnapshot) SnapshotHash() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d", s.Header.ConfigEpoch))
	sb.WriteString("|")
	sb.WriteString(s.Header.SystemHash)
	sb.WriteString("|")
	sb.WriteString(fmt.Sprintf("%d", s.Header.ToolCount))
	sb.WriteString("|")
	sb.WriteString(s.Context.Provider)
	sb.WriteString("|")
	sb.WriteString(s.Context.Model)
	sb.WriteString("|")
	sb.WriteString(s.SystemPrompt)
	return sb.String()
}
