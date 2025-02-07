# pool [![Build Status](https://github.com/go-pkgz/pool/workflows/build/badge.svg)](https://github.com/go-pkgz/pool/actions) [![Go Report Card](https://goreportcard.com/badge/github.com/go-pkgz/pool)](https://goreportcard.com/report/github.com/go-pkgz/pool) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/pool/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/pool?branch=master) [![godoc](https://godoc.org/github.com/go-pkgz/pool?status.svg)](https://godoc.org/github.com/go-pkgz/pool)

`pool` is a Go package that provides a generic, efficient worker pool implementation for parallel task processing. Built for Go 1.21+, it offers a flexible API with features like batching, work distribution strategies, and comprehensive metrics collection.

## Features

- Generic implementation supporting any data type
- Configurable number of parallel workers
- Support for both stateless shared workers and per-worker instances
- Batching capability for processing multiple items at once
- Customizable work distribution through chunk functions
- Built-in metrics collection (processing times, counts, etc.)
- Error handling with continue/stop options
- Context-based cancellation and timeouts
- Optional completion callbacks
- No external dependencies except for the testing framework

## Quick Start

Here's a practical example showing how to process a list of URLs in parallel:

```go
func main() {
    // create a worker that fetches URLs
    worker := pool.WorkerFunc[string](func(ctx context.Context, url string) error {
        resp, err := http.Get(url)
        if err != nil {
            return fmt.Errorf("failed to fetch %s: %w", url, err)
        }
        defer resp.Body.Close()
        
        if resp.StatusCode != http.StatusOK {
            return fmt.Errorf("bad status code from %s: %d", url, resp.StatusCode)
        }
        return nil
    })
	
    // create a pool with 5 workers 
    opts := pool.Options[string]()
    p, err := pool.New[string](5, worker,
        opts.WithContinueOnError(), // don't stop on errors
    )
    if err != nil {
        log.Fatal(err)
    }

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

    // print metrics
    stats := p.Metrics().Stats()
    fmt.Printf("Processed: %d, Errors: %d, Time taken: %v\n",
        stats.Processed, stats.Errors, stats.TotalTime)
}
```

This example demonstrates:
- Creating a worker function that processes URLs
- Setting up a pool with multiple workers
- Submitting work in a separate goroutine
- Using Close/Wait for proper shutdown
- Error handling and metrics collection


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

While these requirements could be implemented using Go's basic concurrency primitives, doing so properly requires significant effort and careful attention to edge cases. This package provides a high-level, production-ready solution that handles these common needs while remaining flexible enough to adapt to specific use cases.

## Core Concepts

### Worker Types

The pool supports two types of workers:

1. Stateless Shared Workers:
   ```go
   // single worker instance shared between all goroutines
   worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
       // process v
       return nil
   })
   
   p, _ := pool.New[string](5, worker)
   ```
   - One worker instance serves all goroutines
   - Good for stateless operations like HTTP requests, file operations
   - More memory efficient
   - No need to synchronize as worker is stateless

2. Per-Worker Instances:
   ```go
   // stateful worker with connection
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
   
   p, _ := pool.NewStateful[string](5, maker)
   ```
   - Each goroutine gets its own worker instance
   - Good for maintaining state or resources (DB connections, caches)
   - No need for mutex as each instance is used by single goroutine
   - More memory usage but better isolation

### Batching Processing

Batching reduces channel communication overhead by processing multiple items at once:

```go
// process items in batches of 10
opts := pool.Options[string]()
p, _ := pool.New[string](2, worker,
    opts.WithBatchSize(10),
)

// worker receives items one by one
worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
    // v is one item from the batch
    return nil
})
```

How batching works:
1. Pool accumulates submitted items until batch size is reached
2. Full batch is sent to worker as single channel operation
3. Worker processes each item in the batch sequentially
4. Last batch may be smaller if items don't divide evenly

When to use batching:
- High-volume processing where channel operations are a bottleneck
- Database operations that can be batched (bulk inserts)
- Network operations that can be combined
- When processing overhead per item is low compared to channel communication

