// Package waterfall provides generic middleware chains matching core/waterfall.
// Agent, tool, approval, plan, and goal interception share an onion model: handlers
// run in registration order, may modify a shared payload, and control continuation.
// Calling next enters downstream middleware; omitting it short-circuits. Handlers
// may transform downstream errors and run post-processing after next returns.
// Each handler may call next only once; repeated calls panic.
package waterfall

import (
	"context"
	"fmt"
)

// NextFunc invokes downstream middleware and returns its error, or nil on success.
type NextFunc func() error

// Handler receives a shared mutable payload and next. Omitting next short-circuits;
// a returned error terminates the chain and is returned to Run's caller.
type Handler[T any] func(payload *T, next NextFunc) error

// Chain is an ordered middleware chain.
type Chain[T any] struct {
	handlers []Handler[T]
}

// New builds a chain in the supplied order. Even an empty chain uses a nonnil
// slice so later Use calls do not mistake it for an uninitialized zero value.
func New[T any](handlers ...Handler[T]) *Chain[T] {
	return &Chain[T]{handlers: append([]Handler[T]{}, handlers...)}
}

// Use appends middleware during construction and panics on a zero-value Chain.
// Callers must not mutate a chain concurrently with execution.
func (c *Chain[T]) Use(handlers ...Handler[T]) *Chain[T] {
	if c.handlers == nil {
		panic("waterfall: cannot Use() on a Chain built with zero value; use New()")
	}
	c.handlers = append(c.handlers, handlers...)
	return c
}

// Run starts at the first handler. Errors stop execution; omitting next
// short-circuits with the handler's own return value.
func (c *Chain[T]) Run(ctx context.Context, payload *T) error {
	if ctx == nil {
		return fmt.Errorf("waterfall: nil context")
	}
	return c.runFrom(ctx, 0, payload, nil)
}

// runFrom begins execution at the specified handler index.
func (c *Chain[T]) runFrom(ctx context.Context, index int, payload *T, terminal NextFunc) error {
	// Past the last handler, execution is complete.
	if index >= len(c.handlers) {
		if terminal != nil {
			return terminal()
		}
		return nil
	}

	current := c.handlers[index]

	// Build a next callback that advances once only.
	called := false
	var nextErr error
	next := func() error {
		if called {
			panic("waterfall: next() called more than once in a single handler")
		}
		called = true
		nextErr = c.runFrom(ctx, index+1, payload, terminal)
		return nextErr
	}

	err := current(payload, next)
	if err != nil {
		return err
	}
	// Returning nil without next successfully short-circuits the chain.
	if !called {
		return nil
	}
	return nextErr
}

// RunSafe converts chain panics into errors. Use it at external callback
// boundaries so one injected tool panic cannot crash the whole turn.
func (c *Chain[T]) RunSafe(ctx context.Context, payload *T) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("waterfall: panic in chain: %v", p)
		}
	}()
	return c.Run(ctx, payload)
}

// RunWithTerminal runs a per-call terminal inside the middleware chain without mutating it.
func (c *Chain[T]) RunWithTerminal(ctx context.Context, payload *T, terminal NextFunc) error {
	if ctx == nil {
		return fmt.Errorf("waterfall: nil context")
	}
	return c.runFrom(ctx, 0, payload, terminal)
}
