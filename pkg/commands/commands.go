// Package commands implements slash commands, matching interaction/commands.
// Definitions describe commands such as /plan off; ctx.commands registers, lists,
// and dispatches them. Execution emits command/run and command/done rather than
// ordinary user messages. For example, /plan off directly emits plan/mode(off).
package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/registry"
	"github.com/herlanai/omnix/pkg/session"
)

// CommandHandler receives arguments and a log, writes events, and returns output.
type CommandHandler func(ctx context.Context, args string, sl *session.SessionLog) (output string, err error)

// CommandDefinition defines a slash command.
type CommandDefinition struct {
	// Name excludes the slash prefix, for example plan.
	Name string
	// Description is shown in help.
	Description string
	// Handler executes the command.
	Handler CommandHandler
}

// Registry uses a freezable shared registry with immutable snapshots after Freeze (H07).
type Registry struct {
	cmds *registry.Freezable[string, *CommandDefinition]
}

// NewRegistry creates a registry with the built-in plan and goal commands.
func NewRegistry() *Registry {
	r := &Registry{cmds: registry.NewFreezable[string, *CommandDefinition]()}
	_ = r.cmds.Put("plan", &CommandDefinition{Name: "plan", Description: "切换计划模式 (on/off)", Handler: handlePlan})
	_ = r.cmds.Put("goal", &CommandDefinition{Name: "goal", Description: "设置目标", Handler: handleGoal})
	return r
}

// Register adds a command or returns ErrFrozen after freezing.
func (r *Registry) Register(def *CommandDefinition) error {
	if def == nil || def.Name == "" {
		return fmt.Errorf("commands: name required")
	}
	return r.cmds.Put(def.Name, def)
}

// Freeze irreversibly enables immutable snapshot reads and rejects writes with ErrFrozen.
func (r *Registry) Freeze() { r.cmds.Freeze() }

// IsFrozen reports whether the registry is frozen.
func (r *Registry) IsFrozen() bool { return r.cmds.IsFrozen() }

// List returns command names in lexical order.
func (r *Registry) List() []string {
	names := r.cmds.Keys()
	sort.Strings(names)
	return names
}

// Get looks up a command, using immutable snapshots after freezing.
func (r *Registry) Get(name string) (*CommandDefinition, bool) {
	return r.cmds.Get(name)
}

// Dispatch parses /name args and runs the command, or returns ErrUnknownCommand.
func (r *Registry) Dispatch(ctx context.Context, raw string, sl *session.SessionLog) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '/' {
		return "", fmt.Errorf("commands: not a command: %q", raw)
	}
	line := trimmed[1:]
	parts := strings.SplitN(line, " ", 2)
	name := strings.TrimSpace(parts[0])
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}

	def, ok := r.Get(name)
	if !ok {
		return "", ErrUnknownCommand(name)
	}

	// Emit command/run.
	_, _ = sl.Append(session.CommandRunData{Command: "/" + name, Args: args})

	// Execute the handler.
	output, err := def.Handler(ctx, args, sl)
	if err != nil {
		return "", err
	}

	// Emit command/done.
	_, _ = sl.Append(session.CommandDoneData{Command: "/" + name})
	return output, nil
}

// ErrUnknownCommand indicates an unregistered command.
type ErrUnknownCommand string

func (e ErrUnknownCommand) Error() string {
	return fmt.Sprintf("commands: unknown command %q", string(e))
}

// handlePlan emits plan/mode(off|on) for /plan, rather than user/message.
func handlePlan(ctx context.Context, args string, sl *session.SessionLog) (string, error) {
	mode := "off"
	if args == "on" {
		mode = "on"
	}
	if _, err := sl.Append(session.PlanModeData{Mode: mode}); err != nil {
		return "", err
	}
	return "plan mode = " + mode, nil
}

// handleGoal emits goal/change with a CAS revision derived from the fold.
func handleGoal(ctx context.Context, args string, sl *session.SessionLog) (string, error) {
	fold := session.FoldGoalChange(sl.Events())
	revision := fold.Revision + 1
	desc := args
	if desc == "" {
		desc = "(空目标)"
	}
	if _, err := sl.Append(session.GoalChangeData{
		GoalID:      brand.NewSessionID("g_" + itoa64(revision)).Raw(),
		Phase:       "active",
		Description: desc,
		MaxRounds:   5,
		Revision:    revision,
	}); err != nil {
		return "", err
	}
	return "goal set: " + desc, nil
}

// itoa64 converts a uint64 to a string.
func itoa64(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
