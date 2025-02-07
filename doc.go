// Package pool provides a simple worker pool implementation with a single stage only.
// It allows submitting tasks to be processed in parallel by a number of workers.
//
// The package supports both stateless and stateful workers through two distinct constructors:
//   - New - for pools with a single shared worker instance
//   - NewStateful - for pools where each goroutine gets its own worker instance
//
// # Basic Usage
//
// For stateless operations (like HTTP requests, parsing operations, etc.):
//
//	worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
//	    // process the value
//	    return nil
//	})
//
//	p, err := pool.New[string](2, worker)
//	if err != nil {
//	    return err
//	}
//
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
// For stateful operations (like database connections, file handles, etc.):
//
//	// create worker maker function
//	maker := func() pool.Worker[string] {
//	    return &dbWorker{
//	        conn: openConnection(),
//	    }
//	}
//
//	p, err := pool.NewStateful[string](2, maker)
//	if err != nil {
//	    return err
//	}
//
// # Features
//
// The package provides several key features:
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
// # Advanced Features
//
// Batching:
//
//	p, _ := New[string](2, worker,
//	    Options[string]().WithBatchSize(10),
//	)
//
// Chunked distribution:
//
//	p, _ := New[string](2, worker,
//	    Options[string]().WithChunkFn(func(v string) string {
//	        return v // items with same hash go to same worker
//	    }),
//	)
//
// Error handling:
//
//	p, _ := New[string](2, worker,
//	    Options[string]().WithContinueOnError(),
//	)
//
// # Metrics
//
// The pool automatically collects various metrics including:
//
//   - Number of processed items
//   - Number of errors
//   - Number of dropped items
//   - Processing time
//   - Wait time
//   - Initialization time
//   - Total time
//
// Access metrics:
//
//	stats := pool.Metrics().Stats()
//	fmt.Printf("processed: %d, errors: %d", stats.Processed, stats.Errors)
//
// # Data Collection
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
package pool
