# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test Commands

```bash
# run all tests with race detection and coverage
go test -timeout=60s -race -covermode=atomic -coverprofile=profile.cov ./...

# run a single test
go test -run TestName

# run tests with verbose output
go test -v ./...

# run benchmarks
go test -bench=. -run=Bench

# lint (required for CI)
golangci-lint run

# format code
gofmt -s -w .
goimports -w .
```

## Architecture Overview

Generic worker pool library for Go 1.24+ with batching, work distribution, and metrics.

### Package Structure

- **pool.go** - Core `WorkerGroup[T]` with builder-pattern configuration
- **collector.go** - `Collector[V]` for async result gathering
- **metrics/** - Stats tracking (processed, errors, timings, custom counters)
- **middleware/** - Built-in middleware (Retry, Timeout, Recovery, Validator, RateLimiter)
- **examples/** - Usage examples (separate go.mod, excluded from tests)

### Core Types

```go
// Worker interface - implement Do() or use WorkerFunc adapter
type Worker[T any] interface {
    Do(ctx context.Context, v T) error
}

// WorkerFunc adapter for functions
type WorkerFunc[T any] func(ctx context.Context, v T) error

// Middleware wraps worker
type Middleware[T any] func(Worker[T]) Worker[T]
```

### Pool Modes

1. **Stateless** (`New[T](size, worker)`) - Single shared worker for all goroutines
2. **Stateful** (`NewStateful[T](size, maker)`) - Each goroutine gets own worker via maker function

### Channel Architecture

WorkerGroup maintains two sets of channels:

**Shared channels** (used when no `WithChunkFn`):
- `sharedCh chan T` - all workers compete for items
- `sharedBatchCh chan []T` - for batch mode

**Per-worker channels** (used with `WithChunkFn`):
- `workersCh []chan T` - dedicated channel per worker
- `workerBatchCh []chan []T` - batch channel per worker

Channel selection in `Go()`:
```go
workerCh := p.sharedCh
batchCh := p.sharedBatchCh
if p.chunkFn != nil {
    workerCh = p.workersCh[i]
    batchCh = p.workerBatchCh[i]
}
```

**Important**: `WithWorkerChanSize()` recreates ALL channels (both shared and per-worker).

### Work Distribution

**Direct mode (batchSize=0):**
- Without `WithChunkFn`: Shared channel - workers compete for items
- With `WithChunkFn`: Per-worker channels with FNV-1a hash routing

**Batching mode (batchSize>0):**
- Without `WithChunkFn`: Random accumulator slot selection via `rand.Intn(poolSize)`
- With `WithChunkFn`: Consistent hashing using FNV-1a hash:
```go
h := fnv.New32a()
h.Write([]byte(p.chunkFn(v)))
id := int(h.Sum32()) % p.poolSize
```

### Batching

When `batchSize > 0`:
1. Items accumulate in `accumulators[id]` (one per worker slot)
2. When accumulator reaches `batchSize`, batch sent to channel
3. `Close()` flushes remaining partial batches

### Lifecycle

1. Create pool: `New[T]()` or `NewStateful[T]()`
2. Configure: chain `With*()` methods (must be before `Go()`)
3. Start: `Go(ctx)` - starts worker goroutines via errgroup
4. Submit: `Submit(v)` (single producer) or `Send(v)` (concurrent-safe)
5. Finish: `Close(ctx)` - flushes batches, closes channels, waits for workers

### Metrics System

`metrics.Value` tracks per-worker stats without locks (each worker writes to own slot):
- `workerStats []Stats` - per-worker counters (Processed, Errors, Dropped, timings)
- `userData map[string]int` - custom counters (mutex-protected)

Workers access metrics via context:
```go
m := metrics.Get(ctx)
m.Inc("custom-counter")
```

Timer types: `TimerProc`, `TimerWait`, `TimerInit`, `TimerWrap`

### Middleware Pattern

Middleware applied in reverse order (first = outermost):
```go
// For stateless: wraps p.worker directly
// For stateful: wraps the maker function
for i := len(middlewares) - 1; i >= 0; i-- {
    wrapped = middlewares[i](wrapped)
}
```

Built-in middleware in `middleware/`:
- `Retry(attempts, baseDelay)` - exponential backoff with jitter
- `Timeout(duration)` - per-operation timeout
- `Recovery(handler)` - panic recovery
- `Validator(fn)` - input validation
- `RateLimiter(rate, burst)` - token bucket rate limiting (global, not per-worker)

### Collector

Simple async-to-sync bridge:
```go
type Collector[V any] struct {
    ch  chan V
    ctx context.Context
}
```

- `Submit(v)` - blocks if buffer full
- `Iter()` - returns `iter.Seq2[V, error]` (Go 1.23 range-over-func)
- `All()` - collects all into slice

### Error Handling

- Default: first error stops pool (errgroup behavior)
- `WithContinueOnError()`: accumulates errors, returns last error with total count
- Worker completion callbacks only run if no error or `continueOnError` is set
- Pool completion callback runs when last worker finishes (skipped only on `context.Canceled`, still runs on `context.DeadlineExceeded`)

### Key Implementation Details

1. `activated` flag prevents double `Go()` calls
2. `activeWorkers` atomic counter tracks live workers for pool completion callback
3. `sendMu` mutex makes `Send()` concurrent-safe (wraps `Submit()`)
4. errgroup manages worker goroutines and error propagation
5. Context from errgroup used for cancellation propagation
