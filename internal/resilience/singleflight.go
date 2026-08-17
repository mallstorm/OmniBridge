package resilience

import (
	"context"
	"sync"
)

type flightCall struct {
	done chan struct{}
	val  any
	err  error
}

// SingleFlight collapses concurrent control-plane refreshes with the same key.
// The first caller starts the operation; later callers wait for the same result.
// A waiting caller may cancel without cancelling the shared operation.
type SingleFlight struct {
	mu sync.Mutex
	m  map[string]*flightCall
}

func NewSingleFlight() *SingleFlight { return &SingleFlight{m: map[string]*flightCall{}} }

func (g *SingleFlight) Do(ctx context.Context, key string, fn func() (any, error)) (any, error, bool) {
	g.mu.Lock()
	if c := g.m[key]; c != nil {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err(), true
		case <-c.done:
			return c.val, c.err, true
		}
	}
	c := &flightCall{done: make(chan struct{})}
	g.m[key] = c
	g.mu.Unlock()

	go func() {
		c.val, c.err = fn()
		g.mu.Lock()
		delete(g.m, key)
		close(c.done)
		g.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err(), false
	case <-c.done:
		return c.val, c.err, false
	}
}

func (g *SingleFlight) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.m)
}
