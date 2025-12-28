# Pool Completion Example

Demonstrates `WithPoolCompleteFn` callback that runs once when all workers finish.

## What it demonstrates

- Pool completion callback with `WithPoolCompleteFn`
- Difference between worker completion and pool completion
- Final aggregation/cleanup when all work is done

## Worker vs Pool completion

| Callback | When it runs | Use case |
|----------|-------------|----------|
| `WithWorkerCompleteFn` | Once per worker, when that worker finishes | Collect per-worker results |
| `WithPoolCompleteFn` | Once, when ALL workers have finished | Final aggregation, cleanup, trigger downstream |

## Running

```bash
go run main.go
```

## Output

```
worker 2 completed
worker 0 completed
worker 1 completed

all workers completed, total items processed: 10
performing final cleanup...

final stats: processed=10, time=66ms
```

Notice: worker callbacks fire individually, pool callback fires once at the end.

## Code walkthrough

### Setup both callbacks

```go
p := pool.New[int](3, worker).
    WithWorkerCompleteFn(func(_ context.Context, workerID int, _ pool.Worker[int]) error {
        // called 3 times (once per worker)
        fmt.Printf("worker %d completed\n", workerID)
        return nil
    }).
    WithPoolCompleteFn(func(_ context.Context) error {
        // called once when ALL workers done
        fmt.Println("all workers completed")
        return nil
    })
```

### Typical use cases

**Worker completion** - collect per-worker state:
```go
WithWorkerCompleteFn(func(ctx context.Context, id int, w pool.Worker[T]) error {
    results := w.(*MyWorker).GetResults()
    collector.Submit(results)
    return nil
})
```

**Pool completion** - final actions:
```go
WithPoolCompleteFn(func(ctx context.Context) error {
    // close downstream collectors
    collector.Close()
    // trigger next stage
    nextPool.Close(ctx)
    // send notification
    notifyComplete()
    return nil
})
```

## Pipeline coordination

Pool completion is essential for chaining pools. See [direct_chain](../direct_chain):

```go
pool1 := pool.New[A](...).WithPoolCompleteFn(func(ctx context.Context) error {
    return pool2.Close(ctx)  // close next pool when this one finishes
})
```

## Related examples

- [tokenizer_stateful](../tokenizer_stateful) - worker completion for result collection
- [direct_chain](../direct_chain) - pool completion for pipeline coordination
- [collectors_chain](../collectors_chain) - alternative coordination via collectors
