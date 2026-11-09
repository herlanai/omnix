// Goal transitions follow upstream source-phase allowlists. Valid destination
// values alone are insufficient: complete is terminal and only active can become
// paused or blocked. Invalid transitions return GOAL_INVALID_TRANSITION.
package goal

// CanTransition validates transitions:
//   - active to active, paused, blocked, or complete;
//   - paused or blocked to active or complete;
//   - complete is terminal.
func CanTransition(from, to Phase) bool {
	if !to.Valid() || !from.Valid() {
		return false
	}
	if from == PhaseComplete {
		return false
	}
	switch to {
	case PhaseActive:
		// Resume accepts active, paused, or blocked; callers also check that active is not armed.
		return from == PhaseActive || from == PhasePaused || from == PhaseBlocked
	case PhasePaused, PhaseBlocked:
		// Only active goals may pause or block.
		return from == PhaseActive
	case PhaseComplete:
		// Active, paused, and blocked goals may complete.
		return from == PhaseActive || from == PhasePaused || from == PhaseBlocked
	default:
		return false
	}
}
