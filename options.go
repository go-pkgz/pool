package pool

import "context"

// Option represents a configuration option for WorkerGroup[T]
type Option[T any] func(*WorkerGroup[T])

// options creates a collection of typed options to avoid repeating type parameter
type options[T any] struct{}

// Options returns typed options builder
func Options[T any]() options[T] { //nolint:revive // no need for exporting this type
	return options[T]{}
}

// WithWorker sets the worker for the pool. This has to be a stateless worker
// that can be shared between multiple goroutines.
func (options[T]) WithWorker(w Worker[T]) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.worker = w
	}
}

// WithWorkerMaker sets the worker maker for the pool. This is used to create
// a new worker for each goroutine. This is useful when worker has to maintain state.
func (options[T]) WithWorkerMaker(m WorkerMaker[T]) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.workerMaker = m
	}
}

// WithChunkFn sets the chunk function, converting a value to some sort of hash.
// This is used to distribute values to workers predictably.
func (options[T]) WithChunkFn(fn func(T) string) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.chunkFn = fn
	}
}

// WithCompleteFn sets the complete function, called when the pool is complete.
func (options[T]) WithCompleteFn(fn CompleteFn[T]) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.completeFn = fn
	}
}

// WithBatchSize sets the size of the batches. This is used to send multiple values
// to workers in a single batch. This can be useful to reduce contention on worker channels.
func (options[T]) WithBatchSize(size int) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.batchSize = size
	}
}

// WithWorkerChanSize sets the size of the worker channel. Echo worker has its own channel
// to receive values from the pool and process them.
func (options[T]) WithWorkerChanSize(size int) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.workerChanSize = size
	}
}

// WithContext sets the context for the pool. This is used to control the lifecycle of the pool.
func (options[T]) WithContext(ctx context.Context) Option[T] {
	return func(p *WorkerGroup[T]) {
		p.ctx = ctx
	}
}

// WithContinueOnError sets whether the pool should continue on error.
func (options[T]) WithContinueOnError() Option[T] {
	return func(p *WorkerGroup[T]) {
		p.continueOnError = true
	}
}
