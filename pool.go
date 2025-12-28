package pool

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/go-pkgz/pool/metrics"
)

// WorkerGroup represents a pool of workers processing items in parallel.
// Supports both direct item processing and batching modes.
type WorkerGroup[T any] struct {
	poolSize         int                 // number of workers (goroutines)
	workerChanSize   int                 // size of worker channels
	workerCompleteFn WorkerCompleteFn[T] // completion callback function, called by each worker on completion
	poolCompleteFn   GroupCompleteFn[T]  // pool-level completion callback, called once when all workers are done
	continueOnError  bool                // don't terminate on first error
	chunkFn          func(T) string      // worker selector function
	worker           Worker[T]           // worker function
	workerMaker      WorkerMaker[T]      // worker maker function

	metrics *metrics.Value // shared metrics

	workersCh     []chan T     // workers input channels
	sharedCh      chan T       // shared input channel for all workers
	activeWorkers atomic.Int32 // track number of active workers

	// batching support
	batchSize     int        // if > 0, accumulate items up to this size
	accumulators  [][]T      // per-worker accumulators for batching
	workerBatchCh []chan []T // per-worker batch channels
	sharedBatchCh chan []T   // shared batch channel

	eg        *errgroup.Group
	activated bool
	ctx       context.Context

	sendMu sync.Mutex
}

// Worker is the interface that wraps the Submit method.
type Worker[T any] interface {
	Do(ctx context.Context, v T) error
}

// WorkerFunc is an adapter to allow the use of ordinary functions as Workers.
type WorkerFunc[T any] func(ctx context.Context, v T) error

// Do calls f(ctx, v).
func (f WorkerFunc[T]) Do(ctx context.Context, v T) error { return f(ctx, v) }

// WorkerMaker is a function that returns a new Worker
type WorkerMaker[T any] func() Worker[T]

// WorkerCompleteFn called on worker completion
type WorkerCompleteFn[T any] func(ctx context.Context, id int, worker Worker[T]) error

// GroupCompleteFn called once when all workers are done
type GroupCompleteFn[T any] func(ctx context.Context) error

// Send func called by worker code to publish results
type Send[T any] func(val T) error

// New creates a worker pool with a shared worker instance.
// All goroutines share the same worker, suitable for stateless processing.
func New[T any](size int, worker Worker[T]) *WorkerGroup[T] {
	if size < 1 {
		size = 1
	}

	res := &WorkerGroup[T]{
		poolSize:       size,
		worker:         worker,
		workerChanSize: 1,
		batchSize:      10, // default batch size

		// initialize channels
		workersCh:     make([]chan T, size),
		sharedCh:      make(chan T, size),
		workerBatchCh: make([]chan []T, size),
		sharedBatchCh: make(chan []T, size),
		accumulators:  make([][]T, size),
	}

	// initialize worker's channels
	for i := range size {
		res.workersCh[i] = make(chan T, res.workerChanSize)
		res.workerBatchCh[i] = make(chan []T, res.workerChanSize)
	}

	return res
}

// NewStateful creates a worker pool where each goroutine gets its own worker instance.
// Suitable for operations requiring state (e.g., database connections).
func NewStateful[T any](size int, maker func() Worker[T]) *WorkerGroup[T] {
	if size < 1 {
		size = 1
	}

	res := &WorkerGroup[T]{
		poolSize:       size,
		workerMaker:    maker,
		workerChanSize: 1,
		batchSize:      10, // default batch size

		// initialize channels
		workersCh:     make([]chan T, size),
		sharedCh:      make(chan T, size),
		workerBatchCh: make([]chan []T, size),
		sharedBatchCh: make(chan []T, size),
		accumulators:  make([][]T, size),
	}

	// initialize worker's channels
	for i := range size {
		res.workersCh[i] = make(chan T, res.workerChanSize)
		res.workerBatchCh[i] = make(chan []T, res.workerChanSize)
	}

	return res
}

