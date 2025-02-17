# Pool Chain Processing (with collectors) - Example

This example demonstrates how to chain multiple worker pools using [go-pkgz/pool](https://github.com/go-pkgz/pool) package to create a concurrent processing pipeline. It shows how to transform data through multiple processing stages while maintaining type safety and proper coordination between pools.

## What Makes it Special?

1. Pool Chaining:
    - Multiple pools connected via collectors
    - Each stage processes independently
    - Type-safe data transformation between stages
    - Automatic coordination via iterators

2. Concurrent Processing:
    - Each pool runs its own workers
    - Non-blocking data flow between pools
    - Independent scaling of each stage
    - Automatic backpressure handling

3. Data Flow Patterns:
    - Type transformation between stages
    - Filtering capability (skip items)
    - Progress tracking with timestamps
    - Performance metrics collection

## Features

- Multi-stage processing pipeline
- Independent worker pools for each stage
- Type-safe data transformation
- Concurrent processing across all stages
- Automatic cleanup and resource management
- Built-in metrics collection
- Processing time tracking
- Optional data filtering between stages

## Implementation Details

The implementation demonstrates several key concepts:

1. Pool Type Definition:
   ```go
   type counterPool struct {
       *pool.WorkerGroup[stringData]      // processes input type
       collector *pool.Collector[countData] // produces output type
   }
   ```

2. Pool Construction:
   ```go
   func newCounterPool(ctx context.Context, workers int) *counterPool {
       collector := pool.NewCollector[countData](ctx, workers)
       p := pool.New[stringData](workers, pool.WorkerFunc[stringData](
           func(ctx context.Context, n stringData) error {
               // process data and submit to collector
               return nil
           }))
       return &counterPool{WorkerGroup: p, collector: collector}
   }
   ```

3. Pool Chaining:
   ```go
   counter := newCounterPool(ctx, 2)
   multiplier := newMultiplierPool(ctx, 4)
   squares := newSquarePool(ctx, 4)

   // pipe data between pools
   go func() {
       for v := range counter.collector.Iter() {
           multiplier.Submit(v)
       }
       multiplier.Close(ctx)
   }()
   ```

## Architecture

The pipeline consists of three stages:

```
Input Strings
     │
     ▼
Counter Pool (2 workers)
  │  counts 'a' chars
  │  filters count > 2
  │
  ▼
Multiplier Pool (4 workers)
  │  multiplies by 10
  │
  ▼
Square Pool (4 workers)
  │  squares the value
  │
  ▼
Final Results
```

Each stage:
- Runs independently
- Has its own workers pool
- Processes items as they arrive
- Transforms data to next type
- Reports processing metrics

## Data Flow Types

The pipeline uses distinct types for each stage:

```go
stringData     → countData → multipliedData → finalData
{idx, ts}      {idx, count} {idx, value}     {idx, result}
```

- Each type carries minimal necessary data
- Index maintains reference to original input
- Timestamp tracks processing duration

## Example Output

```
submitting: "alabama"
counted 'a' in "alabama" -> 4, duration: 123ms
multiplied: 4 -> 40 (src: "alabama", processing time: 234ms)
squared: 40 -> 1600 (src: "alabama", processing time: 345ms)

metrics:
counter: processed:11, errors:0, workers:2
multiplier: processed:6, errors:0, workers:4
squares: processed:6, errors:0, workers:4
```

## Notes

- Each pool can scale independently via worker count
- Collector's Iter() handles backpressure automatically
- Close() must be called on both pool and collector after submission done
- Metrics track processing stats for each stage
- Type safety is maintained throughout the pipeline
- Data filtering can happen at any stage

The example demonstrates a practical approach to building concurrent processing pipelines with proper resource management and type safety.