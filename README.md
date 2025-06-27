# pool [![Build Status](https://github.com/go-pkgz/pool/workflows/build/badge.svg)](https://github.com/go-pkgz/pool/actions) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/pool/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/pool?branch=master) [![godoc](https://godoc.org/github.com/go-pkgz/pool?status.svg)](https://godoc.org/github.com/go-pkgz/pool)

`pool` is a Go package that provides a generic, efficient worker pool implementation for parallel task processing. Built for Go 1.21+, it offers a flexible API with features like batching, work distribution strategies, and comprehensive metrics collection.

## Features

- Generic implementation supporting any data type
- Configurable number of parallel workers
- Support for both stateless shared workers and per-worker instances
- Batching capability for processing multiple items at once
- Customizable work distribution through chunk functions
- Built-in stats collection (processing times, counts, etc.)
- Ability to submit custom metrics
- Error handling with continue/stop options
- Context-based cancellation and timeouts
- Optional completion callbacks
- Extensible middleware system for custom functionality
- Built-in middlewares for common tasks
- No external dependencies except for the testing framework

## Quick Start

Here's a practical example showing how to process a list of URLs in parallel:

```go
func main() {
    // create a worker that fetches URLs and tracks custom metrics
    worker := pool.WorkerFunc[string](func(ctx context.Context, url string) error {
        // get metrics from context to track custom values
        m := metrics.Get(ctx)
        
        resp, err := http.Get(url)
        if err != nil {
            m.Inc("fetch_errors")
            return fmt.Errorf("failed to fetch %s: %w", url, err)
        }
        defer resp.Body.Close()
        
        // track response codes
        m.Inc(fmt.Sprintf("status_%d", resp.StatusCode))
        
        // track content length
        if cl := resp.ContentLength; cl > 0 {
            m.Add("bytes_fetched", int(cl))
        }
        
        if resp.StatusCode != http.StatusOK {
            return fmt.Errorf("bad status code from %s: %d", url, resp.StatusCode)
        }
        
        m.Inc("successful_fetches")
        return nil
    })
	
    // create a pool with 5 workers 
    p := pool.New[string](5, worker).WithContinueOnError(), // don't stop on errors

    // start the pool
    if err := p.Go(context.Background()); err != nil {
        log.Fatal(err)
    }

    // submit URLs for processing
    urls := []string{
        "https://example.com",
        "https://example.org",
        "https://example.net",
    }
    
    go func() {
        // submit URLs and signal when done
        defer p.Close(context.Background())
        for _, url := range urls {
            p.Submit(url)
        }
    }()

    // wait for all URLs to be processed
    if err := p.Wait(context.Background()); err != nil {
        log.Printf("some URLs failed: %v", err)
    }

    // get metrics object
    metrics := p.Metrics()
    
    // display custom metrics
    fmt.Printf("Custom Metrics:\n")
    fmt.Printf("  Successful fetches: %d\n", metrics.Get("successful_fetches"))
    fmt.Printf("  Fetch errors: %d\n", metrics.Get("fetch_errors"))
    fmt.Printf("  Status 200: %d\n", metrics.Get("status_200"))
    fmt.Printf("  Total bytes: %d\n", metrics.Get("bytes_fetched"))
    fmt.Printf("  All metrics: %s\n", metrics.String())
    
    // get aggregated statistics
    stats := metrics.GetStats()
    fmt.Printf("\nPerformance Statistics:\n")
    fmt.Printf("  Processed: %d URLs\n", stats.Processed)
    fmt.Printf("  Errors: %d (%.1f%%)\n", stats.Errors, stats.ErrorRate*100)
    fmt.Printf("  Rate: %.1f URLs/sec\n", stats.RatePerSec)
    fmt.Printf("  Avg latency: %v\n", stats.AvgLatency)
    fmt.Printf("  Total time: %v\n", stats.TotalTime)
}
```

