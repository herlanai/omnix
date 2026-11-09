// Package goal implements goal state, automatic rounds, and six goal_* tools,
// matching core/goal. State persists as goal/change events with monotonic CAS
// revisions. RoundDriver continues active goals on turn-stopping until completion
// or maxRounds. Tools list goals, set phase/description/max rounds, add blockers,
// and report blockers; reporting a blocker concludes the turn.
package goal

import (
	"context"

	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

// State machine.

// Phase identifies a goal phase.
type Phase string

// The four upstream goal phases.
const (
	PhaseActive   Phase = "active"
	PhasePaused   Phase = "paused"
	PhaseBlocked  Phase = "blocked"
	PhaseComplete Phase = "complete"
)

// Valid reports whether the phase is one of the four valid GoalPhase values.
func (p Phase) Valid() bool {
	switch p {
	case PhaseActive, PhasePaused, PhaseBlocked, PhaseComplete:
		return true
	default:
		return false
	}
}

// Goal holds runtime goal state.
type Goal struct {
	ID          string   `json:"id"`
	Phase       Phase    `json:"phase"`
	Description string   `json:"description"`
	MaxRounds   int      `json:"maxRounds"`
	Revision    uint64   `json:"revision"`
	Blockers    []string `json:"blockers,omitempty"`
	// BlockReason exists only for blocked goals, matching GoalSnapshot.blockedReason.
	BlockReason *GoalBlockReason `json:"blockedReason,omitempty"`
}

// FromLog derives the latest goal using monotonically increasing CAS revisions.
func FromLog(sl *session.SessionLog) (Goal, bool) {
	fold := session.FoldGoalChange(sl.Events())
	if !fold.Present {
		return Goal{}, false
	}
	g := Goal{
		ID:          fold.GoalID,
		Phase:       phaseFrom(fold.Phase),
		Description: fold.Description,
		MaxRounds:   fold.MaxRounds,
		Revision:    fold.Revision,
	}
	// Restore the block reason from the persisted event (R06).
	if fold.BlockReason != nil {
		g.BlockReason = &GoalBlockReason{Code: fold.BlockReason.Code, Message: fold.BlockReason.Message}
	}
	return g, true
}

// toSessionBlockReason converts a domain reason to its persisted session form.
func toSessionBlockReason(r *GoalBlockReason) *session.GoalBlockReason {
	if r == nil {
		return nil
	}
	return &session.GoalBlockReason{Code: r.Code, Message: r.Message}
}

// phaseFrom converts a folded phase.
func phaseFrom(p session.GoalPhase) Phase {
	return Phase(p)
}

// writeGoal emits goal/change with the current CAS revision plus one.
func writeGoal(sl *session.SessionLog, g Goal) error {
	_, err := sl.Append(session.GoalChangeData{
		GoalID:      g.ID,
		Phase:       session.GoalPhase(g.Phase),
		Description: g.Description,
		MaxRounds:   g.MaxRounds,
		Revision:    g.Revision,
		BlockReason: toSessionBlockReason(g.BlockReason),
	})
	return err
}

// NewGoal creates a goal starting at revision one.
func NewGoal(id, desc string, maxRounds int) Goal {
	return Goal{ID: id, Phase: PhaseActive, Description: desc, MaxRounds: maxRounds, Revision: 1}
}

// Round continuation driver.

// RoundDriver decides whether active goals continue at turn-stopping.
type RoundDriver struct {
	// roundCh signals continuation to the agent loop.
	roundCh chan int
}

// NewRoundDriver creates a continuation driver.
func NewRoundDriver() *RoundDriver {
	return &RoundDriver{roundCh: make(chan int, 1)}
}

// OnTurnStopping reports whether another round should start when a turn stops.
func (d *RoundDriver) OnTurnStopping(sl *session.SessionLog, round int) bool {
	g, ok := FromLog(sl)
	// Only active goals continue automatically; paused, blocked, and complete goals stop.
	if !ok || g.Phase != PhaseActive {
		return false
	}
	// Continue while below the round limit.
	if round < g.MaxRounds {
		// Emit goal/round.
		_, _ = sl.Append(session.GoalRoundData{Round: round + 1})
		select {
		case d.roundCh <- round + 1:
		default:
		}
		return true
	}
	return false
}

// WaitRound waits for continuation or context cancellation/timeout.
func (d *RoundDriver) WaitRound(ctx context.Context) (int, error) {
	select {
	case r := <-d.roundCh:
		return r, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// The six goal_* tools.

// GoalToolset contains goal tools.
type GoalToolset struct {
	sl *session.SessionLog
}

// NewGoalToolset creates a goal toolset.
func NewGoalToolset(sl *session.SessionLog) *GoalToolset {
	return &GoalToolset{sl: sl}
}

// Tools returns all six tools for registration in the M23 pipeline.
func (g *GoalToolset) Tools() []*tools.Tool {
	return []*tools.Tool{
		{Name: "goal_list", Execute: g.goalList},
		{Name: "goal_set_phase", Execute: g.goalSetPhase},
		{Name: "goal_set_description", Execute: g.goalSetDescription},
		{Name: "goal_set_max_rounds", Execute: g.goalSetMaxRounds},
		{Name: "goal_add_blocker", Execute: g.goalAddBlocker},
		{Name: "goal_report_blocker", Execute: g.goalReportBlocker},
	}
}

func (g *GoalToolset) current() (Goal, bool) {
	return FromLog(g.sl)
}

// goalList lists goal state.
func (g *GoalToolset) goalList(ctx context.Context, input map[string]any) (any, error) {
	if cur, ok := g.current(); ok {
		return map[string]any{"goal": cur}, nil
	}
	return map[string]any{"goal": nil}, nil
}

// goalSetPhase changes the phase.
func (g *GoalToolset) goalSetPhase(ctx context.Context, input map[string]any) (any, error) {
	phase, _ := input["phase"].(string)
	cur, ok := g.current()
	if !ok {
		return nil, NewGoalError(ErrorNotFound, "no active goal", nil)
	}
	// Reject unknown phases with GOAL_INVALID_TRANSITION.
	target := Phase(phase)
	if !target.Valid() {
		return nil, NewGoalError(ErrorInvalidTransition, "invalid phase: "+phase, nil)
	}
	// Validate transitions: complete is terminal; only active can become paused or blocked.
	if !CanTransition(cur.Phase, target) {
		return nil, NewGoalError(ErrorInvalidTransition,
			"cannot transition "+string(cur.Phase)+" -> "+phase, nil)
	}
	cur.Phase = target
	cur.Revision++
	if err := writeGoal(g.sl, cur); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "phase": phase}, nil
}

// goalSetDescription changes the goal description.
func (g *GoalToolset) goalSetDescription(ctx context.Context, input map[string]any) (any, error) {
	desc, _ := input["description"].(string)
	cur, ok := g.current()
	if !ok {
		return nil, NewGoalError(ErrorNotFound, "no active goal", nil)
	}
	cur.Description = desc
	cur.Revision++
	if err := writeGoal(g.sl, cur); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "description": desc}, nil
}

// goalSetMaxRounds changes the round limit.
func (g *GoalToolset) goalSetMaxRounds(ctx context.Context, input map[string]any) (any, error) {
	rounds, _ := input["maxRounds"].(float64)
	cur, ok := g.current()
	if !ok {
		return nil, NewGoalError(ErrorNotFound, "no active goal", nil)
	}
	// Reject nonpositive limits with GOAL_INVALID_MAX_ROUNDS.
	if int(rounds) <= 0 {
		return nil, NewGoalError(ErrorInvalidMaxRounds, "maxRounds must be positive", nil)
	}
	cur.MaxRounds = int(rounds)
	cur.Revision++
	if err := writeGoal(g.sl, cur); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "maxRounds": cur.MaxRounds}, nil
}

