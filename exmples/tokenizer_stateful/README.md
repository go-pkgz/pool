# Simple Text Tokenizer - Stateful Workers Example

This example demonstrates how to use stateful workers with [go-pkgz/pool](https://github.com/go-pkgz/pool) package. It implements a parallel text tokenizer that counts word frequencies, where each worker maintains its own independent state.

## What Makes it Stateful?

Stateful workers are useful when each worker needs to maintain its own independent data during processing. In this example:

1. Each worker keeps its own word frequency map:
   - No shared maps or mutexes needed
   - No coordination between workers required
   - Each worker counts words it sees independently

2. Results are combined only at the end:
   - Workers don't communicate during processing
   - Final results are merged after all processing is done
   - Shows how to handle independent worker results

3. Real-world analogy:
   - Like having multiple people count words in different parts of a book
   - Each person keeps their own tally
   - At the end, all tallies are added together

This pattern is particularly useful for:
- Processing that can be partitioned (like our text analysis)
- When sharing state would create contention
- When workers need different initialization
- When tracking per-worker statistics

## Stateful vs Non-Stateful Approaches

To understand why this example uses stateful workers, let's compare two approaches:

### Non-Stateful (Wrong Way)
```go
// Shared state between all workers - requires synchronization
sharedCounts := sync.Map{}

worker := pool.WorkerFunc[string](func(ctx context.Context, line string) error {
    for _, word := range strings.Fields(line) {
        // Need to synchronize access to shared map
        v, _ := sharedCounts.LoadOrStore(word, 0)
        sharedCounts.Store(word, v.(int) + 1)
    }
    return nil
})
```

### Stateful (Our Approach)
```go
// Each worker has its own state
type TokenizingWorker struct {
    counts map[string]int  // private to this worker
}

func (w *TokenizingWorker) Do(ctx context.Context, line string) error {
    for _, word := range strings.Fields(line) {
        w.counts[word]++ // no synchronization needed
    }
    return nil
}
```

The stateful approach is better because:
- No synchronization overhead
- Better performance due to no lock contention
- Cleaner code without mutex handling
- Easier to maintain and debug

## Features

- Demonstration of stateful worker pattern
- Parallel processing of text files using configurable number of workers
- Batch processing support for better performance
- Word frequency counting
- Processing statistics including timing and error counts
- Word cleanup (lowercase conversion, punctuation removal)

## Install

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

## Output Example

```
Processing stats:
Processed lines: 192
Total words processed: 321
Errors: 0
Processing time: 171.214µs
Total time: 265.541µs

Per-worker stats:
Worker 0 processed 79 words
Worker 1 processed 63 words
Worker 2 processed 88 words
Worker 3 processed 91 words

Top 10 most common words:
1. "counts": 10 times
2. "return": 8 times
3. "words": 7 times
4. "range": 7 times
5. "processed": 7 times
6. "word": 6 times
7. "type": 6 times
8. "count": 5 times
9. "line": 5 times
10. "worker": 5 times
```

## Implementation Details

The example demonstrates true stateful worker usage in go-pkgz/pool:

1. Stateful worker implementation:
   ```go
   type TokenizingWorker struct {
       counts    map[string]int  // each worker maintains its own counts
       processed int
   }
   ```

2. Worker creation with independent state:
   ```go
   p := pool.NewStateful[string](workers, func() pool.Worker[string] {
       return &TokenizingWorker{
           counts: make(map[string]int),
       }
   })
   ```

3. Result collection using completion callback:
   ```go
   WithCompleteFn(func(ctx context.Context, id int, w pool.Worker[string]) error {
       tw := w.(*TokenizingWorker)
       collector.Submit(Result{
           workerID:  id,
           counts:    tw.counts,
           processed: tw.processed,
       })
       return nil
   })
   ```

4. Final results merging:
   ```go
   totalCounts := make(map[string]int)
   for result := range collector.Iter() {
       for word, count := range result.counts {
           totalCounts[word] += count
       }
   }
   ```

## Architecture

```
File Reader              Worker Pool              Collector               Results Merger
(main goroutine)   →    (N workers)     →     (buffer channel)    →     (main goroutine)
reads lines             counts words           collects final           merges counts
submits to pool         in own state          results from workers     prints statistics
```

The program demonstrates true parallel processing where:
- Each worker maintains independent word counts
- No state is shared between workers during processing
- Workers submit their final counts when done
- Main goroutine merges results and calculates totals
- Per-worker statistics show work distribution

## Notes

- The example can process any text file but works best with plain text
- Processing is done in parallel, but results maintain correct counts
- No word order or position information is preserved