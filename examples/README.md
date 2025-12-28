# Examples

This directory contains examples demonstrating various aspects of the [go-pkgz/pool](https://github.com/go-pkgz/pool) package.

**Important Note:** These examples are intentionally minimalistic and somewhat artificial. They may not represent how one would solve similar problems in real-life applications. Instead, they focus on clearly demonstrating specific features and usage patterns of the pool package.

## Available Examples

### [basic](./basic)
Minimal "hello world" example to get started quickly. Demonstrates:
- Simplest pool creation and usage
- Submitting work items
- Basic metrics

### [chunking](./chunking)
Shows how to use WithChunkFn for consistent work distribution by key. Demonstrates:
- Key-based routing (same key always goes to same worker)
- Per-key aggregation without synchronization
- Worker ID tracking via context

### [tokenizer_stateful](./tokenizer_stateful)
Shows how to use stateful workers where each worker maintains its own independent state (word frequency counters). Demonstrates:
- Worker state isolation
- Result collection through completion callbacks
- Performance statistics tracking

### [tokenizer_stateless](./tokenizer_stateless)
Implements the same text processing but using stateless workers with shared collector. Demonstrates:
- Simple worker functions
- Shared result collection
- Batch processing

### [parallel_files](./parallel_files)
Shows how to process multiple files in parallel using chunks. Demonstrates:
- Chunk-based file processing
- Custom metrics collection
- Work distribution across workers

### [middleware](./middleware)
Shows how to use middleware to add cross-cutting functionality to pool processing. Demonstrates:
- Built-in and custom middleware
- Error handling with retries
- Input validation
- Structured logging
- Recovery from panics

### [direct_chain](./direct_chain)
Shows how to chain multiple worker pools by having workers directly submit to the next pool. Demonstrates:
- Multi-stage processing pipeline
- Direct pool submission between stages
- Type transformation
- Pool coordination

### [collectors_chain](./collectors_chain)
Shows how to chain multiple worker pools using collectors. Demonstrates:
- Multi-stage processing pipeline
- Type-safe data transformation
- Automatic coordination via iterators
- Independent pool scaling

### [collector_errors](./collector_errors)
Shows how to handle and categorize errors in parallel processing. Demonstrates:
- Error collection pattern
- Error categorization and grouping
- Timing information tracking
- Statistical reporting on errors

### [pool_completion](./pool_completion)
Shows how to use the pool completion callback for final aggregation. Demonstrates:
- Pool completion callback (WithPoolCompleteFn)
- Final cleanup when all workers finish
- Difference between worker and pool completion callbacks

## Running Examples

Each example can be run from its directory:
```bash
cd basic
go run main.go

cd ../chunking
go run main.go

cd ../tokenizer_stateful
go run main.go -file input.txt

cd ../tokenizer_stateless
go run main.go -file input.txt

cd ../parallel_files
go run main.go -pattern "*.txt"

cd ../middleware
go run main.go -workers 4 -retries 3

cd ../direct_chain
go run main.go

cd ../collectors_chain
go run main.go

cd ../collector_errors
go run main.go -workers 8 -jobs 100 -error-rate 0.3

cd ../pool_completion
go run main.go
```

## Common Patterns

While the examples are simplified, they showcase important pool package features:
- Basic pool usage (basic)
- Consistent work distribution by key (chunking)
- Worker state management (stateful vs stateless)
- Result collection strategies
- Error handling approaches
- Metrics and monitoring
- Middleware integration
- Multi-stage processing pipelines