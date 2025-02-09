# Simple Text Tokenizer - Stateless Example

This example demonstrates how to use WorkerFunc with [go-pkgz/pool](https://github.com/go-pkgz/pool) package for simple stateless parallel processing. It implements a text tokenizer that counts word frequencies using a shared collector.

## What Makes it Stateless?

This example uses a stateless approach where:
1. Workers are simple functions (WorkerFunc) without any state
2. All workers share a common collector for results
3. Word counting is done at the end in the main goroutine

This is simpler than the stateful approach when:
- Workers don't need to maintain state
- Single shared collection point is sufficient
- No need for per-worker initialization or cleanup

## Installation

```bash
# assuming you are in go-pkgz/pool/examples/tokenizer
go build
```

## Usage

```bash
go run main.go [options] -file=input.txt
```

Options:
- `-file` - input file to process (required)
- `-workers` - number of worker goroutines (default: 4)
- `-batch` - batch size for processing (default: 100)

Example:
```bash
go run main.go -file main.go -workers 8
```

## Implementation Details

The key components are:

1. Simple worker function:
```go
worker := pool.WorkerFunc[string](func(ctx context.Context, line string) error {
    for _, word := range strings.Fields(line) {
        word = strings.ToLower(strings.Trim(word, ".,!?()[]{}\"';:"))
        if word == "" {
            continue
        }
        collector.Submit(word)
    }
    return nil
})
```

2. Pool creation with shared worker:
```go
p := pool.New[string](workers, worker).
    WithBatchSize(batchSize).
    WithContinueOnError()
```

3. Result collection:
```go
wordCounts := make(map[string]int)
for word := range collector.Iter() {
    wordCounts[word]++
}
```

## Architecture

```
File Reader         Worker Pool         Collector         Word Counter
(main goroutine) → (N workers)   →   (shared channel) → (main goroutine)
reads lines        tokenize text      buffers words      counts frequencies
```

The program flow:
1. Main goroutine reads file line by line
2. Pool distributes lines to worker functions
3. Workers break lines into words and submit to shared collector
4. Main goroutine counts word frequencies from collector

## Output Example

```
Processing stats:
Processed lines: 146
Total words: 238
Unique words: 152
Errors: 0
Processing time: 77.707µs
Total time: 245.417µs

Top 10 most common words:
1. "words": 9 times
2. "word": 8 times
3. "line": 6 times
4. "pool": 5 times
5. "return": 5 times
6. "file": 5 times
7. "context": 4 times
8. "worker": 4 times
9. "%d\\n": 4 times
10. "count": 4 times
```

## Why Use This Approach?

The stateless approach is better when:
- Processing is simple and doesn't require state
- Shared collection is more efficient than per-worker state
- Code simplicity is more important than perfect parallelism
- Memory usage needs to be minimized (no per-worker state)