_For more examples, see the [examples](https://github.com/go-pkgz/pool/tree/master/examples) directory._

## Motivation

While Go provides excellent primitives for concurrent programming with goroutines, channels, and sync primitives, building production-ready concurrent data processing systems often requires more sophisticated patterns. This package emerged from real-world needs encountered in various projects where basic concurrency primitives weren't enough.

Common challenges this package addresses:

1. **Stateful Processing**
   - Need to maintain worker-specific state (counters, caches, connections)
   - Each worker requires its own resources (database connections, file handles)
   - State needs to be isolated to avoid synchronization

2. **Controlled Work Distribution**
   - Ensuring related items are processed by the same worker
   - Maintaining processing order for specific groups of items
   - Optimizing cache usage by routing similar items together

3. **Resource Management**
   - Limiting number of goroutines in large-scale processing
   - Managing cleanup of worker resources
   - Handling graceful shutdown

4. **Performance Optimization**
   - Batching items to reduce channel communication overhead
   - Balancing worker load with different distribution strategies
   - Buffering to handle uneven processing speeds

5. **Operational Visibility**
   - Need for detailed metrics about processing
   - Understanding bottlenecks and performance issues
   - Monitoring system health

## Core Concepts

### Worker Types

**Core Interface**:
   ```go
   // Worker is the interface that wraps the Do method
   type Worker[T any] interface {
       Do(ctx context.Context, v T) error
   }
   
   // WorkerFunc is an adapter to allow using ordinary functions as Workers
   type WorkerFunc[T any] func(ctx context.Context, v T) error
   
   func (f WorkerFunc[T]) Do(ctx context.Context, v T) error { return f(ctx, v) }
   ```
The pool supports two ways to implement and manage workers:

1. **Stateless Shared Workers**:
   ```go
   // single worker instance shared between all goroutines
   worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
       // process v
       return nil
   })
   
   p := pool.New[string](5, worker)
   ```
   - One worker instance serves all goroutines
   - Good for stateless operations
   - More memory efficient

2. **Per-Worker Instances (stateful)**:
   ```go
   type dbWorker struct {
       conn *sql.DB
       processed int
   }
   
   func (w *dbWorker) Do(ctx context.Context, v string) error {
       w.processed++
       return w.conn.ExecContext(ctx, "INSERT INTO items (value) VALUES (?)", v)
   }
   
   // create new instance for each goroutine
   maker := func() pool.Worker[string] {
       w := &dbWorker{
           conn: openConnection(), // each worker gets own connection
       }
       return w
   }
   
   p := pool.NewStateful[string](5, maker)
   ```

### Batching Processing

Batching reduces channel communication overhead by processing multiple items at once:

```go
// process items in batches of 10
p := pool.New[string](2, worker).WithBatchSize(10)

// worker receives items one by one
worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
    // v is one item from the batch
    return nil
})
```

How batching works:
1. Pool accumulates submitted items internally until batch size is reached
2. Full batch is sent to worker as a single channel operation
3. Worker processes each item in the batch sequentially
4. Last batch may be smaller if items don't divide evenly

When to use batching:
- High-volume processing where channel operations are a bottleneck
- When processing overhead per item is low compared to channel communication

### Work Distribution

Control how work is distributed among workers using chunk functions:

```go
// distribute by first character of string
p := pool.New[string](3, worker).WithChunkFn(func(v string) string {
	return v[:1] // same first char goes to same worker
})

// distribute by user ID to ensure user's tasks go to same worker
p := pool.New[Task](3, worker).WithChunkFn(func(t Task) string {
	return strconv.Itoa(t.UserID)
})
```

How distribution works:
1. Without chunk function:
   - Items are distributed randomly among workers
   - Good for independent tasks

2. With chunk function:
   - Function returns string key for each item
   - Items with the same key always go to the same worker
   - Uses consistent hashing to map keys to workers

When to use custom distribution:
- Maintain ordering for related items
- Optimize cache usage by worker
- Ensure exclusive access to resources
- Process data consistently

## Middleware Support

The package supports middleware pattern similar to HTTP middleware in Go. Middleware can be used to add cross-cutting concerns like:
- Retries with backoff
- Timeouts
- Panic recovery
- Rate limiting
- Metrics and logging
- Error handling

Built-in middleware:
```go
// Add retry with exponential backoff
p.Use(middleware.Retry[string](3, time.Second))

// Add timeout per operation
p.Use(middleware.Timeout[string](5 * time.Second))

// Add panic recovery
p.Use(middleware.Recovery[string](func(p interface{}) {
    log.Printf("recovered from panic: %v", p)
}))

// Add validation before processing
p.Use(middleware.Validator[string](validator))

// Add rate limiting
p.Use(middleware.RateLimiter[string](10, 5))  // 10 requests/sec with burst of 5
```

Custom middleware:
```go
logging := func(next pool.Worker[string]) pool.Worker[string] {
    return pool.WorkerFunc[string](func(ctx context.Context, v string) error {
        log.Printf("processing: %v", v)
        err := next.Do(ctx, v)
        log.Printf("completed: %v, err: %v", v, err)
        return err
    })
}

p.Use(logging)
```

Multiple middleware execute in the same order as provided:
```go
p.Use(logging, metrics, retry)  // order: logging -> metrics -> retry -> worker
```

## Install and update

```bash
go get -u github.com/go-pkgz/pool
```

## Usage Examples

### Basic Example

```go
func main() {
    // create a worker function processing strings
    worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
        fmt.Printf("processing: %s\n", v)
        return nil
    })

    // create a pool with 2 workers
    p := pool.New[string](2, worker)

    // start the pool
    if err := p.Go(context.Background()); err != nil {
        log.Fatal(err)
    }

    // submit work
    p.Submit("task1")
    p.Submit("task2")
    p.Submit("task3")

    // close the pool and wait for completion
    if err := p.Close(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

### Error Handling

```go
worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
    if strings.Contains(v, "error") {
        return fmt.Errorf("failed to process %s", v)
    }
    return nil
})

