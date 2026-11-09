// Package approval implements approval policies, matching packages/core/approval (M27).
//
// Policies are allow-all, deny-all, ask-dangerous, and ask-dangerous-tool-edit.
// Resolution applies preset, user, then session overrides; the nearest scope wins
// and Source records the effective layer for auditing. Approval grants apply to
// one tool call only: subsequent calls must ask again. Evaluate supplies the
// Allow/Deny/Ask decision consumed by PreToolDecision (M22).
//
// User Questions handles prompts. Injecting the preset-to-policy mapping avoids
// a dependency cycle between approval and presets.
package approval

import (
	"context"
	"fmt"
	"sync"

	"github.com/herlanai/omnix/pkg/scope"
	"github.com/herlanai/omnix/pkg/userq"
)

// Policies and decisions.

// Policy identifies an approval policy.
type Policy string

// Approval policies, aligned with pkg/session.ApprovalPolicy.
const (
	PolicyAllowAll         Policy = "allow-all"
	PolicyDenyAll          Policy = "deny-all"
	PolicyAskDangerous     Policy = "ask-dangerous"
	PolicyAskDangerousEdit Policy = "ask-dangerous-tool-edit"
)

// EffSource identifies the layer supplying the effective policy.
type EffSource string

const (
	SourcePreset  EffSource = "preset"
	SourceUser    EffSource = "user"
	SourceSession EffSource = "session"
)

// Effective holds a resolved policy and its audit source.
type Effective struct {
	Policy Policy    `json:"policy"`
	Source EffSource `json:"source"`
}

// Decision is the three-state tool pre-execution decision used by M22.
type Decision int

// The three possible decisions.
const (
	DecideAllow Decision = iota
	DecideDeny
	DecideAsk
)

// String returns the human-readable decision.
func (d Decision) String() string {
	switch d {
	case DecideAllow:
		return "allow"
	case DecideDeny:
		return "deny"
	case DecideAsk:
		return "ask"
	default:
		return "unknown"
	}
}

// ============================================================================
// Service
// ============================================================================

// PresetPolice maps a preset name to a policy; false selects the fallback.
type PresetPolice func(preset string) (Policy, bool)

// validate resolves the injected preset mapping, failing closed to deny-all.
func (p PresetPolice) resolve(preset string) (Policy, bool) {
	if p == nil {
		return PolicyDenyAll, false
	}
	pol, ok := p(preset)
	if !ok {
		return PolicyDenyAll, false
	}
	return pol, true
}

// DangerClass classifies tools for the ask-dangerous policies.
type DangerFunc func(tool string) bool

// Request describes approval for one tool invocation.
type Request struct {
	// Tool is the tool name.
	Tool string
	// CallID uniquely identifies the invocation covered by a one-time approval.
	CallID string
	// Preset selects the preset-layer policy.
	Preset string
	// SessionID selects the session override; empty means no override.
	SessionID string
	// DangerousOverride supplies classification; nil uses the built-in list.
	DangerousOverride DangerFunc
}

// Service implements ctx.approval.
type Service struct {
	preset PresetPolice
	uq     *userq.Service

	// User-layer overrides (M03 scope), keyed by policy; nearer entries win.
	userLayer    *scope.Layer[Policy]
	sessionLayer *scope.Layer[Policy]

	// Built-in dangerous-tool list used when DangerFunc is absent.
	dangerous map[string]bool

	mu   sync.Mutex
	asks int // Total prompts issued, for test assertions.
}

// New creates the approval service with an injected preset mapping and question
// service. PresetPolice resolves the preset layer at runtime, outside scope.
func New(preset PresetPolice, uq *userq.Service) *Service {
	return &Service{
		preset:       preset,
		uq:           uq,
		userLayer:    scope.NewLayer[Policy](scope.Key("user")),
		sessionLayer: scope.NewLayer[Policy](scope.Key("session")),
		dangerous:    defaultDangerous(),
	}
}

// SetDangerous replaces the dangerous-tool list.
func (s *Service) SetDangerous(m map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dangerous = m
}

