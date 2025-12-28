// Example chunking demonstrates WithChunkFn for consistent work distribution.
// Items with the same key always go to the same worker, enabling per-key
// aggregation without synchronization. Useful for grouping events by user,
// session, or any other key.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-pkgz/pool"
	"github.com/go-pkgz/pool/metrics"
)

// Event represents something happening for a user
type Event struct {
	UserID string
	Action string
}

func main() {
	// track which worker processes which users (for demonstration)
	var mu sync.Mutex
	workerUsers := make(map[int]map[string]int) // workerID -> userID -> count

	// create pool with chunking by UserID
	// this ensures all events for a user go to the same worker
	p := pool.New[Event](3, pool.WorkerFunc[Event](func(ctx context.Context, e Event) error {
		// get worker ID from context
		workerID := metrics.WorkerID(ctx)

		mu.Lock()
		if workerUsers[workerID] == nil {
			workerUsers[workerID] = make(map[string]int)
		}
		workerUsers[workerID][e.UserID]++
		mu.Unlock()

		fmt.Printf("worker %d: user=%s action=%s\n", workerID, e.UserID, e.Action)
		time.Sleep(10 * time.Millisecond)
		return nil
	})).WithChunkFn(func(e Event) string {
		return e.UserID // route by user ID
	})

	ctx := context.Background()
	if err := p.Go(ctx); err != nil {
		fmt.Printf("failed to start: %v\n", err)
		return
	}

	// submit events for different users
	// notice: same user's events will always go to the same worker
	events := []Event{
		{"alice", "login"},
		{"bob", "login"},
		{"charlie", "login"},
		{"alice", "view_page"},
		{"bob", "view_page"},
		{"alice", "click_button"},
		{"charlie", "view_page"},
		{"bob", "logout"},
		{"alice", "logout"},
		{"charlie", "click_button"},
		{"charlie", "logout"},
	}

	for _, e := range events {
		p.Submit(e)
	}

	if err := p.Close(ctx); err != nil {
		fmt.Printf("error: %v\n", err)
	}

	// show which worker handled which users
	fmt.Printf("\nworker assignment (each user always goes to same worker):\n")
	for workerID, users := range workerUsers {
		fmt.Printf("  worker %d: ", workerID)
		for user, count := range users {
			fmt.Printf("%s(%d) ", user, count)
		}
		fmt.Println()
	}

	stats := p.Metrics().GetStats()
	fmt.Printf("\nprocessed %d events in %v\n", stats.Processed, stats.TotalTime.Round(time.Millisecond))
}
