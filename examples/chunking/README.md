# Chunking Example

Demonstrates `WithChunkFn` for consistent work distribution by key.

## What it demonstrates

- Key-based routing with `WithChunkFn`
- Same key always goes to the same worker (consistent hashing)
- Per-key aggregation without synchronization
- Getting worker ID from context with `metrics.WorkerID(ctx)`

## Use cases

- Aggregating events by user/session ID
- Processing messages by partition key
- Any scenario where items with the same key must be handled by the same worker

## Running

```bash
go run main.go
```

## Output

```
worker 1: user=charlie action=login
worker 2: user=alice action=login
worker 1: user=charlie action=view_page
worker 2: user=bob action=login
worker 2: user=alice action=view_page
...

worker assignment (each user always goes to same worker):
  worker 1: charlie(4)
  worker 2: bob(3) alice(4)

processed 11 events in 76ms
```

Notice: charlie always goes to worker 1, alice and bob always go to worker 2.

## Code walkthrough

### Define chunk function

```go
p := pool.New[Event](3, worker).WithChunkFn(func(e Event) string {
    return e.UserID  // route by user ID
})
```

The chunk function extracts a key from each item. Items with the same key are routed to the same worker using consistent hashing (FNV-1a).

### Get worker ID in worker

```go
import "github.com/go-pkgz/pool/metrics"

func worker(ctx context.Context, e Event) error {
    workerID := metrics.WorkerID(ctx)
    // workerID is consistent for this user
}
```

### Benefits

Without chunking:
- Workers compete for items randomly
- Aggregating by key requires synchronization (mutex/channel)

With chunking:
- Same key → same worker, always
- Per-key state can be local to worker (no locks needed)
- Useful for building per-user counters, session state, etc.

## How it works

1. `WithChunkFn` creates per-worker channels (instead of shared channel)
2. On `Submit()`, the chunk function extracts the key
3. Key is hashed with FNV-1a: `hash(key) % numWorkers`
4. Item is sent to that worker's dedicated channel

## Related examples

- [basic](../basic) - simplest pool usage
- [tokenizer_stateful](../tokenizer_stateful) - stateful workers
- [direct_chain](../direct_chain) - multi-stage pipelines
