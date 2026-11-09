// Package userq provides User Questions, matching interaction/user-questions.
// Ask collects selections or custom input through an interchangeable provider.
// The synchronous test stub returns defaults; production SDKs inject real UI or
// callbacks. Multi-selection and intent labels keep decision logic independent
// of the presentation backend.
package userq

import (
	"context"
	"fmt"
)

// QuestionOptions describes a question and its options.
type QuestionOptions struct {
	// Prompt is the user-facing question.
	Prompt string
	// Choices lists candidate answers.
	Choices []string
	// DefaultIndex is the default selection; -1 means none.
	DefaultIndex int
	// MultiSelect permits multiple selected indices.
	MultiSelect bool
	// Intent labels the question, such as approval, plan-exit, or goal-blocker.
	Intent string
}

// QuestionResult contains a response.
type QuestionResult struct {
	// SelectedIndex is the selected option, or the first in a multi-selection.
	SelectedIndex int
	// Custom contains user-entered custom input.
	Custom string
	// SelectedIndices contains all multi-selected indices.
	SelectedIndices []int
}

// Provider is the question backend interface.
type Provider interface {
	// Ask blocks until an answer and returns its result or error.
	Ask(ctx context.Context, opts QuestionOptions) (*QuestionResult, error)
}

// Service supports replaceable question providers.
type Service struct {
	provider Provider
}

// New creates a question service.
func New(p Provider) *Service {
	return &Service{provider: p}
}

// SetProvider replaces the provider at runtime for SDK integration.
func (s *Service) SetProvider(p Provider) {
	s.provider = p
}

// Provider returns the current provider for assertions.
func (s *Service) Provider() Provider {
	return s.provider
}

// Ask returns selection index, custom text, and error, matching the upstream signature.
func (s *Service) Ask(ctx context.Context, opts QuestionOptions) (int, string, error) {
	if s.provider == nil {
		return 0, "", fmt.Errorf("userq: no provider installed")
	}
	res, err := s.provider.Ask(ctx, opts)
	if err != nil {
		return 0, "", err
	}
	return res.SelectedIndex, res.Custom, nil
}

// AskDetailed returns the complete response, including multiple indices.
func (s *Service) AskDetailed(ctx context.Context, opts QuestionOptions) (*QuestionResult, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("userq: no provider installed")
	}
	return s.provider.Ask(ctx, opts)
}

// Synchronous test provider.

// StubProvider returns default selections synchronously for tests.
type StubProvider struct {
	// DefaultIndex is the default returned selection.
	DefaultIndex int
}

// NewStub creates a stub defaulting to the first option.
func NewStub() *StubProvider {
	return &StubProvider{DefaultIndex: 0}
}

// Ask implements Provider with a default selection and empty custom text.
func (s *StubProvider) Ask(ctx context.Context, opts QuestionOptions) (*QuestionResult, error) {
	idx := opts.DefaultIndex
	if idx < 0 || idx >= len(opts.Choices) {
		idx = s.DefaultIndex
		if idx < 0 || idx >= len(opts.Choices) {
			idx = 0
		}
	}
	selected := []int{idx}
	if opts.MultiSelect {
		// For multi-selection, the stub returns every index.
		selected = make([]int, 0, len(opts.Choices))
		for i := range opts.Choices {
			selected = append(selected, i)
		}
		idx = 0
	}
	return &QuestionResult{SelectedIndex: idx, SelectedIndices: selected}, nil
}
