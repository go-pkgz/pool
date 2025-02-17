# Pool Chain Processing (direct) - Example

This example demonstrates how to chain multiple worker pools using [go-pkgz/pool](https://github.com/go-pkgz/pool) package to create a concurrent processing pipeline. Pools directly submit data to the next stage, with a collector only at the final stage to gather results.

## Key Concepts

1. Pool Chaining:
   - Pools directly reference and send to the next pool
   - Single collector at the end of chain
   - Each stage processes independently
   - Type-safe data transformation between stages

2. Data Flow:
   - Input strings -> count 'a's -> multiply by 10 -> square
   - Each stage has its own worker pool
   - Final collector gathers results
   - Processing time tracked at each stage

## Implementation Details

The example shows three key patterns:

1. Pool Declaration and Cross-References:
   ```go
   var pCounter *pool.WorkerGroup[stringData]
   var pMulti *pool.WorkerGroup[countData]
   var pSquares *pool.WorkerGroup[multipliedData]
   collector := pool.NewCollector[finalData](ctx, 10)
   ```

2. Direct Pool Submission:
   ```go
   pCounter = pool.New[stringData](2, pool.WorkerFunc[stringData](
       func(_ context.Context, d stringData) error {
           count := strings.Count(d.data, "a")
           if count > 2 {
               pMulti.Send(countData{...}) // direct submission to next pool, thread safe version of Submit
           }
           return nil
       }))
   ```

3. Pipeline Coordination:
   ```go
   go func() {
       pCounter.Wait(ctx)  // wait for first pool
       pMulti.Close(ctx)   // close second pool
       pSquares.Close(ctx) // close final pool
       collector.Close()   // close collector
   }()
   ```

## Data Flow Types

```go
stringData {          countData {          multipliedData {        finalData {
    idx  int             idx   int            idx   int              idx    int
    data string          count int            value int              result int
    ts   time.Time       ts    time.Time      ts    time.Time
}                     }                     }                      }
```

## Features

- Batch processing (size=3) in each pool
- Filtering capabilities (count > 2)
- Processing time tracking
- Independent worker counts per stage
- Built-in metrics collection
- Simulated processing delays

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

## Usage

```go
res, err := ProcessStrings(context.Background(), []string{
    "alabama", "california", "canada", "australia",
})
```

## Important Notes

- Pools must be declared before creation to allow cross-references
- Each stage can filter data (skip items)
- Send can be done directly from workers
- Close() propagates through the chain
- Single collector simplifies result gathering
- Batch size optimizes throughput
- Processing time tracked through pipeline

This simplified version demonstrates essential patterns for building concurrent processing pipelines while maintaining clean and efficient code structure.