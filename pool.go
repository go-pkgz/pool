package pool

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync"

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

	buf        [][]T             // batch buffers for workers
	workersCh  []chan []T        // workers input channels
	workerCtxs []context.Context // store worker contexts

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
type Send[OUT any] func(val OUT) error

// New creates worker pool, can be activated once
func New[T any](poolSize int, opts ...Option[T]) (*WorkerGroup[T], error) {
	if poolSize < 1 {
		poolSize = 1
	}

	res := &WorkerGroup[T]{
		poolSize:       poolSize,
		workersCh:      make([]chan []T, poolSize),
		buf:            make([][]T, poolSize),
		workerCtxs:     make([]context.Context, poolSize),
		completeFn:     nil, // no worker completion func by default
		chunkFn:        nil, // no custom chunkFn (workers distribution), random by default
		batchSize:      1,
		workerChanSize: 1,
		ctx:            context.Background(),
	}
	res.err.ch = make(chan struct{})

	// apply all options
	for _, opt := range opts {
		opt(res)
	}

	// verify worker or worker maker provided
	if res.worker == nil && res.workerMaker == nil {
		return nil, fmt.Errorf("worker or worker maker not provided")
	}
	// verify if both worker and worker maker provided
	if res.worker != nil && res.workerMaker != nil {
		return nil, fmt.Errorf("both worker and worker maker provided")
	}

	// initialize worker's channels and batch buffers
	for id := 0; id < poolSize; id++ {
		res.workersCh[id] = make(chan []T, res.workerChanSize)
		if res.batchSize > 1 {
			res.buf[id] = make([]T, 0, poolSize)
		}
	}

	return res, nil
}

// Submit record to pool, can be blocked if worker channels are full
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
		// commit copy to workers
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

	// Create errgroup first
	var egCtx context.Context
	p.eg, egCtx = errgroup.WithContext(ctx)
	p.ctx = egCtx

	// start all goroutines
	for i := 0; i < p.poolSize; i++ {
		workerCtx := metrics.Make(metrics.WithWorkerID(egCtx, i))
		p.workerCtxs[i] = workerCtx
		p.eg.Go(p.workerProc(workerCtx, i, p.workersCh[i]))
	}

	return nil
}

// workerProc is a worker goroutine function, reads from the input channel and processes records
func (p *WorkerGroup[T]) workerProc(wCtx context.Context, id int, inCh chan []T) func() error {
	return func() error {
		var lastErr error
		var totalErrs int

		m := metrics.Get(wCtx)

		worker := p.worker // use worker if provided, stateless
		if worker == nil {
			worker = p.workerMaker() // create new worker for each goroutine
		}

		// track initialization time
		initEndTmr := m.StartTimer(metrics.DurationInit)
		initEndTmr()

		for {
			select {
			case vv, ok := <-inCh:
				if !ok { // input channel closed
					wrapEndTmr := m.StartTimer(metrics.DurationWrap)
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

				// track wait time - from when item was received till processing starts
				waitEndTmr := m.StartTimer(metrics.DurationWait)

				// read from the input slice
				for _, v := range vv {
					// even if not continue on error has to read from input channel all it has
					if lastErr != nil && !p.continueOnError {
						m.Inc(metrics.CountDropped)
						continue
					}

					// Removed procEndTmr since it's now handled in the worker

					if err := worker.Do(wCtx, v); err != nil {
						m.Inc(metrics.CountErrors)
						e := fmt.Errorf("worker %d failed: %w", id, err)
						if !p.continueOnError {
							// close err.ch once. indicates to Submit what all other records can be ignored
							p.err.Do(func() { close(p.err.ch) })
						}
						totalErrs++
						lastErr = e // errors allowed to continue, capture the last error only
					}
				}
				waitEndTmr()

			case <-p.ctx.Done(): // parent context, passed by caller
				return p.ctx.Err()

			case <-wCtx.Done(): // worker context from errgroup
				// triggered by other worker, kill only if errors not allowed
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
		if e := worker.Do(ctx, v); e != nil {
			if !p.continueOnError {
				return fmt.Errorf("worker %d failed in finalizer: %w", id, e)
			}
		}
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

// Wait till workers completed and result channel closed. This can be used instead of the cursor
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
	values := make([]*metrics.Value, p.poolSize)
	for i := 0; i < p.poolSize; i++ {
		values[i] = metrics.Get(p.workerCtxs[i])
	}
	return metrics.Aggregate(values...)
}
