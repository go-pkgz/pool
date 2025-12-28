// Package pool provides a simple worker pool implementation with a single stage only.
// It allows submitting tasks to be processed in parallel by a number of workers.
//
// The package supports both stateless and stateful workers through two distinct constructors:
//   - New - for pools with a single shared worker instance
//   - NewStateful - for pools where each goroutine gets its own worker instance
//
// Worker Types:
//
// The package provides a simple Worker interface that can be implemented in two ways:
//
//	type Worker[T any] interface {
//	    Do(ctx context.Context, v T) error
//	}
//
// 1. Direct implementation for complex stateful workers:
//
//	type dbWorker struct {
//	    conn *sql.DB
//	}
//
//	func (w *dbWorker) Do(ctx context.Context, v string) error {
//	    return w.conn.ExecContext(ctx, "INSERT INTO items (value) VALUES (?)", v)
//	}
//
// 2. Function adapter for simple stateless workers:
//
//	worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
//	    // process the value
//	    return nil
//	})
//
// Basic Usage:
//
// For stateless operations (like HTTP requests, parsing operations, etc.):
//
//	worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
//	    resp, err := http.Get(v)
//	    if err != nil {
//	        return err
//	    }
//	    defer resp.Body.Close()
//	    return nil
//	})
//
//	p := pool.New[string](2, worker)
//	if err := p.Go(context.Background()); err != nil {
//	    return err
//	}
//
//	// submit work
//	p.Submit("task1")
//	p.Submit("task2")
//
//	if err := p.Close(context.Background()); err != nil {
//	    return err
//	}
//
// Note: all With* configuration methods are builder-style and must be called before Go().
// Calling them after Go() is unsupported.
//
// For stateful operations (like database connections, file handles, etc.):
//
//	maker := func() pool.Worker[string] {
//	    return &dbWorker{
//	        conn: openConnection(),
//	    }
//	}
//	p := pool.NewStateful[string](2, maker)
//
// Features:
//
//   - Generic worker pool implementation supporting any data type
//   - Configurable number of workers running in parallel
//   - Support for both stateless shared workers and per-worker instances
//   - Batching capability for processing multiple items at once
//   - Customizable work distribution through chunk functions
//   - Built-in metrics collection including processing times and counts
//   - Error handling with options to continue or stop on errors
//   - Context-based cancellation and timeouts
//   - Optional completion callbacks
//
// Advanced Features:
//
// Batching:
//
//	p := New[string](2, worker).WithBatchSize(10)
//
// Chunked distribution:
//
//	p := New[string](2, worker).WithChunkFn(func(v string) string {
//	    return v // items with same hash go to same worker
//	})
//
// Error handling:
//
//	p := New[string](2, worker).WithContinueOnError()
//
// Metrics:
//
// The pool automatically tracks standard stats metrics (processed counts, errors, timings).
// Workers can also record additional custom metrics:
//
//	m := metrics.Get(ctx)
//	m.Inc("custom-counter")
//
// Access metrics:
//
//	metrics := p.Metrics()
//	value := metrics.Get("custom-counter")
//
// Statistical metrics including:
//
//   - Number of processed items
//   - Number of errors
//   - Number of dropped items
//   - Processing time
//   - Wait time
//   - Initialization time
//   - Total time
//
// Access stats:
//
//	metrics := p.Metrics()
//	stats := metrics.GetStats()
//	fmt.Printf("processed: %d, errors: %d", stats.Processed, stats.Errors)
//
// Data Collection:
//
// For collecting results from workers, use the Collector:
//
//	collector := pool.NewCollector[Result](ctx, 10)
//	worker := pool.WorkerFunc[Input](func(ctx context.Context, v Input) error {
//	    result := process(v)
//	    collector.Submit(result)
//	    return nil
//	})
//
// Results can be retrieved either through iteration:
//
//	for v, err := range collector.Iter() {
//	    if err != nil {
//	        return err
//	    }
//	    // use v
//	}
//
// Or by collecting all at once:
//
//	results, err := collector.All()
//
// Middleware Support:
//
// The pool supports middleware pattern similar to HTTP middleware in Go. Middleware can be used
// to add functionality like retries, timeouts, metrics, or error handling:
//
//	// retry middleware
//	retryMiddleware := func(next Worker[string]) Worker[string] {
//	    return WorkerFunc[string](func(ctx context.Context, v string) error {
//	        var lastErr error
//	        for i := 0; i < 3; i++ {
//	            if err := next.Do(ctx, v); err == nil {
//	                return nil
//	            } else {
//	                lastErr = err
//	            }
//	            time.Sleep(time.Second * time.Duration(i))
//	        }
//	        return fmt.Errorf("failed after 3 attempts: %w", lastErr)
//	    })
//	}
//
//	p := New[string](2, worker).Use(retryMiddleware)
//
// Multiple middleware can be chained, and they execute in the same order as provided:
//
//	p.Use(logging, metrics, retry)  // executes: logging -> metrics -> retry -> worker
package pool