// continue processing on errors
p := pool.New[string](2, worker).WithContinueOnError()
```

### Collecting Results

The pool package includes a powerful Collector for gathering results from workers:

```go
// create a collector for results
collector := pool.NewCollector[Result](ctx, 10)

// worker that produces results
worker := pool.WorkerFunc[Input](func(ctx context.Context, v Input) error {
    result := process(v)
    collector.Submit(result)
    return nil
})

p := pool.New[Input](2, worker)

// process results as they arrive
go func() {
    for v, err := range collector.Iter() {
        if err != nil {
            log.Printf("collection cancelled: %v", err)
            return
        }
        // use v immediately
    }
}()

// submit work and cleanup
submitWork(p)
p.Close(ctx)
collector.Close()
```

See the [Collector section](#collector) for detailed documentation and advanced usage patterns.

### Metrics and Monitoring

```go
// create worker with metrics tracking
worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
    m := metrics.Get(ctx)
    if strings.HasPrefix(v, "important") {
        m.Inc("important-tasks")
    }
    return process(v)
})

// create and run pool
p := pool.New[string](2, worker)
p.Go(context.Background())

// process work
p.Submit("task1")
p.Submit("important-task2")
p.Close(context.Background())

// get metrics
metrics := p.Metrics()
stats := metrics.GetStats()
fmt.Printf("Processed: %d\n", stats.Processed)
fmt.Printf("Errors: %d\n", stats.Errors)
fmt.Printf("Processing time: %v\n", stats.ProcessingTime)
fmt.Printf("Wait time: %v\n", stats.WaitTime)
fmt.Printf("Total time: %v\n", stats.TotalTime)

// get custom metrics
fmt.Printf("Important tasks: %d\n", metrics.Get("important-tasks"))
```

### Stats Structure

The `GetStats()` method returns a comprehensive `Stats` structure with the following fields:

**Counters:**
- `Processed` - total number of successfully processed items
- `Errors` - total number of items that returned errors
- `Dropped` - total number of items dropped (e.g., due to context cancellation)

**Timing Metrics:**
- `ProcessingTime` - cumulative time spent processing items (max across workers)
- `WaitTime` - cumulative time workers spent waiting for items
- `InitTime` - total time spent initializing workers
- `WrapTime` - total time spent in wrap-up phase
- `TotalTime` - total elapsed time since pool started

**Derived Statistics:**
- `RatePerSec` - items processed per second (Processed / TotalTime)
- `AvgLatency` - average processing time per item (ProcessingTime / Processed)
- `ErrorRate` - percentage of items that failed (Errors / Total)
- `DroppedRate` - percentage of items dropped (Dropped / Total)
- `Utilization` - percentage of time spent processing vs waiting (ProcessingTime / (ProcessingTime + WaitTime))

Example usage:
```go
stats := metrics.GetStats()

