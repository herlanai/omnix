// Ralph orchestration matches workflow/tool-ralph. Each round starts a fresh
// child agent for a fixed goal and receives a structured report. Continue carries
// a bounded handoff to the next round; complete succeeds, blocked needs human
// input, and exhausting maxRounds produces budget-limited.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
)

// RoundStatus identifies a round's outcome.
type RoundStatus string

const (
	StatusContinue RoundStatus = "continue"
	StatusComplete RoundStatus = "complete"
	StatusBlocked  RoundStatus = "blocked"
)

// RoundReport is a child agent's structured report.
type RoundReport struct {
	Status    RoundStatus `json:"status"`
	Summary   string      `json:"summary"`
	Evidence  string      `json:"evidence"`
	NextSteps []string    `json:"nextSteps"`
	Blocker   string      `json:"blocker"`
}

// RunStatus identifies the final Ralph-loop outcome.
type RunStatus string

const (
	RunCompleted     RunStatus = "complete"
	RunBlocked       RunStatus = "blocked"
	RunBudgetLimited RunStatus = "budget-limited"
)

// RalphResult contains the loop result.
type RalphResult struct {
	Status RunStatus
	Rounds int
	Final  RoundReport
}

// RoundRunner receives the goal and previous handoff (empty initially) and returns a round report.
type RoundRunner func(ctx context.Context, objective, priorHandoff string) (RoundReport, error)

// DefaultRalphMaxRounds is the default round limit.
const DefaultRalphMaxRounds = 256

// ValidateReport checks report consistency with its status.
func ValidateReport(r RoundReport) error {
	switch r.Status {
	case StatusComplete:
		if r.Evidence == "" || len(r.NextSteps) > 0 || r.Blocker != "" {
			return errors.New("complete 报告须有 evidence、无 nextSteps、blocker 为空")
		}
	case StatusContinue:
		if len(r.NextSteps) == 0 {
			return errors.New("continue 报告须至少含一条 nextSteps")
		}
	case StatusBlocked:
		if r.Blocker == "" {
			return errors.New("blocked 报告须给出 blocker")
		}
	default:
		return errors.New("无效的轮次状态")
	}
	return nil
}

// RunRalph uses default rounds when maxRounds is nonpositive and enforces handoff
// length when maxHandoffChars is positive.
func RunRalph(ctx context.Context, objective string, runner RoundRunner, maxRounds, maxHandoffChars int) (*RalphResult, error) {
	if maxRounds <= 0 {
		maxRounds = DefaultRalphMaxRounds
	}
	prior := ""
	var last RoundReport
	for round := 1; round <= maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report, err := runner(ctx, objective, prior)
		if err != nil {
			return nil, err
		}
		if err := ValidateReport(report); err != nil {
			return nil, err
		}
		last = report
		switch report.Status {
		case StatusComplete:
			return &RalphResult{Status: RunCompleted, Rounds: round, Final: report}, nil
		case StatusBlocked:
			return &RalphResult{Status: RunBlocked, Rounds: round, Final: report}, nil
		}
		b, _ := json.Marshal(report)
		if maxHandoffChars > 0 && len(b) > maxHandoffChars {
			return nil, errors.New("handoff 超过 maxHandoffChars")
		}
		prior = string(b)
	}
	return &RalphResult{Status: RunBudgetLimited, Rounds: maxRounds, Final: last}, nil
}