### Work Distribution

Control how work is distributed among workers using chunk functions:

```go
// distribute by first character of string
p, _ := pool.New[string](3, worker, 
    pool.Options[string]().WithChunkFn(func(v string) string {
        return v[:1] // same first char goes to same worker
    }),
)

// distribute by user ID to ensure user's tasks go to same worker
p, _ := pool.New[Task](3, worker,
    pool.Options[Task]().WithChunkFn(func(t Task) string {
        return strconv.Itoa(t.UserID)
    }),
)
```

How distribution works:
1. Without chunk function:
   - Items are distributed randomly among workers
   - Good for independent tasks

2. With chunk function:
   - Function returns string key for each item
   - Items with same key always go to same worker
   - Uses consistent hashing to map keys to workers

When to use custom distribution:
- Maintain ordering for related items
- Optimize cache usage by worker
- Ensure exclusive access to resources
- Process user/session data consistently

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
    p, err := pool.New[string](2, worker)
    if err != nil {
        log.Fatal(err)
    }

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
p, _ := pool.New[string](2, worker,
    pool.Options[string]().WithContinueOnError(),
)
```

### Collecting Results

```go
// create a collector for results
collector := pool.NewCollector[Result](ctx, 10)

// worker that produces results
worker := pool.WorkerFunc[Input](func(ctx context.Context, v Input) error {
    result := process(v)
    collector.Submit(result)
    return nil
})

p, _ := pool.New[Input](2, worker)

// get results through iteration
for v, err := range collector.Iter() {
    if err != nil {
        return err
    }
    // use v
}

// or collect all at once
results, err := collector.All()
```

### Worker State Management

```go
// stateful worker with counter
type countingWorker struct {
    count int
}

// create new worker for each goroutine
maker := func() pool.Worker[string] {
    w := &countingWorker{}
    return pool.WorkerFunc[string](func(ctx context.Context, v string) error {
        w.count++
        return nil
    })
}

p, _ := pool.NewStateful[string](2, maker)
```

### Metrics and Monitoring

```go
// create worker with metrics tracking
worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
    m := metrics.Get(ctx)
    
    // track operation timing
    operationEnd := m.StartTimer("operation")
    defer operationEnd()
    
    // simulate work
    time.Sleep(time.Millisecond * 100)
    
    // track custom metrics
    if strings.HasPrefix(v, "important") {
        m.Inc("important-tasks")
    }
    
    // track success/failure
    if err := process(v); err != nil {
        m.Inc(metrics.CountErrors)
        return err
    }
    m.Inc(metrics.CountProcessed)
    return nil
})

// create and run pool
p, _ := pool.New[string](2, worker)
p.Go(context.Background())

// process some work
p.Submit("task1")
p.Submit("important-task2")
p.Close(context.Background())

// get structured metrics
stats := p.Metrics().Stats()
fmt.Printf("Processed: %d\n", stats.Processed)
fmt.Printf("Errors: %d\n", stats.Errors)
fmt.Printf("Processing time: %v\n", stats.ProcessingTime)
fmt.Printf("Wait time: %v\n", stats.WaitTime)
fmt.Printf("Total time: %v\n", stats.TotalTime)

