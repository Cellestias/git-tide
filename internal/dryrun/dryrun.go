// Package dryrun carries the plan of a --dry-run through the context: the
// places that would change something (Git, the provider's API, files) record the
// step instead of doing it, and the command prints the plan at the end.
package dryrun

import (
	"context"
	"sync"
)

// Plan is what a dry run would have done, in order.
type Plan struct {
	mu    sync.Mutex
	steps []string
}

type key struct{}

// With returns ctx carrying p: code that finds it must not change anything.
func With(ctx context.Context, p *Plan) context.Context { return context.WithValue(ctx, key{}, p) }

// From returns the plan of ctx, or nil when this is a real run.
func From(ctx context.Context) *Plan {
	p, _ := ctx.Value(key{}).(*Plan)
	return p
}

// Add records one step.
func (p *Plan) Add(step string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, step)
}

// Steps returns a copy of the recorded steps.
func (p *Plan) Steps() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.steps...)
}

// Len is the number of recorded steps.
func (p *Plan) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.steps)
}
