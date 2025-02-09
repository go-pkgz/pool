package pool

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/go-pkgz/pool/metrics"
)

// WorkerGroup is a simple case of flow with a single stage only running a common function in workers pool.
// IN type if for input (submitted) records, OUT type is for output records in case if worker function should
// return some values.
type WorkerGroup[T any] struct {
	poolSize  int // number of workers (goroutines)
	batchSize int // size of batch sends to workers

	chunkFn         func(T) string // worker selector function
	workerChanSize  int            // size of worker channels
	worker          Worker[T]      // worker function
	workerMaker     WorkerMaker[T] // worker maker function
	completeFn      CompleteFn[T]  // completion callback function, called by each worker on completion
	continueOnError bool           // don't terminate on first error

	buf       [][]T          // batch buffers for workers
	workersCh []chan []T     // workers input channels
	metrics   *metrics.Value // shared metrics

	eg  *errgroup.Group
	err struct {
		sync.Once
		ch chan struct{}
	}

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
		workersCh:      make([]chan []T, size),
		buf:            make([][]T, size),
		worker:         worker,
		batchSize:      1,
		workerChanSize: 1,
	}
	res.err.ch = make(chan struct{})

	// initialize worker's channels and batch buffers
	for id := range size {
		res.workersCh[id] = make(chan []T, res.workerChanSize)
		if res.batchSize > 1 {
			res.buf[id] = make([]T, 0, size)
		}
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
		workersCh:      make([]chan []T, size),
		buf:            make([][]T, size),
		workerMaker:    maker,
		batchSize:      1,
		workerChanSize: 1,
		ctx:            context.Background(),
	}
	res.err.ch = make(chan struct{})

	// initialize worker's channels and batch buffers
	for id := range size {
		res.workersCh[id] = make(chan []T, res.workerChanSize)
		if res.batchSize > 1 {
			res.buf[id] = make([]T, 0, size)
		}
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

// WithBatchSize sets the size of the batches. This is used to send multiple values
// to workers in a single batch. This can be useful to reduce contention on worker channels.
// Default: 1
func (p *WorkerGroup[T]) WithBatchSize(size int) *WorkerGroup[T] {
	p.batchSize = size
	if size < 1 {
		p.batchSize = 1
	}
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
	// randomize distribution by default
	id := rand.Intn(p.poolSize) //nolint:gosec // no need for secure random here, just distribution
	if p.chunkFn != nil {
		// chunked distribution
		h := fnv.New32a()
		_, _ = h.Write([]byte(p.chunkFn(v)))
		id = int(h.Sum32()) % p.poolSize
	}

	if p.batchSize <= 1 {
		// skip all buffering if batch size is 1 or less
		p.workersCh[id] <- append([]T{}, v)
		return
	}

	if !p.continueOnError {
		select {
		case <-p.err.ch: // closed due to worker error
			return
		default:
		}
	}

	p.buf[id] = append(p.buf[id], v)   // add to batch buffer
	if len(p.buf[id]) >= p.batchSize { // submit buffer to workers
		// create a copy to avoid race conditions
		cp := make([]T, len(p.buf[id]))
		copy(cp, p.buf[id])
		p.workersCh[id] <- cp
		p.buf[id] = p.buf[id][:0] // reset size, keep capacity
	}
}

// Go activates worker pool, closes cursor on completion
func (p *WorkerGroup[T]) Go(ctx context.Context) error {
	if p.activated {
		return fmt.Errorf("workers poll already activated")
	}
	defer func() { p.activated = true }()

	// create errgroup to track all workers
	var egCtx context.Context
	p.eg, egCtx = errgroup.WithContext(ctx)
	p.ctx = egCtx

	// create shared metrics context for workers
	workerCtx := metrics.Make(egCtx, p.poolSize)
	p.metrics = metrics.Get(workerCtx)

	// start all goroutines
	for i := range p.poolSize {
		p.eg.Go(p.workerProc(metrics.WithWorkerID(workerCtx, i), i, p.workersCh[i]))
	}

	return nil
}

// workerProc is a worker goroutine function, reads from the input channel and processes records
// update workerProc in pool.go

func (p *WorkerGroup[T]) workerProc(wCtx context.Context, id int, inCh chan []T) func() error {
	return func() error {
		var lastErr error
		var totalErrs int
		lastActivity := time.Now()

		m := metrics.Get(wCtx)

		// initialize worker
		var worker Worker[T]
		initEndTmr := m.StartTimer(id, metrics.TimerInit)
		if p.worker != nil {
			worker = p.worker // use shared worker for stateless mode
		} else {
			worker = p.workerMaker() // create new worker instance for stateful mode
		}
		initEndTmr()

		for {
			select {
			case vv, ok := <-inCh:
				if !ok { // input channel closed
					wrapEndTmr := m.StartTimer(id, metrics.TimerWrap)
					e := p.finalizeWorker(wCtx, id, worker)
					wrapEndTmr()
					if e != nil {
						return e
					}
					if lastErr != nil {
						return fmt.Errorf("total errors: %d, last error: %w", totalErrs, lastErr)
					}
					return nil
				}

				// track wait time - from last activity till now
				waitTime := time.Since(lastActivity)
				m.AddWaitTime(id, waitTime)

				// read from the input slice
				for _, v := range vv {
					// even if not continue on error has to read from input channel all it has
					if lastErr != nil && !p.continueOnError {
						m.IncDropped(id)
						continue
					}

					procEndTmr := m.StartTimer(id, metrics.TimerProc)
					if err := worker.Do(wCtx, v); err != nil {
						procEndTmr()
						m.IncErrors(id)
						e := fmt.Errorf("worker %d failed: %w", id, err)
						if !p.continueOnError {
							// close err.ch once. indicates to Submit what all other records can be ignored
							p.err.Do(func() { close(p.err.ch) })
						}
						totalErrs++
						lastErr = e // errors allowed to continue, capture the last error only
						continue
					}
					procEndTmr()
					m.IncProcessed(id)
				}
				lastActivity = time.Now()

			case <-p.ctx.Done(): // parent context, passed by caller
				return p.ctx.Err()

			case <-wCtx.Done(): // worker context from errgroup
				// triggered by another worker, kill only if errors not allowed
				if !p.continueOnError {
					return wCtx.Err()
				}
			}
		}
	}
}

// finWorker worker flushes records left in buffer to workers, called once for each worker
// if completeFn allowed, will be called as well
func (p *WorkerGroup[T]) finalizeWorker(ctx context.Context, id int, worker Worker[T]) (err error) {
	// process all requests left in the not submitted yet buffer
	for _, v := range p.buf[id] {
		procEndTmr := p.metrics.StartTimer(id, metrics.TimerProc)
		if e := worker.Do(ctx, v); e != nil {
			procEndTmr()
			p.metrics.IncErrors(id)
			if !p.continueOnError {
				return fmt.Errorf("worker %d failed in finalizer: %w", id, e)
			}
			continue
		}
		procEndTmr()
		p.metrics.IncProcessed(id)
	}

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
func (p *WorkerGroup[T]) Close(ctx context.Context) (err error) {
	for _, ch := range p.workersCh {
		close(ch)
	}

	doneCh := make(chan error)
	go func() {
		doneCh <- p.eg.Wait()
	}()

	for {
		select {
		case err := <-doneCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Wait till workers completed and the result channel closed. This can be used instead of the cursor
// in case if the result channel can be ignored and the goal is to wait for the completion.
func (p *WorkerGroup[T]) Wait(ctx context.Context) (err error) {
	doneCh := make(chan error)
	go func() {
		doneCh <- p.eg.Wait()
	}()

	for {
		select {
		case err := <-doneCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Metrics returns combined metrics from all workers
func (p *WorkerGroup[T]) Metrics() *metrics.Value {
	return p.metrics
}
