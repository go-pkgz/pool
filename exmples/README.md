# Examples

This directory contains examples demonstrating various aspects of the [go-pkgz/pool](https://github.com/go-pkgz/pool) package.

**Important Note:** These examples are intentionally minimalistic and somewhat artificial. They may not represent how one would solve similar problems in real-life applications. Instead, they focus on clearly demonstrating specific features and usage patterns of the pool package.

## Available Examples

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

## Running Examples

Each example can be run from its directory:
```bash
cd tokenizer_stateful
go run main.go -file input.txt

cd ../tokenizer_stateless
go run main.go -file input.txt

cd ../parallel_files
go run main.go -pattern "*.txt"
```

## Common Patterns

While the examples are simplified, they showcase important pool package features:
- Worker state management (stateful vs stateless)
- Result collection strategies
- Error handling approaches
- Metrics and monitoring
- Work distribution patterns