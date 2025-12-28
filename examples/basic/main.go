// Example basic demonstrates the simplest possible pool usage.
// This is a minimal "hello world" example to get started quickly.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-pkgz/pool"
)

func main() {
	// create a pool with 3 workers using a simple function
	p := pool.New[string](3, pool.WorkerFunc[string](func(_ context.Context, item string) error {
		fmt.Printf("processing: %s\n", item)
		time.Sleep(50 * time.Millisecond) // simulate work
		return nil
	}))

	// start the pool
	ctx := context.Background()
	if err := p.Go(ctx); err != nil {
		fmt.Printf("failed to start: %v\n", err)
		return
	}

	// submit work items
	items := []string{"apple", "banana", "cherry", "date", "elderberry"}
	for _, item := range items {
		p.Submit(item)
	}

	// close and wait for completion
	if err := p.Close(ctx); err != nil {
		fmt.Printf("error: %v\n", err)
	}

	// print stats
	stats := p.Metrics().GetStats()
	fmt.Printf("\ndone: processed %d items in %v\n", stats.Processed, stats.TotalTime.Round(time.Millisecond))
}