// WithWorkerChanSize sets channel buffer size for each worker.
// Larger sizes can help with bursty workloads but increase memory usage.
// Default: 1
func (p *WorkerGroup[T]) WithWorkerChanSize(size int) *WorkerGroup[T] {
	p.workerChanSize = max(size, 1)

	// recreate per-worker channels with new size
	for i := range p.poolSize {
		p.workersCh[i] = make(chan T, p.workerChanSize)
		p.workerBatchCh[i] = make(chan []T, p.workerChanSize)
	}

	// recreate shared channels with new size
	p.sharedCh = make(chan T, p.workerChanSize)
	p.sharedBatchCh = make(chan []T, p.workerChanSize)

	return p
}

// WithWorkerCompleteFn sets callback executed on worker completion.
// Useful for cleanup or finalization of worker resources.
// Default: none (disabled)
func (p *WorkerGroup[T]) WithWorkerCompleteFn(fn WorkerCompleteFn[T]) *WorkerGroup[T] {
	p.workerCompleteFn = fn
	return p
}

// WithPoolCompleteFn sets callback executed once when all workers are done
func (p *WorkerGroup[T]) WithPoolCompleteFn(fn GroupCompleteFn[T]) *WorkerGroup[T] {
	p.poolCompleteFn = fn
	return p
}

// WithChunkFn enables predictable item distribution.
// Items with the same key (returned by fn) are processed by the same worker.
// Useful for maintaining order within groups of related items.
// Default: none (random distribution)
func (p *WorkerGroup[T]) WithChunkFn(fn func(T) string) *WorkerGroup[T] {
	p.chunkFn = fn
	return p
}

// WithContinueOnError sets whether the pool should continue on error.
// Default: false
func (p *WorkerGroup[T]) WithContinueOnError() *WorkerGroup[T] {
	p.continueOnError = true
	return p
}

// WithBatchSize enables item batching with specified size.
// Items are accumulated until batch is full before processing.
// Set to 0 to disable batching.
// Default: 10
func (p *WorkerGroup[T]) WithBatchSize(size int) *WorkerGroup[T] {
	p.batchSize = size
	if size > 0 {
		// initialize accumulators with capacity
		for i := range p.poolSize {
			p.accumulators[i] = make([]T, 0, size)
		}
	}
	return p
}

// Submit adds an item to the pool for processing. May block if worker channels are full.
// Not thread-safe, intended for use by the main thread ot a single producer's thread.
func (p *WorkerGroup[T]) Submit(v T) {
	// check context early
	select {
	case <-p.ctx.Done():
		return // don't submit if context is cancelled
	default:
	}

	if p.batchSize == 0 {
		// direct submission mode
		if p.chunkFn == nil {
			p.sharedCh <- v
			return
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(p.chunkFn(v)))
		id := int(h.Sum32()) % p.poolSize
		p.workersCh[id] <- v
		return
	}

	// batching mode
	var id int
	if p.chunkFn != nil {
		h := fnv.New32a()
		_, _ = h.Write([]byte(p.chunkFn(v)))
		id = int(h.Sum32()) % p.poolSize
	} else {
		id = rand.Intn(p.poolSize) //nolint:gosec // no need for secure random here
	}

	// add to accumulator
	p.accumulators[id] = append(p.accumulators[id], v)

	// check if we should flush
	var shouldFlush bool
	select {
	case <-p.ctx.Done():
		shouldFlush = true // always flush on context cancellation
	default:
		// in normal case, flush only when batch is full
		shouldFlush = len(p.accumulators[id]) >= p.batchSize
	}

	if shouldFlush && len(p.accumulators[id]) > 0 {
		if p.chunkFn == nil {
			select {
			case p.sharedBatchCh <- p.accumulators[id]:
			case <-p.ctx.Done(): // handle case where channel send would block
				return
			}
		} else {
			select {
			case p.workerBatchCh[id] <- p.accumulators[id]:
			case <-p.ctx.Done():
				return
			}
		}
		p.accumulators[id] = make([]T, 0, p.batchSize)
	}
}

// Send adds an item to the pool for processing.
// Safe for concurrent use, intended for worker-to-pool submissions or for use by multiple concurrent producers.
func (p *WorkerGroup[T]) Send(v T) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	p.Submit(v)
}

