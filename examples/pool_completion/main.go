// Example pool_completion demonstrates the pool completion callback (WithPoolCompleteFn).
// This callback executes once when ALL workers have finished, useful for final
// aggregation, cleanup, or triggering downstream processes.
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-pkgz/pool"
)

func main() {
	// track total items processed across all workers
	var totalProcessed atomic.Int64

	// create a pool with worker completion and pool completion callbacks
	p := pool.New[int](3, pool.WorkerFunc[int](func(_ context.Context, _ int) error {
		// simulate some work
		time.Sleep(10 * time.Millisecond)
		totalProcessed.Add(1)
		return nil
	})).
		WithContinueOnError().
		WithWorkerCompleteFn(func(_ context.Context, workerID int, _ pool.Worker[int]) error {
			// called when each individual worker completes
			fmt.Printf("worker %d completed\n", workerID)
			return nil
		}).
		WithPoolCompleteFn(func(_ context.Context) error {
			// called ONCE when ALL workers have finished
			// useful for final aggregation, cleanup, or triggering downstream processes
			fmt.Printf("\nall workers completed, total items processed: %d\n", totalProcessed.Load())
			fmt.Println("performing final cleanup...")
			return nil
		})

	// start the pool
	ctx := context.Background()
	if err := p.Go(ctx); err != nil {
		fmt.Printf("failed to start pool: %v\n", err)
		return
	}

	// submit some work
	for i := 1; i <= 10; i++ {
		p.Submit(i)
	}

	// close and wait for completion
	if err := p.Close(ctx); err != nil {
		fmt.Printf("pool error: %v\n", err)
	}

	// print final metrics
	stats := p.Metrics().GetStats()
	fmt.Printf("\nfinal stats: processed=%d, time=%v\n", stats.Processed, stats.TotalTime.Round(time.Millisecond))
}
