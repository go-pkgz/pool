package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-pkgz/pool"
)

// Result represents either a success or an error from processing
type Result struct {
	JobID     string        // ID of the job
	Success   bool          // whether the job succeeded
	Error     error         // error if job failed
	Timestamp time.Time     // when the job completed
	Duration  time.Duration // how long the job took
}

func main() {
	// parse command line arguments
	workers := flag.Int("workers", 4, "number of workers")
	jobs := flag.Int("jobs", 20, "number of jobs to process")
	errorRate := flag.Float64("error-rate", 0.3, "probability of job failure (0-1)")
	timeout := flag.Duration("timeout", 10*time.Second, "timeout for the entire operation")
	verbose := flag.Bool("verbose", false, "verbose output")
	flag.Parse()

	// create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// create a collector for results, both successes and errors
	collector := pool.NewCollector[Result](ctx, 100)

	workerFunc := worker(workerParam{collector: collector, verbose: *verbose, errorRate: *errorRate})
	p := pool.New[string](*workers, pool.WorkerFunc[string](workerFunc)).WithContinueOnError().WithBatchSize(5)

	// start the pool
	if err := p.Go(ctx); err != nil {
		fmt.Printf("Failed to start pool: %v\n", err)
		os.Exit(1)
	}

	// submit jobs in the background, this is usually done in a separate goroutine
	go func() {
		for i := 0; i < *jobs; i++ {
			jobID := fmt.Sprintf("job-%03d", i+1)
			p.Submit(jobID)
		}
		// close pool to signal all jobs have been submitted
		if err := p.Close(ctx); err != nil && *verbose {
			fmt.Printf("Pool closed with error: %v\n", err)
		}
	}()

	go func() {
		// wait for all jobs to finish
		err := p.Wait(ctx) // wait for all jobs to finish
		if err != nil && *verbose {
			fmt.Printf("Pool wait error: %v\n", err)
		}
		collector.Close() // close collector to signal all results has been submitted
	}()

	// collect results using the collector's Iter method
	var results []Result
	for result, err := range collector.Iter() {
		if err != nil {
			fmt.Printf("Collector error: %v\n", err)
			break
		}
		results = append(results, result)
	}

	// separate successes and errors
	var successes, failures []Result
	for _, result := range results {
		if result.Success {
			successes = append(successes, result)
		} else {
			failures = append(failures, result)
		}
	}

	// print processing summary
	stats := p.Metrics().GetStats()
	fmt.Printf("\nProcessing summary:\n")
	fmt.Printf("Total jobs:        %d\n", *jobs)
	fmt.Printf("Results collected: %d\n", len(results))
	fmt.Printf("Successful jobs:   %d\n", len(successes))
	fmt.Printf("Failed jobs:       %d\n", len(failures))
	fmt.Printf("Processing time:   %v\n", stats.ProcessingTime.Round(time.Millisecond))
	fmt.Printf("Total time:        %v\n", stats.TotalTime.Round(time.Millisecond))

	// Calculate average duration for successes and failures
	var totalSuccessDuration, totalFailureDuration time.Duration
	for _, s := range successes {
		totalSuccessDuration += s.Duration
	}
	for _, f := range failures {
		totalFailureDuration += f.Duration
	}

	if len(successes) > 0 {
		fmt.Printf("Avg success time:   %v\n", (totalSuccessDuration / time.Duration(len(successes))).Round(time.Millisecond))
	}
	if len(failures) > 0 {
		fmt.Printf("Avg failure time:   %v\n", (totalFailureDuration / time.Duration(len(failures))).Round(time.Millisecond))
	}

	if len(failures) > 0 {
		fmt.Printf("\nError details:\n")

		// group errors by type
		errorsByType := make(map[string][]Result)
		for _, result := range failures {
			errType := errorTypeString(result.Error)
			errorsByType[errType] = append(errorsByType[errType], result)
		}

		// print grouped errors
		errorTypes := make([]string, 0, len(errorsByType))
		for errType := range errorsByType {
			errorTypes = append(errorTypes, errType)
		}
		sort.Strings(errorTypes)

		for _, errType := range errorTypes {
			results := errorsByType[errType]
			fmt.Printf("\n• %s (%d occurrences):\n", errType, len(results))

			// Sort results by timestamp
			sort.Slice(results, func(i, j int) bool {
				return results[i].Timestamp.Before(results[j].Timestamp)
			})

			for _, result := range results {
				fmt.Printf("  - %s (at %s, took %v)\n",
					result.JobID,
					result.Timestamp.Format("15:04:05.000"),
					result.Duration.Round(time.Millisecond))
			}
		}
	}
}

type workerParam struct {
	verbose   bool
	errorRate float64
	collector *pool.Collector[Result]
}

func worker(p workerParam) func(ctx context.Context, jobID string) error {
	return func(ctx context.Context, jobID string) error {
		start := time.Now()

		// simulate processing time
		processingTime := time.Duration(50+rand.Intn(150)) * time.Millisecond

		if p.verbose {
			fmt.Printf("Processing %s (will take %v)...\n", jobID, processingTime)
		}

		// simulate work
		select {
		case <-time.After(processingTime):
			duration := time.Since(start)

			// randomly generate error based on error rate
			if rand.Float64() < p.errorRate {
				// choose a random error type
				var err error
				switch rand.Intn(3) {
				case 0:
					err = errors.New("validation failed")
				case 1:
					err = errors.New("database connection failed")
				case 2:
					err = errors.New("timeout exceeded")
				}

				if p.verbose {
					fmt.Printf("❌ %s failed: %v\n", jobID, err)
				}

				// submit error result to collector
				p.collector.Submit(Result{
					JobID:     jobID,
					Success:   false,
					Error:     err,
					Timestamp: time.Now(),
					Duration:  duration,
				})

				// return error so pool metrics track it correctly
				return err
			}

			if p.verbose {
				fmt.Printf("✅ %s completed successfully\n", jobID)
			}

			// submit success result to collector
			p.collector.Submit(Result{
				JobID:     jobID,
				Success:   true,
				Timestamp: time.Now(),
				Duration:  duration,
			})

			return nil

		case <-ctx.Done():
			p.collector.Submit(Result{
				JobID:     jobID,
				Success:   false,
				Error:     ctx.Err(),
				Timestamp: time.Now(),
				Duration:  time.Since(start),
			})
			return ctx.Err()
		}
	}
}

// errorTypeString extracts a consistent string representation of error type
func errorTypeString(err error) string {
	if err == nil {
		return "nil error"
	}

	msg := err.Error()
	// extract the main error type without variable parts
	if idx := strings.IndexByte(msg, ':'); idx > 0 {
		return msg[:idx]
	}
	return msg
}