// check performance
if stats.RatePerSec < 100 {
    log.Printf("Warning: processing rate is low: %.1f items/sec", stats.RatePerSec)
}

// check error rate
if stats.ErrorRate > 0.05 {
    log.Printf("High error rate: %.1f%%", stats.ErrorRate * 100)
}

// check worker utilization
if stats.Utilization < 0.5 {
    log.Printf("Workers are underutilized: %.1f%%", stats.Utilization * 100)
}

// formatted output
fmt.Printf("Stats: %s\n", stats.String())
```

## Flow Control

The package provides several methods for flow control and completion:

```go
// Submit adds items to the pool. Not safe for concurrent use.
// Used by the producer (sender) of data.
p.Submit(item)

// Send safely adds items to the pool from multiple goroutines.
// Used when submitting from worker to another pool, or when multiple goroutines send data.
p.Send(item)

// Close tells workers no more data will be submitted.
// Used by the producer (sender) of data.
p.Close(ctx)  

// Wait blocks until all processing is done.
// Used by the consumer (receiver) of results.
p.Wait(ctx)   
```

Common usage patterns:

```go
// 1. Single producer submitting items
go func() {
    defer p.Close(ctx) // signal no more data
    for _, task := range tasks {
        p.Submit(task) // Submit is safe here - single goroutine
    }
}()

// 2. Workers submitting to next stage
p1 := pool.New[int](5, pool.WorkerFunc[int](func(ctx context.Context, v int) error {
    result := process(v)
    p2.Send(result) // Send is safe for concurrent calls from workers
    return nil
}))

// 3. Consumer waiting for completion
if err := p.Wait(ctx); err != nil {
    // handle error
}
```

Pool completion callback allows executing code when all workers are done:
```go
p := pool.New[string](5, worker).
    WithPoolCompleteFn(func(ctx context.Context) error {
        // called once after all workers complete
        log.Println("all workers finished")
        return nil
    })
```

The completion callback executes when:
- All workers have completed processing
- Errors occurred but pool continued (`WithContinueOnError()`)
- Does not execute on context cancellation

Important notes:
- Use `Submit` when sending items from a single goroutine
- Use `Send` when workers need to submit items to another pool
- Pool completion callback helps coordinate multi-stage processing
- Errors in completion callback are included in pool's error result

## Optional parameters

Configure pool behavior using With methods:

```go
p := pool.New[string](2, worker).  // pool with 2 workers
    WithBatchSize(10).             // process items in batches
    WithWorkerChanSize(5).         // set worker channel buffer size
    WithChunkFn(chunkFn).          // control work distribution
    WithContinueOnError().         // don't stop on errors
    WithCompleteFn(completeFn)     // called when worker finishes
```

Available options:
- `WithBatchSize(size int)` - enables batch processing, accumulating items before sending to workers (default: 10)
- `WithWorkerChanSize(size int)` - sets buffer size for worker channels (default: 1)
- `WithChunkFn(fn func(T) string)` - controls work distribution by key (default: none, random distribution)
- `WithContinueOnError()` - continues processing on errors (default: false)
- `WithWorkerCompleteFn(fn func(ctx, id, worker))` - called on worker completion (default: none)
- `WithPoolCompleteFn(fn func(ctx))` - called on pool completion, i.e., when all workers have completed (default: none)

## Collector

The Collector provides a bridge between asynchronous pool workers and synchronous result processing. It's designed to gather results from concurrent workers and present them through a simple, type-safe interface. This is essential when your workers produce values that need to be collected, processed, or aggregated.

### Key Concepts

1. **Asynchronous to Synchronous Bridge**: Workers submit results asynchronously, while consumers read them synchronously
2. **Type Safety**: Uses Go generics to ensure type safety for any result type
3. **Buffered Channel**: Internal buffered channel prevents worker blocking
4. **Context Integration**: Respects context cancellation for graceful shutdown

### Architecture

```
Workers → Submit() → [Buffered Channel] → Iter()/All() → Consumer
```

### Basic Usage

```go
// create a collector with buffer size matching worker count
collector := pool.NewCollector[Result](ctx, workerCount)