// get raw metrics
m := p.Metrics()
fmt.Printf("Important tasks: %d\n", m.Get("important-tasks"))
fmt.Printf("Operation time: %v\n", m.GetDuration("operation"))
```

## Complete Example: Processing Pipeline

Here's a more complex example showing how to create a processing pipeline with multiple stages:

```go
func Example_chainedCalculation() {
    // stage 1: calculate fibonacci numbers in parallel
    type FibResult struct {
        n   int
        fib uint64
    }
    stage1Collector := pool.NewCollector[FibResult](context.Background(), 10)

    fibWorker := pool.WorkerFunc[int](func(_ context.Context, n int) error {
        var a, b uint64 = 0, 1
        for i := 0; i < n; i++ {
            a, b = b, a+b
        }
        stage1Collector.Submit(FibResult{n: n, fib: a})
        return nil
    })

    // stage 2: calculate factors for each fibonacci number
    type FactorsResult struct {
        n       uint64
        factors []uint64
    }
    stage2Collector := pool.NewCollector[FactorsResult](context.Background(), 10)

    factorsWorker := pool.WorkerFunc[FibResult](func(_ context.Context, res FibResult) error {
        if res.fib <= 1 {
            stage2Collector.Submit(FactorsResult{n: res.fib, factors: []uint64{res.fib}})
            return nil
        }

        var factors []uint64
        n := res.fib
        for i := uint64(2); i*i <= n; i++ {
            for n%i == 0 {
                factors = append(factors, i)
                n /= i
            }
        }
        if n > 1 {
            factors = append(factors, n)
        }

        stage2Collector.Submit(FactorsResult{n: res.fib, factors: factors})
        return nil
    })

    // create and start both pools
    pool1, _ := pool.New[int](3, fibWorker)
    pool1.Go(context.Background())

    pool2, _ := pool.NewStateful[FibResult](2, func() pool.Worker[FibResult] {
        return factorsWorker
    })
    pool2.Go(context.Background())

    // submit numbers to calculate
    numbers := []int{5, 7, 10}
    for _, n := range numbers {
        pool1.Submit(n)
    }

    // close pools and collectors in order
    pool1.Close(context.Background())
    stage1Collector.Close()

    // process stage 1 results in stage 2
    for fibRes, err := range stage1Collector.Iter() {
        if err != nil {
            log.Printf("stage 1 error: %v", err)
            continue
        }
        pool2.Submit(fibRes)
    }

    pool2.Close(context.Background())
    stage2Collector.Close()

    // collect and sort final results to ensure deterministic output order
    results, _ := stage2Collector.All()
    sort.Slice(results, func(i, j int) bool {
        return results[i].n < results[j].n
    })

    // print results in sorted order
	for _, res := range results {
        fmt.Printf("number %d has factors %v\n", res.n, res.factors)
    }

    // Output:
    // number 5 has factors [5]
    // number 13 has factors [13]
    // number 55 has factors [5 11]
}
```

## Flow Control

The package provides two methods for completion:

```go
// Close tells workers no more data will be submitted
// Used by the producer (sender) of data
p.Close(ctx)  

// Wait blocks until all processing is done
// Used by the consumer (receiver) of results
p.Wait(ctx)   
```

Typical producer/consumer pattern:
```go
// Producer goroutine
go func() {
    defer p.Close(ctx) // signal no more data
    for _, task := range tasks {
        p.Submit(task)
    }
}()

// Consumer waits for completion
if err := p.Wait(ctx); err != nil {
    // handle error
}
```

## Options

Configure pool behavior using options:

```go
opts := pool.Options[string]()
p, err := pool.New[string](2, worker,
    opts.WithBatchSize(10),             // process items in batches
    opts.WithWorkerChanSize(5),         // set worker channel buffer size
    opts.WithChunkFn(chunkFn),          // control work distribution
    opts.WithContext(ctx),              // set custom context
    opts.WithContinueOnError(),         // don't stop on errors
    opts.WithCompleteFn(completeFn),    // called when worker finishes
)
```

Available options:
- `WithBatchSize(size int)` - enables batch processing, accumulating items before sending to workers
- `WithWorkerChanSize(size int)` - sets buffer size for worker channels
- `WithChunkFn(fn func(T) string)` - controls work distribution by key
- `WithContext(ctx context.Context)` - sets custom context for cancellation
- `WithContinueOnError()` - continues processing on errors
- `WithCompleteFn(fn func(ctx, id, worker))` - called on worker completion

## Install and update

```bash
go get -u github.com/go-pkgz/pool
```

## Contributing

Contributions to `pool` are welcome! Please submit a pull request or open an issue for any bugs or feature requests.

## License

`pool` is available under the MIT license. See the [LICENSE](LICENSE) file for more info.