func defaultDangerous() map[string]bool {
	return map[string]bool{
		"shell":      true,
		"bash":       true,
		"subprocess": true,
		"fs_write":   true,
		"fs_edit":    true,
		"rm":         true,
	}
}

// SetUserPolicy sets the user-layer override (source=user).
func (s *Service) SetUserPolicy(p Policy) {
	s.userLayer.Unregister("policy")
	s.userLayer.Register("policy", p, 100)
}

// SetUserPolicyByKey sets the policy override for a user.
func (s *Service) SetUserPolicyByKey(userKey string, p Policy) {
	s.userLayer.Unregister(userKey)
	s.userLayer.Register(userKey, p, 100)
}

// SetSessionPolicy sets the highest-priority session override (source=session).
func (s *Service) SetSessionPolicy(sessionID string, p Policy) {
	s.sessionLayer.Unregister(sessionID)
	s.sessionLayer.Register(sessionID, p, 100)
}

// ClearSessionPolicy removes a session override.
func (s *Service) ClearSessionPolicy(sessionID string) {
	s.sessionLayer.Unregister(sessionID)
}

// AskCount returns the total prompt count for assertions.
func (s *Service) AskCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asks
}

// Resolve applies preset, user, then session policies; session has priority.
func (s *Service) Resolve(preset string, sessionID string) Effective {
	// The nearest session override wins.
	if sessionID != "" {
		if p, ok := s.sessionLayer.Get(sessionID); ok {
			return Effective{Policy: p, Source: SourceSession}
		}
	}
	// User-layer override.
	if p, ok := s.userLayer.Get("policy"); ok {
		return Effective{Policy: p, Source: SourceUser}
	}
	// Preset layer.
	if p, ok := s.preset.resolve(preset); ok {
		return Effective{Policy: p, Source: SourcePreset}
	}
	// No configured policy: fail closed to deny-all.
	return Effective{Policy: PolicyDenyAll, Source: SourcePreset}
}

// Evaluate returns an allow, deny, or ask decision for one tool call.
// Allow-all permits and deny-all rejects. The ask-dangerous policies prompt
// through User Questions for dangerous tools and allow other tools. User approval
// permits only this call; the next invocation must ask again.
func (s *Service) Evaluate(req Request) (Decision, error) {
	eff := s.Resolve(req.Preset, req.SessionID)
	switch eff.Policy {
	case PolicyAllowAll:
		return DecideAllow, nil
	case PolicyDenyAll:
		return DecideDeny, nil
	case PolicyAskDangerous, PolicyAskDangerousEdit:
		if !s.isDangerous(req.Tool, req.DangerousOverride) {
			return DecideAllow, nil
		}
		allowed, err := s.ask(req)
		if err != nil {
			// Question failures deny the call but propagate the error for auditing.
			return DecideDeny, err
		}
		if allowed {
			return DecideAllow, nil
		}
		return DecideDeny, nil
	default:
		return DecideDeny, fmt.Errorf("approval: unknown policy %q", eff.Policy)
	}
}

// isDangerous uses request classification first, then the built-in list.
func (s *Service) isDangerous(tool string, fn DangerFunc) bool {
	if fn != nil {
		return fn(tool)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dangerous[tool]
}

// ask prompts through User Questions and reports whether permission was granted.
func (s *Service) ask(req Request) (bool, error) {
	s.mu.Lock()
	s.asks++
	s.mu.Unlock()
	if s.uq == nil {
		return false, fmt.Errorf("approval: no userq service for ask on %q", req.Tool)
	}
	idx, _, err := s.uq.Ask(context.Background(), userq.QuestionOptions{
		Prompt:       fmt.Sprintf("是否准许危险工具调用 %s？", req.Tool),
		Choices:      []string{"准许", "拒绝"},
		DefaultIndex: 1, // Default to denial (fail closed).
		Intent:       "approval",
	})
	if err != nil {
		// Prompt failures deny permission and propagate the error.
		return false, err
	}
	// Allow this call only; do not persist approval in any layer.
	return idx == 0, nil
}
