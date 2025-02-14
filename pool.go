package pool

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/go-pkgz/pool/metrics"
)

// WorkerGroup is a simple case of flow with a single stage only running a common function in workers pool.
type WorkerGroup[T any] struct {
	poolSize        int            // number of workers (goroutines)
	workerChanSize  int            // size of worker channels
	completeFn      CompleteFn[T]  // completion callback function, called by each worker on completion
	continueOnError bool           // don't terminate on first error
	chunkFn         func(T) string // worker selector function
	worker          Worker[T]      // worker function
	workerMaker     WorkerMaker[T] // worker maker function

	metrics *metrics.Value // shared metrics

	workersCh []chan T // workers input channels
	sharedCh  chan T   // shared input channel for all workers

	eg        *errgroup.Group
	activated bool
	ctx       context.Context
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

// CompleteFn called (optionally) on worker completion
type CompleteFn[T any] func(ctx context.Context, id int, worker Worker[T]) error

// Send func called by worker code to publish results
type Send[T any] func(val T) error

// New creates a worker pool with a shared, stateless worker.
// Size defines the number of goroutines (workers) processing requests.
func New[T any](size int, worker Worker[T]) *WorkerGroup[T] {
	if size < 1 {
		size = 1
	}

	res := &WorkerGroup[T]{
		poolSize:       size,
		workersCh:      make([]chan T, size),
		sharedCh:       make(chan T, size),
		worker:         worker,
		workerChanSize: 1,
	}

	// initialize worker's channels
	for id := range size {
		res.workersCh[id] = make(chan T, res.workerChanSize)
	}

	return res
}

// NewStateful creates a worker pool with a separate worker instance for each goroutine.
// Size defines number of goroutines (workers) processing requests.
// Maker function is called for each goroutine to create a new worker instance.
func NewStateful[T any](size int, maker func() Worker[T]) *WorkerGroup[T] {
	if size < 1 {
		size = 1
	}

	res := &WorkerGroup[T]{
		poolSize:       size,
		workersCh:      make([]chan T, size),
		sharedCh:       make(chan T, size),
		workerMaker:    maker,
		workerChanSize: 1,
		ctx:            context.Background(),
	}

	// initialize worker's channels
	for id := range size {
		res.workersCh[id] = make(chan T, res.workerChanSize)
	}

	return res
}

// WithWorkerChanSize sets the size of the worker channel. Each worker has its own channel
// to receive values from the pool and process them.
// Default: 1
func (p *WorkerGroup[T]) WithWorkerChanSize(size int) *WorkerGroup[T] {
	p.workerChanSize = size
	if size < 1 {
		p.workerChanSize = 1
	}
	return p
}

// WithCompleteFn sets the complete function, called when the pool is complete.
// This is useful for cleanup or finalization tasks.
// Default: none
func (p *WorkerGroup[T]) WithCompleteFn(fn CompleteFn[T]) *WorkerGroup[T] {
	p.completeFn = fn
	return p
}

// WithChunkFn sets the chunk function, used to distribute records to workers.
// This is useful to distribute records to workers predictably.
// Default: none
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

// Submit record to pool, can be blocked if worker channels are full.
// The call is ignored if the pool is stopping due to an error in non-continueOnError mode.
func (p *WorkerGroup[T]) Submit(v T) {
	if p.chunkFn == nil { // fast path for shared channel
		p.sharedCh <- v
		return
	}

	// chunked mode with worker selection
	h := fnv.New32a()
	_, _ = h.Write([]byte(p.chunkFn(v))) // hash by chunkFn
	id := int(h.Sum32()) % p.poolSize    // select worker by hash
	p.workersCh[id] <- v
}

// Go activates worker pool
// Go activates worker pool
func (p *WorkerGroup[T]) Go(ctx context.Context) error {
	if p.activated {
		return fmt.Errorf("workers poll already activated")
	}
	defer func() { p.activated = true }()

	// create errgroup to track all workers
	var egCtx context.Context
	p.eg, egCtx = errgroup.WithContext(ctx)
	p.ctx = egCtx

	// create metrics context for all workers
	metricsCtx := metrics.Make(egCtx, p.poolSize) // always create metrics context for worker IDs
	p.metrics = metrics.Get(metricsCtx)           // store metrics in pool only if enabled

	// start all goroutines (workers)
	for i := range p.poolSize {
		withWorkerIdCtx := metrics.WithWorkerID(metricsCtx, i) // always set worker ID
		workerCh := p.sharedCh                                 // use the shared channel by default
		if p.chunkFn != nil {
			workerCh = p.workersCh[i]
		}
		r := workerRequest[T]{inCh: workerCh, m: p.metrics, id: i}
		p.eg.Go(p.workerProc(withWorkerIdCtx, r))
	}

	return nil
}

// workerRequest is a request to worker goroutine containing all necessary data
type workerRequest[T any] struct {
	inCh <-chan T
	m    *metrics.Value
	id   int
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
		for v := range r.inCh {
			waitTime := time.Since(lastActivity)
			r.m.AddWaitTime(r.id, waitTime)
			lastActivity = time.Now()
			select {
			case <-wCtx.Done():
				if !p.continueOnError {
					return wCtx.Err()
				}
			default:
			}

			procEndTmr := r.m.StartTimer(r.id, metrics.TimerProc)
			if err := worker.Do(wCtx, v); err != nil {
				procEndTmr()
				r.m.IncErrors(r.id)
				totalErrs++
				if !p.continueOnError {
					return fmt.Errorf("worker %d failed: %w", r.id, err)
				}
				lastErr = fmt.Errorf("worker %d failed: %w", r.id, err)
				continue
			}
			procEndTmr()
			r.m.IncProcessed(r.id)
		}

		// handle completion
		if p.completeFn != nil && (lastErr == nil || p.continueOnError) {
			wrapFinTmr := r.m.StartTimer(r.id, metrics.TimerWrap)
			if e := p.completeFn(wCtx, r.id, worker); e != nil {
				lastErr = fmt.Errorf("complete func for %d failed: %w", r.id, e)
			}
			wrapFinTmr()
		}

		if lastErr != nil {
			return fmt.Errorf("total errors: %d, last error: %w", totalErrs, lastErr)
		}
		return nil
	}
}

// finWorker worker flushes records left in buffer to workers, called once for each worker
// if completeFn allowed, will be called as well
func (p *WorkerGroup[T]) finalizeWorker(ctx context.Context, id int, worker Worker[T]) (err error) {
	// call completeFn for given worker id
	if p.completeFn != nil {
		if e := p.completeFn(ctx, id, worker); e != nil {
			err = fmt.Errorf("complete func for %d failed: %w", id, e)
		}
	}
	return err
}

// Close pool. Has to be called by consumer as the indication of "all records submitted".
// The call is blocking till all processing completed by workers. After this call poll can't be reused.
// Returns an error if any happened during the run
func (p *WorkerGroup[T]) Close(ctx context.Context) error {
	close(p.sharedCh)
	for _, ch := range p.workersCh {
		close(ch)
	}
	return p.eg.Wait()
}

// Wait till workers completed and the result channel closed. This can be used instead of the cursor
// in case if the result channel can be ignored and the goal is to wait for the completion.
func (p *WorkerGroup[T]) Wait(ctx context.Context) error {
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