// worker submits results to collector
worker := pool.WorkerFunc[Input](func(ctx context.Context, v Input) error {
    result := processInput(v)
    collector.Submit(result) // non-blocking if buffer has space
    return nil
})

// create and start pool
p := pool.New[Input](workerCount, worker)
p.Go(ctx)

// submit work in background
go func() {
    defer p.Close(ctx)      // signal no more work
    defer collector.Close() // signal no more results
    
    for _, item := range items {
        p.Submit(item)
    }
}()

// consume results
for result, err := range collector.Iter() {
    if err != nil {
        return err // context cancelled
    }
    // process result
}
```

### Advanced Patterns

#### Pattern 1: Error Collection
Collect both successful results and errors in a unified way:

```go
type Result struct {
    Value   string
    Error   error
    JobID   int
}

collector := pool.NewCollector[Result](ctx, workers)

worker := pool.WorkerFunc[Job](func(ctx context.Context, job Job) error {
    value, err := processJob(job)
    collector.Submit(Result{
        JobID: job.ID,
        Value: value,
        Error: err,
    })
    return nil // always return nil to continue processing
})

// process results and errors together
for result, err := range collector.Iter() {
    if err != nil {
        return err // context error
    }
    if result.Error != nil {
        log.Printf("Job %d failed: %v", result.JobID, result.Error)
    } else {
        log.Printf("Job %d succeeded: %s", result.JobID, result.Value)
    }
}
```

#### Pattern 2: Pipeline Processing
Chain multiple pools with collectors for pipeline processing:

```go
// Stage 1: Parse data
parseCollector := pool.NewCollector[ParsedData](ctx, 10)
parsePool := pool.New[RawData](5, parseWorker(parseCollector))

// Stage 2: Transform data  
transformCollector := pool.NewCollector[TransformedData](ctx, 10)
transformPool := pool.New[ParsedData](5, transformWorker(transformCollector))

// Connect stages
go func() {
    for parsed, err := range parseCollector.Iter() {
        if err != nil {
            return
        }
        transformPool.Submit(parsed)
    }
    transformPool.Close(ctx)
    transformCollector.Close()
}()
```

#### Pattern 3: Selective Collection
Filter results at the worker level:

```go
collector := pool.NewCollector[ProcessedItem](ctx, 10)

worker := pool.WorkerFunc[Item](func(ctx context.Context, item Item) error {
    processed := process(item)
    
    // only collect items meeting criteria
    if processed.Score > threshold {
        collector.Submit(processed)
    }
    
    return nil
})
```

### API Reference

```go
// NewCollector creates a collector with specified buffer size
// Buffer size affects how many results can be pending before workers block
func NewCollector[V any](ctx context.Context, size int) *Collector[V]

// Submit sends a result to the collector (blocks if buffer is full)
func (c *Collector[V]) Submit(v V)

// Close signals no more results will be submitted
// Must be called when all workers are done submitting
func (c *Collector[V]) Close()

// Iter returns an iterator for processing results as they arrive
// Returns (zero-value, error) when context is cancelled
// Returns when Close() is called and all results are consumed
func (c *Collector[V]) Iter() iter.Seq2[V, error]