// goalAddBlocker adds a blocker.
func (g *GoalToolset) goalAddBlocker(ctx context.Context, input map[string]any) (any, error) {
	blocker, _ := input["blocker"].(string)
	cur, ok := g.current()
	if !ok {
		return nil, NewGoalError(ErrorNotFound, "no active goal", nil)
	}
	// Require a nonempty block reason (GOAL_INVALID_BLOCK_REASON).
	if blocker == "" {
		return nil, NewGoalError(ErrorInvalidBlockReason, "blocker required", nil)
	}
	cur.Blockers = append(cur.Blockers, blocker)
	cur.Revision++
	if err := writeGoal(g.sl, cur); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "blockers": cur.Blockers}, nil
}

// goalReportBlocker reports a blocker and concludes the turn.
func (g *GoalToolset) goalReportBlocker(ctx context.Context, input map[string]any) (any, error) {
	blocker, _ := input["blocker"].(string)
	phase := PhaseBlocked
	if ph, ok := input["phase"].(string); ok && ph != "" {
		phase = Phase(ph)
	}
	cur, ok := g.current()
	if !ok {
		return nil, NewGoalError(ErrorNotFound, "no active goal", nil)
	}
	// Blocking requires an explanation (GOAL_INVALID_BLOCK_REASON).
	if blocker == "" {
		return nil, NewGoalError(ErrorInvalidBlockReason, "blocker reason required", nil)
	}
	cur.Phase = phase
	cur.Blockers = append(cur.Blockers, blocker)
	// Blocked state carries a stable BlockReason.
	if phase == PhaseBlocked {
		cur.BlockReason = &GoalBlockReason{Code: "user-reported", Message: blocker}
	}
	cur.Revision++
	if err := writeGoal(g.sl, cur); err != nil {
		return nil, err
	}
	// Conclude the turn so the agent does not start another step.
	if conclude := tools.ConcludeFrom(ctx); conclude != nil {
		conclude("goal-blocker")
	}
	out := map[string]any{"ok": true, "phase": phase, "blockers": cur.Blockers}
	if cur.BlockReason != nil {
		out["blockReason"] = cur.BlockReason
	}
	return out, nil
}
