# Basic Example

Minimal "hello world" example demonstrating the simplest pool usage.

## What it demonstrates

- Creating a pool with `pool.New[T]`
- Using `pool.WorkerFunc` adapter for simple functions
- Submitting work items with `Submit()`
- Closing pool and waiting with `Close()`
- Basic metrics via `Metrics().GetStats()`

## Running

```bash
go run main.go
```

## Output

```
processing: date
processing: apple
processing: banana
processing: cherry
processing: elderberry

done: processed 5 items in 102ms
```

## Code walkthrough

### Create the pool

```go
p := pool.New[string](3, pool.WorkerFunc[string](func(_ context.Context, item string) error {
    fmt.Printf("processing: %s\n", item)
    time.Sleep(50 * time.Millisecond)
    return nil
}))
```

- `pool.New[string](3, ...)` creates a pool with 3 workers processing strings
- `pool.WorkerFunc` adapts a function to the `Worker` interface

### Start, submit, close

```go
p.Go(ctx)                    // start workers
p.Submit("apple")            // submit work (not thread-safe)
p.Close(ctx)                 // close input and wait for completion
```

### Get metrics

```go
stats := p.Metrics().GetStats()
fmt.Printf("processed %d items in %v\n", stats.Processed, stats.TotalTime)
```

## Next steps

After understanding this basic example, explore:
- [chunking](../chunking) - consistent work distribution by key
- [tokenizer_stateful](../tokenizer_stateful) - workers with state
- [middleware](../middleware) - adding retry, timeout, validation