// Go activates the pool and starts worker goroutines.
// Must be called before submitting items.
func (p *WorkerGroup[T]) Go(ctx context.Context) error {
	if p.activated {
		return fmt.Errorf("workers poll already activated")
	}
	defer func() { p.activated = true }()

	var egCtx context.Context
	p.eg, egCtx = errgroup.WithContext(ctx)
	p.ctx = egCtx

	// create metrics context for all workers
	metricsCtx := metrics.Make(egCtx, p.poolSize)
	p.metrics = metrics.Get(metricsCtx)

	// set initial count
	p.activeWorkers.Store(int32(p.poolSize)) //nolint:gosec // no risk of overflow

	// start all goroutines (workers)
	for i := range p.poolSize {
		withWorkerIDctx := metrics.WithWorkerID(metricsCtx, i)
		workerCh := p.sharedCh
		batchCh := p.sharedBatchCh
		if p.chunkFn != nil {
			workerCh = p.workersCh[i]
			batchCh = p.workerBatchCh[i]
		}
		r := workerRequest[T]{inCh: workerCh, batchCh: batchCh, m: p.metrics, id: i}
		p.eg.Go(p.workerProc(withWorkerIDctx, r))
	}

	return nil
}

// workerRequest is a request to worker goroutine containing all necessary data
type workerRequest[T any] struct {
	inCh    <-chan T
	batchCh <-chan []T
	m       *metrics.Value
	id      int
}

// workerProc is a worker goroutine function, reads from the input channel and processes records
func (p *WorkerGroup[T]) workerProc(wCtx context.Context, r workerRequest[T]) func() error {
	return func() error {
		var lastErr error
		var totalErrs int

		initEndTmr := r.m.StartTimer(r.id, metrics.TimerInit)
		worker := p.worker
		if p.workerMaker != nil {
			worker = p.workerMaker()
		}
		initEndTmr()

		lastActivity := time.Now()

		// processItem handles a single item with metrics
		processItem := func(v T) error {
			waitTime := time.Since(lastActivity)
			r.m.AddWaitTime(r.id, waitTime)
			lastActivity = time.Now()

			procEndTmr := r.m.StartTimer(r.id, metrics.TimerProc)
			defer procEndTmr()

			if err := worker.Do(wCtx, v); err != nil {
				r.m.IncErrors(r.id)
				totalErrs++
				if !p.continueOnError {
					return fmt.Errorf("worker %d failed: %w", r.id, err)
				}
				lastErr = fmt.Errorf("worker %d failed: %w", r.id, err)
				return nil // continue on error
			}
			r.m.IncProcessed(r.id)
			return nil
		}

		// processBatch handles batch of items
		processBatch := func(items []T) error {
			waitTime := time.Since(lastActivity)
			r.m.AddWaitTime(r.id, waitTime)
			lastActivity = time.Now()

			procEndTmr := r.m.StartTimer(r.id, metrics.TimerProc)
			defer procEndTmr()

			for _, v := range items {
				if err := worker.Do(wCtx, v); err != nil {
					r.m.IncErrors(r.id)
					totalErrs++
					if !p.continueOnError {
						return fmt.Errorf("worker %d failed: %w", r.id, err)
					}
					lastErr = fmt.Errorf("worker %d failed: %w", r.id, err)
					continue
				}
				r.m.IncProcessed(r.id)
			}
			return nil
		}

		// track if channels are closed
		normalClosed := false
		batchClosed := false

		// main processing loop
		for {
			if normalClosed && batchClosed {
				return p.finishWorker(wCtx, r.id, worker, lastErr, totalErrs)
			}

			select {
			case <-wCtx.Done():
				return p.finishWorker(wCtx, r.id, worker, wCtx.Err(), totalErrs)

			case v, ok := <-r.inCh:
				if !ok {
					normalClosed = true
					continue
				}
				if err := processItem(v); err != nil {
					return p.finishWorker(wCtx, r.id, worker, err, totalErrs)
				}

			case batch, ok := <-r.batchCh:
				if !ok {
					batchClosed = true
					continue
				}
				if err := processBatch(batch); err != nil {
					return p.finishWorker(wCtx, r.id, worker, err, totalErrs)
				}
			}
		}
	}
}

