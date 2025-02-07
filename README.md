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

## Architecture and Components

The package consists of several key components that work together:

### WorkerGroup

The core component managing the worker pool. It:
- Maintains a pool of goroutines (workers)
- Handles work distribution
- Manages worker lifecycles
- Coordinates error handling
- Collects metrics

```go
type WorkerGroup[T any] struct {
    // configuration options
    poolSize  int
    batchSize int
    // ...
}
```

### Worker Interface

Defines the contract for workers processing tasks:

```go
type Worker[T any] interface {
    Do(ctx context.Context, v T) error
}
```

The package provides `WorkerFunc` adapter to turn simple functions into `Worker` interface:

```go
// WorkerFunc adapts a function to Worker interface
type WorkerFunc[T any] func(ctx context.Context, v T) error

// Do implements Worker interface
func (f WorkerFunc[T]) Do(ctx context.Context, v T) error { 
    return f(ctx, v) 
}
```

You can implement workers in two ways:

1. Using `WorkerFunc` for stateless functions:
   ```go
   worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
       fmt.Println(v)
       return nil
   })
   ```

2. Creating a struct that implements `Worker` interface:
   ```go
   type customWorker struct {
       count int // worker state
   }
   
   func (w *customWorker) Do(ctx context.Context, v string) error {
       w.count++
       return nil
   }
   ```

### Options

Configures the worker pool through a fluent API:
- `WithWorker` - sets a stateless worker
- `WithWorkerMaker` - provides worker factory for stateful workers
- `WithBatchSize` - enables batch processing
- `WithChunkFn` - controls work distribution
- `WithContext` - sets cancellation context
- `WithContinueOnError` - configures error handling
- `WithCompleteFn` - sets completion callback

### Collector

Handles result collection from workers:
- Thread-safe submission
- Iterator-based retrieval
- Bulk collection
- Context cancellation support

### Metrics

Provides insight into pool performance:
- Processing counts
- Error counts
- Timing measurements
- Per-worker statistics

### Flow

1. Pool Creation and Configuration:
   ```go
   p, _ := pool.New[T](size, options...)
   ```

2. Pool Activation:
   ```go
   p.Go(ctx)
   ```

3. Work Submission:
   ```go
   p.Submit(task)  // can be called multiple times
   ```

4. Processing:
    - Tasks are distributed to workers
    - Optional batching occurs
    - Workers process tasks
    - Metrics are collected
    - Results are optionally collected

5. Completion:
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

## Install and update

```bash
go get -u github.com/go-pkgz/pool
```

## Usage

### Basic Example

```go
func main() {
    // create a worker function processing strings
    worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
        fmt.Printf("processing: %s\n", v)
        return nil
    })

    // create a pool with 2 workers
    p, err := pool.New[string](2, pool.Options[string]().WithWorker(worker))
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

### Processing with Batching

```go
// process items in batches of 10
opts := pool.Options[string]()
p, _ := pool.New[string](2, opts.WithWorker(worker), opts.WithBatchSize(10))
```

### Controlled Work Distribution

```go
// items with the same hash go to the same worker
opts := pool.Options[string]()
p, _ := pool.New[string](2,
    opts.WithWorker(worker),
    opts.WithChunkFn(func(v string) string {
        return v[:1] // distribute by first character
    }),
)
```

### Error Handling

```go
// continue processing on errors
opts := pool.Options[string]()
p, _ := pool.New[string](2,
    opts.WithWorker(worker),
    opts.WithContinueOnError(),
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
// stateful worker example
type statefulWorker struct {
    count int
}

// create new worker for each goroutine
workerMaker := func() pool.Worker[string] {
    w := &statefulWorker{}
    return pool.WorkerFunc[string](func(ctx context.Context, v string) error {
        w.count++
        return nil
    })
}

p, _ := pool.New[string](2,
    pool.Options[string]().WithWorkerMaker(workerMaker),
)
```

### Metrics and Monitoring

```go
// process some work
p.Submit("task1")
p.Submit("task2")
p.Close(context.Background())

// get metrics
stats := p.Metrics().Stats()
fmt.Printf("Processed: %d\n", stats.Processed)
fmt.Printf("Errors: %d\n", stats.Errors)
fmt.Printf("Processing time: %v\n", stats.ProcessingTime)
fmt.Printf("Total time: %v\n", stats.TotalTime)
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
    pool1, _ := pool.New[int](3, pool.Options[int]().WithWorker(fibWorker))
    pool1.Go(context.Background())

    pool2, _ := pool.New[FibResult](2, pool.Options[FibResult]().WithWorker(factorsWorker))
    pool2.Go(context.Background())

    // submit work to stage 1
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

    // collect and print final results
    results, _ := stage2Collector.All()
    for _, res := range results {
        fmt.Printf("number %d has factors %v\n", res.n, res.factors)
    }
}
```

## Contributing

Contributions to `pool` are welcome! Please submit a pull request or open an issue for any bugs or feature requests.

## License

`pool` is available under the MIT license. See the [LICENSE](LICENSE) file for more info.