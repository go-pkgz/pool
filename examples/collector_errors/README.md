# Error Collection and Handling Example

This example demonstrates how to effectively handle and categorize errors in parallel processing using the [go-pkgz/pool](https://github.com/go-pkgz/pool) package. It shows a pattern for collecting, tracking, and analyzing errors that occur during concurrent task execution.

## What Makes it Special?

1. Error collection pattern:
    - Collects both successes and failures
    - Preserves error context and timing information
    - Allows post-processing analysis of error patterns
    - Continues processing despite errors

2. Error classification:
    - Groups errors by type for easier analysis
    - Tracks when and where errors occurred
    - Maintains job context with each error
    - Provides statistical insights on error distribution

3. Result aggregation:
    - Separates successes from failures
    - Calculates performance metrics by result type
    - Shows error distribution patterns
    - Provides comprehensive error reporting

## Features

- Parallel job processing with configurable worker count
- Configurable error rate for testing failure scenarios
- Detailed error categorization and reporting
- Timing information for both successful and failed jobs
- Comprehensive statistics on processing performance
- Graceful handling of context cancellation

## Installation

```bash
go build
```

## Usage

```bash
go run main.go [options]
```

Options:
- `-workers` - number of worker goroutines (default: 4)
- `-jobs` - number of jobs to process (default: 20)
- `-error-rate` - probability of job failure from 0 to 1 (default: 0.3)
- `-timeout` - timeout for the entire operation (default: 10s)
- `-verbose` - enable detailed logging (default: false)

Example:
```bash
go run main.go -workers 8 -jobs 100 -error-rate 0.4 -timeout 30s
```

## Implementation Details

The example demonstrates several key patterns:

1. Result type definition:
   ```go
   type Result struct {
       JobID     string        // ID of the job
       Success   bool          // whether the job succeeded
       Error     error         // error if job failed
       Timestamp time.Time     // when the job completed
       Duration  time.Duration // how long the job took
   }
   ```

2. Error collection in workers:
   ```go
   if rand.Float64() < errorRate {
       err := errors.New("operation failed")
       collector.Submit(Result{
           JobID:     jobID,
           Success:   false,
           Error:     err,
           Timestamp: time.Now(),
           Duration:  duration,
       })
       return err  // return error so pool metrics track it
   }
   ```

3. Error categorization:
   ```go
   // group errors by type
   errorsByType := make(map[string][]Result)
   for _, result := range failures {
       errType := errorTypeString(result.Error)
       errorsByType[errType] = append(errorsByType[errType], result)
   }
   ```

## Output Example

```
Processing summary:
Total jobs:        100
Results collected: 100
Successful jobs:   61
Failed jobs:       39
Processing time:   218ms
Total time:        223ms
Avg success time:  104ms
Avg failure time:  106ms

Error details:
• database connection failed (12 occurrences):
  - job-005 (at 15:04:02.123, took 115ms)
  - job-012 (at 15:04:02.247, took 98ms)
  - ...

• validation failed (14 occurrences):
  - job-003 (at 15:04:02.089, took 103ms)
  - job-007 (at 15:04:02.187, took 112ms)
  - ...

• timeout exceeded (13 occurrences):
  - job-001 (at 15:04:02.042, took 95ms)
  - job-014 (at 15:04:02.301, took 106ms)
  - ...
```

## Architecture

The program follows this architecture:

```
Job Submission    →    Worker Pool    →    Result Collector    →    Error Analyzer
(main goroutine)       (N workers)         (buffer channel)        (main goroutine)
submits jobs           processes jobs      collects results        categorizes errors
                       with random         from workers            generates reports
                       success/failure                             calculates statistics
```

Key components:
- Pool with configurable worker count
- Collector for gathering both successes and failures
- Type-safe error collection through `Result` type
- Error categorization by error message
- Statistical processing of successes vs failures

## Real-World Applications

This pattern is useful for:
- ETL processes that need to track failed records
- API clients that need to analyze error patterns
- Batch processing systems that need error reporting
- Monitoring systems that track error rates
- System health checks with error categorization
- Performance testing with error simulation

## Notes

- The example uses simulated random errors with configurable rate
- Error types are categorized by message prefix for simplicity
- In real applications, you might want to use typed errors or error codes
- The pattern works well with both stateless and stateful workers
- This approach provides much richer error information than simple error counts