// finishWorker handles worker completion logic
func (p *WorkerGroup[T]) finishWorker(ctx context.Context, id int, worker Worker[T], lastErr error, totalErrs int) error {
	// worker completion should be called only if we are continuing on error or no error
	if p.workerCompleteFn != nil && (lastErr == nil || p.continueOnError) {
		wrapFinTmr := p.metrics.StartTimer(id, metrics.TimerWrap)
		if e := p.workerCompleteFn(ctx, id, worker); e != nil {
			if lastErr == nil {
				lastErr = fmt.Errorf("complete worker func for %d failed: %w", id, e)
			}
		}
		wrapFinTmr()
	}

	activeWorkers := p.activeWorkers.Add(-1)

	// pool completion should be called when this is the last worker
	// regardless of error state, except for context cancellation
	if activeWorkers == 0 && p.poolCompleteFn != nil && !errors.Is(lastErr, context.Canceled) {
		if e := p.poolCompleteFn(ctx); e != nil {
			if lastErr == nil {
				lastErr = fmt.Errorf("complete pool func for %d failed: %w", id, e)
			}
		}
	}

	if lastErr != nil {
		return fmt.Errorf("total errors: %d, last error: %w", totalErrs, lastErr)
	}
	return nil
}

// Close pool. Has to be called by consumer as the indication of "all records submitted".
// The call is blocking till all processing completed by workers. After this call poll can't be reused.
// Returns an error if any happened during the run
func (p *WorkerGroup[T]) Close(ctx context.Context) error {
	// if context canceled, return immediately
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	default:
	}

	// flush any remaining items in accumulators
	if p.batchSize > 0 {
		for i, acc := range p.accumulators {
			if len(acc) > 0 {
				// ensure we flush any non-empty accumulator, regardless of size
				if p.chunkFn == nil {
					p.sharedBatchCh <- acc
				} else {
					p.workerBatchCh[i] <- acc
				}
				p.accumulators[i] = nil // help GC
			}
		}
	}

	close(p.sharedCh)
	close(p.sharedBatchCh)
	for i := range p.poolSize {
		close(p.workersCh[i])
		close(p.workerBatchCh[i])
	}
	return p.eg.Wait()
}

// Wait till workers completed and the result channel closed.
func (p *WorkerGroup[T]) Wait(ctx context.Context) error {
	// if context canceled, return immediately
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	default:
	}
	return p.eg.Wait()
}

// Metrics returns combined metrics from all workers
func (p *WorkerGroup[T]) Metrics() *metrics.Value {
	return p.metrics
}

// Middleware wraps worker and adds functionality
type Middleware[T any] func(Worker[T]) Worker[T]

// Use applies middlewares to the worker group's worker. Middlewares are applied
// in the same order as they are provided, matching the HTTP middleware pattern in Go.
// The first middleware is the outermost wrapper, and the last middleware is the
// innermost wrapper closest to the original worker.
func (p *WorkerGroup[T]) Use(middlewares ...Middleware[T]) *WorkerGroup[T] {
	if len(middlewares) == 0 {
		return p
	}

	// if we have a worker maker (stateful), wrap it
	if p.workerMaker != nil {
		originalMaker := p.workerMaker
		p.workerMaker = func() Worker[T] {
			worker := originalMaker()
			// apply middlewares in order from last to first
			// this makes first middleware outermost
			wrapped := worker
			for i := len(middlewares) - 1; i >= 0; i-- {
				prev := wrapped
				wrapped = middlewares[i](prev)
			}
			return wrapped
		}
		return p
	}

	// for stateless worker, just wrap it directly
	wrapped := p.worker
	for i := len(middlewares) - 1; i >= 0; i-- {
		prev := wrapped
		wrapped = middlewares[i](prev)
	}
	p.worker = wrapped
	return p
}
