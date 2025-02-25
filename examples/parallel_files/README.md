# Simple Text Processor - Parallel Files Example

This example demonstrates how to use parallel processing with [go-pkgz/pool](https://github.com/go-pkgz/pool) package for efficient file analysis. It reads multiple files in chunks and counts word frequencies using multiple workers.

## What Makes it Special?

1. File chunking:
    - Files read in 32KB chunks for memory efficiency
    - Each chunk processed independently
    - Allows parallel processing of large files

2. Independent worker state:
    - Each worker has its own word frequency map
    - No synchronization needed between workers
    - Results merged only on completion

3. Built-in metrics:
    - Shows processing rates and latencies
    - Tracks word length distribution
    - Demonstrates metrics collection API

## Features

- Process multiple files in parallel
- Pattern-based file selection
- Word frequency analysis
- Performance metrics tracking
- Configurable worker count

## Installation

```bash
go build
```

## Usage

```bash
go run main.go [options]
```

Options:
- `-dir` - directory to process (default: ".")
- `-pattern` - file pattern to match (default: "*.txt")
- `-workers` - number of worker goroutines (default: 4)
- `-top` - number of top words to show (default: 10)

Example:
```bash
go run main.go -pattern "*.go" -workers 8
```

## Implementation Details

The key components are:

1. Chunk-based file reading:
   ```go
   buffer := make([]byte, 32*1024)
   for {
       n, err := file.Read(buffer)
       if err == io.EOF {
           break
       }
       p.Submit(chunk{data: data})
   }
   ```

2. Stateful worker processing:
   ```go
   type fileWorker struct {
       words     map[string]int
       byteCount int64
   }
   ```

3. Metrics tracking:
   ```go
   m := metrics.Get(ctx)
   if len(word) > 3 {
       m.Inc("long words")
   } else {
       m.Inc("short words")
   }
   ```

## Output Example

```
Processing statistics: [processed:3, rate:5603.1/s, avg_latency:0s, proc:0s, total:1ms]
Total bytes: 11522
Unique words: 302
Short words: 647
Long words: 829

Top 10 words:
1. "words": 29 times
2. "return": 22 times
3. "word": 18 times
...
```

## Architecture

The program flows through these stages:
1. Read files in chunks (32KB)
2. Distribute chunks to worker pool
3. Process chunks in parallel
4. Collect results through collector
5. Merge and present statistics

## Notes

- Memory efficient due to chunk-based processing
- No locks needed in worker implementation
- Scales well with additional workers