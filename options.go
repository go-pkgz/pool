package pool

import (
	"context"
	"fmt"
)

// Option represents a configuration option for WorkerGroup[T]
type Option[T any] func(*WorkerGroup[T]) error

// options creates a collection of typed options to avoid repeating type parameter
type options[T any] struct{}

// Options returns typed options builder
func Options[T any]() options[T] { //nolint:revive // no need for exporting this type
	return options[T]{}
}

// WithChunkFn sets the chunk function, converting a value to some sort of hash.
// This is used to distribute values to workers predictably.
// Default: none
func (options[T]) WithChunkFn(fn func(T) string) Option[T] {
	return func(p *WorkerGroup[T]) error {
		p.chunkFn = fn
		return nil
	}
}

// WithCompleteFn sets the complete function, called when the pool is complete.
// This is useful for cleanup or finalization tasks.
// Default: none
func (options[T]) WithCompleteFn(fn CompleteFn[T]) Option[T] {
	return func(p *WorkerGroup[T]) error {
		p.completeFn = fn
		return nil
	}
}

// WithBatchSize sets the size of the batches. This is used to send multiple values
// to workers in a single batch. This can be useful to reduce contention on worker channels.
// Default: 1 (no batching)
func (options[T]) WithBatchSize(size int) Option[T] {
	return func(p *WorkerGroup[T]) error {
		p.batchSize = size
		if size < 1 {
			return fmt.Errorf("batch size must be greater than 0")
		}
		return nil
	}
}

// WithWorkerChanSize sets the size of the worker channel. Each worker has its own channel
// to receive values from the pool and process them.
// Default: 1 (unbuffered)
func (options[T]) WithWorkerChanSize(size int) Option[T] {
	return func(p *WorkerGroup[T]) error {
		p.workerChanSize = size
		if size < 1 {
			return fmt.Errorf("worker channel size must be greater than 0")
		}
		return nil
	}
}

// WithContext sets the context for the pool. This is used to control the lifecycle of the pool.
// Default: context.Background()
func (options[T]) WithContext(ctx context.Context) Option[T] {
	return func(p *WorkerGroup[T]) error {
		p.ctx = ctx
		return nil
	}
}

// WithContinueOnError sets whether the pool should continue on error.
// Default: false
func (options[T]) WithContinueOnError() Option[T] {
	return func(p *WorkerGroup[T]) error {
		p.continueOnError = true
		return nil
	}
}