// All collects all results into a slice
// Blocks until Close() is called or context is cancelled
func (c *Collector[V]) All() ([]V, error)
```

### Iteration Methods

#### Method 1: Range-based Iteration
Process results as they arrive:
```go
for result, err := range collector.Iter() {
    if err != nil {
        return fmt.Errorf("collection cancelled: %w", err)
    }
    processResult(result)
}
```

#### Method 2: Collect All
Wait for all results before processing:
```go
results, err := collector.All()
if err != nil {
    return fmt.Errorf("collection failed: %w", err)
}
// process all results at once
```

### Best Practices

1. **Buffer Size Selection**
   ```go
   // Match worker count for balanced flow
   collector := pool.NewCollector[T](ctx, workerCount)
   
   // Larger buffer for bursty processing
   collector := pool.NewCollector[T](ctx, workerCount * 10)
   
   // Smaller buffer to limit memory usage
   collector := pool.NewCollector[T](ctx, 1)
   ```

2. **Proper Cleanup Sequence**
   ```go
   // 1. Close pool first (no more work)
   p.Close(ctx)
   
   // 2. Wait for workers to finish
   p.Wait(ctx)
   
   // 3. Close collector (no more results)
   collector.Close()
   
   // 4. Process remaining results
   for result, err := range collector.Iter() {
       // ...
   }
   ```

3. **Context Handling**
   ```go
   // Use same context for pool and collector
   ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
   defer cancel()
   
   collector := pool.NewCollector[Result](ctx, 10)
   p := pool.New[Input](5, worker)
   ```

4. **Error Propagation**
   ```go
   // Don't let worker errors stop collection
   p := pool.New[T](5, worker).WithContinueOnError()
   
   // Collect errors separately
   type Result struct {
       Data  ProcessedData
       Error error
   }
   ```

### Common Pitfalls

1. **Forgetting to Close**: Always close the collector after all submissions
2. **Buffer Size Too Small**: Can cause workers to block waiting for consumer
3. **Context Mismatch**: Using different contexts for pool and collector
4. **Not Handling Iterator Error**: Always check the error from `Iter()`
   
## Performance

The pool package is designed for high performance and efficiency. Benchmarks show that it consistently outperforms both the standard `errgroup`-based approach and traditional goroutine patterns with shared channels.

### Benchmark Results

Tests running 1,000,000 tasks with 8 workers on Apple M4 Max:

```
errgroup:                                     1.878s
pool (default):                               1.213s (~35% faster)
pool (chan size=100):                         1.199s
pool (chan size=100, batch size=100):         1.105s (~41% faster)
pool (with chunking):                         1.113s
```

Detailed benchmark comparison (lower is better):
```
errgroup:                                     18.56ms/op
pool (default):                               12.29ms/op
pool (chan size=100):                         12.35ms/op
pool (batch size=100):                        11.22ms/op
pool (with batching and chunking):            11.43ms/op
```

### Why Pool is Faster

1. **Efficient Channel Usage**
   - The pool uses dedicated channels per worker when chunking is enabled
   - Default channel buffer size is optimized for common use cases
   - Minimizes channel contention compared to shared channel approaches

2. **Smart Batching**
   - Reduces channel communication overhead by processing multiple items at once
   - Default batch size of 10 provides good balance between latency and throughput
   - Accumulators pre-allocated with capacity to minimize memory allocations

3. **Work Distribution**
   - Optional chunking ensures related tasks go to the same worker
   - Improves cache locality and reduces cross-worker coordination
   - Hash-based distribution provides good load balancing

4. **Resource Management**
   - Workers are pre-initialized and reused
   - No per-task goroutine creation overhead
   - Efficient cleanup and resource handling

### Configuration Impact

- **Default Settings**: Out of the box, the pool is ~35% faster than errgroup
- **Channel Buffering**: Increasing channel size can help with bursty workloads
- **Batching**: Adding batching improves performance by another ~6%
- **Chunking**: Optional chunking has minimal overhead when enabled

### When to Use What

1. **Default Settings** - Good for most use cases
   ```go
   p := pool.New[string](5, worker)
   ```

2. **High-Throughput** - For heavy workloads with many items
   ```go
   p := pool.New[string](5, worker).
       WithWorkerChanSize(100).
       WithBatchSize(100)
   ```

3. **Related Items** - When items need to be processed by the same worker
   ```go
   p := pool.New[string](5, worker).
       WithChunkFn(func(v string) string {
           return v[:1] // group by first character
       })
   ```
   
### Alternative pool implementations

- [pond](https://github.com/alitto/pond) - pond is a minimalistic and high-performance Go library designed to elegantly manage concurrent tasks.
- [goworker](https://github.com/benmanns/goworker) - goworker is a Resque-compatible, Go-based background worker. It allows you to push jobs into a queue using an expressive language like Ruby while harnessing the efficiency and concurrency of Go to minimize job latency and cost.
- [gowp](https://github.com/xxjwxc/gowp) - golang worker pool
- [conc](https://github.com/sourcegraph/conc) - better structured concurrency for go
- for more see [awesome-go goroutines](https://awesome-go.com/goroutines/) list

## Contributing

Contributions to `pool` are welcome! Please submit a pull request or open an issue for any bugs or feature requests.

## License

`pool` is available under the MIT license. See the [LICENSE](LICENSE) file for more info.