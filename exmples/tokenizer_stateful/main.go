package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-pkgz/pool"
)

// TokenizingWorker maintains its own state - counts of words it has processed
type TokenizingWorker struct {
	counts    map[string]int
	processed int
}

// Result represents final counts from a single worker
type Result struct {
	workerID  int
	counts    map[string]int
	processed int
}

// Do implements pool.Worker interface
func (w *TokenizingWorker) Do(ctx context.Context, line string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// split line into words and clean them up
	words := strings.Fields(line)
	for _, word := range words {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// clean up the word - remove punctuation, convert to lower case
		word = strings.ToLower(strings.Trim(word, ".,!?()[]{}\"';:"))
		if len(word) <= 3 { // skip short words
			continue
		}

		w.counts[word]++
		w.processed++
	}
	return nil
}

func main() {
	// command line flags
	var (
		workers   = flag.Int("workers", 4, "number of workers")
		batchSize = flag.Int("batch", 100, "batch size")
		file      = flag.String("file", "", "input file to process")
	)
	flag.Parse()

	if *file == "" {
		log.Fatal("file parameter is required")
	}

	// create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// create collector for results from workers
	collector := pool.NewCollector[Result](ctx, *workers)

	// create pool with worker maker function
	p := pool.NewStateful[string](*workers, func() pool.Worker[string] {
		return &TokenizingWorker{
			counts: make(map[string]int),
		}
	}).WithBatchSize(*batchSize).
		WithContinueOnError().
		WithCompleteFn(func(ctx context.Context, id int, w pool.Worker[string]) error {
			// type assert to get our concrete worker type
			tw, ok := w.(*TokenizingWorker)
			if !ok {
				return fmt.Errorf("unexpected worker type")
			}
			// submit worker's results
			collector.Submit(Result{
				workerID:  id,
				counts:    tw.counts,
				processed: tw.processed,
			})
			return nil
		})

	// start the pool
	if err := p.Go(ctx); err != nil {
		log.Fatal(err)
	}

	// read file line by line and submit to pool
	go func() {
		defer p.Close(ctx)

		f, err := os.Open(*file)
		if err != nil {
			log.Printf("failed to open file: %v", err)
			return
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			p.Submit(scanner.Text())
		}

		if err := scanner.Err(); err != nil {
			log.Printf("error reading file: %v", err)
		}
	}()

	// wait for pool to finish and then close collector
	if err := p.Wait(ctx); err != nil {
		log.Printf("pool error: %v", err)
	}
	collector.Close()

	// merge results from all workers
	totalCounts := make(map[string]int)
	totalProcessed := 0
	workerResults := make(map[int]int) // worker ID -> words processed

	for result, err := range collector.Iter() {
		if err != nil {
			log.Printf("error collecting result: %v", err)
			continue
		}
		// merge counts
		for word, count := range result.counts {
			totalCounts[word] += count
		}
		totalProcessed += result.processed
		workerResults[result.workerID] = result.processed
	}

	// get pool metrics
	stats := p.Metrics().GetStats()
	fmt.Printf("\nProcessing stats:\n")
	fmt.Printf("Processed lines: %d\n", stats.Processed)
	fmt.Printf("Total words processed: %d\n", totalProcessed)
	fmt.Printf("Errors: %d\n", stats.Errors)
	fmt.Printf("Processing time: %v\n", stats.ProcessingTime)
	fmt.Printf("Total time: %v\n", stats.TotalTime)

	// print per-worker stats
	fmt.Printf("\nPer-worker stats:\n")
	workerIDs := make([]int, 0, len(workerResults))
	for id := range workerResults {
		workerIDs = append(workerIDs, id)
	}
	sort.Ints(workerIDs)
	for _, id := range workerIDs {
		fmt.Printf("Worker %d processed %d words\n", id, workerResults[id])
	}

	// print top N most common tokens
	const topN = 10
	type wordCount struct {
		word  string
		count int
	}
	counts := make([]wordCount, 0, len(totalCounts))
	for word, count := range totalCounts {
		counts = append(counts, wordCount{word, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		return counts[i].count > counts[j].count
	})

	fmt.Printf("\nTop %d most common words:\n", topN)
	for i, wc := range counts {
		if i >= topN {
			break
		}
		fmt.Printf("%d. %q: %d times\n", i+1, wc.word, wc.count)
	}
}
