// Session statistics match session/session-stats: fold step and tool boundaries
// into turn/step counts, model wall time, first-token latency, and tool wall time.
package telemetry

// Stats holds cumulative statistics for a log.
type Stats struct {
	Turns  int   // Distinct turns with at least one completed step.
	Steps  int   // Completed steps.
	LLMMs  int64 // Total model wall time from step/start to assistant/message.
	TTFTMs int64 // Total first-token latency.
	ToolMs int64 // Total tool call-to-result duration.
}

type openStepStat struct {
	turn       int
	start      int64
	firstToken int64 // Zero means not observed yet.
}

// StatsCollector incrementally folds statistics; it is not concurrency-safe.
type StatsCollector struct {
	stats    Stats
	lastTurn *int
	open     *openStepStat
	pending  map[string]int64
}

// NewStatsCollector creates a collector.
func NewStatsCollector() *StatsCollector {
	return &StatsCollector{pending: map[string]int64{}}
}

// StepStart records the beginning of a step.
func (c *StatsCollector) StepStart(turn int, timeMs int64) {
	c.open = &openStepStat{turn: turn, start: timeMs}
}

// FirstToken records the first nonempty token only; repeated calls are ignored.
func (c *StatsCollector) FirstToken(turn int, timeMs int64) {
	if c.open == nil || c.open.turn != turn || c.open.firstToken > 0 {
		return
	}
	c.open.firstToken = timeMs
}

// Message settles model duration and first-token latency when message assembly completes.
func (c *StatsCollector) Message(turn int, timeMs int64) {
	if c.open == nil || c.open.turn != turn {
		return
	}
	if d := timeMs - c.open.start; d > 0 {
		c.stats.LLMMs += d
	}
	if c.open.firstToken > 0 {
		if d := c.open.firstToken - c.open.start; d > 0 {
			c.stats.TTFTMs += d
		}
	}
	c.open = nil
}

// ToolCall records a tool dispatch.
func (c *StatsCollector) ToolCall(callID string, timeMs int64) {
	c.pending[callID] = timeMs
}

// ToolResult settles tool duration, ignoring results without a matching call.
func (c *StatsCollector) ToolResult(callID string, timeMs int64) {
	dispatched, ok := c.pending[callID]
	if !ok {
		return
	}
	delete(c.pending, callID)
	if d := timeMs - dispatched; d > 0 {
		c.stats.ToolMs += d
	}
}

// StepEnd closes a step and counts its turn once.
func (c *StatsCollector) StepEnd(turn int) {
	if c.lastTurn == nil || *c.lastTurn != turn {
		c.stats.Turns++
		t := turn
		c.lastTurn = &t
	}
	c.stats.Steps++
	c.open = nil
}

// Snapshot returns the current statistics.
func (c *StatsCollector) Snapshot() Stats {
	return c.stats
}
