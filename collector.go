package pool

import (
	"context"
	"iter"
)

// Collector provides synchronous access to async data from pool's response channel
type Collector[V any] struct {
	ch  chan V
	ctx context.Context
}

// NewCollector creates a new collector with a given context and buffer size for the channel
func NewCollector[V any](ctx context.Context, size int) *Collector[V] {
	return &Collector[V]{
		ch:  make(chan V, size),
		ctx: ctx,
	}
}

// Submit sends a value to the collector
func (c *Collector[V]) Submit(v V) {
	c.ch <- v
}

// Close closes the collector
func (c *Collector[V]) Close() {
	close(c.ch)
}

// Iter returns an iterator over collector values
func (c *Collector[V]) Iter() iter.Seq2[V, error] {
	return func(yield func(V, error) bool) {
		for {
			select {
			case v, ok := <-c.ch:
				if !ok {
					return
				}
				if !yield(v, nil) {
					return
				}
			case <-c.ctx.Done():
				var zero V
				yield(zero, c.ctx.Err())
				return
			}
		}
	}
}

// All gets all data from the collector
func (c *Collector[V]) All() (res []V, err error) {
	for v, err := range c.Iter() {
		if err != nil {
			return res, err
		}
		res = append(res, v)
	}
	return res, nil
}
